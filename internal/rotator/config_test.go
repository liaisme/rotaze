package rotator

import (
	"encoding/json"
	"testing"

	"github.com/rotaze/rotaze/pkg/types"
)

// ============================================================
// BuildConfig
// ============================================================

func TestBuildConfig_VLESS(t *testing.T) {
	node := &types.Node{
		Server:     "1.2.3.4",
		Port:       443,
		Protocol:   "vless",
		UUID:       "abc-123",
		TLSEnabled: true,
		TLSSNI:     "example.com",
		Tag:        "vless-test",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	// Inbound
	inbounds := parsed["inbounds"].([]interface{})
	inbound := inbounds[0].(map[string]interface{})
	if inbound["listen_port"].(float64) != 1080 {
		t.Errorf("listen_port = %v, want 1080", inbound["listen_port"])
	}
	if inbound["listen"] != "127.0.0.1" {
		t.Errorf("listen = %v, want 127.0.0.1", inbound["listen"])
	}

	// Outbound
	outbounds := parsed["outbounds"].([]interface{})
	out := outbounds[0].(map[string]interface{})
	if out["type"] != "vless" {
		t.Errorf("type = %v, want vless", out["type"])
	}
	if out["uuid"] != "abc-123" {
		t.Errorf("uuid = %v, want abc-123", out["uuid"])
	}
	if out["server"] != "1.2.3.4" {
		t.Errorf("server = %v, want 1.2.3.4", out["server"])
	}
	if out["server_port"].(float64) != 443 {
		t.Errorf("server_port = %v, want 443", out["server_port"])
	}

	// TLS
	tls := out["tls"].(map[string]interface{})
	if tls["enabled"] != true {
		t.Error("tls.enabled should be true")
	}
	if tls["server_name"] != "example.com" {
		t.Errorf("tls.server_name = %v, want example.com", tls["server_name"])
	}

	// Route
	route := parsed["route"].(map[string]interface{})
	if route["final"] != "vless-test" {
		t.Errorf("route.final = %v, want vless-test", route["final"])
	}
}

func TestBuildConfig_VLESS_WS(t *testing.T) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          443,
		Protocol:      "vless",
		UUID:          "abc-123",
		TLSEnabled:    true,
		TLSSNI:        "example.com",
		TransportType: "ws",
		TransportHost: "example.com",
		TransportPath: "/ws",
		Tag:           "vless-ws",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	transport := out["transport"].(map[string]interface{})

	if transport["type"] != "ws" {
		t.Errorf("transport.type = %v, want ws", transport["type"])
	}
	host := transport["host"].([]interface{})
	if host[0] != "example.com" {
		t.Errorf("transport.host = %v, want example.com", host[0])
	}
	if transport["path"] != "/ws" {
		t.Errorf("transport.path = %v, want /ws", transport["path"])
	}
}

func TestBuildConfig_VLESS_GRPC(t *testing.T) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          443,
		Protocol:      "vless",
		UUID:          "abc-123",
		TLSEnabled:    true,
		TransportType: "grpc",
		TransportPath: "myService",
		Tag:           "vless-grpc",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	transport := out["transport"].(map[string]interface{})

	if transport["type"] != "grpc" {
		t.Errorf("transport.type = %v, want grpc", transport["type"])
	}
	if transport["service_name"] != "myService" {
		t.Errorf("transport.service_name = %v, want myService", transport["service_name"])
	}
}

func TestBuildConfig_VLESS_HTTPUpgrade(t *testing.T) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          443,
		Protocol:      "vless",
		UUID:          "abc-123",
		TLSEnabled:    true,
		TransportType: "httpupgrade",
		TransportHost: "example.com",
		TransportPath: "/ws",
		Tag:           "vless-httpupgrade",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	transport := out["transport"].(map[string]interface{})

	if transport["type"] != "httpupgrade" {
		t.Errorf("transport.type = %v, want httpupgrade", transport["type"])
	}
	if transport["host"] != "example.com" {
		t.Errorf("transport.host = %v, want example.com", transport["host"])
	}
	if transport["path"] != "/ws" {
		t.Errorf("transport.path = %v, want /ws", transport["path"])
	}
}

func TestBuildConfig_SS(t *testing.T) {
	node := &types.Node{
		Server:        "3.96.124.46",
		Port:          443,
		Protocol:      "ss",
		Password:      "test123",
		EncryptMethod: "aes-256-gcm",
		Tag:           "ss-test",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	if out["type"] != "shadowsocks" {
		t.Errorf("type = %v, want shadowsocks", out["type"])
	}
	if out["method"] != "aes-256-gcm" {
		t.Errorf("method = %v, want aes-256-gcm", out["method"])
	}
	if out["password"] != "test123" {
		t.Errorf("password = %v, want test123", out["password"])
	}
}

func TestBuildConfig_SS_DefaultMethod(t *testing.T) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: "ss",
		Password: "pass",
		Tag:      "ss-nomethod",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	if out["method"] != "aes-256-gcm" {
		t.Errorf("default method = %v, want aes-256-gcm", out["method"])
	}
}

