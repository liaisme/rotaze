// Package fetcher - GeoIP country lookup for nodes with unknown country.
package fetcher

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
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
func EnrichCountries(ctx context.Context, nodes []*types.Node) {
	if len(nodes) == 0 {
		return
	}

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

	// Map server names to resolved IPs for GeoIP lookup. Literal IPs are filled
	// on this goroutine first; only the DNS lookups below run concurrently, and
	// they write serverToIP under mu.
	serverToIP := make(map[string]string, len(unknownNodes))
	mu := sync.Mutex{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 32)
	for _, n := range unknownNodes {
		if net.ParseIP(n.Server) != nil {
			serverToIP[n.Server] = n.Server
		}
	}
	for _, n := range unknownNodes {
		if net.ParseIP(n.Server) != nil {
			continue
		}
		n := n
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			ips, err := net.DefaultResolver.LookupHost(dctx, n.Server)
			cancel()
			if err == nil && len(ips) > 0 {
				mu.Lock()
				serverToIP[n.Server] = ips[0]
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Deduplicate resolved IPs
	ipSet := make(map[string]bool)
	for _, ip := range serverToIP {
		ipSet[ip] = true
	}

	// Build batch list
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

		countries, err := batchGeoLookup(ctx, batch)
		if err != nil {
			continue
		}
		for ip, country := range countries {
			ipToCountry[ip] = country
		}

		// Rate limit: ip-api.com allows ~45 requests/minute
		if i+batchSize < len(ips) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1500 * time.Millisecond):
			}
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
}

// batchGeoLookup queries ip-api.com for a batch of IPs.
func batchGeoLookup(ctx context.Context, ips []string) (map[string]string, error) {
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

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
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

	var results []struct {
		Query       string `json:"query"`
		CountryCode string `json:"countryCode"`
		Country     string `json:"country"`
	}
	if err := json.Unmarshal(respBody, &results); err != nil {
		return nil, err
	}

	res := make(map[string]string, len(ips))
	for _, r := range results {
		if r.Query != "" && r.CountryCode != "" {
			res[r.Query] = r.CountryCode
		}
	}
	if len(res) == 0 && len(results) == len(ips) {
		for i, ip := range ips {
			if results[i].CountryCode != "" {
				res[ip] = results[i].CountryCode
			}
		}
	}
	return res, nil
}
