package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/yura/vmcollector/internal/config"
	"github.com/yura/vmcollector/internal/metric"
	"github.com/yura/vmcollector/internal/modbus"
	"github.com/yura/vmcollector/internal/remotewrite"
	"github.com/yura/vmcollector/internal/system"
	"github.com/yura/vmcollector/internal/web"
)

func systemMetricKey(index int) string {
	return fmt.Sprintf("system:%d", index)
}

func modbusMetricKey(controller string, index int) string {
	return fmt.Sprintf("modbus:%s:%d", controller, index)
}

func metricVMEnabled(enabled *bool) bool {
	return enabled == nil || *enabled
}

func metricWSEnabled(enabled bool) bool {
	return enabled
}

func main() {
	configPath := flag.String("config", "config.json", "JSON configuration file")
	setAdminPassword := flag.Bool("set-admin-password", false, "Set admin password and exit")
	setPort := flag.Int("set-port", 0, "Set web port and exit")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	if *setPort != 0 {
		if *setPort < 1 || *setPort > 65535 {
			log.Fatal("port must be between 1 and 65535")
		}

		cfg.Web.Listen = fmt.Sprintf("0.0.0.0:%d", *setPort)

		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			log.Fatal(err)
		}

		data = append(data, '\n')

		if err := os.WriteFile(*configPath, data, 0600); err != nil {
			log.Fatal(err)
		}

		fmt.Printf("Web port updated to %d.\n", *setPort)
		return
	}

	if *setAdminPassword {
		fmt.Print("Enter admin password: ")
		password, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			log.Fatal(err)
		}

		if len(password) < 8 {
			log.Fatal("password must be at least 8 characters")
		}
		if len(password) > 72 {
			log.Fatal("password must not exceed 72 bytes")
		}

		fmt.Print("Confirm admin password: ")
		confirmation, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			log.Fatal(err)
		}

		if string(password) != string(confirmation) {
			log.Fatal("passwords do not match")
		}

		hash, err := bcrypt.GenerateFromPassword(password, bcrypt.DefaultCost)
		if err != nil {
			log.Fatal(err)
		}

		cfg.Web.PasswordHash = string(hash)

		data, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			log.Fatal(err)
		}

		data = append(data, '\n')

		if err := os.WriteFile(*configPath, data, 0600); err != nil {
			log.Fatal(err)
		}

		fmt.Println("Admin password updated.")
		return
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	store := config.NewStore(*configPath, cfg)
	ws := web.New(store)
	go ws.RunWebSocket(ctx)

	if cfg.Web.Listen != "" {
		go func() {
			log.Printf("web: http://%s", cfg.Web.Listen)

			if err := http.ListenAndServe(cfg.Web.Listen, ws.Handler()); err != nil &&
				err != http.ErrServerClosed {
				log.Printf("web: %v", err)
			}
		}()
	}

	scheduler := newMetricScheduler()
	sysCollector := system.New(cfg.System)

	run := func(modbusSelected, systemSelected map[int]bool) {
		current := store.Get()
		sysCollector.UpdateConfig(current.System)
		currentTimeout, _ := time.ParseDuration(current.Collector.Timeout)

		allSamples := 0

		var rw *remotewrite.Client

		if current.Output.VictoriaMetrics.Enabled {
			t := 10 * time.Second

			if current.Output.VictoriaMetrics.Timeout != "" {
				if parsed, err := time.ParseDuration(current.Output.VictoriaMetrics.Timeout); err == nil {
					t = parsed
				}
			}

			client, err := remotewrite.New(
				current.Output.VictoriaMetrics.Address,
				t,
			)

			if err != nil {
				ws.SetError(err)
				log.Printf("victoriametrics: %v", err)
			} else {
				rw = client
			}

		}

		// ------------------------------------------------------------
		// Modbus TCP

		for i, reg := range current.Modbus.Registers {
			key := modbusMetricKey(reg.Name, i)

			if !reg.Enabled {
				ws.SetMetricStatus(key, "OFF")
				continue
			}

			if !modbusSelected[i] {
				continue
			}

			reader := modbus.NewReader(
				reg,
				currentTimeout,
				current.Collector.Retries,
			)

			results, metricErrors := reader.Read(ctx)

			for _, result := range results {
				ws.SetMetricStatus(
					modbusMetricKey(reg.Name, result.Index),
					"OK",
				)
			}

			for _, me := range metricErrors {
				ws.SetMetricStatus(
					modbusMetricKey(reg.Name, me.Index),
					"ERROR",
				)

				err := fmt.Errorf(
					"modbus %s metric %s @ %d: %w",
					reg.Device,
					me.Name,
					me.Address,
					me.Err,
				)

				ws.SetError(err)
				log.Printf("modbus: %v", err)
			}

			vmSamples := make([]metric.Sample, 0, len(results))
			wsSamples := make([]metric.Sample, 0, len(results))

			for _, result := range results {
				if metricVMEnabled(reg.VictoriaMetricsEnabled) {
					vmSamples = append(vmSamples, result.Sample)
				}
				if metricWSEnabled(reg.WebSocketEnabled) {
					wsSamples = append(wsSamples, result.Sample)
				}
			}

			allSamples += len(results)
			ws.PublishWebSocket(wsSamples)

			if rw != nil && len(vmSamples) > 0 {
				if e := rw.Write(ctx, vmSamples); e != nil {
					ws.SetError(e)
					log.Printf("victoriametrics: %v", e)
				} else {
					log.Printf("sent %d samples from %s", len(vmSamples), reg.Name)
				}
			}

		}

		// System metrics
		// ------------------------------------------------------------

		for i, m := range current.System.Metrics {
			key := systemMetricKey(i)

			if !current.System.Enabled || !m.Enabled {
				ws.SetMetricStatus(key, "OFF")
			}
		}

		systemResults, metricErrors := sysCollector.ReadSelected(
			ctx,
			systemSelected,
		)
		vmSystemSamples := make([]metric.Sample, 0, len(systemResults))
		wsSystemSamples := make([]metric.Sample, 0, len(systemResults))

		for _, result := range systemResults {
			ws.SetMetricStatus(systemMetricKey(result.Index), "OK")

			if result.Index < 0 || result.Index >= len(current.System.Metrics) {
				continue
			}
			m := current.System.Metrics[result.Index]

			if metricVMEnabled(m.VictoriaMetricsEnabled) {
				vmSystemSamples = append(vmSystemSamples, result.Sample)
			}
			if metricWSEnabled(m.WebSocketEnabled) {
				wsSystemSamples = append(wsSystemSamples, result.Sample)
			}
		}

		ws.PublishWebSocket(wsSystemSamples)

		for _, me := range metricErrors {
			ws.SetMetricStatus(
				systemMetricKey(me.Index),
				"ERROR",
			)

			err := fmt.Errorf(
				"system metric %s: %w",
				me.Name,
				me.Err,
			)

			ws.SetError(err)
			log.Printf("system: %v", err)
		}

		allSamples += len(systemResults)

		if rw != nil && len(vmSystemSamples) > 0 {
			if e := rw.Write(ctx, vmSystemSamples); e != nil {
				ws.SetError(e)
				log.Printf("victoriametrics: %v", e)
			} else {
				log.Printf("sent %d system samples", len(vmSystemSamples))
			}
		}

		ws.SetSampleCount(allSamples)

		if rw != nil {
			rw.Close()
		}
	}
	for {
		if ctx.Err() != nil {
			return
		}

		current := store.Get()
		now := time.Now()

		modbusSelected, systemSelected := scheduler.selectMetrics(
			current,
			now,
		)

		if len(modbusSelected) > 0 || len(systemSelected) > 0 {
			run(modbusSelected, systemSelected)
		}

		timer := time.NewTimer(scheduler.nextDelay(time.Now()))

		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
