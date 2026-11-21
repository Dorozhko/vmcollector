package system

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/yura/vmcollector/internal/config"
)

func assertClose(t *testing.T, got, want float64) {
	t.Helper()

	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMemoryValue(t *testing.T) {
	mem := map[string]uint64{
		"MemTotal":     16 * 1024 * 1024 * 1024,
		"MemAvailable": 6 * 1024 * 1024 * 1024,
		"MemFree":      4 * 1024 * 1024 * 1024,
	}

	tests := []struct {
		name string
		want float64
	}{
		{"total", 16 * 1024 * 1024 * 1024},
		{"available", 6 * 1024 * 1024 * 1024},
		{"free", 4 * 1024 * 1024 * 1024},
		{"used", 10 * 1024 * 1024 * 1024},
		{"used_mb", 10240},
		{"used_percent", 62.5},
		{"available_percent", 37.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := memoryValue(mem, tt.name)
			if err != nil {
				t.Fatal(err)
			}
			assertClose(t, got, tt.want)
		})
	}
}

func TestMemoryValueInvalidTotal(t *testing.T) {
	_, err := memoryValue(map[string]uint64{
		"MemAvailable": 100,
	}, "used_percent")

	if err == nil {
		t.Fatal("expected error for zero MemTotal")
	}
}

func TestStorageValueUnsupportedMetric(t *testing.T) {
	_, err := storageValue("/", "invalid_metric")

	if err == nil {
		t.Fatal("expected unsupported metric error")
	}
}

func TestStorageValue(t *testing.T) {
	tests := []string{
		"total",
		"used",
		"free",
		"available",
		"total_mb",
		"total_gb",
		"used_mb",
		"used_gb",
		"available_mb",
		"available_gb",
		"used_percent",
		"free_percent",
		"available_percent",
	}

	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := storageValue("/", name)
			if err != nil {
				t.Fatal(err)
			}
			if math.IsNaN(got) || math.IsInf(got, 0) || got < 0 {
				t.Fatalf("invalid storage value: %v", got)
			}
		})
	}
}

func TestCPUUsage(t *testing.T) {
	tests := []struct {
		name string
		prev cpuTimes
		cur  cpuTimes
		want float64
	}{
		{
			name: "50 percent",
			prev: cpuTimes{idle: 50, total: 100},
			cur:  cpuTimes{idle: 75, total: 150},
			want: 50,
		},
		{
			name: "100 percent busy",
			prev: cpuTimes{idle: 50, total: 100},
			cur:  cpuTimes{idle: 50, total: 150},
			want: 100,
		},
		{
			name: "0 percent busy",
			prev: cpuTimes{idle: 50, total: 100},
			cur:  cpuTimes{idle: 100, total: 150},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cpuUsage(tt.prev, tt.cur)
			if err != nil {
				t.Fatal(err)
			}
			assertClose(t, got, tt.want)
		})
	}
}

func TestCPUUsageInvalidCounters(t *testing.T) {
	tests := []struct {
		name string
		prev cpuTimes
		cur  cpuTimes
	}{
		{
			name: "decreased total",
			prev: cpuTimes{idle: 50, total: 100},
			cur:  cpuTimes{idle: 40, total: 90},
		},
		{
			name: "decreased idle",
			prev: cpuTimes{idle: 50, total: 100},
			cur:  cpuTimes{idle: 40, total: 150},
		},
		{
			name: "zero delta",
			prev: cpuTimes{idle: 50, total: 100},
			cur:  cpuTimes{idle: 50, total: 100},
		},
		{
			name: "idle exceeds total delta",
			prev: cpuTimes{idle: 50, total: 100},
			cur:  cpuTimes{idle: 120, total: 150},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cpuUsage(tt.prev, tt.cur)
			if err == nil {
				t.Fatal("expected CPU counter error")
			}
		})
	}
}

func TestCPUResultsOverallUsage(t *testing.T) {
	c := &Collector{}

	previous := &cpuSnapshot{
		cores: map[string]cpuTimes{
			"cpu":  {idle: 50, total: 100},
			"cpu0": {idle: 25, total: 50},
			"cpu1": {idle: 25, total: 50},
		},
	}

	current := cpuSnapshot{
		cores: map[string]cpuTimes{
			"cpu":  {idle: 75, total: 150},
			"cpu0": {idle: 35, total: 75},
			"cpu1": {idle: 40, total: 75},
		},
	}

	m := config.SystemMetric{
		Name:   "cpu_usage_percent",
		Source: "cpu",
		Metric: "usage",
	}

	results, err := c.cpuResults(current, previous, m, 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}

	assertClose(t, results[0].Sample.Value, 50)

	if results[0].Sample.Name != "cpu_usage_percent" {
		t.Fatalf("unexpected metric name: %s", results[0].Sample.Name)
	}
}

