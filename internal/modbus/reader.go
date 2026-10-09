package modbus

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
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
        cfg     config.Register
        timeout time.Duration
        retries int
}

func NewReader(cfg config.Register, timeout time.Duration, retries int) *Reader {
        return &Reader{
                cfg:     cfg,
                timeout: timeout,
                retries: retries,
        }
}

func (r *Reader) Read(ctx context.Context) ([]MetricResult, []MetricError) {
        results := make([]MetricResult, 0, 1)
        errors := make([]MetricError, 0, 1)

        if !r.cfg.Enabled {
                return results, errors
        }

        handler := goburrow.NewTCPClientHandler(r.cfg.Device)
        handler.Timeout = r.timeout
        handler.SlaveId = r.cfg.UnitID

        client := goburrow.NewClient(handler)

        select {
        case <-ctx.Done():
                errors = append(errors, MetricError{
                        Index:   0,
                        Name:    r.cfg.Name,
                        Address: r.cfg.Address,
                        Err:     ctx.Err(),
                })
                return results, errors
        default:
        }

        var (
                data  []byte
                err   error
                words uint16 = 1
        )

        switch r.cfg.Type {
        case "uint32", "int32", "float32":
                words = 2
        }

        for attempt := 0; attempt <= r.retries; attempt++ {
                select {
                case <-ctx.Done():
                        err = ctx.Err()
                default:
                        if r.cfg.Function == "input" {
                                data, err = client.ReadInputRegisters(
                                        modbusOffset(r.cfg.Address),
                                        words,
                                )
                        } else {
                                data, err = client.ReadHoldingRegisters(
                                        modbusOffset(r.cfg.Address),
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
                        Index:   0,
                        Name:    r.cfg.Name,
                        Address: r.cfg.Address,
                        Err: fmt.Errorf(
                                "%s unit %d: %w",
                                r.cfg.Device,
                                r.cfg.UnitID,
                                err,
                        ),
                })
                return results, errors
        }

        value, err := decode(
                data,
                r.cfg.Type,
                r.cfg.ByteOrder,
                r.cfg.WordOrder,
        )
        if err != nil {
                errors = append(errors, MetricError{
                        Index:   0,
                        Name:    r.cfg.Name,
                        Address: r.cfg.Address,
                        Err:     err,
                })
                return results, errors
        }

        scale := r.cfg.Scale
        if scale == 0 {
                scale = 1
        }

        value = value*scale + r.cfg.Offset

        labels := make([]metric.Label, 0, len(r.cfg.Labels))

        for k, v := range r.cfg.Labels {
                labels = append(labels, metric.Label{
                        Name:  k,
                        Value: v,
                })
        }

        results = append(results, MetricResult{
                Index: 0,
                Sample: metric.Sample{
                        Name:      r.cfg.Name,
                        Labels:    labels,
                        Value:     value,
                        Timestamp: time.Now().UnixMilli(),
                },
        })

        return results, errors
}

func modbusOffset(address uint16) uint16 {
	if address >= 40001 && address <= 49999 {
		return address - 40001
	}

	return address
}

func decode(b []byte, typ, byteOrder, wordOrder string) (float64, error) {
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

		return float64(decodeUint32(b, byteOrder, wordOrder)), nil

	case "uint32":
		if len(b) < 4 {
			return 0, fmt.Errorf("short response")
		}

		return float64(decodeUint32(b, byteOrder, wordOrder)), nil

	case "float32":
		if len(b) < 4 {
			return 0, fmt.Errorf("short response")
		}

		bits := decodeUint32(b, byteOrder, wordOrder)
		return float64(math.Float32frombits(bits)), nil

	default:
		return 0, fmt.Errorf("unsupported type %q", typ)
	}
}

func decodeUint32(b []byte, byteOrder, wordOrder string) uint32 {
	if byteOrder == "" {
		byteOrder = "ABCD"
	}
	if wordOrder == "" {
		wordOrder = "AB"
	}

	var x [4]byte

	x[0], x[1], x[2], x[3] = b[0], b[1], b[2], b[3]

	switch wordOrder {
	case "BA":
		x[0], x[1], x[2], x[3] = x[2], x[3], x[0], x[1]
	}

	switch byteOrder {
	case "BADC":
		x[0], x[1], x[2], x[3] = x[1], x[0], x[3], x[2]
	case "CDAB":
		x[0], x[1], x[2], x[3] = x[2], x[3], x[0], x[1]
	case "DCBA":
		x[0], x[1], x[2], x[3] = x[3], x[2], x[1], x[0]
	}

	return binary.BigEndian.Uint32(x[:])
}
