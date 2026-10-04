package cli

import (
	"testing"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/pkg/types"
)

func setupTestCache(t *testing.T) *cache.Cache {
	t.Helper()
	c, err := cache.NewCache(":memory:")
	if err != nil {
		t.Fatalf("failed to create test cache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// ============================================================
// pickBestNode
// ============================================================

func TestPickBestNode_EmptyNodes(t *testing.T) {
	result := pickBestNode(nil)
	if result != nil {
		t.Error("expected nil for empty nodes")
	}
}

func TestPickBestNode_PrefersSSWithCredentials(t *testing.T) {
	nodes := []*types.Node{
		{ID: 1, Protocol: "vless", Server: "1.2.3.4", Port: 443},
		{ID: 2, Protocol: "ss", Server: "5.6.7.8", Port: 8080, Password: "pass", EncryptMethod: "aes-256-gcm"},
		{ID: 3, Protocol: "trojan", Server: "9.10.11.12", Port: 443, Password: "pass"},
	}

	result := pickBestNode(nodes)
	if result == nil {
		t.Fatal("expected a node")
	}
	if result.ID != 2 {
		t.Errorf("expected node 2 (SS), got %d", result.ID)
	}
}

func TestPickBestNode_FallsBackToFirst(t *testing.T) {
	nodes := []*types.Node{
		{ID: 1, Protocol: "vless", Server: "1.2.3.4", Port: 443},
		{ID: 2, Protocol: "trojan", Server: "5.6.7.8", Port: 443},
	}

	result := pickBestNode(nodes)
	if result == nil {
		t.Fatal("expected a node")
	}
	if result.ID != 1 {
		t.Errorf("expected node 1 (first), got %d", result.ID)
	}
}

func TestPickBestNode_SSWithoutPassword(t *testing.T) {
	nodes := []*types.Node{
		{ID: 1, Protocol: "ss", Server: "1.2.3.4", Port: 8080},
		{ID: 2, Protocol: "vless", Server: "5.6.7.8", Port: 443},
	}

	result := pickBestNode(nodes)
	if result == nil {
		t.Fatal("expected a node")
	}
	if result.ID != 1 {
		t.Errorf("expected node 1, got %d", result.ID)
	}
}

func TestPickBestNode_SingleNode(t *testing.T) {
	nodes := []*types.Node{
		{ID: 1, Protocol: "vless", Server: "1.2.3.4", Port: 443},
	}
	result := pickBestNode(nodes)
	if result == nil {
		t.Fatal("expected a node")
	}
	if result.ID != 1 {
		t.Errorf("expected node 1, got %d", result.ID)
	}
}

func TestPickBestNode_MultipleSSWithCredentials(t *testing.T) {
	nodes := []*types.Node{
		{ID: 1, Protocol: "ss", Server: "1.2.3.4", Port: 8080, Password: "pass", EncryptMethod: "aes-256-gcm"},
		{ID: 2, Protocol: "ss", Server: "5.6.7.8", Port: 8080, Password: "pass", EncryptMethod: "aes-128-gcm"},
	}
	result := pickBestNode(nodes)
	if result == nil {
		t.Fatal("expected a node")
	}
	// Should pick the first SS node with credentials
	if result.ID != 1 {
		t.Errorf("expected node 1, got %d", result.ID)
	}
}

// ============================================================
// pickWorkingNode
// ============================================================

func TestPickWorkingNode_EmptyCache(t *testing.T) {
	c := setupTestCache(t)
	result := pickWorkingNode(c, "")
	if result != nil {
		t.Error("expected nil for empty cache")
	}
}

func TestPickWorkingNode_NoSSNodes(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Protocol: "vless", Server: "1.2.3.4", Port: 443, Tag: "test-vless"},
	})
	result := pickWorkingNode(c, "")
	if result != nil {
		t.Error("expected nil when no SS nodes with credentials")
	}
}

func TestPickWorkingNode_PrefersSupportedMethods(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Protocol: "ss", Server: "1.2.3.4", Port: 8080, Password: "pass", EncryptMethod: "rc4", Tag: "rc4"},
		{Protocol: "ss", Server: "5.6.7.8", Port: 8080, Password: "pass", EncryptMethod: "aes-256-gcm", Tag: "aes"},
	})

	result := pickWorkingNode(c, "")
	if result == nil {
		t.Fatal("expected a node")
	}
	if result.ID != 2 {
		t.Errorf("expected node 2 (aes-256-gcm), got %d", result.ID)
	}
}

