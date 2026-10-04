package fetcher

import (
	"encoding/base64"
	"strings"

	"github.com/rotaze/rotaze/pkg/types"
)

// vpngateHeader is the CSV header line returned by the VPNGate API before
// the data rows. Column order (0-based):
//
//	0 HostName, 1 IP, 2 Score, 3 Ping, 4 Speed, 5 CountryLong,
//	6 CountryShort, 7 NumVpnSessions, 8 Uptime, 9 TotalUsers,
//	10 TotalTraffic, 11 LogType, 12 Operator, 13 Message,
//	14 OpenVPN_ConfigData_Base64
const vpngateDataColumns = 15 // minimum field count for a data row

// parseVPNGateContent parses the VPNGate CSV body into OpenVPN nodes.
// Each data row carries a base64-encoded OpenVPN config in its last field;
// nodes take the CountryShort column as authoritative exit country (the
// config itself has no country hint).
func parseVPNGateContent(content string) []*types.Node {
	var nodes []*types.Node
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < vpngateDataColumns {
			continue
		}
		// Config data is base64 (no commas), so a naive split is safe on
		// the last field.
		cfgData, err := base64.StdEncoding.DecodeString(fields[14])
		if err != nil {
			continue
		}
		node, err := ParseOVPN(cfgData)
		if err != nil {
			continue
		}
		country := strings.ToUpper(strings.TrimSpace(fields[6]))
		if country != "" && country != "??" {
			node.Country = country
		}
		if fields[1] != "" {
			node.Server = fields[1] // authoritative IP beats DNS host
		}
		nodes = append(nodes, node)
	}
	return nodes
}
