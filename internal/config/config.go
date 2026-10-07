package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Config struct {
	Version   int       `json:"version"`
	Collector Collector `json:"collector"`
	Modbus    Modbus    `json:"modbus"`
	System    System    `json:"system"`
	Output    Output    `json:"output"`
	Web       Web       `json:"web"`
}

type Collector struct {
	PollInterval string `json:"poll_interval"`
	Timeout      string `json:"timeout"`
	Retries      int    `json:"retries"`
}
type Modbus struct {
	Controllers []Controller `json:"controllers"`
}
type Controller struct {
	Name      string     `json:"name"`
	Address   string     `json:"address"`
	UnitID    byte       `json:"unit_id"`
	Registers []Register `json:"registers"`
}
type Register struct {
	Enabled       bool              `json:"enabled"`
	Name          string            `json:"name"`
	DeviceAddress string            `json:"device_address,omitempty"`
	DevicePort    uint16            `json:"device_port,omitempty"`
	UnitID        byte              `json:"unit_id,omitempty"`
	Address       uint16            `json:"address"`
	Function      string            `json:"function"`
	Type          string            `json:"type"`
	ByteOrder     string            `json:"byte_order,omitempty"`
	WordOrder     string            `json:"word_order,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Scale         float64           `json:"scale,omitempty"`
	Offset        float64           `json:"offset,omitempty"`
}

type System struct {
	Enabled      bool           `json:"enabled"`
	PollInterval string         `json:"poll_interval,omitempty"`
	Metrics      []SystemMetric `json:"metrics"`
}
type SystemMetric struct {
	Enabled bool              `json:"enabled"`
	Name    string            `json:"name"`
	Source  string            `json:"source"`
	Metric  string            `json:"metric,omitempty"`
	Path    string            `json:"path,omitempty"`
	Scale   float64           `json:"scale,omitempty"`
	Offset  float64           `json:"offset,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}
type Output struct {
	VictoriaMetrics VictoriaMetrics `json:"victoriametrics"`
}
type VictoriaMetrics struct {
	Address string `json:"address"`
	Enabled bool   `json:"enabled"`
	Timeout string `json:"timeout"`
}
type Web struct {
	Listen       string `json:"listen"`
	PasswordHash string `json:"password_hash,omitempty"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func Save(path string, c *Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func (c *Config) Validate() error {
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Version != 1 {
		return fmt.Errorf("unsupported config version: %d", c.Version)
	}
	if c.Collector.PollInterval == "" {
		c.Collector.PollInterval = "5s"
	}
	if c.Collector.Timeout == "" {
		c.Collector.Timeout = "1s"
	}
	if c.Collector.Retries < 0 {
		return fmt.Errorf("collector.retries cannot be negative")
	}
	if _, err := time.ParseDuration(c.Collector.PollInterval); err != nil {
		return fmt.Errorf("invalid collector.poll_interval: %w", err)
	}
	if _, err := time.ParseDuration(c.Collector.Timeout); err != nil {
		return fmt.Errorf("invalid collector.timeout: %w", err)
	}
	if c.System.PollInterval == "" {
		c.System.PollInterval = c.Collector.PollInterval
	}
	if _, err := time.ParseDuration(c.System.PollInterval); err != nil {
		return fmt.Errorf("invalid system.poll_interval: %w", err)
	}
	for _, p := range c.Modbus.Controllers {
		if p.Name == "" || p.Address == "" {
			return fmt.Errorf("each controller requires name and address")
		}
		if p.UnitID == 0 {
			return fmt.Errorf("controller %q: unit_id must be 1..247", p.Name)
		}
		for _, r := range p.Registers {
			if r.Name == "" {
				return fmt.Errorf("controller %q: register name is required", p.Name)
			}
			switch r.Type {
			case "int16", "uint16", "int32", "uint32", "float32":
			default:
				return fmt.Errorf("register %q: unsupported type %q", r.Name, r.Type)
			}
			if r.Function == "" {
				r.Function = "holding"
			}
			if r.Function != "holding" && r.Function != "input" {
				return fmt.Errorf("register %q: unsupported function %q", r.Name, r.Function)
			}
		}
	}
	for _, m := range c.System.Metrics {
		if m.Name == "" || m.Source == "" {
			return fmt.Errorf("system metric requires name and source")
		}
		switch m.Source {
		case "cpu", "memory", "storage", "temperature", "network", "uptime", "file":
		default:
			return fmt.Errorf("system metric %q: unsupported source %q", m.Name, m.Source)
		}
	}
	if c.Output.VictoriaMetrics.Enabled {
		if c.Output.VictoriaMetrics.Address == "" {
			return fmt.Errorf("victoriametrics.address is required")
		}
	}
	return nil
}

// Store keeps the active configuration and persists Web UI edits atomically.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  *Config
}

func NewStore(path string, cfg *Config) *Store { return &Store{path: path, cfg: cfg} }
func (s *Store) Get() Config                   { s.mu.RLock(); defer s.mu.RUnlock(); return clone(*s.cfg) }
func (s *Store) Update(c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := Save(s.path, &c); err != nil {
		return err
	}
	s.cfg = &c
	return nil
}
func clone(c Config) Config {
	b, _ := json.Marshal(c)
	var out Config
	_ = json.Unmarshal(b, &out)
	return out
}
