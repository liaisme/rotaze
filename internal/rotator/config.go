package rotator

import (
	"encoding/json"

	"github.com/rotaze/rotaze/pkg/types"
)

// BuildConfig generates a sing-box JSON config for the given node.
func BuildConfig(node *types.Node, listenAddr string, listenPort int) json.RawMessage {
	// OpenVPN is an endpoint in sing-box, not an outbound: it needs its own
	// endpoints[] entry plus a direct outbound that routing falls through to.
	config := map[string]interface{}{
		"log": map[string]interface{}{
			"level": "warn",
		},
		// Use the system resolver instead of sing-box's built-in default
		// (8.8.8.8/8.8.4.4 over UDP), which fails on networks that block
		// outbound UDP 53 or on boxes without a routable default interface.
		// For OpenVPN the tunnel's DHCP-overridden resolver must not be used
		// either: on boxes with a hijacked/fake-ip resolver the tunnel cannot
		// route the fake addresses, so DNS must resolve through DoH.
		"dns": buildDNS(node),
		"inbounds": []interface{}{
			map[string]interface{}{
				// "mixed" serves both SOCKS5 and HTTP CONNECT on the same
				// port, so apps can point at http://127.0.0.1:1080 or
				// socks5://127.0.0.1:1080 interchangeably.
				"type":        "mixed",
				"tag":         "mixed-in",
				"listen":      listenAddr,
				"listen_port": listenPort,
			},
		},
		"outbounds": []interface{}{
			map[string]interface{}{
				"type": "direct",
				"tag":  "direct",
			},
		},
		"route": map[string]interface{}{
			"rules": []interface{}{},
			"final": node.Tag,
		},
	}

	if node.Protocol == "ovpn" {
		config["endpoints"] = []interface{}{buildOpenVPNServer(node)}
	} else {
		config["outbounds"] = []interface{}{
			buildOutbound(node),
			map[string]interface{}{
				"type": "direct",
				"tag":  "direct",
			},
		}
	}

	b, _ := json.MarshalIndent(config, "", "  ")
	return b
}

// buildDNS returns the DNS server block. OpenVPN tunnels need DoH because
// the tunnel cannot carry fake-ip / hijacked resolver answers; everything
// else uses the local system resolver.
func buildDNS(node *types.Node) map[string]interface{} {
	if node.Protocol == "ovpn" {
		return map[string]interface{}{
			"servers": []interface{}{
				map[string]interface{}{
					"type":        "https",
					"tag":         "doh",
					"server":      "8.8.8.8",
					"server_port": 443,
				},
			},
			"final":    "doh",
			"strategy": "ipv4_only",
		}
	}
	return map[string]interface{}{
		"servers": []interface{}{
			map[string]interface{}{
				"type": "local",
				"tag":  "local-dns",
			},
		},
	}
}

// buildOutbound creates an outbound configuration based on the node's protocol.
func buildOutbound(node *types.Node) map[string]interface{} {
	switch node.Protocol {
	case "vless":
		return buildVLESSOutbound(node)
	case "ss":
		return buildSSOutbound(node)
	case "trojan":
		return buildTrojanOutbound(node)
	case "vmess":
		return buildVMessOutbound(node)
	case "hy2":
		return buildHy2Outbound(node)
	case "http":
		return buildHTTPOutbound(node)
	case "socks":
		return buildSocksOutbound(node)
	default:
		return buildDirectOutbound(node.Tag)
	}
}

// buildOpenVPNServer builds a sing-box 1.14 openvpn-client endpoint config.
// sing-box moved OpenVPN support into endpoints[] (type "openvpn-client");
// route.final references the endpoint tag and traffic falls through the
// direct outbound once the tunnel carries it.
func buildOpenVPNServer(node *types.Node) map[string]interface{} {
	proto := "tcp"
	if node.TransportType != "" {
		proto = node.TransportType
	}
	cipher := node.EncryptMethod
	if cipher == "" {
		cipher = "AES-128-CBC"
	}
	auth := node.AuthMethod
	if auth == "" {
		auth = "SHA1"
	}
	endpoint := map[string]interface{}{
		"type":         "openvpn-client",
		"tag":          node.Tag,
		"mode":         "tls",
		"server":       node.Server,
		"server_port":  node.Port,
		"network":      proto,
		"username":     "vpn",
		"password":     node.Password,
		"auth":         auth,
		"data_ciphers": []interface{}{cipher},
		"tls": map[string]interface{}{
			"certificate_path":        node.CACert,
			"client_certificate_path": node.ClientCert,
			"client_key_path":         node.ClientKey,
		},
	}
	if node.TLSSNI != "" {
		endpoint["tls"].(map[string]interface{})["server_name"] = node.TLSSNI
	}
	return endpoint
}

// buildSocksOutbound creates a plain SOCKS5 upstream outbound.
func buildSocksOutbound(node *types.Node) map[string]interface{} {
	out := map[string]interface{}{
		"type":        "socks",
		"server":      node.Server,
		"server_port": node.Port,
		"version":     "5",
		"tag":         node.Tag,
	}

	if node.UUID != "" {
		out["username"] = node.UUID
	}
	if node.Password != "" {
		out["password"] = node.Password
	}

	return out
}