func TestBuildConfig_Trojan(t *testing.T) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: "trojan",
		Password: "pass123",
		TLSEnabled: true,
		TLSSNI:   "example.com",
		Tag:      "trojan-test",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	if out["type"] != "trojan" {
		t.Errorf("type = %v, want trojan", out["type"])
	}
	if out["password"] != "pass123" {
		t.Errorf("password = %v, want pass123", out["password"])
	}

	tls := out["tls"].(map[string]interface{})
	if tls["enabled"] != true {
		t.Error("tls.enabled should be true")
	}
}

func TestBuildConfig_Trojan_WS(t *testing.T) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          443,
		Protocol:      "trojan",
		Password:      "pass",
		TLSEnabled:    true,
		TransportType: "ws",
		TransportHost: "example.com",
		TransportPath: "/tr",
		Tag:           "trojan-ws",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	transport := out["transport"].(map[string]interface{})
	if transport["type"] != "ws" {
		t.Errorf("transport.type = %v, want ws", transport["type"])
	}
}

func TestBuildConfig_VMess(t *testing.T) {
	node := &types.Node{
		Server:     "1.2.3.4",
		Port:       443,
		Protocol:   "vmess",
		UUID:       "abc-123",
		AlterID:    0,
		TLSEnabled: true,
		TLSSNI:     "example.com",
		Tag:        "vmess-test",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	if out["type"] != "vmess" {
		t.Errorf("type = %v, want vmess", out["type"])
	}
	if out["uuid"] != "abc-123" {
		t.Errorf("uuid = %v, want abc-123", out["uuid"])
	}
	if out["alter_id"].(float64) != 0 {
		t.Errorf("alter_id = %v, want 0", out["alter_id"])
	}
}

func TestBuildConfig_VMess_GRPC(t *testing.T) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          443,
		Protocol:      "vmess",
		UUID:          "abc-123",
		TLSEnabled:    true,
		TransportType: "grpc",
		TransportPath: "myService",
		Tag:           "vmess-grpc",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	transport := out["transport"].(map[string]interface{})
	if transport["type"] != "grpc" {
		t.Errorf("transport.type = %v, want grpc", transport["type"])
	}
	if transport["service_name"] != "myService" {
		t.Errorf("service_name = %v, want myService", transport["service_name"])
	}
}

func TestBuildConfig_VMess_HTTPUpgrade(t *testing.T) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          8080,
		Protocol:      "vmess",
		UUID:          "abc-123",
		TLSEnabled:    true,
		TLSSNI:        "example.com",
		TransportType: "httpupgrade",
		TransportHost: "example.com",
		TransportPath: "/ws",
		Tag:           "vmess-httpupgrade",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	transport := out["transport"].(map[string]interface{})
	if transport["type"] != "httpupgrade" {
		t.Errorf("transport.type = %v, want httpupgrade", transport["type"])
	}
}

func TestBuildConfig_VMess_WS(t *testing.T) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          443,
		Protocol:      "vmess",
		UUID:          "abc-123",
		TransportType: "ws",
		TransportHost: "example.com",
		TransportPath: "/ws",
		Tag:           "vmess-ws",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	transport := out["transport"].(map[string]interface{})
	if transport["type"] != "ws" {
		t.Errorf("transport.type = %v, want ws", transport["type"])
	}
	host := transport["host"].([]interface{})
	if host[0] != "example.com" {
		t.Errorf("host = %v, want example.com", host[0])
	}
	if transport["path"] != "/ws" {
		t.Errorf("path = %v, want /ws", transport["path"])
	}
}

func TestBuildConfig_Hy2(t *testing.T) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     8443,
		Protocol: "hy2",
		Password: "test123",
		Tag:      "hy2-test",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	if out["type"] != "hysteria2" {
		t.Errorf("type = %v, want hysteria2", out["type"])
	}
	if out["password"] != "test123" {
		t.Errorf("password = %v, want test123", out["password"])
	}
}

func TestBuildConfig_Hy2_TLS(t *testing.T) {
	node := &types.Node{
		Server:     "1.2.3.4",
		Port:       8443,
		Protocol:   "hy2",
		Password:   "pass",
		TLSEnabled: true,
		TLSSNI:     "example.com",
		Tag:        "hy2-tls",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	tls := out["tls"].(map[string]interface{})
	if tls["enabled"] != true {
		t.Error("tls.enabled should be true")
	}
}

func TestBuildConfig_UnknownProtocol(t *testing.T) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: "unknown",
		Tag:      "unknown-test",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	if out["type"] != "direct" {
		t.Errorf("unknown protocol should default to direct, got %v", out["type"])
	}
}

