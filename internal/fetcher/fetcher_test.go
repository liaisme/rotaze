package fetcher

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/rotaze/rotaze/pkg/types"
)

// ============================================================
// DeduplicateNodes
// ============================================================

func TestDeduplicateNodes(t *testing.T) {
	nodes := []*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"}, // dup
		{Server: "5.6.7.8", Port: 80, Protocol: "ss"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss"},   // dup
		{Server: "9.10.11.12", Port: 443, Protocol: "trojan"},
	}

	deduped := DeduplicateNodes(nodes)
	if len(deduped) != 3 {
		t.Errorf("DeduplicateNodes() returned %d, want 3", len(deduped))
	}
}

func TestDeduplicateNodes_NoDups(t *testing.T) {
	nodes := []*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "5.6.7.8", Port: 80, Protocol: "ss"},
	}
	deduped := DeduplicateNodes(nodes)
	if len(deduped) != 2 {
		t.Errorf("DeduplicateNodes() returned %d, want 2", len(deduped))
	}
}

func TestDeduplicateNodes_Empty(t *testing.T) {
	deduped := DeduplicateNodes(nil)
	if len(deduped) != 0 {
		t.Errorf("DeduplicateNodes(nil) returned %d, want 0", len(deduped))
	}
}

func TestDeduplicateNodes_DifferentPorts(t *testing.T) {
	nodes := []*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "1.2.3.4", Port: 8443, Protocol: "vless"}, // different port
	}
	deduped := DeduplicateNodes(nodes)
	if len(deduped) != 2 {
		t.Errorf("DeduplicateNodes() should keep different ports, got %d", len(deduped))
	}
}

func TestDeduplicateNodes_DifferentProtocols(t *testing.T) {
	nodes := []*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless"},
		{Server: "1.2.3.4", Port: 443, Protocol: "ss"}, // different protocol
	}
	deduped := DeduplicateNodes(nodes)
	if len(deduped) != 2 {
		t.Errorf("DeduplicateNodes() should keep different protocols, got %d", len(deduped))
	}
}

// ============================================================
// FilterByCountry
// ============================================================

func TestFilterByCountry(t *testing.T) {
	nodes := []*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "VN"},
		{Server: "5.6.7.8", Port: 443, Protocol: "ss", Country: "US"},
		{Server: "9.10.11.12", Port: 443, Protocol: "trojan", Country: "unknown"},
	}

	filtered := FilterByCountry(nodes, "VN")
	if len(filtered) != 1 {
		t.Errorf("FilterByCountry(VN) returned %d, want 1 (strict: unknown excluded)", len(filtered))
	}

	for _, n := range filtered {
		if n.Country != "VN" {
			t.Errorf("unexpected country: %s", n.Country)
		}
	}
}

func TestFilterByCountry_CaseInsensitive(t *testing.T) {
	nodes := []*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "vn"},
		{Server: "5.6.7.8", Port: 443, Protocol: "ss", Country: "US"},
	}

	filtered := FilterByCountry(nodes, "VN")
	if len(filtered) != 1 {
		t.Errorf("FilterByCountry(VN) on lowercase returned %d, want 1", len(filtered))
	}
}

func TestFilterByCountry_NoMatch(t *testing.T) {
	nodes := []*types.Node{
		{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: "VN"},
	}

	filtered := FilterByCountry(nodes, "US")
	if len(filtered) != 0 {
		t.Errorf("FilterByCountry(US) returned %d, want 0", len(filtered))
	}
}

func TestFilterByCountry_Empty(t *testing.T) {
	filtered := FilterByCountry(nil, "VN")
	if len(filtered) != 0 {
		t.Errorf("FilterByCountry(nil) returned %d, want 0", len(filtered))
	}
}

// ============================================================
// toUpper
// ============================================================

