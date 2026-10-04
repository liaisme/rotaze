package rotator

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rotaze/rotaze/pkg/types"
)

func TestBuildConfig_OpenVPN(t *testing.T) {
	node := &types.Node{
		Server:        "1.2.3.4",
		Port:          1194,
		Protocol:      "ovpn",
		Password:      "vpn",
		EncryptMethod: "AES-128-CBC",
		TransportType: "udp",
		CACert:        "ca-pem",
		ClientCert:    "cert-pem",
		ClientKey:     "key-pem",
		Tag:           "ovpn-test",
	}

	cfg := BuildConfig(node, "127.0.0.1", 1080)
	var parsed map[string]interface{}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("invalid config JSON: %v", err)
	}

	// Endpoint present, no outbound of type openvpn (endpoints[] is the
	// sing-box 1.14 home for OpenVPN).
	endpoints, ok := parsed["endpoints"].([]interface{})
	if !ok || len(endpoints) != 1 {
		t.Fatalf("endpoints = %#v, want 1 entry", parsed["endpoints"])
	}
	ep := endpoints[0].(map[string]interface{})
	if ep["type"] != "openvpn-client" {
		t.Errorf("endpoint type = %v, want openvpn-client", ep["type"])
	}
	if ep["server"] != "1.2.3.4" {
		t.Errorf("server = %v, want 1.2.3.4", ep["server"])
	}
	if ep["server_port"].(float64) != 1194 {
		t.Errorf("server_port = %v, want 1194", ep["server_port"])
	}
	if ep["network"] != "udp" {
		t.Errorf("network = %v, want udp", ep["network"])
	}
	ciphers := ep["data_ciphers"].([]interface{})
	if len(ciphers) != 1 || ciphers[0] != "AES-128-CBC" {
		t.Errorf("data_ciphers = %v, want [AES-128-CBC]", ep["data_ciphers"])
	}
	tls := ep["tls"].(map[string]interface{})
	if tls["certificate_path"] != "ca-pem" {
		t.Errorf("tls.certificate_path = %v, want ca-pem", tls["certificate_path"])
	}
	if tls["client_certificate_path"] != "cert-pem" {
		t.Errorf("tls.client_certificate_path = %v, want cert-pem", tls["client_certificate_path"])
	}
	if tls["client_key_path"] != "key-pem" {
		t.Errorf("tls.client_key_path = %v, want key-pem", tls["client_key_path"])
	}

	// route.final references the endpoint tag; no openvpn outbound exists.
	if parsed["route"].(map[string]interface{})["final"] != "ovpn-test" {
		t.Errorf("route.final = %v, want ovpn-test", parsed["route"].(map[string]interface{})["final"])
	}
	outbounds := parsed["outbounds"].([]interface{})
	for _, ob := range outbounds {
		if ob.(map[string]interface{})["type"] == "openvpn" || ob.(map[string]interface{})["type"] == "openvpn-client" {
			t.Errorf("OpenVPN must be an endpoint, not outbound: %#v", ob)
		}
	}
}

func TestBuildConfig_OpenVPN_Defaults(t *testing.T) {
	node := &types.Node{
		Server:     "5.6.7.8",
		Port:       443,
		Protocol:   "ovpn",
		Password:   "vpn",
		CACert:     "ca-pem",
		ClientCert: "cert-pem",
		ClientKey:  "key-pem",
		Tag:        "ovpn-tcp",
	}
	cfg := BuildConfig(node, "127.0.0.1", 1081)
	var parsed map[string]interface{}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	ep := parsed["endpoints"].([]interface{})[0].(map[string]interface{})
	if ep["network"] != "tcp" {
		t.Errorf("network default = %v, want tcp", ep["network"])
	}
	ciphers := ep["data_ciphers"].([]interface{})
	if ciphers[0] != "AES-128-CBC" {
		t.Errorf("data_ciphers default = %v, want AES-128-CBC", ciphers[0])
	}
	if s, _ := json.Marshal(cfg); strings.Contains(string(s), "outbounds.*openvpn") {
		t.Error("openvpn leaked into outbounds")
	}
}
