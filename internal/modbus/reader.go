package modbus

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"strconv"
	"time"

	goburrow "github.com/goburrow/modbus"

	"github.com/yura/vmcollector/internal/config"
	"github.com/yura/vmcollector/internal/metric"
)

type MetricResult struct {
	Index  int
	Sample metric.Sample
}

type MetricError struct {
	Index   int
	Name    string
	Address uint16
	Err     error
}

type Reader struct {
	cfg     config.Controller
	timeout time.Duration
	retries int
}

func NewReader(cfg config.Controller, timeout time.Duration, retries int) *Reader {
	return &Reader{
		cfg:     cfg,
		timeout: timeout,
		retries: retries,
	}
}

func (r *Reader) Read(ctx context.Context) ([]MetricResult, []MetricError) {
	results := make([]MetricResult, 0, len(r.cfg.Registers))
	errors := make([]MetricError, 0)

	clients := make(map[string]goburrow.Client)

	for i, reg := range r.cfg.Registers {
		if !reg.Enabled {
			continue
		}

		select {
		case <-ctx.Done():
			errors = append(errors, MetricError{
				Index:   i,
				Name:    reg.Name,
				Address: reg.Address,
				Err:     ctx.Err(),
			})
			return results, errors
		default:
		}

		// Each metric may have its own Modbus TCP device.
		// Empty fields keep backward compatibility with the old
		// controller-based configuration.
		deviceAddress := reg.DeviceAddress
		devicePort := reg.DevicePort
		unitID := reg.UnitID

		if deviceAddress == "" {
			deviceAddress = r.cfg.Address

			// Legacy controller configuration may contain host:port.
			// Split it so we do not construct host:port:port.
			if host, port, err := net.SplitHostPort(r.cfg.Address); err == nil {
				deviceAddress = host

				if devicePort == 0 {
					if parsedPort, err := strconv.ParseUint(port, 10, 16); err == nil {
						devicePort = uint16(parsedPort)
					}
				}
			}
		}

		if devicePort == 0 {
			devicePort = 502
		}

		if unitID == 0 {
			unitID = r.cfg.UnitID
		}

		endpoint := fmt.Sprintf("%s:%d", deviceAddress, devicePort)
		clientKey := fmt.Sprintf("%s/%d", endpoint, unitID)

		client, ok := clients[clientKey]

		if !ok {
			handler := goburrow.NewTCPClientHandler(endpoint)
			handler.Timeout = r.timeout
			handler.SlaveId = unitID

			client = goburrow.NewClient(handler)
			clients[clientKey] = client
		}

		var (
			data  []byte
			err   error
			words uint16 = 1
		)

		switch reg.Type {
		case "uint32", "int32", "float32":
			words = 2
		}

		for attempt := 0; attempt <= r.retries; attempt++ {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			default:
				if reg.Function == "input" {
					data, err = client.ReadInputRegisters(
						modbusOffset(reg.Address),
						words,
					)
				} else {
					data, err = client.ReadHoldingRegisters(
						modbusOffset(reg.Address),
						words,
					)
				}
			}

			if err == nil {
				break
			}
		}

		if err != nil {
			errors = append(errors, MetricError{
				Index:   i,
				Name:    reg.Name,
				Address: reg.Address,
				Err: fmt.Errorf(
					"%s unit %d: %w",
					endpoint,
					unitID,
					err,
				),
			})
			continue
		}

		value, err := decode(data, reg.Type)
		if err != nil {
			errors = append(errors, MetricError{
				Index:   i,
				Name:    reg.Name,
				Address: reg.Address,
				Err:     err,
			})
			continue
		}

		scale := reg.Scale
		if scale == 0 {
			scale = 1
		}

		value = value*scale + reg.Offset

		labels := make([]metric.Label, 0, len(reg.Labels)+1)

		labels = append(labels, metric.Label{
			Name:  "plc",
			Value: r.cfg.Name,
		})

		for k, v := range reg.Labels {
			labels = append(labels, metric.Label{
				Name:  k,
				Value: v,
			})
		}

		results = append(results, MetricResult{
			Index: i,
			Sample: metric.Sample{
				Name:      reg.Name,
				Labels:    labels,
				Value:     value,
				Timestamp: time.Now().UnixMilli(),
			},
		})
	}

	return results, errors
}

func modbusOffset(address uint16) uint16 {
	if address >= 40001 && address <= 49999 {
		return address - 40001
	}

	return address
}

func decode(b []byte, typ string) (float64, error) {
	if len(b) < 2 {
		return 0, fmt.Errorf("short response")
	}

	switch typ {
	case "int16":
		return float64(int16(binary.BigEndian.Uint16(b))), nil

	case "uint16":
		return float64(binary.BigEndian.Uint16(b)), nil

	case "int32":
		if len(b) < 4 {
			return 0, fmt.Errorf("short response")
		}

		return float64(int32(binary.BigEndian.Uint32(b))), nil

	case "uint32":
		if len(b) < 4 {
			return 0, fmt.Errorf("short response")
		}

		return float64(uint32(binary.BigEndian.Uint32(b))), nil

	case "float32":
		if len(b) < 4 {
			return 0, fmt.Errorf("short response")
		}

		return float64(
			math.Float32frombits(binary.BigEndian.Uint32(b)),
		), nil

	default:
		return 0, fmt.Errorf("unsupported type %q", typ)
	}
}
