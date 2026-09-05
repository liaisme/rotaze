// Package fetcher - GeoIP country lookup for nodes with unknown country.
package fetcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net"
	"time"

	"github.com/rotaze/rotaze/pkg/types"
)

// geoIPResult holds the ip-api.com response for a single IP.
type geoIPResult struct {
	CountryCode string `json:"countryCode"`
	Country     string `json:"country"`
}

// geoAPIURL is the ip-api.com batch endpoint. Overridable in tests.
var geoAPIURL = "http://ip-api.com/batch?fields=countryCode"

// EnrichCountries fills in "unknown" country fields using ip-api.com batch lookups.
// Processes IPs in batches of 100 (ip-api.com limit for non-commercial use).
func EnrichCountries(nodes []*types.Node) {
	// Collect nodes with unknown country
	var unknownNodes []*types.Node
	for _, n := range nodes {
		if n.Country == "" || n.Country == "unknown" {
			unknownNodes = append(unknownNodes, n)
		}
	}

	if len(unknownNodes) == 0 {
		return
	}

	// Map server names to resolved IPs for GeoIP lookup
	serverToIP := make(map[string]string)
	for _, n := range unknownNodes {
		if net.ParseIP(n.Server) != nil {
			serverToIP[n.Server] = n.Server
		} else {
			ips, err := net.LookupHost(n.Server)
			if err == nil && len(ips) > 0 {
				serverToIP[n.Server] = ips[0]
			}
		}
	}

	// Deduplicate resolved IPs
	ipSet := make(map[string]bool)
	for _, ip := range serverToIP {
		ipSet[ip] = true
	}

	// Process in batches of 100
	batchSize := 100
	ips := make([]string, 0, len(ipSet))
	for ip := range ipSet {
		ips = append(ips, ip)
	}

	ipToCountry := make(map[string]string)

	for i := 0; i < len(ips); i += batchSize {
		end := i + batchSize
		if end > len(ips) {
			end = len(ips)
		}
		batch := ips[i:end]

		countries, err := batchGeoLookup(batch)
		if err != nil {
			continue
		}
		for ip, country := range countries {
			ipToCountry[ip] = country
		}

		// Rate limit: ip-api.com allows ~45 requests/minute
		if i+batchSize < len(ips) {
			time.Sleep(1500 * time.Millisecond)
		}
	}

	// Apply results using the server-to-IP mapping
	enriched := 0
	for _, n := range unknownNodes {
		ip := serverToIP[n.Server]
		if country, ok := ipToCountry[ip]; ok && country != "" {
			n.Country = country
			enriched++
		}
	}

	if enriched > 0 {
		fmt.Printf("Enriched %d nodes with country data\n", enriched)
	}
}

// batchGeoLookup queries ip-api.com for a batch of IPs.
func batchGeoLookup(ips []string) (map[string]string, error) {
	type queryItem struct {
		Query  string `json:"query"`
		Fields string `json:"fields"`
	}
	var body []queryItem
	for _, ip := range ips {
		body = append(body, queryItem{Query: ip, Fields: "countryCode"})
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", geoAPIURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var results []geoIPResult
	if err := json.Unmarshal(respBody, &results); err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for i, ip := range ips {
		if i < len(results) && results[i].CountryCode != "" {
			result[ip] = results[i].CountryCode
		}
	}

	return result, nil
}
