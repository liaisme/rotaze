package fetcher

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestParseVPNGateContent verifies a real VPNGate CSV row decodes into an
// OpenVPN node with the authoritative country and IP.
func TestParseVPNGateContent(t *testing.T) {
	cfg := base64.StdEncoding.EncodeToString([]byte(sampleOVPN))
	csv := "*vpn_servers\n#HostName,IP,Score,Ping,Speed,CountryLong,CountryShort,NumVpnSessions,Uptime,TotalUsers,TotalTraffic,LogType,Operator,Message,OpenVPN_ConfigData_Base64\n" +
		"public-vpn-1,1.2.3.4,2834781,11,449130471,Japan,JP,145,9543947179,25934377,1552488912438691,2weeks,op,msg," + cfg + "\n"

	nodes := parseVPNGateContent(csv)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	n := nodes[0]
	if n.Country != "JP" {
		t.Errorf("Country = %q, want JP", n.Country)
	}
	if n.Server != "1.2.3.4" {
		t.Errorf("Server = %q, want 1.2.3.4", n.Server)
	}
	if n.Protocol != "ovpn" {
		t.Errorf("Protocol = %q, want ovpn", n.Protocol)
	}
	if !strings.Contains(n.CACert, "BEGIN CERTIFICATE") {
		t.Error("CACert missing PEM block")
	}
}

func TestParseVPNGateContent_SkipsMalformed(t *testing.T) {
	csv := "*vpn_servers\n" +
		"badrow,no,enough,fields\n" +
		"good,9.9.9.9,100,10,1000,Japan,JP,1,1,1,1,1weeks,op,msg," + base64.StdEncoding.EncodeToString([]byte(sampleOVPN)) + "\n" +
		"notbase64,8.8.8.8,,,,,,,,,,,,,!!!\n"
	nodes := parseVPNGateContent(csv)
	if len(nodes) != 1 {
		t.Fatalf("expected 1 valid node, got %d", len(nodes))
	}
	if nodes[0].Server != "9.9.9.9" {
		t.Errorf("Server = %q, want 9.9.9.9", nodes[0].Server)
	}
}
