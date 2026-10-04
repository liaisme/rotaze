// Package fetcher handles node fetching from multiple sources and URI parsing.
package fetcher

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/rotaze/rotaze/pkg/types"
)

// ParseURIPublic is the public wrapper for parseURI.
func ParseURIPublic(uri string) (*types.Node, error) {
	return parseURI(uri)
}

// parseURI detects the protocol and delegates to the appropriate parser.
func parseURI(uri string) (*types.Node, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return nil, fmt.Errorf("empty URI")
	}

	switch {
	case strings.HasPrefix(uri, "vless://"):
		return parseVLESS(uri)
	case strings.HasPrefix(uri, "ss://"):
		return parseShadowsocks(uri)
	case strings.HasPrefix(uri, "trojan://"):
		return parseTrojan(uri)
	case strings.HasPrefix(uri, "hy2://") || strings.HasPrefix(uri, "hysteria2://"):
		return parseHy2(uri)
	case strings.HasPrefix(uri, "socks://") || strings.HasPrefix(uri, "socks5://"):
		return parseSocks(uri)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", uri[:min(20, len(uri))])
	}
}

// parseSocks parses a plain SOCKS5 proxy URI.
// Format: socks5://[user:pass@]host:port#tag (socks:// alias accepted)
func parseSocks(uri string) (*types.Node, error) {
	tag := ""
	if idx := strings.Index(uri, "#"); idx != -1 {
		tag = uri[idx+1:]
		uri = uri[:idx]
	}

	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SOCKS URI: %w", err)
	}

	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, fmt.Errorf("SOCKS URI invalid host:port: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("SOCKS URI invalid port: %w", err)
	}

	country := extractCountry(tag)
	if tag == "" {
		tag = fmt.Sprintf("socks-%s-%d", host, port)
	}

	node := &types.Node{
		Server:     host,
		Port:       port,
		Protocol:   "socks",
		Country:    country,
		UUID:       u.User.Username(),
		Password:   "",
		TLSEnabled: false,
		Tag:        tag,
	}
	if pw, ok := u.User.Password(); ok {
		node.Password = pw
	}

	return node, nil
}

// parseVLESS parses a VLESS URI.
// Format: vless://uuid@server:port?type=ws&security=tls&sni=domain#tag
func parseVLESS(uri string) (*types.Node, error) {
	// Remove fragment first
	tag := ""
	if idx := strings.Index(uri, "#"); idx != -1 {
		tag = uri[idx+1:]
		uri = uri[:idx]
	}

	// Parse URL
	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to parse VLESS URI: %w", err)
	}

	uuid := u.User.String()
	if uuid == "" {
		return nil, fmt.Errorf("VLESS URI missing UUID")
	}

	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, fmt.Errorf("VLESS URI invalid host:port: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("VLESS URI invalid port: %w", err)
	}

	q := u.Query()
	security := q.Get("security")
	tlsEnabled := security == "tls" || security == "reality"
	tlsSNI := q.Get("sni")
	if tlsSNI == "" {
		tlsSNI = q.Get("peer")
	}

	transportType := q.Get("type")
	if transportType == "" {
		transportType = "tcp"
	}
	transportHost := q.Get("host")
	transportPath := q.Get("path")

	country := extractCountry(tag)

	node := &types.Node{
		Server:        host,
		Port:          port,
		Protocol:      "vless",
		Country:       country,
		UUID:          uuid,
		TLSEnabled:    tlsEnabled,
		TLSSNI:        tlsSNI,
		TransportType: transportType,
		TransportHost: transportHost,
		TransportPath: transportPath,
		Tag:           fmt.Sprintf("vless-%s-%d", host, port),
	}

	return node, nil
}