func TestToUpper(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"vn", "VN"},
		{"US", "US"},
		{"Hello", "HELLO"},
		{"", ""},
		{"123", "123"},
		{"aBcDeF", "ABCDEF"},
	}

	for _, tt := range tests {
		got := toUpper(tt.input)
		if got != tt.expected {
			t.Errorf("toUpper(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// ============================================================
// isBase64
// ============================================================

func TestIsBase64(t *testing.T) {
	tests := []struct {
		input  string
		expect bool
	}{
		{base64.StdEncoding.EncodeToString([]byte("hello world")), true},
		{"dGVzdA==", true},
		{"short", false},
		{"", false},
		{"abc", false},
		{"!@#$%^&*", false},
		{base64.URLEncoding.EncodeToString([]byte("test")), true},
	}

	for _, tt := range tests {
		got := isBase64(tt.input)
		if got != tt.expect {
			t.Errorf("isBase64(%q) = %v, want %v", tt.input, got, tt.expect)
		}
	}
}

// ============================================================
// isKnownProtocol
// ============================================================

func TestIsKnownProtocol(t *testing.T) {
	tests := []struct {
		uri  string
		want bool
	}{
		{"vless://x@1.2.3.4:443", true},
		{"ss://x@1.2.3.4:443", true},
		{"trojan://x@1.2.3.4:443", true},
		{"vmess://x", true},
		{"hy2://x@1.2.3.4:443", true},
		{"hysteria2://x@1.2.3.4:443", true},
		{"http://example.com", false},
		{"ftp://example.com", false},
		{"", false},
		{"random text", false},
	}

	for _, tt := range tests {
		if got := isKnownProtocol(tt.uri); got != tt.want {
			t.Errorf("isKnownProtocol(%q) = %v, want %v", tt.uri, got, tt.want)
		}
	}
}

// ============================================================
// parseSourceContent
// ============================================================

func TestParseSourceContent_DirectURIs(t *testing.T) {
	src := Source{Name: "test", URL: ""}
	content := `vless://abc@1.2.3.4:443#VN
ss://aes-256-gcm:pass@5.6.7.8:443#US
trojan://pass@9.10.11.12:443#JP
`
	uris := parseSourceContent(src, content)
	if len(uris) != 3 {
		t.Errorf("parseSourceContent() returned %d URIs, want 3", len(uris))
	}
}

func TestParseSourceContent_Base64(t *testing.T) {
	src := Source{Name: "test", URL: ""}
	// Base64 encode a URI list
	content := "vless://abc@1.2.3.4:443#VN\nss://aes-256-gcm:pass@5.6.7.8:443#US"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	uris := parseSourceContent(src, encoded)
	if len(uris) != 2 {
		t.Errorf("parseSourceContent(base64) returned %d URIs, want 2", len(uris))
	}
}

func TestParseSourceContent_Empty(t *testing.T) {
	src := Source{Name: "test", URL: ""}
	uris := parseSourceContent(src, "")
	if len(uris) != 0 {
		t.Errorf("parseSourceContent(empty) returned %d, want 0", len(uris))
	}
}

func TestParseSourceContent_CommentsAndBlank(t *testing.T) {
	src := Source{Name: "test", URL: ""}
	content := `# This is a comment
// This is another comment

vless://abc@1.2.3.4:443#VN
`
	uris := parseSourceContent(src, content)
	if len(uris) != 1 {
		t.Errorf("parseSourceContent(comments) returned %d, want 1", len(uris))
	}
}

func TestParseSourceContent_JSONOutbound(t *testing.T) {
	src := Source{Name: "test", URL: ""}
	cfg := map[string]interface{}{
		"type":        "vless",
		"server":      "1.2.3.4",
		"server_port": 443,
		"uuid":        "abc-123",
		"tag":         "json-test",
	}
	b, _ := json.Marshal(cfg)
	content := string(b)

	uris := parseSourceContent(src, content)
	if len(uris) != 1 {
		t.Errorf("parseSourceContent(JSON) returned %d, want 1", len(uris))
	}
	if len(uris) > 0 && uris[0] == "" {
		t.Error("JSON URI should not be empty")
	}
}

func TestParseSourceContent_MixedFormats(t *testing.T) {
	src := Source{Name: "test", URL: ""}
	// Mix of direct URIs, base64, and comments
	directURI := "vless://abc@1.2.3.4:443#VN"
	b64Content := "ss://aes-256-gcm:pass@5.6.7.8:443#US"
	encoded := base64.StdEncoding.EncodeToString([]byte(b64Content))

	content := "# Comment\n" + directURI + "\n" + encoded + "\n"

	uris := parseSourceContent(src, content)
	if len(uris) < 2 {
		t.Errorf("parseSourceContent(mixed) returned %d, want >= 2", len(uris))
	}
}

// ============================================================
// convertSingboxJSONToURI
// ============================================================

func TestConvertSingboxJSONToURI_VLESS(t *testing.T) {
	cfg := map[string]interface{}{
		"type":        "vless",
		"server":      "1.2.3.4",
		"server_port": 443,
		"uuid":        "abc-123",
		"tag":         "json-vless",
	}
	b, _ := json.Marshal(cfg)

	uri := convertSingboxJSONToURI(string(b))
	if uri == "" {
		t.Fatal("convertSingboxJSONToURI(vless) returned empty")
	}
	if uri[:8] != "vless://" {
		t.Errorf("expected vless:// prefix, got %s", uri[:20])
	}
}

func TestConvertSingboxJSONToURI_SS(t *testing.T) {
	cfg := map[string]interface{}{
		"type":        "shadowsocks",
		"server":      "1.2.3.4",
		"server_port": 443,
		"method":      "aes-256-gcm",
		"password":    "pass123",
		"tag":         "json-ss",
	}
	b, _ := json.Marshal(cfg)

	uri := convertSingboxJSONToURI(string(b))
	if uri == "" {
		t.Fatal("convertSingboxJSONToURI(ss) returned empty")
	}
	if uri[:5] != "ss://" {
		t.Errorf("expected ss:// prefix, got %s", uri[:20])
	}
}

func TestConvertSingboxJSONToURI_Trojan(t *testing.T) {
	cfg := map[string]interface{}{
		"type":        "trojan",
		"server":      "1.2.3.4",
		"server_port": 443,
		"password":    "pass123",
		"tag":         "json-trojan",
	}
	b, _ := json.Marshal(cfg)

	uri := convertSingboxJSONToURI(string(b))
	if uri == "" {
		t.Fatal("convertSingboxJSONToURI(trojan) returned empty")
	}
	if len(uri) < 9 || uri[:9] != "trojan://" {
		t.Errorf("expected trojan:// prefix, got %s", uri[:20])
	}
}

func TestConvertSingboxJSONToURI_VMess(t *testing.T) {
	cfg := map[string]interface{}{
		"type":        "vmess",
		"server":      "1.2.3.4",
		"server_port": 443,
		"uuid":        "abc-123",
		"tag":         "json-vmess",
	}
	b, _ := json.Marshal(cfg)

	uri := convertSingboxJSONToURI(string(b))
	if uri == "" {
		t.Fatal("convertSingboxJSONToURI(vmess) returned empty")
	}
	if len(uri) < 8 || uri[:8] != "vmess://" {
		t.Errorf("expected vmess:// prefix, got %s", uri[:20])
	}
}

func TestConvertSingboxJSONToURI_InvalidJSON(t *testing.T) {
	uri := convertSingboxJSONToURI("not json")
	if uri != "" {
		t.Errorf("expected empty for invalid JSON, got %s", uri)
	}
}

func TestConvertSingboxJSONToURI_NoServer(t *testing.T) {
	cfg := map[string]interface{}{
		"type":        "vless",
		"server":      "",
		"server_port": 443,
		"uuid":        "abc",
	}
	b, _ := json.Marshal(cfg)

	uri := convertSingboxJSONToURI(string(b))
	if uri != "" {
		t.Errorf("expected empty for no server, got %s", uri)
	}
}

func TestConvertSingboxJSONToURI_NoPort(t *testing.T) {
	cfg := map[string]interface{}{
		"type":        "vless",
		"server":      "1.2.3.4",
		"server_port": 0,
		"uuid":        "abc",
	}
	b, _ := json.Marshal(cfg)

	uri := convertSingboxJSONToURI(string(b))
	if uri != "" {
		t.Errorf("expected empty for no port, got %s", uri)
	}
}

func TestConvertSingboxJSONToURI_WithTLS(t *testing.T) {
	cfg := map[string]interface{}{
		"type":        "vless",
		"server":      "1.2.3.4",
		"server_port": 443,
		"uuid":        "abc-123",
		"tag":         "tls-test",
		"tls": map[string]interface{}{
			"enabled":     true,
			"server_name": "example.com",
		},
	}
	b, _ := json.Marshal(cfg)

	uri := convertSingboxJSONToURI(string(b))
	if uri == "" {
		t.Fatal("expected non-empty URI")
	}
	// Should contain security=tls
	if !contains(uri, "security=tls") {
		t.Errorf("expected security=tls in URI: %s", uri)
	}
}

func TestConvertSingboxJSONToURI_WithTransport(t *testing.T) {
	cfg := map[string]interface{}{
		"type":        "vless",
		"server":      "1.2.3.4",
		"server_port": 443,
		"uuid":        "abc-123",
		"tag":         "ws-test",
		"transport": map[string]interface{}{
			"type": "ws",
			"host": "example.com",
			"path": "/ws",
		},
	}
	b, _ := json.Marshal(cfg)

	uri := convertSingboxJSONToURI(string(b))
	if uri == "" {
		t.Fatal("expected non-empty URI")
	}
	if !contains(uri, "type=ws") {
		t.Errorf("expected type=ws in URI: %s", uri)
	}
}

func TestConvertSingboxJSONToURI_NoTag(t *testing.T) {
	cfg := map[string]interface{}{
		"type":        "vless",
		"server":      "1.2.3.4",
		"server_port": 443,
		"uuid":        "abc-123",
		// No tag
	}
	b, _ := json.Marshal(cfg)

	uri := convertSingboxJSONToURI(string(b))
	if uri == "" {
		t.Fatal("expected non-empty URI even without tag")
	}
	// Should have auto-generated tag
	if !contains(uri, "#") {
		t.Errorf("expected # tag in URI: %s", uri)
	}
}

// ============================================================
// ParseURIPublic
// ============================================================

func TestParseURIPublic_VLESS(t *testing.T) {
	node, err := ParseURIPublic("vless://abc@1.2.3.4:443#VN-Test")
	if err != nil {
		t.Fatal(err)
	}
	if node.Protocol != "vless" {
		t.Errorf("protocol = %s, want vless", node.Protocol)
	}
	if node.Server != "1.2.3.4" {
		t.Errorf("server = %s, want 1.2.3.4", node.Server)
	}
	if node.Port != 443 {
		t.Errorf("port = %d, want 443", node.Port)
	}
}

func TestParseURIPublic_SS(t *testing.T) {
	node, err := ParseURIPublic("ss://aes-256-gcm:pass@1.2.3.4:443#US-SS")
	if err != nil {
		t.Fatal(err)
	}
	if node.Protocol != "ss" {
		t.Errorf("protocol = %s, want ss", node.Protocol)
	}
	if node.EncryptMethod != "aes-256-gcm" {
		t.Errorf("method = %s, want aes-256-gcm", node.EncryptMethod)
	}
}

func TestParseURIPublic_Trojan(t *testing.T) {
	node, err := ParseURIPublic("trojan://pass@1.2.3.4:443#JP-Trojan")
	if err != nil {
		t.Fatal(err)
	}
	if node.Protocol != "trojan" {
		t.Errorf("protocol = %s, want trojan", node.Protocol)
	}
}

func TestParseURIPublic_Hy2(t *testing.T) {
	node, err := ParseURIPublic("hy2://pass@1.2.3.4:443#SG-Hy2")
	if err != nil {
		t.Fatal(err)
	}
	if node.Protocol != "hy2" {
		t.Errorf("protocol = %s, want hy2", node.Protocol)
	}
}

func TestParseURIPublic_Empty(t *testing.T) {
	_, err := ParseURIPublic("")
	if err == nil {
		t.Error("expected error for empty URI")
	}
}

func TestParseURIPublic_Unsupported(t *testing.T) {
	_, err := ParseURIPublic("ftp://example.com")
	if err == nil {
		t.Error("expected error for unsupported protocol")
	}
}

// ============================================================
// Sources
// ============================================================

func TestSources(t *testing.T) {
	sources := Sources("VN")
	if len(sources) < 4 {
		t.Errorf("Sources() returned %d, want >= 4", len(sources))
	}
}

func TestSources_All(t *testing.T) {
	sources := Sources("all")
	if len(sources) < 4 {
		t.Errorf("Sources(all) returned %d, want >= 4", len(sources))
	}
}

func TestSources_Empty(t *testing.T) {
	sources := Sources("")
	if len(sources) < 4 {
		t.Errorf("Sources(\"\") returned %d, want >= 4", len(sources))
	}
}

// ============================================================
// Helpers
// ============================================================

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ============================================================
// Benchmarks
// ============================================================

func BenchmarkDeduplicateNodes(b *testing.B) {
	nodes := make([]*types.Node, 1000)
	for i := range nodes {
		nodes[i] = &types.Node{
			Server:   "1.2.3.4",
			Port:     i % 10,
			Protocol: "vless",
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		DeduplicateNodes(nodes)
	}
}

func BenchmarkFilterByCountry(b *testing.B) {
	nodes := make([]*types.Node, 1000)
	for i := range nodes {
		nodes[i] = &types.Node{
			Server:   "1.2.3.4",
			Port:     443,
			Protocol: "vless",
			Country:  "VN",
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FilterByCountry(nodes, "VN")
	}
}

func BenchmarkParseSourceContent(b *testing.B) {
	content := ""
	for i := 0; i < 100; i++ {
		content += "vless://abc@1.2.3.4:443#VN\n"
	}
	src := Source{Name: "bench", URL: ""}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseSourceContent(src, content)
	}
}

func BenchmarkConvertSingboxJSONToURI(b *testing.B) {
	cfg := map[string]interface{}{
		"type":        "vless",
		"server":      "1.2.3.4",
		"server_port": 443,
		"uuid":        "abc-123",
		"tag":         "bench",
		"tls": map[string]interface{}{
			"enabled":     true,
			"server_name": "example.com",
		},
		"transport": map[string]interface{}{
			"type": "ws",
			"host": "example.com",
			"path": "/ws",
		},
	}
	jsonData, _ := json.Marshal(cfg)
	jsonStr := string(jsonData)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		convertSingboxJSONToURI(jsonStr)
	}
}
