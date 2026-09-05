package fetcher

import (
	"strings"
	"testing"

	"github.com/rotaze/rotaze/pkg/types"
)

// ============================================================
// Fuzz Tests
// ============================================================

func FuzzParseURI(f *testing.F) {
	// Seed corpus
	f.Add("vless://abc@1.2.3.4:443#test")
	f.Add("ss://aes-256-gcm:pass@1.2.3.4:443#test")
	f.Add("trojan://pass@1.2.3.4:443#test")
	f.Add("hy2://pass@1.2.3.4:443#test")
	f.Add("vmess://eyJ2IjoiMiJ9")
	f.Add("")
	f.Add("://")
	f.Add("vless://")
	f.Add("vless@1.2.3.4:443")
	f.Add("ss://")
	f.Add("trojan://")
	f.Add("hy2://")
	f.Add("hysteria2://pass@1.2.3.4:443#test")
	f.Add("ftp://example.com")
	f.Add("http://example.com")
	f.Add("vless://abc@999.999.999.999:99999#test")
	f.Add("vless://abc@0.0.0.0:0#test")
	f.Add("ss://method:password@[::1]:443#test")
	f.Add("vless://abc@1.2.3.4:443?security=tls&sni=example.com&type=ws&host=example.com&path=/ws#test")

	f.Fuzz(func(t *testing.T, uri string) {
		// Should never panic
		node, err := parseURI(uri)
		if err != nil {
			return
		}

		// If parsing succeeded, basic invariants must hold
		if node.Server == "" {
			t.Errorf("parseURI(%q) returned node with empty Server", uri)
		}
		// Port validity is checked at a higher level; parser just parses the URI
		if node.Port < 0 {
			t.Errorf("parseURI(%q) returned negative port %d", uri, node.Port)
		}
		if node.Protocol == "" {
			t.Errorf("parseURI(%q) returned node with empty Protocol", uri)
		}
	})
}

func FuzzParseVLESS(f *testing.F) {
	f.Add("vless://abc@1.2.3.4:443#test")
	f.Add("vless://abc@1.2.3.4:443?security=tls&sni=example.com#test")
	f.Add("vless://abc@1.2.3.4:443?type=ws&host=example.com&path=/ws#test")
	f.Add("vless://abc@1.2.3.4:443?security=tls&type=grpc&path=myService#test")

	f.Fuzz(func(t *testing.T, uri string) {
		node, err := parseVLESS(uri)
		if err != nil {
			return
		}
		if node.Protocol != "vless" {
			t.Errorf("parseVLESS(%q) protocol = %s, want vless", uri, node.Protocol)
		}
	})
}

func FuzzParseShadowsocks(f *testing.F) {
	f.Add("ss://aes-256-gcm:password@1.2.3.4:443#test")
	f.Add("ss://chacha20-ietf-poly1305:pass@1.2.3.4:8388#test")

	f.Fuzz(func(t *testing.T, uri string) {
		node, err := parseShadowsocks(uri)
		if err != nil {
			return
		}
		if node.Protocol != "ss" {
			t.Errorf("parseShadowsocks(%q) protocol = %s, want ss", uri, node.Protocol)
		}
	})
}

func FuzzParseTrojan(f *testing.F) {
	f.Add("trojan://pass@1.2.3.4:443#test")
	f.Add("trojan://pass@1.2.3.4:443?security=tls#test")

	f.Fuzz(func(t *testing.T, uri string) {
		node, err := parseTrojan(uri)
		if err != nil {
			return
		}
		if node.Protocol != "trojan" {
			t.Errorf("parseTrojan(%q) protocol = %s, want trojan", uri, node.Protocol)
		}
	})
}

func FuzzParseHy2(f *testing.F) {
	f.Add("hy2://pass@1.2.3.4:443#test")
	f.Add("hysteria2://pass@1.2.3.4:443#test")

	f.Fuzz(func(t *testing.T, uri string) {
		node, err := parseHy2(uri)
		if err != nil {
			return
		}
		if node.Protocol != "hy2" {
			t.Errorf("parseHy2(%q) protocol = %s, want hy2", uri, node.Protocol)
		}
	})
}

