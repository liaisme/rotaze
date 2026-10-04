package cli

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/pkg/types"
)

// seedStats describes the check history to replay for one node. SuccessRate
// in the DB is derived from checks/failures, so a rate is expressed as counts.
type seedStats struct {
	latencyMS int64
	checks    int
	failures  int
}

// newSortTestCache returns a cache seeded with nodes plus per-node check
// history. A nil *seedStats entry means the node has no stats row at all.
func newSortTestCache(t *testing.T, nodes []*types.Node, seeds map[int]*seedStats) *cache.Cache {
	t.Helper()
	c, err := cache.NewCache(filepath.Join(t.TempDir(), "sort.db"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if err := c.SaveNodes(nodes); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}
	saved, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	byServer := make(map[string]int, len(saved))
	for _, n := range saved {
		byServer[n.Server] = n.ID
	}
	for i, s := range seeds {
		if s == nil {
			continue
		}
		id, ok := byServer[nodes[i].Server]
		if !ok {
			t.Fatalf("node %q missing after SaveNodes", nodes[i].Server)
		}
		for k := range s.checks {
			ok := k < s.checks-s.failures
			if err := c.UpdateNodeStats(id, s.latencyMS, ok); err != nil {
				t.Fatalf("UpdateNodeStats: %v", err)
			}
		}
	}
	return c
}
func serversOf(nodes []*types.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Server
	}
	return out
}

func assertOrder(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %d nodes %v, want %d %v", label, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: order[%d] = %q, want %q (full: %v)", label, i, got[i], want[i], got)
		}
	}
}

// TestSortNodesLatencyFastestFirst pins the latency ordering contract:
// ascending recorded latency, nodes without stats last.
func TestSortNodesLatencyFastestFirst(t *testing.T) {
	nodes := []*types.Node{
		{Server: "slow.example.com", Port: 1, Protocol: "ss"},
		{Server: "fast.example.com", Port: 2, Protocol: "ss"},
		{Server: "nostats.example.com", Port: 3, Protocol: "ss"},
	}
	c := newSortTestCache(t, nodes, map[int]*seedStats{
		0: {latencyMS: 300, checks: 2},
		1: {latencyMS: 100, checks: 2},
		// 2: no stats row -> must sort last
	})

	pool, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	sortNodes(pool, "latency", c)

	assertOrder(t, "latency", serversOf(pool),
		[]string{"fast.example.com", "slow.example.com", "nostats.example.com"})
}

// TestSortNodesSuccessHighestFirst pins the success ordering contract:
// descending success rate, nodes without stats (rate 0) last.
func TestSortNodesSuccessHighestFirst(t *testing.T) {
	nodes := []*types.Node{
		{Server: "mid.example.com", Port: 1, Protocol: "ss"},
		{Server: "best.example.com", Port: 2, Protocol: "ss"},
		{Server: "nostats.example.com", Port: 3, Protocol: "ss"},
	}
	c := newSortTestCache(t, nodes, map[int]*seedStats{
		0: {latencyMS: 100, checks: 10, failures: 5}, // 0.5
		1: {latencyMS: 100, checks: 10, failures: 1}, // 0.9
		// 2: no stats row -> rate 0 -> must sort last
	})

	pool, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	sortNodes(pool, "success", c)

	assertOrder(t, "success", serversOf(pool),
		[]string{"best.example.com", "mid.example.com", "nostats.example.com"})
}

// TestSortNodesDefaultLeavesOrder guards that the "id" default is a no-op
// (the DB already returns rows ordered by id).
func TestSortNodesDefaultLeavesOrder(t *testing.T) {
	nodes := []*types.Node{
		{Server: "a.example.com", Port: 1, Protocol: "ss"},
		{Server: "b.example.com", Port: 2, Protocol: "ss"},
		{Server: "c.example.com", Port: 3, Protocol: "ss"},
	}
	c := newSortTestCache(t, nodes, map[int]*seedStats{
		0: {latencyMS: 300, checks: 1},
		1: {latencyMS: 100, checks: 1},
		2: {latencyMS: 200, checks: 1},
	})

	pool, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	before := serversOf(pool)
	sortNodes(pool, "id", c)
	assertOrder(t, "id", serversOf(pool), before)
}

// TestSortNodesLargePoolIsFast guards the performance contract. Sorting used
// to be a bubble sort that issued two DB queries per comparison, which took
// over a minute on a real 8.5k-node pool; the bulk-stats version must stay
// well under the budget here. Nodes are seeded without stats rows on
// purpose: the old code queried once per comparison whether or not a row
// existed, so this still catches a return to per-comparison lookups while
// keeping setup cheap.
func TestSortNodesLargePoolIsFast(t *testing.T) {
	const n = 4000
	nodes := make([]*types.Node, n)
	for i := range n {
		nodes[i] = &types.Node{
			Server:   "n" + strconv.Itoa(i) + ".example.com",
			Port:     1000 + i%60000,
			Protocol: "ss",
		}
	}
	c, err := cache.NewCache(filepath.Join(t.TempDir(), "big.db"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.SaveNodes(nodes); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}

	pool, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(pool) != n {
		t.Fatalf("seeded %d nodes, got %d", n, len(pool))
	}

	start := time.Now()
	sortNodes(pool, "latency", c)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("sorting %d nodes took %v, want < 5s", n, elapsed)
	}
}

func TestValidateSort(t *testing.T) {
	for _, ok := range []string{"id", "latency", "success", "random"} {
		if err := validateSort(ok); err != nil {
			t.Errorf("validateSort(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"bogus", "", "LATENCY", "lat"} {
		if err := validateSort(bad); err == nil {
			t.Errorf("validateSort(%q) = nil, want error", bad)
		}
	}
}

func TestValidateProtocol(t *testing.T) {
	for _, ok := range []string{"", "all", "ss", "vless", "vmess", "trojan", "hy2", "socks", "http", "ovpn", "openvpn", "SS"} {
		if err := validateProtocol(ok); err != nil {
			t.Errorf("validateProtocol(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"bogus", "shadowsocks", "open-vpn", "ss "} {
		if err := validateProtocol(bad); err == nil {
			t.Errorf("validateProtocol(%q) = nil, want error", bad)
		}
	}
}
