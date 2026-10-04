// Package health provides a lightweight HTTP health check endpoint.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// Server is a lightweight health check HTTP server.
type Server struct {
	mu       sync.RWMutex
	server   *http.Server
	status   string
	nodeInfo map[string]interface{}
	port     int
}

// New creates a new health server on a random available port.
func New() *Server {
	return &Server{
		status:   "starting",
		nodeInfo: make(map[string]interface{}),
	}
}

// NewWithPort creates a new health server bound to a specific port.
func NewWithPort(port int) *Server {
	return &Server{
		status:   "starting",
		nodeInfo: make(map[string]interface{}),
		port:     port,
	}
}

// Start starts the health check HTTP server.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/health/ready", s.handleReady)
	mux.HandleFunc("/health/live", s.handleLive)

	// Use configured port, or pick a random available one when unset
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	if s.port <= 0 {
		addr = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to find available port: %w", err)
	}
	s.port = listener.Addr().(*net.TCPAddr).Port

	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := s.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Health server error: %v\n", err)
		}
	}()

	s.mu.Lock()
	s.status = "healthy"
	s.mu.Unlock()

	return nil
}

// Stop gracefully shuts down the health server.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.status = "shutting_down"
	s.mu.Unlock()

	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// GetPort returns the port the health server is listening on.
func (s *Server) GetPort() int {
	return s.port
}

// SetStatus updates the health status.
func (s *Server) SetStatus(status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

// SetNodeInfo updates the current node information.
func (s *Server) SetNodeInfo(info map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeInfo = info
}

// handleHealth returns the full health status.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	if s.status != "healthy" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	resp := map[string]interface{}{
		"status":    s.status,
		"timestamp": time.Now().UTC(),
		"version":   "1.0.0",
		"components": map[string]interface{}{
			"proxy": map[string]interface{}{
				"status": s.status,
			},
		},
	}

	if len(s.nodeInfo) > 0 {
		resp["node"] = s.nodeInfo
	}

	json.NewEncoder(w).Encode(resp)
}

// handleReady returns readiness status (for Kubernetes).
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")

	if s.status == "healthy" {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ready":  true,
			"status": s.status,
		})
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ready":  false,
			"status": s.status,
		})
	}
}

// handleLive returns liveness status (for Kubernetes).
func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"alive": true,
	})
}
