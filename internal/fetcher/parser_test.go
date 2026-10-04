package fetcher

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestParseVLESS(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
		server  string
		port    int
		proto   string
	}{
		{
			name:    "basic vless",
			uri:     "vless://abc123@1.2.3.4:443#VN-Test",
			wantErr: false,
			server:  "1.2.3.4",
			port:    443,
			proto:   "vless",
		},
		{
			name:    "vless with tls",
			uri:     "vless://abc123@1.2.3.4:443?security=tls&sni=example.com&type=ws&path=/ws#US-Node",
			wantErr: false,
			server:  "1.2.3.4",
			port:    443,
			proto:   "vless",
		},
		{
			name:    "vless missing uuid",
			uri:     "vless@1.2.3.4:443#test",
			wantErr: true,
		},
		{
			name:    "vless invalid host",
			uri:     "vless://abc123@invalid#test",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := parseVLESS(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseVLESS() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if node.Server != tt.server {
					t.Errorf("server = %v, want %v", node.Server, tt.server)
				}
				if node.Port != tt.port {
					t.Errorf("port = %v, want %v", node.Port, tt.port)
				}
				if node.Protocol != tt.proto {
					t.Errorf("protocol = %v, want %v", node.Protocol, tt.proto)
				}
			}
		})
	}
}

func TestParseShadowsocks(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
		server  string
		port    int
		method  string
	}{
		{
			name:    "plaintext ss",
			uri:     "ss://aes-256-gcm:password123@1.2.3.4:8388#VN-SS",
			wantErr: false,
			server:  "1.2.3.4",
			port:    8388,
			method:  "aes-256-gcm",
		},
		{
			name:    "ss missing @",
			uri:     "ss://aes-256-gcm:password123#test",
			wantErr: true,
		},
		{
			name:    "ss invalid port",
			uri:     "ss://aes-256-gcm:password123@1.2.3.4:abc#test",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := parseShadowsocks(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseShadowsocks() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if node.Server != tt.server {
					t.Errorf("server = %v, want %v", node.Server, tt.server)
				}
				if node.Port != tt.port {
					t.Errorf("port = %v, want %v", node.Port, tt.port)
				}
				if node.EncryptMethod != tt.method {
					t.Errorf("method = %v, want %v", node.EncryptMethod, tt.method)
				}
			}
		})
	}
}

func TestParseTrojan(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
		server  string
		port    int
	}{
		{
			name:    "basic trojan",
			uri:     "trojan://pass123@1.2.3.4:443?security=tls#SG-Trojan",
			wantErr: false,
			server:  "1.2.3.4",
			port:    443,
		},
		{
			name:    "trojan missing password",
			uri:     "trojan@1.2.3.4:443#test",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := parseTrojan(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTrojan() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if node.Server != tt.server {
					t.Errorf("server = %v, want %v", node.Server, tt.server)
				}
				if node.Port != tt.port {
					t.Errorf("port = %v, want %v", node.Port, tt.port)
				}
			}
		})
	}
}

func TestParseHy2(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
		server  string
		port    int
	}{
		{
			name:    "basic hy2",
			uri:     "hy2://pass123@1.2.3.4:443#JP-Hy2",
			wantErr: false,
			server:  "1.2.3.4",
			port:    443,
		},
		{
			name:    "hysteria2 prefix",
			uri:     "hysteria2://pass123@1.2.3.4:443#JP-Hy2",
			wantErr: false,
			server:  "1.2.3.4",
			port:    443,
		},
		{
			name:    "hy2 missing password",
			uri:     "hy2@1.2.3.4:443#test",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := parseHy2(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseHy2() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if node.Server != tt.server {
					t.Errorf("server = %v, want %v", node.Server, tt.server)
				}
				if node.Port != tt.port {
					t.Errorf("port = %v, want %v", node.Port, tt.port)
				}
				if node.Protocol != "hy2" {
					t.Errorf("protocol = %v, want hy2", node.Protocol)
				}
			}
		})
	}
}

func TestParseURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
		proto   string
	}{
		{"vless", "vless://abc@1.2.3.4:443", false, "vless"},
		{"ss", "ss://aes-256-gcm:pass@1.2.3.4:8388", false, "ss"},
		{"trojan", "trojan://pass@1.2.3.4:443", false, "trojan"},
		{"hy2", "hy2://pass@1.2.3.4:443", false, "hy2"},
		{"socks", "socks5://1.2.3.4:1080", false, "socks"},
		{"socks alias", "socks://1.2.3.4:1080", false, "socks"},
		{"empty", "", true, ""},
		{"unsupported", "ftp://example.com", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := parseURI(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseURI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && node.Protocol != tt.proto {
				t.Errorf("protocol = %v, want %v", node.Protocol, tt.proto)
			}
		})
	}
}

