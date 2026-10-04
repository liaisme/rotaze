package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/pkg/types"
)

// captureOutput runs fn while capturing os.Stdout and returns what was printed.
func captureOutput(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = oldStdout
	var out bytes.Buffer
	_, _ = io.Copy(&out, r)
	r.Close()
	return out.String()
}

func TestHandleListCommand_WithNodes(t *testing.T) {
	c := setupGlobalCache(t)
	seedNode(t, c, &types.Node{Server: "1.1.1.1", Port: 8080, Protocol: "ss", Country: "VN", Tag: "a"})
	seedNode(t, c, &types.Node{Server: "2.2.2.2", Port: 1080, Protocol: "http", Country: "SG", Tag: "b"})

	out := captureOutput(t, func() { handleListCommand(c, nil) })

	if !strings.Contains(out, "1.1.1.1") || !strings.Contains(out, "2.2.2.2") {
		t.Errorf("expected both servers listed, got: %s", out)
	}
	if !strings.Contains(out, "Total: 2 nodes") {
		t.Errorf("expected total count, got: %s", out)
	}
	if !strings.Contains(out, "ID") || !strings.Contains(out, "Server") {
		t.Errorf("expected table header, got: %s", out)
	}
}

func TestHandleListCommand_Empty(t *testing.T) {
	c := setupGlobalCache(t)
	out := captureOutput(t, func() { handleListCommand(c, nil) })
	if !strings.Contains(out, "No nodes in pool") {
		t.Errorf("expected empty-pool message, got: %s", out)
	}
}

// Regression: the REPL help advertised `list --country VN` while the handler
// ignored every argument and always printed the whole pool.
func TestHandleListCommand_Filters(t *testing.T) {
	newCache := func(t *testing.T) *cache.Cache {
		c := setupGlobalCache(t)
		seedNode(t, c, &types.Node{Server: "1.1.1.1", Port: 8080, Protocol: "ss", Country: "VN", Tag: "a"})
		seedNode(t, c, &types.Node{Server: "2.2.2.2", Port: 1080, Protocol: "http", Country: "SG", Tag: "b"})
		return c
	}

	t.Run("country", func(t *testing.T) {
		c := newCache(t)
		out := captureOutput(t, func() { handleListCommand(c, []string{"--country", "vn"}) })
		if !strings.Contains(out, "1.1.1.1") {
			t.Errorf("expected VN node listed, got: %s", out)
		}
		if strings.Contains(out, "2.2.2.2") {
			t.Errorf("expected SG node filtered out, got: %s", out)
		}
		if !strings.Contains(out, "Total: 1 nodes") {
			t.Errorf("expected filtered total, got: %s", out)
		}
	})

	t.Run("country no match", func(t *testing.T) {
		c := newCache(t)
		out := captureOutput(t, func() { handleListCommand(c, []string{"--country", "JP"}) })
		if !strings.Contains(out, `No nodes matching country "JP"`) {
			t.Errorf("expected no-match message, got: %s", out)
		}
	})

	t.Run("limit", func(t *testing.T) {
		c := newCache(t)
		out := captureOutput(t, func() { handleListCommand(c, []string{"--limit", "1"}) })
		if !strings.Contains(out, "Showing 1 of 2 nodes") {
			t.Errorf("expected truncation notice, got: %s", out)
		}
	})
}

func TestHandleUseCommand(t *testing.T) {
	t.Run("invalid ID", func(t *testing.T) {
		c := setupGlobalCache(t)
		out := captureOutput(t, func() { handleUseCommand(c, "abc") })
		if !strings.Contains(out, "Invalid node ID") {
			t.Errorf("expected invalid ID error, got: %s", out)
		}
	})

	t.Run("not found", func(t *testing.T) {
		c := setupGlobalCache(t)
		out := captureOutput(t, func() { handleUseCommand(c, "999") })
		if !strings.Contains(out, "Node not found") {
			t.Errorf("expected not-found error, got: %s", out)
		}
	})

	t.Run("success records rotation", func(t *testing.T) {
		c := setupGlobalCache(t)
		id := seedNode(t, c, &types.Node{Server: "3.3.3.3", Port: 443, Protocol: "trojan", Country: "VN", Tag: "x"})

		out := captureOutput(t, func() { handleUseCommand(c, "1") })

		if !strings.Contains(out, "Switched to node") || !strings.Contains(out, "3.3.3.3:443") {
			t.Errorf("expected success output, got: %s", out)
		}
		cur, err := c.GetCurrentNode()
		if err != nil || cur != id {
			t.Errorf("current node should be %d, got %d (err=%v)", id, cur, err)
		}
	})
}

func TestHandleLockCommand(t *testing.T) {
	t.Run("on with no current node", func(t *testing.T) {
		c := setupGlobalCache(t)
		out := captureOutput(t, func() { handleLockCommand(c, "on") })
		if !strings.Contains(out, "No current node to lock to") {
			t.Errorf("expected no-node error, got: %s", out)
		}
	})

	t.Run("on and off round trip", func(t *testing.T) {
		c := setupGlobalCache(t)
		id := seedNode(t, c, &types.Node{Server: "4.4.4.4", Port: 80, Protocol: "ss", Country: "VN", Tag: "l"})
		if err := c.SetCurrentNode(id); err != nil {
			t.Fatalf("SetCurrentNode: %v", err)
		}

		out := captureOutput(t, func() { handleLockCommand(c, "on") })
		if !strings.Contains(out, "Locked") {
			t.Errorf("expected locked confirmation, got: %s", out)
		}
		if locked, _ := c.IsNodeLocked(); !locked {
			t.Error("node should be locked")
		}

		out = captureOutput(t, func() { handleLockCommand(c, "off") })
		if !strings.Contains(out, "Unlocked") {
			t.Errorf("expected unlocked confirmation, got: %s", out)
		}
		if locked, _ := c.IsNodeLocked(); locked {
			t.Error("node should be unlocked")
		}
	})

	t.Run("invalid action", func(t *testing.T) {
		c := setupGlobalCache(t)
		out := captureOutput(t, func() { handleLockCommand(c, "maybe") })
		if !strings.Contains(out, "Usage: lock on|off") {
			t.Errorf("expected usage message, got: %s", out)
		}
	})
}

