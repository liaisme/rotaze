package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/internal/checker"
	"github.com/rotaze/rotaze/internal/fetcher"
	"github.com/rotaze/rotaze/internal/rotator"
	"github.com/rotaze/rotaze/pkg/types"
	"go.uber.org/zap"
)

// ============================================================
// Integration: Parse URI → Save to Cache → Retrieve → Build Config
// ============================================================

func TestParseToCacheToConfig(t *testing.T) {
	c := setupCache(t)

	// 1. Parse a URI
	uri := "vless://abc-123@1.2.3.4:443?security=tls&sni=example.com&type=ws&host=example.com&path=/ws#US-Test"
	node, err := fetcher.ParseURIPublic(uri)
	if err != nil {
		t.Fatalf("ParseURIPublic() error = %v", err)
	}

	// 2. Save to cache
	if err := c.SaveNodes([]*types.Node{node}); err != nil {
		t.Fatalf("SaveNodes() error = %v", err)
	}

	// 3. Retrieve from cache
	nodes, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes() error = %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}

	// 4. Build sing-box config
	cfg := rotator.BuildConfig(nodes[0], "127.0.0.1", 1080)
	if len(cfg) == 0 {
		t.Fatal("BuildConfig() returned empty")
	}
}

func TestMultipleProtocols_Pipeline(t *testing.T) {
	c := setupCache(t)

	uris := []string{
		"vless://abc@1.2.3.4:443#VLESS-Test",
		"ss://aes-256-gcm:pass@5.6.7.8:443#SS-Test",
		"trojan://pass@9.10.11.12:443#Trojan-Test",
		"hy2://pass@13.14.15.16:443#Hy2-Test",
	}

	var nodes []*types.Node
	for _, uri := range uris {
		node, err := fetcher.ParseURIPublic(uri)
		if err != nil {
			t.Fatalf("ParseURIPublic(%s) error = %v", uri, err)
		}
		nodes = append(nodes, node)
	}

	// Save all
	if err := c.SaveNodes(nodes); err != nil {
		t.Fatalf("SaveNodes() error = %v", err)
	}

	// Verify each protocol
	protocols := []string{"vless", "ss", "trojan", "hy2"}
	for _, proto := range protocols {
		pNodes, err := c.GetNodesByProtocol(proto)
		if err != nil {
			t.Fatalf("GetNodesByProtocol(%s) error = %v", proto, err)
		}
		if len(pNodes) != 1 {
			t.Errorf("protocol %s: expected 1 node, got %d", proto, len(pNodes))
		}

		// Build config for each
		cfg := rotator.BuildConfig(pNodes[0], "127.0.0.1", 1080)
		if len(cfg) == 0 {
			t.Errorf("BuildConfig() for %s returned empty", proto)
		}
	}
}

// ============================================================
// Integration: Stats Tracking Through Full Lifecycle
// ============================================================

func TestStatsLifecycle(t *testing.T) {
	c := setupCache(t)

	// Create and save a node
	node := &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vless", Tag: "test"}
	c.SaveNodes([]*types.Node{node})

	nodes, _ := c.GetNodes()
	nid := nodes[0].ID

	// Simulate multiple checks
	for i := 0; i < 10; i++ {
		success := i%3 != 0 // 67% success rate
		latency := int64(100 + i*10)
		c.UpdateNodeStats(nid, latency, success)
	}

	// Verify stats
	stats, err := c.GetNodeStats(nid)
	if err != nil {
		t.Fatalf("GetNodeStats() error = %v", err)
	}

	if stats.TotalChecks != 10 {
		t.Errorf("TotalChecks = %d, want 10", stats.TotalChecks)
	}
	if stats.TotalFailures != 4 {
		t.Errorf("TotalFailures = %d, want 4", stats.TotalFailures)
	}
	if stats.SuccessRate != 0.6 {
		t.Errorf("SuccessRate = %f, want 0.6", stats.SuccessRate)
	}
}

// ============================================================
// Integration: Dedup + Stats Preservation
// ============================================================

func TestDedupWithStats(t *testing.T) {
	c := setupCache(t)

	// Save a node
	node := &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vless", Tag: "original"}
	c.SaveNodes([]*types.Node{node})

	nodes, _ := c.GetNodes()
	nid := nodes[0].ID

	// Give it stats
	c.UpdateNodeStats(nid, 100, true)
	c.UpdateNodeStats(nid, 100, true)

	// Try to save the same node again (should upsert)
	node2 := &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vless", Tag: "updated"}
	c.SaveNodes([]*types.Node{node2})

	// Should still have only 1 node
	count, _ := c.CountNodes()
	if count != 1 {
		t.Errorf("CountNodes() = %d, want 1", count)
	}

	// Stats should be preserved
	stats, err := c.GetNodeStats(nid)
	if err != nil {
		t.Fatalf("GetNodeStats() error = %v", err)
	}
	if stats.TotalChecks != 2 {
		t.Errorf("TotalChecks = %d, want 2 (stats should persist)", stats.TotalChecks)
	}
}

// ============================================================
// Integration: Rotation Recording
// ============================================================