func TestCPUResultsPerCore(t *testing.T) {
	c := &Collector{}

	previous := &cpuSnapshot{
		cores: map[string]cpuTimes{
			"cpu":  {idle: 50, total: 100},
			"cpu0": {idle: 25, total: 50},
			"cpu1": {idle: 25, total: 50},
		},
	}

	current := cpuSnapshot{
		cores: map[string]cpuTimes{
			"cpu":  {idle: 75, total: 150},
			"cpu0": {idle: 35, total: 75},
			"cpu1": {idle: 40, total: 75},
		},
	}

	m := config.SystemMetric{
		Name:   "cpu_core_usage_percent",
		Source: "cpu",
		Metric: "core_usage",
	}

	results, err := c.cpuResults(current, previous, m, 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	values := make(map[string]float64)

	for _, result := range results {
		core := ""

		for _, label := range result.Sample.Labels {
			if label.Name == "core" {
				core = label.Value
			}
		}

		if core == "" {
			t.Fatal("missing core label")
		}

		if result.Sample.Name != "cpu_core_usage_percent" {
			t.Fatalf("unexpected metric name: %s", result.Sample.Name)
		}

		values[core] = result.Sample.Value
	}

	assertClose(t, values["0"], 60)
	assertClose(t, values["1"], 40)
}

func TestCPUResultsWithoutPreviousSnapshot(t *testing.T) {
	c := &Collector{}

	current := cpuSnapshot{
		cores: map[string]cpuTimes{
			"cpu":  {idle: 75, total: 150},
			"cpu0": {idle: 35, total: 75},
		},
	}

	m := config.SystemMetric{
		Name:   "cpu_usage_percent",
		Source: "cpu",
		Metric: "usage",
	}

	results, err := c.cpuResults(current, nil, m, 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) != 0 {
		t.Fatalf("got %d results, want 0 on first snapshot", len(results))
	}
}

func TestParseCPUStatGuestCounters(t *testing.T) {
	input := `cpu 100 20 30 40 5 6 7 8 9 10
cpu0 50 10 10 20 2 1 1 1 5 2
intr 12345`

	snapshot, err := parseCPUStat(input)
	if err != nil {
		t.Fatal(err)
	}

	aggregate := snapshot.cores["cpu"]
	if aggregate.total != 216 {
		t.Fatalf("aggregate total = %d, want 216", aggregate.total)
	}
	if aggregate.idle != 45 {
		t.Fatalf("aggregate idle = %d, want 45", aggregate.idle)
	}

	core := snapshot.cores["cpu0"]
	if core.total != 95 {
		t.Fatalf("cpu0 total = %d, want 95", core.total)
	}
	if core.idle != 22 {
		t.Fatalf("cpu0 idle = %d, want 22", core.idle)
	}
}

func TestParseCPUStatInvalidData(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "invalid counter",
			input: "cpu 100 20 30 invalid 5",
		},
		{
			name:  "missing aggregate CPU",
			input: "cpu0 100 20 30 40",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseCPUStat(tt.input)
			if err == nil {
				t.Fatal("expected parsing error")
			}
		})
	}
}

func TestParseCPUStatGuestDoesNotAffectTotal(t *testing.T) {
	withoutGuest, err := parseCPUStat("cpu 100 20 30 40 5 6 7 8")
	if err != nil {
		t.Fatal(err)
	}

	withGuest, err := parseCPUStat("cpu 100 20 30 40 5 6 7 8 900 800")
	if err != nil {
		t.Fatal(err)
	}

	if withoutGuest.cores["cpu"].total != withGuest.cores["cpu"].total {
		t.Fatalf(
			"guest counters changed total: without=%d, with=%d",
			withoutGuest.cores["cpu"].total,
			withGuest.cores["cpu"].total,
		)
	}
}

func TestFileSizeValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "database.db")

	data := make([]byte, 2*1024*1024)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		unit string
		want float64
	}{
		{"size_mb", 2},
		{"size_gb", 2.0 / 1024},
	}

	for _, tt := range tests {
		t.Run(tt.unit, func(t *testing.T) {
			got, err := fileSizeValue(path, tt.unit)
			if err != nil {
				t.Fatal(err)
			}
			assertClose(t, got, tt.want)
		})
	}
}

func TestFileSizeValueErrors(t *testing.T) {
	dir := t.TempDir()
	validFile := filepath.Join(dir, "valid.db")

	if err := os.WriteFile(validFile, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		unit string
	}{
		{"empty path", "", "size_mb"},
		{"missing file", filepath.Join(dir, "missing.db"), "size_mb"},
		{"directory instead of file", dir, "size_mb"},
		{"invalid unit", validFile, "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fileSizeValue(tt.path, tt.unit)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestDirectorySizeValue(t *testing.T) {
	dir := t.TempDir()

	nested := filepath.Join(dir, "subdir")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}

	files := map[string]int{
		filepath.Join(dir, "file1.db"):    1024 * 1024,
		filepath.Join(nested, "file2.db"): 2 * 1024 * 1024,
	}

	for path, size := range files {
		if err := os.WriteFile(path, make([]byte, size), 0600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		unit string
		want float64
	}{
		{"size_mb", 3},
		{"size_gb", 3.0 / 1024},
	}

	for _, tt := range tests {
		t.Run(tt.unit, func(t *testing.T) {
			got, err := directorySizeValue(dir, tt.unit)
			if err != nil {
				t.Fatal(err)
			}
			assertClose(t, got, tt.want)
		})
	}
}

func TestDirectorySizeValueErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.db")

	if err := os.WriteFile(file, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		unit string
	}{
		{"empty path", "", "size_mb"},
		{"missing directory", filepath.Join(dir, "missing"), "size_mb"},
		{"file instead of directory", file, "size_mb"},
		{"invalid unit", dir, "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := directorySizeValue(tt.path, tt.unit)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseCPUStatIgnoresNonCoreCPUEntries(t *testing.T) {
	input := `cpu 100 20 30 40 5
cpu0 50 10 10 20 2
cpufreq 999 999 999 999 999`

	snapshot, err := parseCPUStat(input)
	if err != nil {
		t.Fatal(err)
	}

	if _, exists := snapshot.cores["cpufreq"]; exists {
		t.Fatal("cpufreq must not be parsed as a CPU core")
	}

	if _, exists := snapshot.cores["cpu0"]; !exists {
		t.Fatal("cpu0 core was not parsed")
	}
}