func TestHandleStatusCommand(t *testing.T) {
	t.Run("no current node", func(t *testing.T) {
		c := setupGlobalCache(t)
		out := captureOutput(t, func() { handleStatusCommand(c) })
		if !strings.Contains(out, "Current: (none)") {
			t.Errorf("expected (none), got: %s", out)
		}
		if !strings.Contains(out, "UNLOCKED") {
			t.Errorf("expected UNLOCKED, got: %s", out)
		}
	})

	t.Run("with node and lock", func(t *testing.T) {
		c := setupGlobalCache(t)
		id := seedNode(t, c, &types.Node{Server: "5.5.5.5", Port: 8443, Protocol: "vless", Country: "VN", Tag: "s"})
		if err := c.SetCurrentNode(id); err != nil {
			t.Fatalf("SetCurrentNode: %v", err)
		}
		if err := c.LockNode(id); err != nil {
			t.Fatalf("LockNode: %v", err)
		}

		out := captureOutput(t, func() { handleStatusCommand(c) })
		if !strings.Contains(out, "5.5.5.5:8443") || !strings.Contains(out, "VN") {
			t.Errorf("expected node info, got: %s", out)
		}
		if !strings.Contains(out, "LOCKED") {
			t.Errorf("expected LOCKED, got: %s", out)
		}
		if !strings.Contains(out, "Pool: 1 nodes") {
			t.Errorf("expected pool count, got: %s", out)
		}
	})
}

func TestHandleStatsCommand(t *testing.T) {
	c := setupGlobalCache(t)
	seedNode(t, c, &types.Node{Server: "1.1.1.1", Port: 80, Protocol: "ss", Country: "VN", Tag: "s1"})
	seedNode(t, c, &types.Node{Server: "2.2.2.2", Port: 80, Protocol: "ss", Country: "SG", Tag: "s2"})
	seedNode(t, c, &types.Node{Server: "3.3.3.3", Port: 80, Protocol: "http", Country: "VN", Tag: "s3"})

	out := captureOutput(t, func() { handleStatsCommand(c) })

	if !strings.Contains(out, "Protocol breakdown") || !strings.Contains(out, "Country breakdown") {
		t.Errorf("expected breakdown headers, got: %s", out)
	}
	if !strings.Contains(out, "67%") { // 2/3 ss and VN
		t.Errorf("expected 67%% percentages, got: %s", out)
	}
}

func TestHandleRotate(t *testing.T) {
	t.Run("locked refuses", func(t *testing.T) {
		c := setupGlobalCache(t)
		id := seedNode(t, c, &types.Node{Server: "6.6.6.6", Port: 80, Protocol: "ss", Country: "VN", Tag: "r"})
		if err := c.SetCurrentNode(id); err != nil {
			t.Fatalf("SetCurrentNode: %v", err)
		}
		if err := c.LockNode(id); err != nil {
			t.Fatalf("LockNode: %v", err)
		}

		out := captureOutput(t, func() { handleRotate(c) })
		if !strings.Contains(out, "locked") {
			t.Errorf("expected locked refusal, got: %s", out)
		}
	})

	t.Run("empty pool", func(t *testing.T) {
		c := setupGlobalCache(t)
		out := captureOutput(t, func() { handleRotate(c) })
		if !strings.Contains(out, "No nodes available") {
			t.Errorf("expected no-nodes error, got: %s", out)
		}
	})

	t.Run("success moves to different node", func(t *testing.T) {
		c := setupGlobalCache(t)
		id1 := seedNode(t, c, &types.Node{Server: "7.7.7.7", Port: 80, Protocol: "ss", Country: "VN", Tag: "r1"})
		id2 := seedNode(t, c, &types.Node{Server: "8.8.8.8", Port: 80, Protocol: "http", Country: "SG", Tag: "r2"})
		if err := c.SetCurrentNode(id1); err != nil {
			t.Fatalf("SetCurrentNode: %v", err)
		}

		out := captureOutput(t, func() { handleRotate(c) })
		if !strings.Contains(out, "Rotated to") {
			t.Errorf("expected rotation success, got: %s", out)
		}
		cur, err := c.GetCurrentNode()
		if err != nil {
			t.Fatalf("GetCurrentNode: %v", err)
		}
		if cur == id1 {
			t.Error("rotation should move away from current node")
		}
		if cur != id1 && cur != id2 {
			t.Errorf("current should be one of the seeded nodes, got %d", cur)
		}
	})
}

func TestPrintInteractiveHelp(t *testing.T) {
	out := captureOutput(t, printInteractiveHelp)
	for _, want := range []string{"list", "use <id>", "lock on|off", "status", "stats", "test-url", "rotate", "exit"} {
		if !strings.Contains(out, want) {
			t.Errorf("help should mention %q, got: %s", want, out)
		}
	}
}