func FuzzDeduplicateNodes(f *testing.F) {
	f.Add("", "", "", "")
	f.Add("1.2.3.4", "5.6.7.8", "9.10.11.12", "13.14.15.16")

	f.Fuzz(func(t *testing.T, s1, s2, s3, s4 string) {
		nodes := []*types.Node{
			{Server: s1, Port: 443, Protocol: "vless"},
			{Server: s2, Port: 443, Protocol: "vless"},
			{Server: s3, Port: 443, Protocol: "ss"},
			{Server: s4, Port: 443, Protocol: "ss"},
		}

		result := DeduplicateNodes(nodes)
		if len(result) > len(nodes) {
			t.Errorf("DeduplicateNodes returned more nodes than input")
		}

		// Check uniqueness
		seen := make(map[string]bool)
		for _, n := range result {
			key := n.Server + ":" + n.Protocol
			if seen[key] {
				t.Errorf("duplicate in output: %s", key)
			}
			seen[key] = true
		}
	})
}

func FuzzFilterByCountry(f *testing.F) {
	f.Add("VN", "VN", "US", "unknown")
	f.Add("us", "VN", "US", "JP")
	f.Add("ALL", "VN", "US", "JP")

	f.Fuzz(func(t *testing.T, country, c1, c2, c3 string) {
		nodes := []*types.Node{
			{Server: "1.2.3.4", Port: 443, Protocol: "vless", Country: c1},
			{Server: "5.6.7.8", Port: 443, Protocol: "ss", Country: c2},
			{Server: "9.10.11.12", Port: 443, Protocol: "trojan", Country: c3},
		}

		result := FilterByCountry(nodes, country)

		// No filter requested (""/ALL): everything passes through.
		if country == "" || strings.EqualFold(country, "all") {
			if len(result) != len(nodes) {
				t.Errorf("FilterByCountry(%s) should pass through all nodes, got %d of %d", country, len(result), len(nodes))
			}
			return
		}

		// Strict: every returned node's country matches the request exactly.
		// Unknown/unrelated nodes are never assumed to match.
		for _, n := range result {
			if !strings.EqualFold(n.Country, country) {
				t.Errorf("FilterByCountry(%s) returned node with country %s", country, n.Country)
			}
		}
	})
}

func FuzzConvertSingboxJSONToURI(f *testing.F) {
	f.Add(`{"type":"vless","server":"1.2.3.4","server_port":443,"uuid":"abc","tag":"test"}`)
	f.Add(`{"type":"shadowsocks","server":"1.2.3.4","server_port":443,"method":"aes-256-gcm","password":"pass","tag":"test"}`)
	f.Add(`{"type":"trojan","server":"1.2.3.4","server_port":443,"password":"pass","tag":"test"}`)
	f.Add(`{"type":"vmess","server":"1.2.3.4","server_port":443,"uuid":"abc","tag":"test"}`)
	f.Add(`{}`)
	f.Add(`not json`)

	f.Fuzz(func(t *testing.T, jsonStr string) {
		// Should never panic
		uri := convertSingboxJSONToURI(jsonStr)

		// If non-empty, should be a valid-looking URI
		if uri != "" {
			if len(uri) < 5 {
				t.Errorf("convertSingboxJSONToURI returned short URI: %q", uri)
			}
		}
	})
}

func FuzzExtractCountry(f *testing.F) {
	f.Add("VN")
	f.Add("US-Node-1")
	f.Add("vietnam-server")
	f.Add("")
	f.Add("random")
	f.Add("123")
	f.Add("a")
	f.Add("JP Tokyo")

	f.Fuzz(func(t *testing.T, tag string) {
		result := extractCountry(tag)
		if result == "" {
			t.Errorf("extractCountry(%q) returned empty string", tag)
		}
	if result != "unknown" && len(result) > 5 {
		t.Errorf("extractCountry(%q) returned suspicious result: %s", tag, result)
	}
	})
}

func FuzzIsValidCountryCode(f *testing.F) {
	f.Add("VN")
	f.Add("US")
	f.Add("XX")
	f.Add("")
	f.Add("a")
	f.Add("12")
	f.Add("JPN")
	f.Add("de")

	f.Fuzz(func(t *testing.T, code string) {
		// Should never panic
		_ = isValidCountryCode(code)
	})
}

func FuzzIsKnownProtocol(f *testing.F) {
	f.Add("vless://x@1.2.3.4:443")
	f.Add("ss://x@1.2.3.4:443")
	f.Add("trojan://x@1.2.3.4:443")
	f.Add("vmess://x")
	f.Add("hy2://x@1.2.3.4:443")
	f.Add("http://example.com")
	f.Add("")

	f.Fuzz(func(t *testing.T, uri string) {
		// Should never panic
		_ = isKnownProtocol(uri)
	})
}
