package web

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/yura/modbus-vmagent/internal/config"
)

//go:embed static/*
var assets embed.FS

const maxLogs = 100

type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

type Server struct {
	store *config.Store

	lastSamples atomic.Int64
	lastError   atomic.Value

	lastHeartbeatNS atomic.Int64

	mu           sync.RWMutex
	logs         []LogEntry
	errorCount   int64
	running      bool
	metricStatus map[string]string

	authMu   sync.RWMutex
	sessions map[string]time.Time
	vmStatus string
}

func New(store *config.Store) *Server {
	s := &Server{
		store:        store,
		logs:         make([]LogEntry, 0, maxLogs),
		running:      true,
		metricStatus: make(map[string]string),
		sessions:     make(map[string]time.Time),
	}

	s.lastError.Store("")

	return s
}

func (s *Server) SetSampleCount(n int) {
	s.lastSamples.Store(int64(n))
}

const diagnosticsTimeout = 15 * time.Second

func (s *Server) diagnosticsActive() bool {
	ns := s.lastHeartbeatNS.Load()

	if ns == 0 {
		return false
	}

	return time.Since(time.Unix(0, ns)) <= diagnosticsTimeout
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.lastHeartbeatNS.Store(time.Now().UnixNano())

	s.json(w, map[string]any{
		"ok": true,
	})
}

func (s *Server) SetVMStatus(status string) {
	switch status {
	case "Connected", "Error", "Waiting", "Disabled":
	default:
		status = "Disabled"
	}

	s.mu.Lock()
	s.vmStatus = status
	s.mu.Unlock()
}

func (s *Server) SetError(err error) {
	if err == nil {
		s.lastError.Store("")
		return
	}

	s.lastError.Store(err.Error())

	if s.diagnosticsActive() {
		s.AddLog("error", err.Error())
	}
}

func (s *Server) AddLog(level, message string) {
	if message == "" {
		return
	}

	if !s.diagnosticsActive() {
		return
	}

	if level != "info" && level != "warning" && level != "error" {
		level = "info"
	}

	entry := LogEntry{
		Time:    time.Now(),
		Level:   level,
		Message: message,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if level == "error" {
		s.errorCount++
	}

	s.logs = append(s.logs, entry)

	if len(s.logs) > maxLogs {
		s.logs = s.logs[len(s.logs)-maxLogs:]
	}
}

// SetMetricStatus stores the latest read status of one metric.
//
// Valid statuses:
//
//	OK
//	ERROR
//	OFF
func (s *Server) SetMetricStatus(key, status string) {
	if key == "" || !s.diagnosticsActive() {
		return
	}

	switch status {
	case "OK", "ERROR", "OFF":
	default:
		status = "ERROR"
	}

	s.mu.Lock()
	s.metricStatus[key] = status
	s.mu.Unlock()
}

func (s *Server) MetricStatuses() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]string, len(s.metricStatus))

	for k, v := range s.metricStatus {
		out[k] = v
	}

	return out
}

func (s *Server) SetRunning(running bool) {
	s.mu.Lock()
	s.running = running
	s.mu.Unlock()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public authentication endpoints.
	mux.HandleFunc("/login", s.loginPage)
	mux.HandleFunc("/api/login", s.login)
	mux.HandleFunc("/api/logout", s.logout)

	mux.Handle("/static/", http.FileServer(http.FS(assets)))

	// Protected API.
	mux.HandleFunc("/api/status", s.status)
	mux.HandleFunc("/api/session/heartbeat", s.heartbeat)
	mux.HandleFunc("/api/metric-status", s.metricStatusAPI)
	mux.HandleFunc("/api/logs", s.logsAPI)
	mux.HandleFunc("/api/logs/clear", s.clearLogsAPI)
	mux.HandleFunc("/api/config", s.config)
	mux.HandleFunc("/api/registers", s.registers)
	mux.HandleFunc("/api/system-metrics", s.systemMetrics)

	// Protected application.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthenticated(r) {
			if r.URL.Path == "/" {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			http.NotFound(w, r)
			return
		}

		if r.URL.Path == "/" {
			b, err := assets.ReadFile("static/index.html")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}

		http.FileServer(http.FS(assets)).ServeHTTP(w, r)
	})

	return mux
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.isAuthenticated(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	b, err := assets.ReadFile("static/login.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	cfg := s.store.Get()

	if req.Username != "admin" ||
		cfg.Web.PasswordHash == "" ||
		bcrypt.CompareHashAndPassword(
			[]byte(cfg.Web.PasswordHash),
			[]byte(req.Password),
		) != nil {
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		http.Error(w, "unable to create session", http.StatusInternalServerError)
		return
	}

	token := base64.RawURLEncoding.EncodeToString(tokenBytes)

	s.authMu.Lock()
	s.sessions[token] = time.Now().Add(24 * time.Hour)
	s.authMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "vmcollector_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   86400,
	})

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"username":"admin"}`))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("vmcollector_session"); err == nil {
		s.authMu.Lock()
		delete(s.sessions, c.Value)
		s.authMu.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "vmcollector_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) isAuthenticated(r *http.Request) bool {
	c, err := r.Cookie("vmcollector_session")
	if err != nil || c.Value == "" {
		return false
	}

	s.authMu.RLock()
	expires, ok := s.sessions[c.Value]
	s.authMu.RUnlock()

	if !ok {
		return false
	}

	if time.Now().After(expires) {
		s.authMu.Lock()
		delete(s.sessions, c.Value)
		s.authMu.Unlock()
		return false
	}

	return true
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	running := s.running
	errorCount := s.errorCount
	s.mu.RUnlock()

	status := "stopped"

	if running {
		status = "running"
	}

	s.json(w, map[string]any{
		"status":             status,
		"last_samples":       s.lastSamples.Load(),
		"last_error":         s.lastError.Load(),
		"error_count":        errorCount,
		"diagnostics_active": s.diagnosticsActive(),
	})
}

func (s *Server) metricStatusAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.json(w, s.MetricStatuses())
}

func (s *Server) logsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	logs := append([]LogEntry(nil), s.logs...)
	s.mu.RUnlock()

	s.json(w, logs)
}

func (s *Server) clearLogsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	s.logs = nil
	s.errorCount = 0
	s.mu.Unlock()

	s.json(w, map[string]any{
		"ok": true,
	})
}

func (s *Server) config(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.json(w, s.store.Get())
		return

	case http.MethodPut:
		var c config.Config

		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := s.store.Update(c); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		s.AddLog("info", "Configuration updated")

		s.json(w, c)
		return

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) registers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "use PUT /api/config to update registers", http.StatusMethodNotAllowed)
		return
	}

	c := s.store.Get()

	type row struct {
		Controller string `json:"controller"`
		config.Register
	}

	var out []row

	for _, p := range c.Modbus.Controllers {
		for _, reg := range p.Registers {
			out = append(out, row{
				Controller: p.Name,
				Register:   reg,
			})
		}
	}

	s.json(w, out)
}

func (s *Server) systemMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.json(w, s.store.Get().System.Metrics)
}

func (s *Server) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