func TestExtractCountry(t *testing.T) {
	tests := []struct {
		tag  string
		want string
	}{
		{"VN", "VN"},
		{"US-Node-1", "US"},
		{"vietnam-server", "VN"},
		{"越南节点", "VN"},
		{"", "unknown"},
		{"random text", "unknown"},
		{"JP Tokyo", "JP"},
		{"SG Node", "SG"},
	}

	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			got := extractCountry(tt.tag)
			if got != tt.want {
				t.Errorf("extractCountry(%q) = %v, want %v", tt.tag, got, tt.want)
			}
		})
	}
}

func TestParseSocks(t *testing.T) {
	tests := []struct {
		name     string
		uri      string
		wantErr  bool
		server   string
		port     int
		user     string
		password string
		tag      string
	}{
		{"plain", "socks5://1.2.3.4:1080", false, "1.2.3.4", 1080, "", "", "socks-1.2.3.4-1080"},
		{"auth", "socks5://user:pass@1.2.3.4:1080#VN", false, "1.2.3.4", 1080, "user", "pass", "VN"},
		{"alias", "socks://1.2.3.4:1080", false, "1.2.3.4", 1080, "", "", "socks-1.2.3.4-1080"},
		{"no port", "socks5://1.2.3.4", true, "", 0, "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := parseSocks(tt.uri)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseSocks() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if node.Server != tt.server || node.Port != tt.port {
				t.Errorf("server:port = %s:%d, want %s:%d", node.Server, node.Port, tt.server, tt.port)
			}
			if node.Protocol != "socks" {
				t.Errorf("protocol = %q, want socks", node.Protocol)
			}
			if node.UUID != tt.user {
				t.Errorf("username = %q, want %q", node.UUID, tt.user)
			}
			if node.Password != tt.password {
				t.Errorf("password = %q, want %q", node.Password, tt.password)
			}
			if node.Tag != tt.tag {
				t.Errorf("tag = %q, want %q", node.Tag, tt.tag)
			}
		})
	}
}

func TestParseVMess(t *testing.T) {
	// Create a valid VMess URI using proper base64
	vmessConfig := map[string]string{
		"v":    "2",
		"ps":   "JP-Node",
		"add":  "1.2.3.4",
		"port": "443",
		"id":   "abc-123",
		"aid":  "0",
		"net":  "ws",
		"type": "none",
		"host": "example.com",
		"path": "/ws",
		"tls":  "tls",
		"sni":  "example.com",
	}
	b, _ := json.Marshal(vmessConfig)
	encoded := base64.StdEncoding.EncodeToString(b)
	uri := "vmess://" + encoded

	node, err := parseVMess(uri)
	if err != nil {
		t.Fatalf("parseVMess() error = %v", err)
	}

	if node.Server != "1.2.3.4" {
		t.Errorf("server = %v, want 1.2.3.4", node.Server)
	}
	if node.Port != 443 {
		t.Errorf("port = %v, want 443", node.Port)
	}
	if node.UUID != "abc-123" {
		t.Errorf("uuid = %v, want abc-123", node.UUID)
	}
	if node.TransportType != "ws" {
		t.Errorf("transport = %v, want ws", node.TransportType)
	}
	if !node.TLSEnabled {
		t.Error("tls should be enabled")
	}
	if node.Country != "JP" {
		t.Errorf("country = %v, want JP", node.Country)
	}
}

func TestParseVMessInvalid(t *testing.T) {
	// Invalid base64
	_, err := parseVMess("vmess://invalid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}

	// Empty base64
	_, err = parseVMess("vmess://")
	if err == nil {
		t.Error("expected error for empty vmess")
	}
}

func TestIsValidCountryCode(t *testing.T) {
	valid := []string{"VN", "US", "JP", "SG", "KR", "DE", "FR", "GB"}
	for _, code := range valid {
		if !isValidCountryCode(code) {
			t.Errorf("isValidCountryCode(%q) = false, want true", code)
		}
	}

	invalid := []string{"XX", "ZZ", "AB", "12"}
	for _, code := range invalid {
		if isValidCountryCode(code) {
			t.Errorf("isValidCountryCode(%q) = true, want false", code)
		}
	}
}
