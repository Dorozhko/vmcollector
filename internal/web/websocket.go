package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yura/vmcollector/internal/metric"
)

type wsMessage struct {
	Type      string           `json:"type"`
	Timestamp int64            `json:"timestamp"`
	Samples   []wsMetricSample `json:"samples"`
}

type wsMetricSample struct {
	Name      string            `json:"name"`
	Labels    map[string]string `json:"labels"`
	Value     float64           `json:"value"`
	Timestamp int64             `json:"timestamp"`
}

type wsClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil || u.Hostname() == "" {
			return false
		}
		requestHost := r.Host
		if host, _, err := net.SplitHostPort(requestHost); err == nil {
			requestHost = host
		} else {
			requestHost = strings.Trim(requestHost, "[]")
		}
		return strings.EqualFold(u.Hostname(), requestHost)
	},
}

func (s *Server) PublishWebSocket(samples []metric.Sample) {
	cfg := s.store.Get()
	if !cfg.Output.WebSocket.Enabled || len(samples) == 0 {
		return
	}

	message := wsMessage{
		Type:      "metrics",
		Timestamp: time.Now().UnixMilli(),
		Samples:   make([]wsMetricSample, 0, len(samples)),
	}

	for _, sample := range samples {
		labels := make(map[string]string, len(sample.Labels))
		for _, label := range sample.Labels {
			labels[label.Name] = label.Value
		}
		message.Samples = append(message.Samples, wsMetricSample{
			Name:      sample.Name,
			Labels:    labels,
			Value:     sample.Value,
			Timestamp: sample.Timestamp,
		})
	}

	s.wsMu.RLock()
	clients := make([]*wsClient, 0, len(s.wsClients))
	for client := range s.wsClients {
		clients = append(clients, client)
	}
	s.wsMu.RUnlock()

	for _, client := range clients {
		client.mu.Lock()
		_ = client.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		err := client.conn.WriteJSON(message)
		client.mu.Unlock()
		if err != nil {
			client.mu.Lock()
			_ = client.conn.Close()
			client.mu.Unlock()
			s.wsMu.Lock()
			delete(s.wsClients, client)
			s.wsMu.Unlock()
		}
	}
}

// WebSocketHandler returns the handler for the dedicated WebSocket listener.
func (s *Server) WebSocketHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.websocketHandler)
	return mux
}

// RunWebSocket manages the dedicated WebSocket listener according to configuration.
func (s *Server) RunWebSocket(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var server *http.Server
	var address string
	errCh := make(chan error, 1)

	stopServer := func() {
		if server != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = server.Shutdown(shutdownCtx)
			cancel()
			server = nil
			address = ""
		}
		s.closeWebSocketClients()
	}

	update := func() {
		cfg := s.store.Get().Output.WebSocket
		desired := ""
		if cfg.Enabled {
			desired = fmt.Sprintf("0.0.0.0:%d", cfg.Port)
		}

		if desired == address {
			return
		}

		stopServer()
		if desired == "" {
			return
		}

		ln, err := net.Listen("tcp", desired)
		if err != nil {
			s.AddLog("error", "WebSocket listener: "+err.Error())
			return
		}

		srv := &http.Server{
			Handler:           s.WebSocketHandler(),
			ReadHeaderTimeout: 5 * time.Second,
		}
		server = srv
		address = desired

		go func() {
			s.AddLog("info", "WebSocket listener starting on "+desired)
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				select {
				case errCh <- fmt.Errorf("%s: %w", desired, err):
				default:
				}
			}
		}()
	}

	update()
	for {
		select {
		case <-ctx.Done():
			stopServer()
			return
		case err := <-errCh:
			s.AddLog("error", "WebSocket listener stopped: "+err.Error())
			if server != nil {
				server = nil
				address = ""
				s.closeWebSocketClients()
			}
		case <-ticker.C:
			update()
		}
	}
}

func (s *Server) closeWebSocketClients() {
	s.wsMu.Lock()
	clients := make([]*wsClient, 0, len(s.wsClients))
	for client := range s.wsClients {
		clients = append(clients, client)
		delete(s.wsClients, client)
	}
	s.wsMu.Unlock()

	for _, client := range clients {
		client.mu.Lock()
		_ = client.conn.Close()
		client.mu.Unlock()
	}
}

func (s *Server) websocketHandler(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()

	authenticated := s.isAuthenticated(r)
	token := cfg.Output.WebSocket.Token
	tokenAuthenticated := token != "" &&
		r.Header.Get("Authorization") == "Bearer "+token

	if !authenticated && !tokenAuthenticated {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !cfg.Output.WebSocket.Enabled {
		http.Error(w, "WebSocket output disabled", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path != "/ws" {
		http.NotFound(w, r)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &wsClient{conn: conn}
	s.wsMu.Lock()
	if s.wsClients == nil {
		s.wsClients = make(map[*wsClient]struct{})
	}
	s.wsClients[client] = struct{}{}
	s.wsMu.Unlock()

	defer func() {
		s.wsMu.Lock()
		delete(s.wsClients, client)
		s.wsMu.Unlock()
		client.mu.Lock()
		_ = conn.Close()
		client.mu.Unlock()
	}()

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}
