package rotator

import (
	"encoding/json"

	"github.com/rotaze/rotaze/pkg/types"
)

// BuildConfig generates a sing-box JSON config for the given node.
func BuildConfig(node *types.Node, listenAddr string, listenPort int) json.RawMessage {
	outbound := buildOutbound(node)

	// sing-box 1.13+ config format (no legacy fields)
	config := map[string]interface{}{
		"log": map[string]interface{}{
			"level": "warn",
		},
		// Use the system resolver instead of sing-box's built-in default
		// (8.8.8.8/8.8.4.4 over UDP), which fails on networks that block
		// outbound UDP 53 or on boxes without a routable default interface.
		"dns": map[string]interface{}{
			"servers": []interface{}{
				map[string]interface{}{
					"type": "local",
					"tag":  "local-dns",
				},
			},
		},
		"inbounds": []interface{}{
			map[string]interface{}{
				"type":    "socks",
				"tag":     "socks-in",
				"listen":  listenAddr,
				"listen_port": listenPort,
			},
		},
		"outbounds": []interface{}{
			outbound,
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

	b, _ := json.MarshalIndent(config, "", "  ")
	return b
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
	default:
		return buildDirectOutbound(node.Tag)
	}
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
				transport["host"] = []string{node.TransportHost}
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
		if node.TransportHost != "" {
			transport["host"] = node.TransportHost
		}
		if node.TransportPath != "" {
			transport["path"] = node.TransportPath
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
			if node.TransportHost != "" {
				transport["host"] = []string{node.TransportHost}
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

