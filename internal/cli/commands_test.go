package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/pkg/types"
)

// setupFakeHome points HOME at a temp dir with ~/.rotaze created and
// registers cleanup. Use before any test that touches daemon PID files.
func setupFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".rotaze"), 0700); err != nil {
		t.Fatalf("MkdirAll .rotaze: %v", err)
	}
	return home
}

// setupGlobalCache wires globalCache (used by the cobra commands) to a
// file-backed test DB and registers cleanup. File-backed because the same
// cache instance is shared across command invocations in-process.
func setupGlobalCache(t *testing.T) *cache.Cache {
	t.Helper()
	c, err := cache.NewCache(filepath.Join(t.TempDir(), "cli.db"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	prev := globalCache
	globalCache = c
	t.Cleanup(func() { globalCache = prev })
	return c
}

// seedNode inserts a node and returns its ID.
func seedNode(t *testing.T, c *cache.Cache, n *types.Node) int {
	t.Helper()
	if err := c.SaveNodes([]*types.Node{n}); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	nodes, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("no nodes after SaveNodes")
	}
	// Return the ID of the node matching server:port
	for _, got := range nodes {
		if got.Server == n.Server && got.Port == n.Port {
			return got.ID
		}
	}
	return nodes[len(nodes)-1].ID
}

func runCmd(t *testing.T, cmd *cobra.Command, args ...string) string {
	t.Helper()
	// Commands print via fmt.Printf (os.Stdout), so capture that.
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	cmd.SetArgs(args)
	execErr := cmd.Execute()

	w.Close()
	os.Stdout = oldStdout
	var out bytes.Buffer
	_, _ = io.Copy(&out, r)
	r.Close()

	if execErr != nil {
		t.Fatalf("command failed: %v\noutput: %s", execErr, out.String())
	}
	return out.String()
}

func TestLockShow_Unlocked(t *testing.T) {
	c := setupGlobalCache(t)
	_ = c
	out := runCmd(t, newLockShowCmd())
	if !strings.Contains(out, "UNLOCKED") {
		t.Errorf("expected UNLOCKED, got: %s", out)
	}
}

func TestLockOnOff_RoundTrip(t *testing.T) {
	c := setupGlobalCache(t)
	id := seedNode(t, c, &types.Node{
		Server: "1.2.3.4", Port: 443, Protocol: "ss", Country: "VN", Tag: "t1",
	})
	if err := c.SetCurrentNode(id); err != nil {
		t.Fatalf("SetCurrentNode: %v", err)
	}

	out := runCmd(t, newLockOnCmd())
	if !strings.Contains(out, "Locked to 1.2.3.4:443") {
		t.Errorf("lock on output: %s", out)
	}

	locked, _ := c.IsNodeLocked()
	if !locked {
		t.Error("node should be locked after lock on")
	}

	out = runCmd(t, newLockShowCmd())
	if !strings.Contains(out, "DISABLED") {
		t.Errorf("lock show while locked: %s", out)
	}

	out = runCmd(t, newLockOffCmd())
	if !strings.Contains(out, "Unlocked") {
		t.Errorf("lock off output: %s", out)
	}

	locked, _ = c.IsNodeLocked()
	if locked {
		t.Error("node should be unlocked after lock off")
	}
}

func TestLockOn_NoCurrentNode(t *testing.T) {
	setupGlobalCache(t)
	cmd := newLockOnCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	if err == nil {
		t.Error("lock on with no current node should fail")
	} else if !strings.Contains(err.Error(), "no current node") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPoolCount_EmptyPool(t *testing.T) {
	setupGlobalCache(t)
	out := runCmd(t, newPoolCountCmd())
	if !strings.Contains(out, "nodes") {
		t.Errorf("pool count output: %s", out)
	}
}

func TestPoolCount_WithNodes(t *testing.T) {
	c := setupGlobalCache(t)
	seedNode(t, c, &types.Node{Server: "5.6.7.8", Port: 80, Protocol: "ss", Country: "US", Tag: "t2"})
	out := runCmd(t, newPoolCountCmd())
	if !strings.Contains(out, "1 nodes") && !strings.Contains(out, "nodes") {
		t.Errorf("pool count output missing node count: %s", out)
	}
}

func TestPoolStats(t *testing.T) {
	c := setupGlobalCache(t)
	seedNode(t, c, &types.Node{Server: "5.6.7.8", Port: 80, Protocol: "ss", Country: "US", Tag: "t3"})
	out := runCmd(t, newPoolStatsCmd())
	if !strings.Contains(out, "ss") {
		t.Errorf("pool stats should show protocol breakdown, got: %s", out)
	}
}

func TestPoolDedup_NoDuplicates(t *testing.T) {
	c := setupGlobalCache(t)
	seedNode(t, c, &types.Node{Server: "9.9.9.9", Port: 443, Protocol: "vless", Country: "DE", Tag: "d1"})
	out := runCmd(t, newPoolDedupCmd())
	if !strings.Contains(out, "No duplicates") && !strings.Contains(out, "duplicates") {
		t.Errorf("pool dedup output: %s", out)
	}
}

func TestPoolExport_URI(t *testing.T) {
	c := setupGlobalCache(t)
	seedNode(t, c, &types.Node{
		Server: "10.0.0.1", Port: 8388, Protocol: "ss",
		EncryptMethod: "aes-256-gcm", Password: "testpw", Country: "JP", Tag: "exp1",
	})
	out := runCmd(t, newPoolExportCmd(), "--format", "uri")
	if !strings.Contains(out, "ss://") {
		t.Errorf("pool export uri should contain ss:// URIs, got: %s", out)
	}
}

func TestDaemon_PIDFileRoundTrip(t *testing.T) {
	// Point HOME at a temp dir so we never touch the real ~/.rotaze
	setupFakeHome(t)

	// No PID file yet
	if _, err := readPIDFile(); err == nil {
		t.Error("readPIDFile should fail with no PID file")
	}

	// Write a PID
	if err := writePIDFile(12345); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	pid, err := readPIDFile()
	if err != nil {
		t.Fatalf("readPIDFile: %v", err)
	}
	if pid != 12345 {
		t.Errorf("pid = %d, want 12345", pid)
	}

	// Invalid content
	if err := os.WriteFile(getDaemonPIDPath(), []byte("not-a-pid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPIDFile(); err == nil {
		t.Error("readPIDFile should fail on invalid content")
	}

	// Cleanup
	removePIDFile()
	if _, err := readPIDFile(); err == nil {
		t.Error("readPIDFile should fail after removePIDFile")
	}
}

func TestDaemon_IsProcessRunning(t *testing.T) {
	// Current process is always running
	if !isProcessRunning(os.Getpid()) {
		t.Error("isProcessRunning(self) should be true")
	}
	// A PID that cannot exist (on Linux PIDs wrap well below 4M)
	if isProcessRunning(4194300) {
		t.Error("isProcessRunning(unlikely PID) should be false")
	}
}

func TestDaemonStatus_NoPIDFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setupGlobalCache(t)
	out := runCmd(t, newDaemonStatusCmd())
	if !strings.Contains(out, "NOT RUNNING") {
		t.Errorf("daemon status with no PID file should print NOT RUNNING, got: %s", out)
	}
}

func TestDaemonStop_NoPIDFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := newDaemonStopCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err == nil {
		t.Error("daemon stop with no PID file should fail")
	}
}

func TestDaemonStatus_StalePID(t *testing.T) {
	setupFakeHome(t)
	setupGlobalCache(t)
	// Write a PID that cannot be running
	if err := writePIDFile(4194300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(removePIDFile)
	out := runCmd(t, newDaemonStatusCmd())
	if !strings.Contains(out, "stale") && !strings.Contains(out, "NOT RUNNING") {
		t.Errorf("daemon status with dead PID should report stale, got: %s", out)
	}
	// Stale PID file should have been removed
	if _, err := readPIDFile(); err == nil {
		t.Error("stale PID file should have been removed")
	}
}

func TestInteractive_HelpAndQuit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setupGlobalCache(t)

	// Drive the REPL via stdin: 'help' then 'exit'
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	w.WriteString("help\nexit\n")
	w.Close()
	os.Stdin = r

	oldStdout := os.Stdout
	outr, outw, _ := os.Pipe()
	os.Stdout = outw

	cmd := newInteractiveCmd()
	cmd.SetArgs([]string{})
	_ = cmd.Execute()

	outw.Close()
	os.Stdout = oldStdout
	os.Stdin = oldStdin

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, outr)
	r.Close()
	outr.Close()

	if !strings.Contains(buf.String(), "Available commands") {
		t.Errorf("interactive help output missing, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "Goodbye") {
		t.Errorf("interactive should exit cleanly, got: %s", buf.String())
	}
}

func TestPoolExport_JSON(t *testing.T) {
	c := setupGlobalCache(t)
	seedNode(t, c, &types.Node{Server: "10.0.0.2", Port: 443, Protocol: "trojan", Password: "x", Country: "KR", Tag: "exp2"})
	out := runCmd(t, newPoolExportCmd(), "--format", "json")
	if !strings.Contains(out, "\"server\"") {
		t.Errorf("pool export json should contain node objects, got: %s", out)
	}
}
