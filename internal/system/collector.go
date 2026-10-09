package system

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/yura/vmcollector/internal/config"
	"github.com/yura/vmcollector/internal/metric"
)

type MetricResult struct {
	Index  int
	Sample metric.Sample
}

type MetricError struct {
	Index int
	Name  string
	Err   error
}

type Collector struct {
	cfg     config.System
	mu      sync.Mutex
	prevCPU *cpuSnapshot
	prevAt  time.Time
}

func New(cfg config.System) *Collector {
	return &Collector{cfg: cfg}
}

// UpdateConfig replaces the system metric configuration while preserving
// the collector's CPU sampling state.
func (c *Collector) UpdateConfig(cfg config.System) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg = cfg
}

func (c *Collector) Read(ctx context.Context) ([]MetricResult, []MetricError) {
    return c.ReadSelected(ctx, nil)
}

func (c *Collector) ReadSelected(ctx context.Context, selected map[int]bool) ([]MetricResult, []MetricError) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if !c.cfg.Enabled {
        return nil, nil
    }

    results := make([]MetricResult, 0, len(c.cfg.Metrics))
	errors := make([]MetricError, 0)

	var cpu *cpuSnapshot
	var mem map[string]uint64

	for i, m := range c.cfg.Metrics {
        if !m.Enabled || (selected != nil && !selected[i]) {
            continue
        }

		if err := ctx.Err(); err != nil {
			errors = append(errors, MetricError{
				Index: i,
				Name:  m.Name,
				Err:   err,
			})
			break
		}

		var value float64
		var err error

		switch m.Source {
		case "cpu":
			if cpu == nil {
				cpu, err = readCPU()
			}
			if err == nil {
				value, err = c.cpuValue(*cpu, m.Metric)
			}

		case "memory":
			if mem == nil {
				mem, err = readMeminfo()
			}
			if err == nil {
				value, err = memoryValue(mem, m.Metric)
			}

		case "storage":
			value, err = storageValue(m.Path, m.Metric)

		case "temperature", "file":
			value, err = fileValue(m.Path, m.Scale, m.Offset)

		case "network":
			value, err = networkValue(m.Path, m.Metric)

		case "uptime":
			value, err = uptimeValue()

		default:
			err = fmt.Errorf("unsupported system source %q", m.Source)
		}

		if err != nil {
			errors = append(errors, MetricError{
				Index: i,
				Name:  m.Name,
				Err:   err,
			})
			continue
		}

		labels := make([]metric.Label, 0, len(m.Labels)+1)

		labels = append(labels, metric.Label{
			Name:  "source",
			Value: m.Source,
		})

		for k, v := range m.Labels {
			labels = append(labels, metric.Label{
				Name:  k,
				Value: v,
			})
		}

		results = append(results, MetricResult{
			Index: i,
			Sample: metric.Sample{
				Name:      m.Name,
				Labels:    labels,
				Value:     value,
				Timestamp: time.Now().UnixMilli(),
			},
		})
	}

	return results, errors
}

type cpuSnapshot struct {
	idle  uint64
	total uint64
}

func readCPU() (*cpuSnapshot, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)

	for s.Scan() {
		fields := strings.Fields(s.Text())

		if len(fields) >= 5 && fields[0] == "cpu" {
			var total uint64

			for _, x := range fields[1:] {
				n, e := strconv.ParseUint(x, 10, 64)
				if e != nil {
					return nil, e
				}

				total += n
			}

			idle, _ := strconv.ParseUint(fields[4], 10, 64)

			return &cpuSnapshot{
				idle:  idle,
				total: total,
			}, nil
		}
	}

	if err := s.Err(); err != nil {
		return nil, err
	}

	return nil, fmt.Errorf("cpu line not found")
}

func (c *Collector) cpuValue(cur cpuSnapshot, name string) (float64, error) {
	if name != "usage" {
		return 0, fmt.Errorf("unsupported CPU metric %q", name)
	}

	if c.prevCPU == nil {
		c.prevCPU = &cur
		c.prevAt = time.Now()
		return 0, nil
	}

	dt := cur.total - c.prevCPU.total
	di := cur.idle - c.prevCPU.idle

	c.prevCPU = &cur

	if dt == 0 {
		return 0, nil
	}

	return 100 * float64(dt-di) / float64(dt), nil
}

func readMeminfo() (map[string]uint64, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := map[string]uint64{}

	s := bufio.NewScanner(f)

	for s.Scan() {
		fields := strings.Fields(s.Text())

		if len(fields) >= 2 {
			n, e := strconv.ParseUint(fields[1], 10, 64)
			if e == nil {
				out[strings.TrimSuffix(fields[0], ":")] = n * 1024
			}
		}
	}

	return out, s.Err()
}

func memoryValue(m map[string]uint64, name string) (float64, error) {
	total := float64(m["MemTotal"])
	avail := float64(m["MemAvailable"])

	switch name {
	case "total":
		return total, nil
	case "available":
		return avail, nil
	case "free":
		return float64(m["MemFree"]), nil
	case "used":
		return total - avail, nil
	default:
		return 0, fmt.Errorf("unsupported memory metric %q", name)
	}
}

func fileValue(path string, scale, offset float64) (float64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	n, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0, err
	}

	if scale == 0 {
		scale = 1
	}

	return n*scale + offset, nil
}

func storageValue(path, name string) (float64, error) {
	if path == "" {
		path = "/"
	}

	var s syscall.Statfs_t

	if err := syscall.Statfs(path, &s); err != nil {
		return 0, err
	}

	size := uint64(s.Bsize)
	total := float64(s.Blocks) * float64(size)
	free := float64(s.Bavail) * float64(size)

	switch name {
	case "total":
		return total, nil
	case "free":
		return free, nil
	case "used":
		return total - free, nil
	default:
		return 0, fmt.Errorf("unsupported storage metric %q", name)
	}
}

func uptimeValue() (float64, error) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}

	f := strings.Fields(string(b))

	if len(f) == 0 {
		return 0, fmt.Errorf("invalid /proc/uptime")
	}

	return strconv.ParseFloat(f[0], 64)
}

func networkValue(path, name string) (float64, error) {
	if path == "" {
		return 0, fmt.Errorf("network interface is required")
	}

	switch name {
	case "rx_bytes", "tx_bytes", "rx_packets", "tx_packets", "rx_errors", "tx_errors":
	default:
		return 0, fmt.Errorf("unsupported network metric %q", name)
	}

	b, err := os.ReadFile(
		filepath.Join("/sys/class/net", path, "statistics", name),
	)
	if err != nil {
		return 0, err
	}

	return strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
}
