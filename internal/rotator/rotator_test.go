package rotator

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/rotaze/rotaze/pkg/types"
)

func newTestRotator(t *testing.T, port int) *Rotator {
	t.Helper()
	return New(zap.NewNop(), &Config{
		ListenAddr: "127.0.0.1",
		ListenPort: port,
		DataDir:    t.TempDir(),
	})
}

func testNode(protocol string) *types.Node {
	return &types.Node{
		ID:       1,
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: protocol,
		Tag:      "test-" + protocol,
	}
}

func TestRotator_New(t *testing.T) {
	r := newTestRotator(t, 1080)
	if r == nil {
		t.Fatal("New() returned nil")
	}
	if r.listenAddr != "127.0.0.1" {
		t.Errorf("listenAddr = %q", r.listenAddr)
	}
	if r.listenPort != 1080 {
		t.Errorf("listenPort = %d", r.listenPort)
	}
	if r.IsRunning() {
		t.Error("fresh rotator should not be running")
	}
}

func TestRotator_GetListenAddr(t *testing.T) {
	r := New(zap.NewNop(), &Config{ListenAddr: "127.0.0.1", ListenPort: 9999})
	if got := r.GetListenAddr(); got != "127.0.0.1:9999" {
		t.Errorf("GetListenAddr() = %q, want 127.0.0.1:9999", got)
	}
}

func TestRotator_Start_NoDataDir(t *testing.T) {
	// DataDir points at a path that cannot be created (file instead of dir)
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	r := New(zap.NewNop(), &Config{
		ListenAddr: "127.0.0.1",
		ListenPort: 1080,
		DataDir:    filepath.Join(blocker, "sub"),
	})
	err := r.Start(context.Background(), testNode("ss"))
	if err == nil {
		t.Error("Start with unwritable DataDir should fail")
		r.Close()
	}
}

func TestRotator_Start_SingboxMissing(t *testing.T) {
	// findSingbox checks bare PATH names plus absolute locations including
	// $HOME/.local/bin/sing-box. Neutralize both: empty PATH and a fake HOME
	// pointing at an empty temp dir. Absolute /usr/local/bin, /usr/bin paths
	// can only "fail" the test if sing-box actually lives there; in that
	// case we skip since we cannot fake them.
	t.Setenv("PATH", "")
	t.Setenv("HOME", t.TempDir())
	for _, loc := range []string{"/usr/local/bin/sing-box", "/usr/bin/sing-box"} {
		if _, err := os.Stat(loc); err == nil {
			t.Skipf("sing-box exists at %s; cannot simulate absence", loc)
		}
	}
	r := newTestRotator(t, 1080)
	err := r.Start(context.Background(), testNode("ss"))
	if err == nil {
		t.Error("Start without sing-box available should fail")
		r.Close()
	}
	if r.IsRunning() {
		t.Error("rotator should not be running after failed start")
	}
}

func TestRotator_Close_NotRunning(t *testing.T) {
	r := newTestRotator(t, 1080)
	// Close on non-started rotator should not error or panic
	if err := r.Close(); err != nil {
		t.Errorf("Close() on idle rotator returned error: %v", err)
	}
}

func TestRotator_Lifecycle_WithRealSingbox(t *testing.T) {
	// Skip if sing-box is not available on this machine
	if _, err := findSingbox(); err != nil {
		t.Skip("sing-box not installed; skipping lifecycle test")
	}

	r := newTestRotator(t, 19080)
	ctx := context.Background()

	node := testNode("http")
	node.Server = "127.0.0.1"
	node.Port = 1 // dead upstream, but sing-box itself should start

	if err := r.Start(ctx, node); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !r.IsRunning() {
		t.Error("IsRunning should be true after Start")
	}
	if got := r.GetListenAddr(); got != "127.0.0.1:19080" {
		t.Errorf("GetListenAddr = %q", got)
	}

	if err := r.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if r.IsRunning() {
		t.Error("IsRunning should be false after Close")
	}

	// Double close should be safe
	if err := r.Close(); err != nil {
		t.Errorf("double Close returned error: %v", err)
	}
}

func TestRotator_SwitchOutbound_Lifecycle(t *testing.T) {
	if _, err := findSingbox(); err != nil {
		t.Skip("sing-box not installed; skipping switch test")
	}

	r := newTestRotator(t, 19081)
	ctx := context.Background()

	first := testNode("http")
	first.Server = "127.0.0.1"
	first.Port = 1
	first.Tag = "switch-first"

	if err := r.Start(ctx, first); err != nil {
		t.Fatalf("Start: %v", err)
	}

	second := testNode("http")
	second.Server = "127.0.0.2"
	second.Port = 2
	second.Tag = "switch-second"

	if err := r.SwitchOutbound(ctx, second); err != nil {
		t.Fatalf("SwitchOutbound: %v", err)
	}
	if !r.IsRunning() {
		t.Error("rotator should be running after SwitchOutbound")
	}

	if err := r.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if r.IsRunning() {
		t.Error("rotator should not be running after Close")
	}
}

func TestEnsurePortFree_Free(t *testing.T) {
	// Grab a free port by listening, then releasing it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	if err := ensurePortFree("127.0.0.1", port); err != nil {
		t.Errorf("ensurePortFree on free port %d: %v", port, err)
	}
}

func TestEnsurePortFree_Occupied(t *testing.T) {
	// Hold the port with our own listener; even after the stale-process
	// cleanup runs, the probe must still fail and return a clear error.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	if err := ensurePortFree("127.0.0.1", port); err == nil {
		t.Error("expected error when port is held by another process, got nil")
	}
}

func TestFindSingbox_PresentOrError(t *testing.T) {
	path, err := findSingbox()
	if err != nil {
		t.Skip("sing-box not installed on this machine")
	}
	if path == "" {
		t.Error("found path should not be empty")
	}
}
