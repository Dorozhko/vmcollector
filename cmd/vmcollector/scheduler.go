package main

import (
	"time"

	"github.com/yura/vmcollector/internal/config"
)

type metricScheduler struct {
	next      map[string]time.Time
	intervals map[string]time.Duration
}

func newMetricScheduler() *metricScheduler {
	return &metricScheduler{
		next:      make(map[string]time.Time),
		intervals: make(map[string]time.Duration),
	}
}

func resolveInterval(value, fallback string) time.Duration {
	if value != "" {
		if d, err := time.ParseDuration(value); err == nil && d > 0 {
			return d
		}
	}

	if d, err := time.ParseDuration(fallback); err == nil && d > 0 {
		return d
	}

	return 5 * time.Second
}

func (s *metricScheduler) due(key string, interval time.Duration, now time.Time) bool {
	if interval <= 0 {
		interval = 5 * time.Second
	}

	oldInterval, exists := s.intervals[key]
	if !exists || oldInterval != interval {
		s.intervals[key] = interval
		s.next[key] = now
	}

	if now.Before(s.next[key]) {
		return false
	}

	s.next[key] = now.Add(interval)
	return true
}

func (s *metricScheduler) selectMetrics(cfg config.Config, now time.Time) (map[int]bool, map[int]bool) {
	modbusSelected := make(map[int]bool)
	systemSelected := make(map[int]bool)

	for i, reg := range cfg.Modbus.Registers {
		key := modbusMetricKey(reg.Name, i)

		if !reg.Enabled {
			delete(s.next, key)
			delete(s.intervals, key)
			continue
		}

		interval := resolveInterval(reg.PollInterval, cfg.Collector.PollInterval)
		if s.due(key, interval, now) {
			modbusSelected[i] = true
		}
	}

	for i, m := range cfg.System.Metrics {
		key := systemMetricKey(i)

		if !cfg.System.Enabled || !m.Enabled {
			delete(s.next, key)
			delete(s.intervals, key)
			continue
		}

		fallback := cfg.System.PollInterval
		if fallback == "" {
			fallback = cfg.Collector.PollInterval
		}

		interval := resolveInterval(m.PollInterval, fallback)
		if s.due(key, interval, now) {
			systemSelected[i] = true
		}
	}

	return modbusSelected, systemSelected
}

// nextDelay returns the time until the next scheduled metric.
// It caps the wait at 250 ms so configuration changes are noticed promptly.
func (s *metricScheduler) nextDelay(now time.Time) time.Duration {
	const maxWait = 250 * time.Millisecond

	delay := maxWait

	for _, next := range s.next {
		until := next.Sub(now)
		if until <= 0 {
			return time.Millisecond
		}
		if until < delay {
			delay = until
		}
	}

	if delay < time.Millisecond {
		return time.Millisecond
	}

	return delay
}