func TestPickWorkingNode_SingleSS(t *testing.T) {
	c := setupTestCache(t)
	c.SaveNodes([]*types.Node{
		{Protocol: "ss", Server: "1.2.3.4", Port: 8080, Password: "pass", EncryptMethod: "aes-256-gcm", Tag: "only-one"},
	})

	result := pickWorkingNode(c, "")
	if result == nil {
		t.Fatal("expected a node")
	}
	if result.Tag != "only-one" {
		t.Errorf("expected 'only-one' node, got %s", result.Tag)
	}
}

// ============================================================
// supportedMethods
// ============================================================

func TestSupportedMethods(t *testing.T) {
	expected := map[string]bool{
		"aes-128-gcm":             true,
		"aes-256-gcm":             true,
		"chacha20-ietf-poly1305":  true,
		"2022-blake3-aes-128-gcm": true,
		"2022-blake3-aes-256-gcm": true,
	}

	for method := range expected {
		if !supportedMethods[method] {
			t.Errorf("expected %q to be supported", method)
		}
	}

	unsupported := []string{"rc4", "aes-128-cfb", "chacha20", "bf-cfb"}
	for _, method := range unsupported {
		if supportedMethods[method] {
			t.Errorf("expected %q to be unsupported", method)
		}
	}
}

// ============================================================
// nodeToURI
// ============================================================

func TestNodeToURI_VLESS(t *testing.T) {
	node := &types.Node{
		Protocol: "vless",
		UUID:     "abc-123",
		Server:   "1.2.3.4",
		Port:     443,
		Tag:      "VN-Test",
	}
	uri := nodeToURI(node)
	if uri != "vless://abc-123@1.2.3.4:443#VN-Test" {
		t.Errorf("nodeToURI(vless) = %s", uri)
	}
}

func TestNodeToURI_VLESS_TLS(t *testing.T) {
	node := &types.Node{
		Protocol:   "vless",
		UUID:       "abc-123",
		Server:     "1.2.3.4",
		Port:       443,
		TLSEnabled: true,
		TLSSNI:     "example.com",
		Tag:        "US-TLS",
	}
	uri := nodeToURI(node)
	if uri != "vless://abc-123@1.2.3.4:443?security=tls&sni=example.com#US-TLS" {
		t.Errorf("nodeToURI(vless+tls) = %s", uri)
	}
}

func TestNodeToURI_VLESS_WS(t *testing.T) {
	node := &types.Node{
		Protocol:      "vless",
		UUID:          "abc-123",
		Server:        "1.2.3.4",
		Port:          443,
		TransportType: "ws",
		TransportHost: "example.com",
		TransportPath: "/ws",
		Tag:           "JP-WS",
	}
	uri := nodeToURI(node)
	if uri != "vless://abc-123@1.2.3.4:443?type=ws&host=example.com&path=/ws#JP-WS" {
		t.Errorf("nodeToURI(vless+ws) = %s", uri)
	}
}

func TestNodeToURI_VLESS_GRPC(t *testing.T) {
	node := &types.Node{
		Protocol:      "vless",
		UUID:          "abc-123",
		Server:        "1.2.3.4",
		Port:          443,
		TransportType: "grpc",
		TransportPath: "myService",
		Tag:           "SG-GRPC",
	}
	uri := nodeToURI(node)
	if uri != "vless://abc-123@1.2.3.4:443?type=grpc&path=myService#SG-GRPC" {
		t.Errorf("nodeToURI(vless+grpc) = %s", uri)
	}
}

func TestNodeToURI_SS(t *testing.T) {
	node := &types.Node{
		Protocol:      "ss",
		EncryptMethod: "aes-256-gcm",
		Password:      "pass123",
		Server:        "1.2.3.4",
		Port:          8388,
		Tag:           "VN-SS",
	}
	uri := nodeToURI(node)
	if uri == "" {
		t.Error("nodeToURI(ss) returned empty")
	}
	// Should start with ss://
	if len(uri) < 5 || uri[:5] != "ss://" {
		t.Errorf("nodeToURI(ss) should start with ss://, got %s", uri)
	}
	// Should contain server:port
	if !containsStr(uri, "1.2.3.4:8388") {
		t.Errorf("nodeToURI(ss) should contain server:port, got %s", uri)
	}
	// Should end with #tag
	if len(uri) < 6 || uri[len(uri)-6:] != "#VN-SS" {
		t.Errorf("nodeToURI(ss) should end with #VN-SS, got %s", uri)
	}
}

