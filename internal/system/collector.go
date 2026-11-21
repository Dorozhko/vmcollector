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
				var cpuResults []MetricResult
				cpuResults, err = c.cpuResults(*cpu, c.prevCPU, m, i)
				if err == nil {
					results = append(results, cpuResults...)
					continue
				}
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

		case "directory":
			value, err = directorySizeValue(m.Path, m.Metric)

		case "file":
			if m.Metric == "size_mb" || m.Metric == "size_gb" {
				value, err = fileSizeValue(m.Path, m.Metric)
			} else {
				value, err = fileValue(m.Path, m.Scale, m.Offset)
			}

		case "temperature":
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

	if cpu != nil {
		c.prevCPU = cpu
	}

	return results, errors
}

type cpuTimes struct {
	idle  uint64
	total uint64
}

type cpuSnapshot struct {
	cores map[string]cpuTimes
}

func readCPU() (*cpuSnapshot, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, err
	}

	return parseCPUStat(string(data))
}

func parseCPUStat(data string) (*cpuSnapshot, error) {
	snapshot := &cpuSnapshot{
		cores: make(map[string]cpuTimes),
	}

	scanner := bufio.NewScanner(strings.NewReader(data))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}

		name := fields[0]
		if name != "cpu" {
			if !strings.HasPrefix(name, "cpu") {
				continue
			}

			coreID := strings.TrimPrefix(name, "cpu")
			if _, err := strconv.ParseUint(coreID, 10, 32); err != nil {
				continue
			}
		}

		var total, idle uint64

		for i, field := range fields[1:] {
			value, parseErr := strconv.ParseUint(field, 10, 64)
			if parseErr != nil {
				return nil, fmt.Errorf("parse %s: %w", name, parseErr)
			}

			// Exclude guest and guest_nice (already included in user/nice).
			if i <= 7 {
				total += value
			}

			// Linux counters: idle and iowait.
			if i == 3 || i == 4 {
				idle += value
			}
		}

		snapshot.cores[name] = cpuTimes{
			idle:  idle,
			total: total,
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if _, ok := snapshot.cores["cpu"]; !ok {
		return nil, fmt.Errorf("aggregate CPU line not found")
	}

	return snapshot, nil
}

func cpuUsage(previous, current cpuTimes) (float64, error) {
	if current.total < previous.total || current.idle < previous.idle {
		return 0, fmt.Errorf("CPU counters decreased")
	}

	totalDelta := current.total - previous.total
	idleDelta := current.idle - previous.idle

	if totalDelta == 0 {
		return 0, fmt.Errorf("no CPU counter change")
	}
	if idleDelta > totalDelta {
		return 0, fmt.Errorf("invalid CPU idle delta")
	}

	return 100 * float64(totalDelta-idleDelta) / float64(totalDelta), nil
}

func readCPUFrequency(core string) (float64, error) {
	path := filepath.Join(
		"/sys/devices/system/cpu",
		core,
		"cpufreq",
		"scaling_cur_freq",
	)

	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	khz, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, err
	}
	if khz <= 0 {
		return 0, fmt.Errorf("invalid CPU frequency")
	}

	return khz / 1000, nil
}