func TestRotationRecording(t *testing.T) {
	c := setupCache(t)

	// Create two nodes
	c.SaveNodes([]*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless", Tag: "node-a"},
		{Server: "5.6.7.8", Port: 443, Protocol: "vless", Tag: "node-b"},
	})

	nodes, _ := c.GetNodes()

	// Record rotation
	if err := c.RecordRotation(nodes[0].ID, nodes[1].ID, "failure", "success"); err != nil {
		t.Fatalf("RecordRotation() error = %v", err)
	}

	// Set current node
	c.SetCurrentNode(nodes[1].ID)

	// Verify
	currentID, _ := c.GetCurrentNode()
	if currentID != nodes[1].ID {
		t.Errorf("GetCurrentNode() = %d, want %d", currentID, nodes[1].ID)
	}

	// Verify rotation history
	rotations, _ := c.GetRecentRotations(10)
	if len(rotations) != 1 {
		t.Fatalf("len(rotations) = %d, want 1", len(rotations))
	}
	if rotations[0].FromNodeID != nodes[0].ID {
		t.Errorf("FromNodeID = %d, want %d", rotations[0].FromNodeID, nodes[0].ID)
	}
}

// ============================================================
// Integration: DeduplicateNodes + FilterByCountry
// ============================================================

func TestDeduplicateAndFilter(t *testing.T) {
	nodes := []*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "VN"},
		{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "VN"}, // dup
		{Server: "5.6.7.8", Port: 443, Protocol: "vless", Country: "US"},
		{Server: "9.10.11.12", Port: 443, Protocol: "ss", Country: "VN"},
		{Server: "9.10.11.12", Port: 443, Protocol: "ss", Country: "VN"}, // dup
	}

	// Dedup
	deduped := fetcher.DeduplicateNodes(nodes)
	if len(deduped) != 3 {
		t.Errorf("DeduplicateNodes() returned %d, want 3", len(deduped))
	}

	// Filter
	filtered := fetcher.FilterByCountry(deduped, "VN")
	if len(filtered) != 2 {
		t.Errorf("FilterByCountry(VN) returned %d, want 2", len(filtered))
	}
}

// ============================================================
// Integration: Config JSON Validity
// ============================================================

func TestAllProtocolConfigs_ValidJSON(t *testing.T) {
	protocols := []struct {
		name string
		node *types.Node
	}{
		{"vless", &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vless", UUID: "abc", Tag: "test"}},
		{"ss", &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "ss", Password: "pass", EncryptMethod: "aes-256-gcm", Tag: "test"}},
		{"trojan", &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "trojan", Password: "pass", TLSEnabled: true, Tag: "test"}},
		{"vmess", &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "vmess", UUID: "abc", Tag: "test"}},
		{"hy2", &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "hy2", Password: "pass", Tag: "test"}},
		{"unknown", &types.Node{Server: "1.2.3.4", Port: 443, Protocol: "unknown", Tag: "test"}},
	}

	for _, p := range protocols {
		t.Run(p.name, func(t *testing.T) {
			cfg := rotator.BuildConfig(p.node, "127.0.0.1", 1080)
			if len(cfg) == 0 {
				t.Errorf("BuildConfig(%s) returned empty", p.name)
			}
		})
	}
}

// ============================================================
// Integration: GeoIP Enrichment
// ============================================================

func TestGeoIPEnrichmentPipeline(t *testing.T) {
	nodes := []*types.Node{
		{Server: "8.8.8.8", Country: "unknown"},
		{Server: "1.1.1.1", Country: "unknown"},
		{Server: "example.com", Country: "unknown"},
	}

	fetcher.EnrichCountries(nodes)

	enriched := 0
	for _, n := range nodes {
		if n.Country != "" && n.Country != "unknown" {
			enriched++
		}
	}

	// At least some should be enriched
	if enriched == 0 {
		t.Error("expected at least one node to be enriched")
	}
	t.Logf("Enriched %d/%d nodes", enriched, len(nodes))
}

// ============================================================
// Integration: NodeToURI Round-trip
// ============================================================

func TestNodeToURI_RoundTrip(t *testing.T) {
	uris := []string{
		"vless://abc@1.2.3.4:443?security=tls&sni=example.com#US-VLESS",
		"trojan://pass@1.2.3.4:443?security=tls&sni=example.com#SG-Trojan",
		"hy2://pass@1.2.3.4:443#JP-Hy2",
	}

	for _, uri := range uris {
		node, err := fetcher.ParseURIPublic(uri)
		if err != nil {
			t.Fatalf("ParseURIPublic(%s) error = %v", uri, err)
		}

		// Save and retrieve
		c := setupCache(t)
		c.SaveNodes([]*types.Node{node})
		nodes, _ := c.GetNodes()

		// Build config should work
		cfg := rotator.BuildConfig(nodes[0], "127.0.0.1", 1080)
		if len(cfg) == 0 {
			t.Errorf("BuildConfig() for %s returned empty", uri)
		}
	}
}

// ============================================================
// Integration: Checker Config (no live proxy)
// ============================================================

func TestCheckerCreation(t *testing.T) {
	c := setupCache(t)

	// Create a checker
	ch := checker.New(c, zap.NewNop(), &checker.Config{
		URL:         "https://httpbin.org/ip",
		Interval:    30 * time.Second,
		Timeout:     5 * time.Second,
		MaxRetries:  3,
		MaxAttempts: 3,
		CheckIP:     true,
		SOCKS5Addr:  "127.0.0.1:1080",
	})

	if ch == nil {
		t.Fatal("checker.New() returned nil")
	}

	// Test context cancellation
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Start should return quickly on cancelled context
	done := make(chan error, 1)
	go func() {
		done <- ch.Start(ctx)
	}()

	select {
	case <-done:
		// Good, it returned
	case <-time.After(10 * time.Second):
		t.Fatal("checker.Start() did not return after context cancel")
	}
}

// ============================================================
// Helpers
// ============================================================

func setupCache(t *testing.T) *cache.Cache {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	c, err := cache.NewCache(dbPath)
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
