package metrics

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	c := New()
	if c == nil {
		t.Fatal("New() returned nil")
	}
}

func TestIncRotations(t *testing.T) {
	c := New()
	c.IncRotations()
	c.IncRotations()
	c.IncRotations()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "rotaze_rotations_total 3") {
		t.Errorf("expected rotations_total 3 in metrics:\n%s", body)
	}
}

func TestIncChecks(t *testing.T) {
	c := New()
	c.IncChecks(true)
	c.IncChecks(true)
	c.IncChecks(false)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "rotaze_checks_total 3") {
		t.Errorf("expected checks_total 3:\n%s", body)
	}
	if !strings.Contains(body, "rotaze_checks_success_total 2") {
		t.Errorf("expected checks_success_total 2:\n%s", body)
	}
	if !strings.Contains(body, "rotaze_checks_failed_total 1") {
		t.Errorf("expected checks_failed_total 1:\n%s", body)
	}
}

func TestSetNodesFetched(t *testing.T) {
	c := New()
	c.SetNodesFetched(1500)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "rotaze_nodes_fetched_total 1500") {
		t.Errorf("expected nodes_fetched_total 1500:\n%s", body)
	}
}

func TestSetNodesActive(t *testing.T) {
	c := New()
	c.SetNodesActive(500)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "rotaze_nodes_active 500") {
		t.Errorf("expected nodes_active 500:\n%s", body)
	}
}

func TestSetCurrentNode(t *testing.T) {
	c := New()
	c.SetCurrentNode(42, "1.2.3.4", "US")

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "rotaze_current_node_id 42") {
		t.Errorf("expected current_node_id 42:\n%s", body)
	}
	if !strings.Contains(body, `server="1.2.3.4"`) {
		t.Errorf("expected server label:\n%s", body)
	}
	if !strings.Contains(body, `country="US"`) {
		t.Errorf("expected country label:\n%s", body)
	}
}

func TestSetProxyRunning(t *testing.T) {
	c := New()

	c.SetProxyRunning(true)
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "rotaze_proxy_running 1") {
		t.Errorf("expected proxy_running 1:\n%s", body)
	}

	c.SetProxyRunning(false)
	req = httptest.NewRequest("GET", "/metrics", nil)
	w = httptest.NewRecorder()
	c.ServeHTTP(w, req)
	body = w.Body.String()
	if !strings.Contains(body, "rotaze_proxy_running 0") {
		t.Errorf("expected proxy_running 0:\n%s", body)
	}
}

func TestSetCheckInterval(t *testing.T) {
	c := New()
	c.SetCheckInterval(30)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "rotaze_check_interval_seconds 30") {
		t.Errorf("expected check_interval_seconds 30:\n%s", body)
	}
}

func TestMetricsContentType(t *testing.T) {
	c := New()
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %s, want text/plain", ct)
	}
}

func TestMetricsPrometheusFormat(t *testing.T) {
	c := New()
	c.IncRotations()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()

	// Check for Prometheus format elements
	if !strings.Contains(body, "# HELP rotaze_rotations_total") {
		t.Error("missing HELP for rotations_total")
	}
	if !strings.Contains(body, "# TYPE rotaze_rotations_total counter") {
		t.Error("missing TYPE for rotations_total")
	}
}

func TestLastCheckTimestamp(t *testing.T) {
	c := New()
	c.IncChecks(true)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "rotaze_last_check_timestamp") {
		t.Error("missing last_check_timestamp")
	}
	// Should be a non-zero Unix timestamp
	if strings.Contains(body, "rotaze_last_check_timestamp 0") {
		t.Error("last_check_timestamp should not be 0 after check")
	}
}

func TestLastRotationTimestamp(t *testing.T) {
	c := New()
	c.IncRotations()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "rotaze_last_rotation_timestamp") {
		t.Error("missing last_rotation_timestamp")
	}
	if strings.Contains(body, "rotaze_last_rotation_timestamp 0") {
		t.Error("last_rotation_timestamp should not be 0 after rotation")
	}
}

func TestStartMetricsServer(t *testing.T) {
	c := New()
	server, err := StartMetricsServer("127.0.0.1:0", c)
	if err != nil {
		t.Fatalf("StartMetricsServer() error = %v", err)
	}
	defer server.Close()
}