func TestBuildConfig_DirectOutbound(t *testing.T) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: "direct",
		Tag:      "my-direct",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	outbounds := parsed["outbounds"].([]interface{})
	// Should have 2 outbounds: direct + default direct
	for _, o := range outbounds {
		ob := o.(map[string]interface{})
		if ob["type"] != "direct" {
			t.Errorf("expected direct outbound, got %v", ob["type"])
		}
	}
}

func TestBuildConfig_Route(t *testing.T) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: "ss",
		Password: "test",
		Tag:      "my-tag",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	route := parsed["route"].(map[string]interface{})
	if route["final"] != "my-tag" {
		t.Errorf("route.final = %v, want my-tag", route["final"])
	}
	// auto_detect_interface must NOT be set: it requires a detectable default
	// interface and fails on boxes/routers/containers without one.
	if _, ok := route["auto_detect_interface"]; ok {
		t.Error("auto_detect_interface should not be set")
	}
}

func TestBuildConfig_DNS_UsesLocalResolver(t *testing.T) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: "ss",
		Password: "test",
		Tag:      "my-tag",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	dns, ok := parsed["dns"].(map[string]interface{})
	if !ok {
		t.Fatal("config should have a dns section")
	}
	servers, ok := dns["servers"].([]interface{})
	if !ok || len(servers) == 0 {
		t.Fatal("dns.servers should not be empty")
	}
	srv := servers[0].(map[string]interface{})
	// Must use the system resolver, not hardcoded public DNS over UDP
	// (8.8.8.8/8.8.4.4 are unreachable on some networks). The "local"
	// server type is the sing-box 1.12+ format ("address" was removed in
	// 1.14).
	if srv["type"] != "local" {
		t.Errorf("dns server type = %v, want local (system resolver)", srv["type"])
	}
	if srv["tag"] != "local-dns" {
		t.Errorf("dns server tag = %v, want local-dns", srv["tag"])
	}
}

func TestBuildConfig_Log(t *testing.T) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: "ss",
		Password: "test",
		Tag:      "test",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	log := parsed["log"].(map[string]interface{})
	if log["level"] != "warn" {
		t.Errorf("log.level = %v, want warn", log["level"])
	}
}

func TestBuildConfig_JSONValid(t *testing.T) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          443,
		Protocol:      "vless",
		UUID:          "test-uuid",
		TLSEnabled:    true,
		TLSSNI:        "example.com",
		TransportType: "ws",
		TransportHost: "example.com",
		TransportPath: "/ws",
		Tag:           "test",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed interface{}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("invalid JSON config: %v", err)
	}
	if len(cfg) < 100 {
		t.Error("config seems too short")
	}
}

func TestBuildConfig_PrettyPrinted(t *testing.T) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: "ss",
		Password: "test",
		Tag:      "test",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	// Check it's indented (pretty-printed)
	s := string(cfg)
	found := false
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '\n' {
			found = true
			break
		}
	}
	if !found {
		t.Error("config should be pretty-printed with newlines")
	}
}

func TestBuildConfig_VLESS_NoTransport(t *testing.T) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     443,
		Protocol: "vless",
		UUID:     "abc",
		Tag:      "no-transport",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	// Should NOT have transport key
	if _, ok := out["transport"]; ok {
		t.Error("should not have transport when TransportType is empty")
	}
	// Should NOT have tls key
	if _, ok := out["tls"]; ok {
		t.Error("should not have tls when TLSEnabled is false")
	}
}

func TestBuildConfig_VLESS_TCPTransport(t *testing.T) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          443,
		Protocol:      "vless",
		UUID:          "abc",
		TransportType: "tcp",
		Tag:           "tcp",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	json.Unmarshal(cfg, &parsed)

	out := parsed["outbounds"].([]interface{})[0].(map[string]interface{})
	if _, ok := out["transport"]; ok {
		t.Error("should not have transport when TransportType is tcp")
	}
}

// ============================================================
// Benchmarks
// ============================================================

func BenchmarkBuildConfig_SS(b *testing.B) {
	node := &types.Node{
		Server:        "3.96.124.46",
		Port:          443,
		Protocol:      "ss",
		Password:      "test123",
		EncryptMethod: "aes-256-gcm",
		Tag:           "bench-ss",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildConfig(node, "127.0.0.1", 1080)
	}
}

func BenchmarkBuildConfig_VLESS(b *testing.B) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          443,
		Protocol:      "vless",
		UUID:          "abc-123",
		TLSEnabled:    true,
		TLSSNI:        "example.com",
		TransportType: "ws",
		TransportHost: "example.com",
		TransportPath: "/ws",
		Tag:           "bench-vless",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildConfig(node, "127.0.0.1", 1080)
	}
}

func BenchmarkBuildConfig_Hy2(b *testing.B) {
	node := &types.Node{
		Server:   "1.2.3.4",
		Port:     8443,
		Protocol: "hy2",
		Password: "test123",
		Tag:      "bench-hy2",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BuildConfig(node, "127.0.0.1", 1080)
	}
}
