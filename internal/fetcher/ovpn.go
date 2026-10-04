// Package fetcher parses proxy subscription sources into nodes.
package fetcher

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rotaze/rotaze/pkg/types"
)

// ParseOVPN parses a VPNGate-style OpenVPN config file into a Node.
// It extracts the remote endpoint, transport, data cipher, auth, and the
// embedded TLS certificate blocks (ca/cert/key). Only TLS-mode configs with
// a remote endpoint are supported; static-key and missing-cert configs
// return an error.
func ParseOVPN(content []byte) (*types.Node, error) {
	txt := string(content)

	remote := findOpt(txt, "remote")
	if remote == "" {
		return nil, fmt.Errorf("no remote directive found")
	}
	parts := strings.Fields(remote)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid remote directive: %q", remote)
	}
	server := parts[0]
	var port int
	if _, err := fmt.Sscanf(parts[1], "%d", &port); err != nil || port <= 0 {
		return nil, fmt.Errorf("invalid remote port: %q", parts[1])
	}

	proto := findOpt(txt, "proto")
	if proto == "" {
		proto = "udp"
	}

	// Only TLS mode is supported. Static-key configs have no <ca>.
	ca := findBlock(txt, "ca")
	cert := findBlock(txt, "cert")
	key := findBlock(txt, "key")
	if ca == "" || key == "" {
		return nil, fmt.Errorf("unsupported OpenVPN config: missing ca/key (static-key mode not supported)")
	}

	// Country from the hostname if it carries a country tag, else unknown.
	// VPNGate hostnames are of the form vpnNNN.opengw.net with no country.
	country := "unknown"
	if m := regexp.MustCompile(`\.(jp|us|vn|sg|hk|kr|de|gb|fr|ca|nl|id|th|my|ph|au|tw|in|ae|sa|tr|ru|ua|pl|cz|ro|bg|gr|es|it|ch|at|be|dk|fi|no|se)\b`).FindStringSubmatch(server); m != nil {
		country = strings.ToUpper(m[1])
	}

	node := &types.Node{
		Server:        server,
		Port:          port,
		Protocol:      "ovpn",
		Country:       country,
		Password:      "vpn", // VPNGate default credentials
		EncryptMethod: findOpt(txt, "cipher"),
		AuthMethod:    findOpt(txt, "auth"),
		TransportType: proto,
		CACert:        ca,
		ClientCert:    cert,
		ClientKey:     key,
		Tag:           "ovpn-" + server,
	}
	if node.EncryptMethod == "" {
		node.EncryptMethod = "AES-128-CBC"
	}
	if node.AuthMethod == "" {
		node.AuthMethod = "SHA1"
	}
	return node, nil
}

// findOpt returns the value of the first `name value` line.
func findOpt(txt, name string) string {
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s+(.+?)\s*$`)
	m := re.FindStringSubmatch(txt)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// findBlock returns the content between <name> and </name>.
func findBlock(txt, name string) string {
	re := regexp.MustCompile(`(?s)<` + regexp.QuoteMeta(name) + `>(.*?)</` + regexp.QuoteMeta(name) + `>`)
	m := re.FindStringSubmatch(txt)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}
