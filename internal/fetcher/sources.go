package fetcher

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// Fetch timeout per source
	fetchTimeout = 30 * time.Second
)

// Source represents a node subscription source.
type Source struct {
	Name string
	URL  string
	// Kind selects how lines are interpreted:
	//   "uri"    - proxy URIs (vless://, ss://, ...) [default]
	//   "socks"  - plain IP:PORT SOCKS5 proxies
	//   "vpngate" - VPNGate OpenVPN CSV (https://www.vpngate.net/api/iphone/)
	Kind string
}

// Sources returns the configured subscription sources.
func Sources(country string) []Source {
	c := strings.ToUpper(country)
	if c == "" || c == "ALL" {
		c = ""
	}

	sources := []Source{
		// Au1rxx - v2ray base64 format (one URI per line, base64 encoded)
		{
			Name: "Au1rxx",
			URL:  "https://raw.githubusercontent.com/Au1rxx/free-vpn-subscriptions/main/output/v2ray-base64.txt",
		},
		// SSAgg - base64 merged pool
		{
			Name: "SSAgg",
			URL:  "https://raw.githubusercontent.com/mahdibland/SSAggregator/master/sub/sub_merge_base64.txt",
		},
	}

	// Public SOCKS5 proxy lists (plain IP:PORT). The proxy's own IP is its
	// exit IP, so GeoIP on the server address yields the real exit country.
	sources = append(sources, Source{
		Name: "MonosansSocks",
		URL:  "https://raw.githubusercontent.com/monosans/proxy-list/main/proxies/socks5.txt",
		Kind: "socks",
	})
	sources = append(sources, Source{
		Name: "TheSpeedXSocks",
		URL:  "https://raw.githubusercontent.com/TheSpeedX/PROXY-List/master/socks5.txt",
		Kind: "socks",
	})

	// ConfigForge global list (ranked by latency)
	sources = append(sources, Source{
		Name: "ConfigForge",
		URL:  "https://raw.githubusercontent.com/ShatakVPN/ConfigForge-V2Ray/main/configs/all.txt",
	})

	// V2RaySub merged pool
	sources = append(sources, Source{
		Name: "V2RaySub",
		URL:  "https://raw.githubusercontent.com/barry-far/V2ray-Config/main/Sub1.txt",
	})

	//peasoft NoMoreWalls
	sources = append(sources, Source{
		Name: "NoMoreWalls",
		URL:  "https://raw.githubusercontent.com/peasoft/NoMoreWalls/master/list.txt",
	})
	// VPNGate free OpenVPN servers (CSV with base64-encoded configs).
	sources = append(sources, Source{
		Name: "VPNGate",
		URL:  "https://www.vpngate.net/api/iphone/",
		Kind: "vpngate",
	})
	// VPNGate mirror on GitHub (same CSV, updated every 2h). VPNGate itself
	// throttles direct fetches to a few KB/s, making the 1.3MB CSV time out;
	// the mirror serves the identical format from raw.githubusercontent.com.
	sources = append(sources, Source{
		Name: "VPNGateMirror",
		URL:  "https://raw.githubusercontent.com/funcra/vg-mirror/main/servers.csv",
		Kind: "vpngate",
	})

	return sources
}

// fetchSource fetches raw content from a URL with timeout.
func fetchSource(ctx context.Context, source Source) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", source.URL, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	// Set a reasonable user agent
	req.Header.Set("User-Agent", "Rotaze/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", source.Name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch %s: HTTP %d", source.Name, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", source.Name, err)
	}

	return string(body), nil
}

// parseSourceContent parses the raw content from a source into URIs.
func parseSourceContent(source Source, content string) []string {
	lines := strings.Split(content, "\n")
	var uris []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}

		// Plain IP:PORT lines only come from socks-kind sources.
		if source.Kind == "socks" {
			if parseSocksLine(line) {
				uris = append(uris, "socks5://"+line)
			}
			continue
		}

		// Check if this is a sing-box JSON outbound (Au1rxx format)
		if strings.HasPrefix(line, "{") {
			uri := convertSingboxJSONToURI(line)
			if uri != "" {
				uris = append(uris, uri)
			}
			continue
		}

		// Check if this is base64-encoded (SSAgg format)
		if isBase64(line) {
			decoded, err := base64Decode(line)
			if err == nil {
				// Decoded content might contain multiple URIs separated by newlines
				subLines := strings.Split(decoded, "\n")
				for _, subLine := range subLines {
					subLine = strings.TrimSpace(subLine)
					if subLine != "" && isKnownProtocol(subLine) {
						uris = append(uris, subLine)
					}
				}
				continue
			}
		}

		// Direct URI (ConfigForge format)
		if isKnownProtocol(line) {
			uris = append(uris, line)
		}
	}

	return uris
}

