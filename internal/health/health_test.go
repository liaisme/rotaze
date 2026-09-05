package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("New() returned nil")
	}
}

func TestServer_StartStop(t *testing.T) {
	s := New()
	ctx := context.Background()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer s.Stop(ctx)

	if s.GetPort() == 0 {
		t.Error("port should be non-zero after start")
	}
}

func TestServer_SetStatus(t *testing.T) {
	s := New()
	s.SetStatus("degraded")
	// No panic or error expected
}

func TestServer_NewWithPort_StartStop(t *testing.T) {
	const wantPort = 18999
	s := NewWithPort(wantPort)
	ctx := context.Background()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer s.Stop(ctx)

	if s.GetPort() != wantPort {
		t.Errorf("port = %d, want %d (configured port must be honored)", s.GetPort(), wantPort)
	}

	// Endpoint must respond on the exact configured port
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/health", wantPort))
	if err != nil {
		t.Fatalf("GET /health on configured port: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_NewWithPort_ConflictFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	taken := ln.Addr().(*net.TCPAddr).Port

	s := NewWithPort(taken)
	if err := s.Start(context.Background()); err == nil {
		t.Errorf("Start() on occupied port %d should fail", taken)
		s.Stop(context.Background())
	}
}

func TestServer_SetNodeInfo(t *testing.T) {
	s := New()
	s.SetNodeInfo(map[string]interface{}{
		"server": "1.2.3.4",
		"port":   443,
	})
}

func TestServer_HealthEndpoint(t *testing.T) {
	s := New()
	ctx := context.Background()
	s.Start(ctx)
	defer s.Stop(ctx)

	resp, err := http.Get("http://127.0.0.1:" + itoa(s.GetPort()) + "/health")
	if err != nil {
		t.Fatalf("GET /health error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)

	if body["status"] != "healthy" {
		t.Errorf("status = %v, want healthy", body["status"])
	}
}

func TestServer_HealthEndpoint_Unhealthy(t *testing.T) {
	s := New()
	ctx := context.Background()
	s.Start(ctx)
	defer s.Stop(ctx)
	s.SetStatus("degraded")

	resp, err := http.Get("http://127.0.0.1:" + itoa(s.GetPort()) + "/health")
	if err != nil {
		t.Fatalf("GET /health error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestServer_ReadinessEndpoint(t *testing.T) {
	s := New()
	ctx := context.Background()
	s.Start(ctx)
	defer s.Stop(ctx)

	resp, err := http.Get("http://127.0.0.1:" + itoa(s.GetPort()) + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)

	if body["ready"] != true {
		t.Errorf("ready = %v, want true", body["ready"])
	}
}

func TestServer_LivenessEndpoint(t *testing.T) {
	s := New()
	ctx := context.Background()
	s.Start(ctx)
	defer s.Stop(ctx)

	resp, err := http.Get("http://127.0.0.1:" + itoa(s.GetPort()) + "/health/live")
	if err != nil {
		t.Fatalf("GET /health/live error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&body)

	if body["alive"] != true {
		t.Errorf("alive = %v, want true", body["alive"])
	}
}

func TestServer_StopWithoutStart(t *testing.T) {
	s := New()
	// Stop without start should not panic
	s.Stop(context.Background())
}

func TestServer_StopTimeout(t *testing.T) {
	s := New()
	ctx := context.Background()
	s.Start(ctx)

	shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(2 * time.Millisecond) // Let timeout expire

	// Should still succeed or at least not hang
	s.Stop(shortCtx)
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
