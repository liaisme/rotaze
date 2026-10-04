package fetcher

import (
	"strings"
	"testing"
)

const sampleOVPN = `client
dev tun
proto udp
remote vpn123.opengw.net 1194
resolv-retry infinite
nobind
persist-key
persist-tun
cipher AES-256-CBC
auth SHA256
comp-lzo no
<ca>
-----BEGIN CERTIFICATE-----
MIIBCgKCAQEAxCENSORED
-----END CERTIFICATE-----
</ca>
<cert>
-----BEGIN CERTIFICATE-----
MIICERTCENSORED
-----END CERTIFICATE-----
</cert>
<key>
-----BEGIN PRIVATE KEY-----
MIIKEYCENSORED
-----END PRIVATE KEY-----
</key>
`

func TestParseOVPN_Basic(t *testing.T) {
	node, err := ParseOVPN([]byte(sampleOVPN))
	if err != nil {
		t.Fatalf("ParseOVPN() error = %v", err)
	}
	if node.Server != "vpn123.opengw.net" {
		t.Errorf("Server = %q, want vpn123.opengw.net", node.Server)
	}
	if node.Port != 1194 {
		t.Errorf("Port = %d, want 1194", node.Port)
	}
	if node.Protocol != "ovpn" {
		t.Errorf("Protocol = %q, want ovpn", node.Protocol)
	}
	if node.EncryptMethod != "AES-256-CBC" {
		t.Errorf("EncryptMethod = %q, want AES-256-CBC", node.EncryptMethod)
	}
	if node.TransportType != "udp" {
		t.Errorf("TransportType = %q, want udp", node.TransportType)
	}
	if node.AuthMethod != "SHA256" {
		t.Errorf("AuthMethod = %q, want SHA256", node.AuthMethod)
	}
	if !strings.Contains(node.CACert, "BEGIN CERTIFICATE") {
		t.Error("CACert missing PEM block")
	}
	if !strings.Contains(node.ClientCert, "BEGIN CERTIFICATE") {
		t.Error("ClientCert missing PEM block")
	}
	if !strings.Contains(node.ClientKey, "BEGIN PRIVATE KEY") {
		t.Error("ClientKey missing PEM block")
	}
}
func TestParseOVPN_Defaults(t *testing.T) {
	// No proto/cipher lines → udp + AES-128-CBC defaults.
	cfg := strings.Replace(sampleOVPN, "proto udp\n", "", 1)
	cfg = strings.Replace(cfg, "cipher AES-256-CBC\n", "", 1)
	cfg = strings.Replace(cfg, "auth SHA256\n", "", 1)
	node, err := ParseOVPN([]byte(cfg))
	if err != nil {
		t.Fatalf("ParseOVPN() error = %v", err)
	}
	if node.EncryptMethod != "AES-128-CBC" {
		t.Errorf("EncryptMethod default = %q, want AES-128-CBC", node.EncryptMethod)
	}
	if node.TransportType != "udp" {
		t.Errorf("TransportType default = %q, want udp", node.TransportType)
	}
	if node.AuthMethod != "SHA1" {
		t.Errorf("AuthMethod default = %q, want SHA1", node.AuthMethod)
	}
}

func TestParseOVPN_StaticKeyRejected(t *testing.T) {
	cfg := strings.Replace(sampleOVPN, "<ca>\n-----BEGIN CERTIFICATE-----\nMIIBCgKCAQEAxCENSORED\n-----END CERTIFICATE-----\n</ca>\n", "", 1)
	if _, err := ParseOVPN([]byte(cfg)); err == nil {
		t.Error("expected error for static-key config, got nil")
	}
}

func TestParseOVPN_CountryFromHostname(t *testing.T) {
	cfg := strings.Replace(sampleOVPN, "vpn123.opengw.net", "vpn123.jp.opengw.net", 1)
	node, err := ParseOVPN([]byte(cfg))
	if err != nil {
		t.Fatalf("ParseOVPN() error = %v", err)
	}
	if node.Country != "JP" {
		t.Errorf("Country = %q, want JP", node.Country)
	}
}