// parseSocksLine validates a plain "IP:PORT" SOCKS5 proxy line with an IP
// literal host (no hostnames). The proxy's own address is its exit address
// and must be geo-resolvable.
func parseSocksLine(line string) bool {
	host, portStr, err := net.SplitHostPort(line)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	p, err := strconv.Atoi(portStr)
	return err == nil && p > 0 && p <= 65535
}

// convertSingboxJSONToURI converts a sing-box JSON outbound to a URI.
func convertSingboxJSONToURI(jsonStr string) string {

	// Parse sing-box JSON format
	// {"type":"vless","server":"1.2.3.4","server_port":443,"uuid":"xxx",...}
	var cfg struct {
		Type       string `json:"type"`
		Server     string `json:"server"`
		ServerPort int    `json:"server_port"`
		UUID       string `json:"uuid"`
		Password   string `json:"password"`
		Method     string `json:"method"`
		TLS        struct {
			Enabled bool   `json:"enabled"`
			Server  string `json:"server_name"`
		} `json:"tls"`
		Transport struct {
			Type string `json:"type"`
			Host string `json:"host"`
			Path string `json:"path"`
		} `json:"transport"`
		Tag string `json:"tag"`
	}

	if err := jsonUnmarshal([]byte(jsonStr), &cfg); err != nil {
		return ""
	}

	if cfg.Server == "" || cfg.ServerPort == 0 {
		return ""
	}

	tag := cfg.Tag
	if tag == "" {
		tag = fmt.Sprintf("%s-%s-%d", cfg.Type, cfg.Server, cfg.ServerPort)
	}

	serverAddr := fmt.Sprintf("%s:%d", cfg.Server, cfg.ServerPort)

	switch cfg.Type {
	case "vless":
		uri := fmt.Sprintf("vless://%s@%s", cfg.UUID, serverAddr)
		params := []string{}
		if cfg.TLS.Enabled {
			params = append(params, "security=tls")
			if cfg.TLS.Server != "" {
				params = append(params, "sni="+cfg.TLS.Server)
			}
		}
		if cfg.Transport.Type != "" && cfg.Transport.Type != "tcp" {
			params = append(params, "type="+cfg.Transport.Type)
			if cfg.Transport.Host != "" {
				params = append(params, "host="+cfg.Transport.Host)
			}
			if cfg.Transport.Path != "" {
				params = append(params, "path="+cfg.Transport.Path)
			}
		}
		if len(params) > 0 {
			uri += "?" + strings.Join(params, "&")
		}
		uri += "#" + tag
		return uri

	case "shadowsocks", "ss":
		cred := fmt.Sprintf("%s:%s", cfg.Method, cfg.Password)
		encoded := base64.StdEncoding.EncodeToString([]byte(cred))
		return fmt.Sprintf("ss://%s@%s#%s", encoded, serverAddr, tag)

	case "trojan":
		uri := fmt.Sprintf("trojan://%s@%s", cfg.Password, serverAddr)
		if cfg.TLS.Enabled {
			uri += "?security=tls"
			if cfg.TLS.Server != "" {
				uri += "&sni=" + cfg.TLS.Server
			}
		}
		uri += "#" + tag
		return uri

	case "vmess":
		vmess := map[string]interface{}{
			"v":    "2",
			"ps":   tag,
			"add":  cfg.Server,
			"port": fmt.Sprintf("%d", cfg.ServerPort),
			"id":   cfg.UUID,
			"aid":  "0",
			"net":  cfg.Transport.Type,
			"type": "none",
			"host": cfg.Transport.Host,
			"path": cfg.Transport.Path,
			"tls":  "",
			"sni":  cfg.TLS.Server,
		}
		if cfg.TLS.Enabled {
			vmess["tls"] = "tls"
		}
		b, _ := jsonMarshal(vmess)
		encoded := base64.StdEncoding.EncodeToString(b)
		return "vmess://" + encoded

	case "socks":
		uri := fmt.Sprintf("socks5://%s", serverAddr)
		uri += "#" + tag
		return uri
	}

	return ""
}

// isBase64 checks if a string looks like base64-encoded content.
func isBase64(s string) bool {
	if len(s) < 4 {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		_, err = base64.URLEncoding.DecodeString(s)
	}
	return err == nil
}

// isKnownProtocol checks if a string is a known proxy URI.
func isKnownProtocol(s string) bool {
	return strings.HasPrefix(s, "vless://") ||
		strings.HasPrefix(s, "ss://") ||
		strings.HasPrefix(s, "trojan://") ||
		strings.HasPrefix(s, "vmess://") ||
		strings.HasPrefix(s, "hy2://") ||
		strings.HasPrefix(s, "hysteria2://") ||
		strings.HasPrefix(s, "socks://") ||
		strings.HasPrefix(s, "socks5://")
}

// jsonMarshal is a helper for encoding/json.
func jsonMarshal(v interface{}) ([]byte, error) {
	return jsonMarshalImpl(v)
}

// jsonUnmarshal is a helper for encoding/json.
func jsonUnmarshal(data []byte, v interface{}) error {
	return jsonUnmarshalImpl(data, v)
}