func TestNodeToURI_Trojan(t *testing.T) {
	node := &types.Node{
		Protocol:   "trojan",
		Password:   "pass123",
		Server:     "1.2.3.4",
		Port:       443,
		TLSEnabled: true,
		TLSSNI:     "example.com",
		Tag:        "US-Trojan",
	}
	uri := nodeToURI(node)
	expected := "trojan://pass123@1.2.3.4:443?security=tls&sni=example.com#US-Trojan"
	if uri != expected {
		t.Errorf("nodeToURI(trojan) = %s, want %s", uri, expected)
	}
}

func TestNodeToURI_Trojan_NoTLS(t *testing.T) {
	node := &types.Node{
		Protocol: "trojan",
		Password: "pass123",
		Server:   "1.2.3.4",
		Port:     443,
		Tag:      "SG-Trojan",
	}
	uri := nodeToURI(node)
	expected := "trojan://pass123@1.2.3.4:443#SG-Trojan"
	if uri != expected {
		t.Errorf("nodeToURI(trojan no tls) = %s, want %s", uri, expected)
	}
}

func TestNodeToURI_Hy2(t *testing.T) {
	node := &types.Node{
		Protocol: "hy2",
		Password: "pass123",
		Server:   "1.2.3.4",
		Port:     443,
		Tag:      "JP-Hy2",
	}
	uri := nodeToURI(node)
	expected := "hy2://pass123@1.2.3.4:443#JP-Hy2"
	if uri != expected {
		t.Errorf("nodeToURI(hy2) = %s, want %s", uri, expected)
	}
}

func TestNodeToURI_VMess(t *testing.T) {
	node := &types.Node{
		Protocol: "vmess",
		Server:   "1.2.3.4",
		Port:     443,
		Tag:      "test",
	}
	uri := nodeToURI(node)
	if uri != "" {
		t.Errorf("nodeToURI(vmess) should return empty for unimplemented, got %s", uri)
	}
}

func TestNodeToURI_Unknown(t *testing.T) {
	node := &types.Node{
		Protocol: "unknown",
		Server:   "1.2.3.4",
		Port:     443,
		Tag:      "test",
	}
	uri := nodeToURI(node)
	if uri != "" {
		t.Errorf("nodeToURI(unknown) should return empty, got %s", uri)
	}
}

func TestNodeToURI_VLESS_NoSNI(t *testing.T) {
	node := &types.Node{
		Protocol:   "vless",
		UUID:       "abc-123",
		Server:     "1.2.3.4",
		Port:       443,
		TLSEnabled: true,
		// No TLSSNI
		Tag: "no-sni",
	}
	uri := nodeToURI(node)
	// Should have security=tls but no sni
	if uri != "vless://abc-123@1.2.3.4:443?security=tls#no-sni" {
		t.Errorf("nodeToURI(vless tls no sni) = %s", uri)
	}
}

func TestNodeToURI_VLESS_TCPTransport(t *testing.T) {
	node := &types.Node{
		Protocol:      "vless",
		UUID:          "abc-123",
		Server:        "1.2.3.4",
		Port:          443,
		TransportType: "tcp",
		Tag:           "tcp",
	}
	uri := nodeToURI(node)
	// tcp transport should not add type param
	if uri != "vless://abc-123@1.2.3.4:443#tcp" {
		t.Errorf("nodeToURI(vless+tcp) = %s", uri)
	}
}

func TestNodeToURI_SS_NoTag(t *testing.T) {
	node := &types.Node{
		Protocol:      "ss",
		EncryptMethod: "aes-256-gcm",
		Password:      "pass",
		Server:        "1.2.3.4",
		Port:          443,
	}
	uri := nodeToURI(node)
	// Should not have # tag
	for i := len(uri) - 1; i >= 0; i-- {
		if uri[i] == '#' {
			t.Errorf("nodeToURI(ss no tag) should not have #, got %s", uri)
			return
		}
	}
}