// parseShadowsocks parses a Shadowsocks URI.
// Format: ss://method:password@server:port#tag
// or: ss://base64(method:password)@server:port#tag
func parseShadowsocks(uri string) (*types.Node, error) {
	tag := ""
	if idx := strings.Index(uri, "#"); idx != -1 {
		tag = uri[idx+1:]
		uri = uri[:idx]
	}

	// Remove ss:// prefix
	uri = uri[5:]

	// Try to find the @ separator
	atIdx := strings.LastIndex(uri, "@")
	if atIdx == -1 {
		return nil, fmt.Errorf("Shadowsocks URI missing @")
	}

	left := uri[:atIdx]
	right := uri[atIdx+1:]

	// Check if left part is base64-encoded
	method, password := "", ""
	if strings.Contains(left, ":") {
		// Plaintext: method:password
		parts := strings.SplitN(left, ":", 2)
		method = parts[0]
		password = parts[1]
	} else {
		// Try base64 decode
		decoded, err := base64Decode(left)
		if err == nil && strings.Contains(decoded, ":") {
			parts := strings.SplitN(decoded, ":", 2)
			method = parts[0]
			password = parts[1]
		} else {
			return nil, fmt.Errorf("Shadowsocks URI invalid credentials")
		}
	}

	// Parse server:port
	host, portStr, err := net.SplitHostPort(right)
	if err != nil {
		return nil, fmt.Errorf("Shadowsocks URI invalid host:port: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("Shadowsocks URI invalid port: %w", err)
	}

	country := extractCountry(tag)

	node := &types.Node{
		Server:        host,
		Port:          port,
		Protocol:      "ss",
		Country:       country,
		Password:      password,
		EncryptMethod: method,
		TLSEnabled:    false,
		Tag:           fmt.Sprintf("ss-%s-%d", host, port),
	}

	return node, nil
}

// parseTrojan parses a Trojan URI.
// Format: trojan://password@server:port?security=tls&sni=domain#tag
func parseTrojan(uri string) (*types.Node, error) {
	tag := ""
	if idx := strings.Index(uri, "#"); idx != -1 {
		tag = uri[idx+1:]
		uri = uri[:idx]
	}

	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Trojan URI: %w", err)
	}

	password := u.User.String()
	if password == "" {
		return nil, fmt.Errorf("Trojan URI missing password")
	}

	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, fmt.Errorf("Trojan URI invalid host:port: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("Trojan URI invalid port: %w", err)
	}

	q := u.Query()
	security := q.Get("security")
	tlsEnabled := security == "tls"
	tlsSNI := q.Get("sni")
	transportType := q.Get("type")
	if transportType == "" {
		transportType = "tcp"
	}

	country := extractCountry(tag)

	node := &types.Node{
		Server:        host,
		Port:          port,
		Protocol:      "trojan",
		Country:       country,
		Password:      password,
		TLSEnabled:    tlsEnabled,
		TLSSNI:        tlsSNI,
		TransportType: transportType,
		Tag:           fmt.Sprintf("trojan-%s-%d", host, port),
	}

	return node, nil
}

// vmessConfig represents the decoded VMess JSON config.
type vmessConfig struct {
	V    string `json:"v"`
	PS   string `json:"ps"`
	Add  string `json:"add"`
	Port string `json:"port"`
	ID   string `json:"id"`
	Aid  string `json:"aid"`
	Net  string `json:"net"`
	Type string `json:"type"`
	Host string `json:"host"`
	Path string `json:"path"`
	TLS  string `json:"tls"`
	SNI  string `json:"sni"`
}

// parseVMess parses a VMess URI.
// Format: vmess://base64(json)
func parseVMess(uri string) (*types.Node, error) {
	// Remove prefix (vmess:// = 8 chars)
	encoded := uri[8:]

	// Base64 decode
	decoded, err := base64Decode(encoded)
	if err != nil {
		return nil, fmt.Errorf("VMess URI base64 decode failed: %w", err)
	}

	var cfg vmessConfig
	if err := json.Unmarshal([]byte(decoded), &cfg); err != nil {
		return nil, fmt.Errorf("VMess URI JSON parse failed: %w", err)
	}

	if cfg.Add == "" || cfg.ID == "" {
		return nil, fmt.Errorf("VMess URI missing server or UUID")
	}

	port, err := strconv.Atoi(cfg.Port)
	if err != nil {
		return nil, fmt.Errorf("VMess URI invalid port: %w", err)
	}

	tlsEnabled := cfg.TLS == "tls" || cfg.TLS == "reality"
	transportType := cfg.Net
	if transportType == "" {
		transportType = "tcp"
	}

	// Extract country from ps (tag)
	country := extractCountry(cfg.PS)
	tag := cfg.PS
	if tag == "" {
		tag = fmt.Sprintf("vmess-%s-%d", cfg.Add, port)
	}

	node := &types.Node{
		Server:        cfg.Add,
		Port:          port,
		Protocol:      "vmess",
		Country:       country,
		UUID:          cfg.ID,
		AlterID:       0, // Default alter_id
		TLSEnabled:    tlsEnabled,
		TLSSNI:        cfg.SNI,
		TransportType: transportType,
		TransportHost: cfg.Host,
		TransportPath: cfg.Path,
		Tag:           tag,
	}

	return node, nil
}

