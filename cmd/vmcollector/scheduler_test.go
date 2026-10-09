package main

import (
	"testing"
	"time"

	"github.com/yura/vmcollector/internal/config"
)

func TestMetricSchedulerIndependentIntervals(t *testing.T) {
	s := newMetricScheduler()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cfg := config.Config{
		Collector: config.Collector{PollInterval: "5s"},
		Modbus: config.Modbus{
			Registers: []config.Register{
				{Enabled: true, Name: "fast", PollInterval: "1s"},
				{Enabled: true, Name: "slow", PollInterval: "10s"},
			},
		},
	}

	modbus, _ := s.selectMetrics(cfg, now)
	if !modbus[0] || !modbus[1] {
		t.Fatal("both metrics should be due on the first poll")
	}

	modbus, _ = s.selectMetrics(cfg, now.Add(time.Second))
	if !modbus[0] {
		t.Fatal("fast metric should be due after 1 second")
	}
	if modbus[1] {
		t.Fatal("slow metric should not be due after 1 second")
	}

	modbus, _ = s.selectMetrics(cfg, now.Add(10*time.Second))
	if !modbus[0] || !modbus[1] {
		t.Fatal("both metrics should be due after 10 seconds")
	}
}

func TestMetricSchedulerUsesFallbackInterval(t *testing.T) {
	s := newMetricScheduler()
	now := time.Now()

	cfg := config.Config{
		Collector: config.Collector{PollInterval: "5s"},
		Modbus: config.Modbus{
			Registers: []config.Register{
				{Enabled: true, Name: "fallback"},
			},
		},
	}

	modbus, _ := s.selectMetrics(cfg, now)
	if !modbus[0] {
		t.Fatal("metric should be due immediately")
	}

	modbus, _ = s.selectMetrics(cfg, now.Add(4*time.Second))
	if modbus[0] {
		t.Fatal("fallback interval should be 5 seconds")
	}

	modbus, _ = s.selectMetrics(cfg, now.Add(5*time.Second))
	if !modbus[0] {
		t.Fatal("metric should be due after the 5-second fallback interval")
	}
}

func TestMetricSchedulerSkipsDisabledMetrics(t *testing.T) {
	s := newMetricScheduler()

	cfg := config.Config{
		Collector: config.Collector{PollInterval: "5s"},
		Modbus: config.Modbus{
			Registers: []config.Register{
				{Enabled: false, Name: "disabled"},
			},
		},
		System: config.System{
			Enabled: true,
			Metrics: []config.SystemMetric{
				{Enabled: false, Name: "disabled_system", Source: "uptime"},
			},
		},
	}

	modbus, system := s.selectMetrics(cfg, time.Now())
	if len(modbus) != 0 || len(system) != 0 {
		t.Fatal("disabled metrics must not be scheduled")
	}
}