func (c *Collector) cpuResults(
	current cpuSnapshot,
	previous *cpuSnapshot,
	m config.SystemMetric,
	index int,
) ([]MetricResult, error) {
	timestamp := time.Now().UnixMilli()

	makeSample := func(value float64, core string) MetricResult {
		labels := []metric.Label{
			{Name: "source", Value: "cpu"},
		}

		for k, v := range m.Labels {
			if k != "core" {
				labels = append(labels, metric.Label{Name: k, Value: v})
			}
		}

		if core != "" {
			labels = append(labels, metric.Label{Name: "core", Value: core})
		}

		return MetricResult{
			Index: index,
			Sample: metric.Sample{
				Name:      m.Name,
				Labels:    labels,
				Value:     value,
				Timestamp: timestamp,
			},
		}
	}

	switch m.Metric {
	case "usage", "core_usage":
		if previous == nil {
			return nil, nil
		}

		perCore := m.Metric == "core_usage"
		results := make([]MetricResult, 0)

		for core, currentTimes := range current.cores {
			if perCore && core == "cpu" {
				continue
			}
			if !perCore && core != "cpu" {
				continue
			}

			previousTimes, ok := previous.cores[core]
			if !ok {
				continue
			}

			usage, err := cpuUsage(previousTimes, currentTimes)
			if err != nil {
				continue
			}

			coreLabel := ""
			if perCore {
				coreLabel = strings.TrimPrefix(core, "cpu")
			}

			results = append(results, makeSample(usage, coreLabel))
		}

		return results, nil

	case "core_frequency":
		results := make([]MetricResult, 0)

		for core := range current.cores {
			if core == "cpu" {
				continue
			}

			frequency, err := readCPUFrequency(core)
			if err != nil {
				continue
			}

			coreLabel := strings.TrimPrefix(core, "cpu")
			results = append(results, makeSample(frequency, coreLabel))
		}

		if len(results) == 0 {
			return nil, fmt.Errorf("no CPU core frequency sensors available")
		}

		return results, nil

	default:
		return nil, fmt.Errorf("unsupported CPU metric %q", m.Metric)
	}
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
	free := float64(m["MemFree"])
	used := total - avail

	switch name {
	case "total":
		return total, nil
	case "available":
		return avail, nil
	case "free":
		return free, nil
	case "used":
		return used, nil
	case "used_mb":
		return used / (1024 * 1024), nil
	case "used_percent":
		if total <= 0 {
			return 0, fmt.Errorf("invalid MemTotal")
		}
		return used / total * 100, nil
	case "available_percent":
		if total <= 0 {
			return 0, fmt.Errorf("invalid MemTotal")
		}
		return avail / total * 100, nil
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

func bytesToUnit(size int64, unit string) (float64, error) {
	switch unit {
	case "size_mb", "used_mb", "total_mb", "available_mb":
		return float64(size) / (1024 * 1024), nil
	case "size_gb", "used_gb", "total_gb", "available_gb":
		return float64(size) / (1024 * 1024 * 1024), nil
	default:
		return 0, fmt.Errorf("unsupported size unit %q", unit)
	}
}

func fileSizeValue(path, unit string) (float64, error) {
	if path == "" {
		return 0, fmt.Errorf("file path is required")
	}

	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}

	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("path is not a regular file")
	}

	return bytesToUnit(info.Size(), unit)
}

func directorySizeValue(path, unit string) (float64, error) {
	if path == "" {
		return 0, fmt.Errorf("directory path is required")
	}

	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}

	if !info.IsDir() {
		return 0, fmt.Errorf("path is not a directory")
	}

	var total int64

	err = filepath.WalkDir(path, func(
		currentPath string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if entry.IsDir() {
			return nil
		}

		fileInfo, err := entry.Info()
		if err != nil {
			return err
		}

		if fileInfo.Mode().IsRegular() {
			total += fileInfo.Size()
		}

		return nil
	})

	if err != nil {
		return 0, err
	}

	return bytesToUnit(total, unit)
}

func storageValue(path, name string) (float64, error) {
	if path == "" {
		path = "/"
	}

	var s syscall.Statfs_t

	if err := syscall.Statfs(path, &s); err != nil {
		return 0, err
	}

	size := float64(s.Bsize)
	total := float64(s.Blocks) * size
	free := float64(s.Bfree) * size
	available := float64(s.Bavail) * size
	used := total - free

	switch name {
	case "total":
		return total, nil
	case "used":
		return used, nil
	case "free":
		return free, nil
	case "available":
		return available, nil
	case "total_mb", "total_gb":
		return bytesToUnit(int64(total), name)
	case "used_mb", "used_gb":
		return bytesToUnit(int64(used), name)
	case "available_mb", "available_gb":
		return bytesToUnit(int64(available), name)
	case "used_percent":
		if total <= 0 {
			return 0, fmt.Errorf("invalid filesystem size")
		}
		return used / total * 100, nil
	case "free_percent":
		if total <= 0 {
			return 0, fmt.Errorf("invalid filesystem size")
		}
		return free / total * 100, nil
	case "available_percent":
		if total <= 0 {
			return 0, fmt.Errorf("invalid filesystem size")
		}
		return available / total * 100, nil
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