// parseHy2 parses a Hysteria2 URI.
// Format: hy2://password@server:port?insecure=1#tag
// or: hysteria2://password@server:port#tag
func parseHy2(uri string) (*types.Node, error) {
	// Normalize prefix
	if strings.HasPrefix(uri, "hysteria2://") {
		uri = "hy2://" + uri[12:]
	}

	tag := ""
	if idx := strings.Index(uri, "#"); idx != -1 {
		tag = uri[idx+1:]
		uri = uri[:idx]
	}

	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Hy2 URI: %w", err)
	}

	password := u.User.String()
	if password == "" {
		return nil, fmt.Errorf("Hy2 URI missing password")
	}

	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, fmt.Errorf("Hy2 URI invalid host:port: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("Hy2 URI invalid port: %w", err)
	}

	country := extractCountry(tag)

	node := &types.Node{
		Server:     host,
		Port:       port,
		Protocol:   "hy2",
		Country:    country,
		Password:   password,
		TLSEnabled: true, // Hy2 always uses TLS
		Tag:        fmt.Sprintf("hy2-%s-%d", host, port),
	}

	return node, nil
}

// extractCountry extracts a country code from a tag/fragment.
func extractCountry(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "unknown"
	}

	// Common country patterns: #VN, #US, #flag VN, etc.
	// Try to find 2-letter country code
	re := regexp.MustCompile(`(?i)\b([A-Z]{2})\b`)
	matches := re.FindStringSubmatch(strings.ToUpper(tag))
	if len(matches) > 1 {
		code := matches[1]
		if isValidCountryCode(code) {
			return code
		}
	}

	// Check for Vietnamese country names
	lowerTag := strings.ToLower(tag)
	vnPatterns := []string{"vietnam", "viet", "越南", "vn"}
	for _, p := range vnPatterns {
		if strings.Contains(lowerTag, p) {
			return "VN"
		}
	}

	return "unknown"
}

// isValidCountryCode checks if a code is a valid ISO 3166-1 alpha-2 code.
func isValidCountryCode(code string) bool {
	// Common codes only
	valid := map[string]bool{
		"VN": true, "US": true, "JP": true, "SG": true, "KR": true,
		"HK": true, "TW": true, "TH": true, "ID": true, "MY": true,
		"PH": true, "IN": true, "PK": true, "BD": true, "LK": true,
		"NP": true, "MM": true, "KH": true, "LA": true, "BN": true,
		"CN": true, "RU": true, "DE": true, "FR": true, "GB": true,
		"IT": true, "ES": true, "NL": true, "SE": true, "NO": true,
		"FI": true, "DK": true, "PL": true, "CZ": true, "AT": true,
		"CH": true, "BE": true, "IE": true, "PT": true, "GR": true,
		"TR": true, "IL": true, "SA": true, "AE": true, "QA": true,
		"BH": true, "KW": true, "OM": true, "JO": true, "LB": true,
		"EG": true, "ZA": true, "KE": true, "NG": true, "GH": true,
		"BR": true, "AR": true, "CL": true, "CO": true, "PE": true,
		"MX": true, "CA": true, "AU": true, "NZ": true, "UA": true,
		"RO": true, "HU": true, "BG": true, "HR": true, "RS": true,
		"BA": true, "SK": true, "SI": true, "LT": true, "LV": true,
		"EE": true, "IS": true, "LU": true, "MT": true, "CY": true,
		"AM": true, "GE": true, "KZ": true, "UZ": true, "MN": true,
	}
	return valid[code]
}

// base64Decode attempts standard and URL-safe base64 decoding.
func base64Decode(s string) (string, error) {
	// Try standard base64
	b, err := base64.StdEncoding.DecodeString(s)
	if err == nil {
		return string(b), nil
	}

	// Try URL-safe base64
	b, err = base64.URLEncoding.DecodeString(s)
	if err == nil {
		return string(b), nil
	}

	// Try with padding
	if len(s)%4 != 0 {
		s += strings.Repeat("=", 4-len(s)%4)
		b, err = base64.StdEncoding.DecodeString(s)
		if err == nil {
			return string(b), nil
		}
	}

	return "", fmt.Errorf("base64 decode failed")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
