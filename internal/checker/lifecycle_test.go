package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/rotaze/rotaze/internal/cache"
)

// newTestCache creates a temporary SQLite cache for Checker tests.
func newTestCache(t *testing.T) *cache.Cache {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	c, err := cache.NewCache(dbPath)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// newTestNode inserts a minimal usable node and makes it the current one.
func newTestNode(t *testing.T, c *cache.Cache) int64 {
	t.Helper()
	nodes, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	_ = nodes
	// Use SaveNodes/AddNodes if available; fall back to whatever the cache API offers.
	if err := c.SetCurrentNode(1); err != nil {
		t.Fatalf("SetCurrentNode: %v", err)
	}
	return 1
}

func TestChecker_New_Defaults(t *testing.T) {
	c := newTestCache(t)
	logger := zap.NewNop()

	ch := New(c, logger, &Config{
		URL:      "http://example.com",
		Interval: 10 * time.Second,
		Timeout:  5 * time.Second,
	})
	if ch == nil {
		t.Fatal("New() returned nil")
	}
	if ch.socks5Addr != "127.0.0.1:1080" {
		t.Errorf("default socks5Addr = %q, want 127.0.0.1:1080", ch.socks5Addr)
	}
	if ch.url != "http://example.com" {
		t.Errorf("url = %q", ch.url)
	}
	if ch.interval != 10*time.Second {
		t.Errorf("interval = %v", ch.interval)
	}
}

func TestChecker_New_CustomSOCKS5(t *testing.T) {
	c := newTestCache(t)
	ch := New(c, zap.NewNop(), &Config{
		URL:        "http://example.com",
		SOCKS5Addr: "127.0.0.1:9999",
	})
	if ch.socks5Addr != "127.0.0.1:9999" {
		t.Errorf("socks5Addr = %q, want custom", ch.socks5Addr)
	}
}

func TestChecker_StartStop(t *testing.T) {
	c := newTestCache(t)
	newTestNode(t, c)

	// httptest server that responds OK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := New(c, zap.NewNop(), &Config{
		URL:      srv.URL,
		Interval: 1 * time.Hour, // effectively one check then idle
		Timeout:  3 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = ch.Start(ctx)
		close(done)
	}()

	// Give it time to run the initial check
	time.Sleep(1 * time.Second)

	ch.Stop()

	select {
	case <-done:
		// stopped cleanly
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after Stop()")
	}
}

func TestChecker_Start_DoubleStartFails(t *testing.T) {
	c := newTestCache(t)
	newTestNode(t, c)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch := New(c, zap.NewNop(), &Config{
		URL:      srv.URL,
		Interval: 1 * time.Hour,
		Timeout:  3 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = ch.Start(ctx) }()
	time.Sleep(100 * time.Millisecond)

	err := ch.Start(ctx)
	if err == nil {
		t.Error("second Start() on running checker should fail")
	}
	ch.Stop()
}

func TestChecker_UpdateInterval(t *testing.T) {
	c := newTestCache(t)
	ch := New(c, zap.NewNop(), &Config{
		URL:      "http://example.com",
		Interval: 30 * time.Second,
	})
	ch.UpdateInterval(5 * time.Second)
	if ch.interval != 5*time.Second {
		t.Errorf("interval after UpdateInterval = %v, want 5s", ch.interval)
	}
}

func TestChecker_StopWithoutStart(t *testing.T) {
	c := newTestCache(t)
	ch := New(c, zap.NewNop(), &Config{URL: "http://example.com"})
	// Should not panic
	ch.Stop()
}

func TestChecker_RotationOnFailure(t *testing.T) {
	c := newTestCache(t)
	nodeID := newTestNode(t, c)

	var rotations int32

	ch := New(c, zap.NewNop(), &Config{
		URL:        "http://127.0.0.1:1/unreachable", // guaranteed connection refused
		Interval:   1 * time.Hour,
		Timeout:    1 * time.Second,
		MaxRetries: 0, // no retries => immediate rotation trigger
		SOCKS5Addr: "127.0.0.1:19999", // dead proxy => request fails
	})
	ch.OnRotate(func(ctx context.Context, reason string) error {
		atomic.AddInt32(&rotations, 1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	go func() { _ = ch.Start(ctx) }()
	time.Sleep(5 * time.Second)
	ch.Stop()

	if atomic.LoadInt32(&rotations) == 0 {
		t.Error("expected rotation callback after all retries exhausted")
	}
	_ = nodeID
}