// ============================================================
// sortNodes
// ============================================================

func TestSortNodes_ByLatency(t *testing.T) {
	c := setupTestCache(t)
	nodes := []*types.Node{
		{ID: 1, Server: "slow", Protocol: "ss", Tag: "slow"},
		{ID: 2, Server: "fast", Protocol: "ss", Tag: "fast"},
		{ID: 3, Server: "mid", Protocol: "ss", Tag: "mid"},
	}

	c.UpdateNodeStats(1, 500, true)
	c.UpdateNodeStats(2, 50, true)
	c.UpdateNodeStats(3, 200, true)

	sortNodes(nodes, "latency", c)

	if nodes[0].Tag != "fast" {
		t.Errorf("first after latency sort = %s, want fast", nodes[0].Tag)
	}
	if nodes[1].Tag != "mid" {
		t.Errorf("second after latency sort = %s, want mid", nodes[1].Tag)
	}
	if nodes[2].Tag != "slow" {
		t.Errorf("third after latency sort = %s, want slow", nodes[2].Tag)
	}
}

func TestSortNodes_BySuccess(t *testing.T) {
	c := setupTestCache(t)
	nodes := []*types.Node{
		{ID: 1, Server: "bad", Protocol: "ss", Tag: "bad"},
		{ID: 2, Server: "good", Protocol: "ss", Tag: "good"},
		{ID: 3, Server: "ok", Protocol: "ss", Tag: "ok"},
	}

	c.UpdateNodeStats(1, 100, false)
	c.UpdateNodeStats(1, 100, false)
	c.UpdateNodeStats(2, 100, true)
	c.UpdateNodeStats(2, 100, true)
	c.UpdateNodeStats(3, 100, true)
	c.UpdateNodeStats(3, 100, false)

	sortNodes(nodes, "success", c)

	if nodes[0].Tag != "good" {
		t.Errorf("first after success sort = %s, want good", nodes[0].Tag)
	}
	if nodes[1].Tag != "ok" {
		t.Errorf("second after success sort = %s, want ok", nodes[1].Tag)
	}
	if nodes[2].Tag != "bad" {
		t.Errorf("third after success sort = %s, want bad", nodes[2].Tag)
	}
}

func TestSortNodes_ByRandom(t *testing.T) {
	nodes := make([]*types.Node, 100)
	for i := range nodes {
		nodes[i] = &types.Node{ID: i, Protocol: "ss", Tag: "test"}
	}

	original := make([]int, len(nodes))
	for i, n := range nodes {
		original[i] = n.ID
	}

	c := setupTestCache(t)
	sortNodes(nodes, "random", c)

	// Check that at least one element moved
	moved := false
	for i, n := range nodes {
		if n.ID != original[i] {
			moved = true
			break
		}
	}
	if !moved {
		t.Error("random sort did not change order")
	}
}

func TestSortNodes_Default(t *testing.T) {
	nodes := []*types.Node{
		{ID: 3},
		{ID: 1},
		{ID: 2},
	}

	c := setupTestCache(t)
	sortNodes(nodes, "id", c)

	// Should remain in original order (no sort)
	if nodes[0].ID != 3 || nodes[1].ID != 1 || nodes[2].ID != 2 {
		t.Error("default sort should not change order")
	}
}

func TestSortNodes_Empty(t *testing.T) {
	c := setupTestCache(t)
	var nodes []*types.Node
	sortNodes(nodes, "latency", c) // Should not panic
	sortNodes(nodes, "success", c)
	sortNodes(nodes, "random", c)
	sortNodes(nodes, "id", c)
}

func TestSortNodes_SingleNode(t *testing.T) {
	c := setupTestCache(t)
	nodes := []*types.Node{{ID: 1}}
	sortNodes(nodes, "latency", c)
	if nodes[0].ID != 1 {
		t.Error("single node sort should not change it")
	}
}

func TestSortNodes_NoStats(t *testing.T) {
	c := setupTestCache(t)
	nodes := []*types.Node{
		{ID: 1, Server: "a"},
		{ID: 2, Server: "b"},
	}
	sortNodes(nodes, "latency", c) // No stats, should default to 99999
	sortNodes(nodes, "success", c) // No stats, should default to 0
}

// containsStr checks if s contains substr.
func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