// buildVLESSOutbound creates a VLESS outbound configuration.
func buildVLESSOutbound(node *types.Node) map[string]interface{} {
	out := map[string]interface{}{
		"type":        "vless",
		"server":      node.Server,
		"server_port": node.Port,
		"uuid":        node.UUID,
		"tag":         node.Tag,
		"flow":        "xtls-rprx-vision",
	}

	if node.TLSEnabled {
		out["tls"] = map[string]interface{}{
			"enabled":     true,
			"server_name": node.TLSSNI,
			"insecure":    true,
		}
	}

	if node.TransportType != "" && node.TransportType != "tcp" {
		transport := map[string]interface{}{
			"type": node.TransportType,
		}
		switch node.TransportType {
		case "ws":
			if node.TransportHost != "" {
				transport["headers"] = map[string]interface{}{"Host": node.TransportHost}
			}
			if node.TransportPath != "" {
				transport["path"] = node.TransportPath
			}
		case "grpc":
			if node.TransportPath != "" {
				transport["service_name"] = node.TransportPath
			}
		case "httpupgrade":
			if node.TransportHost != "" {
				transport["host"] = node.TransportHost
			}
			if node.TransportPath != "" {
				transport["path"] = node.TransportPath
			}
		default:
			if node.TransportHost != "" {
				transport["host"] = node.TransportHost
			}
			if node.TransportPath != "" {
				transport["path"] = node.TransportPath
			}
		}
		out["transport"] = transport
	}

	return out
}

// buildSSOutbound creates a Shadowsocks outbound configuration.
func buildSSOutbound(node *types.Node) map[string]interface{} {
	method := node.EncryptMethod
	if method == "" {
		method = "aes-256-gcm"
	}

	return map[string]interface{}{
		"type":        "shadowsocks",
		"server":      node.Server,
		"server_port": node.Port,
		"method":      method,
		"password":    node.Password,
		"tag":         node.Tag,
	}
}

// buildTrojanOutbound creates a Trojan outbound configuration.
func buildTrojanOutbound(node *types.Node) map[string]interface{} {
	out := map[string]interface{}{
		"type":        "trojan",
		"server":      node.Server,
		"server_port": node.Port,
		"password":    node.Password,
		"tag":         node.Tag,
	}

	if node.TLSEnabled {
		out["tls"] = map[string]interface{}{
			"enabled":     true,
			"server_name": node.TLSSNI,
			"insecure":    true,
		}
	}

	if node.TransportType != "" && node.TransportType != "tcp" {
		transport := map[string]interface{}{
			"type": node.TransportType,
		}
		switch node.TransportType {
		case "ws":
			// sing-box ws transport has no "host" field; the Host header
			// belongs in headers.Host.
			if node.TransportHost != "" {
				transport["headers"] = map[string]interface{}{"Host": node.TransportHost}
			}
			if node.TransportPath != "" {
				transport["path"] = node.TransportPath
			}
		default:
			if node.TransportHost != "" {
				transport["host"] = node.TransportHost
			}
			if node.TransportPath != "" {
				transport["path"] = node.TransportPath
			}
		}
		out["transport"] = transport
	}

	return out
}

// buildVMessOutbound creates a VMess outbound configuration.
func buildVMessOutbound(node *types.Node) map[string]interface{} {
	out := map[string]interface{}{
		"type":        "vmess",
		"server":      node.Server,
		"server_port": node.Port,
		"uuid":        node.UUID,
		"alter_id":    node.AlterID,
		"security":    "auto",
		"tag":         node.Tag,
	}

	if node.TLSEnabled {
		out["tls"] = map[string]interface{}{
			"enabled":     true,
			"server_name": node.TLSSNI,
			"insecure":    true,
		}
	}

	if node.TransportType != "" && node.TransportType != "tcp" {
		transport := map[string]interface{}{
			"type": node.TransportType,
		}
		switch node.TransportType {
		case "ws":
			// sing-box ws transport has no "host" field; the Host header
			// belongs in headers.Host.
			if node.TransportHost != "" {
				transport["headers"] = map[string]interface{}{"Host": node.TransportHost}
			}
			if node.TransportPath != "" {
				transport["path"] = node.TransportPath
			}
		case "grpc":
			if node.TransportPath != "" {
				transport["service_name"] = node.TransportPath
			}
		case "httpupgrade":
			if node.TransportHost != "" {
				transport["host"] = node.TransportHost
			}
			if node.TransportPath != "" {
				transport["path"] = node.TransportPath
			}
		default:
			// Generic transport fallback
			if node.TransportHost != "" {
				transport["host"] = node.TransportHost
			}
			if node.TransportPath != "" {
				transport["path"] = node.TransportPath
			}
		}
		out["transport"] = transport
	}

	return out
}

// buildHy2Outbound creates a Hysteria2 outbound configuration.
func buildHy2Outbound(node *types.Node) map[string]interface{} {
	out := map[string]interface{}{
		"type":        "hysteria2",
		"server":      node.Server,
		"server_port": node.Port,
		"password":    node.Password,
		"tag":         node.Tag,
	}

	if node.TLSEnabled {
		out["tls"] = map[string]interface{}{
			"enabled":     true,
			"server_name": node.TLSSNI,
			"insecure":    true,
		}
	}

	return out
}

// buildHTTPOutbound creates an HTTP proxy outbound configuration.
func buildHTTPOutbound(node *types.Node) map[string]interface{} {
	out := map[string]interface{}{
		"type":        "http",
		"server":      node.Server,
		"server_port": node.Port,
		"tag":         node.Tag,
	}

	// Add TLS if specified
	if node.TLSEnabled {
		out["tls"] = map[string]interface{}{
			"enabled":     true,
			"server_name": node.TLSSNI,
			"insecure":    true,
		}
	}

	// Add basic auth if password is set
	if node.Password != "" {
		out["username"] = node.UUID // reuse UUID field for HTTP username
		out["password"] = node.Password
	}

	return out
}

// buildDirectOutbound creates a direct (no proxy) outbound.
func buildDirectOutbound(tag string) map[string]interface{} {
	return map[string]interface{}{
		"type": "direct",
		"tag":  tag,
	}
}
