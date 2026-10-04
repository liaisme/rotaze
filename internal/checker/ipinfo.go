package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/proxy"
)

// IPInfo represents the response from an exit-IP lookup provider.
type IPInfo struct {
	IP      string `json:"ip"`
	City    string `json:"city"`
	Region  string `json:"region"`
	Country string `json:"country"`
	Org     string `json:"org"`
}

// ipAPIResponse mirrors ip-api.com's common fields (subnet is returned as a
// plain string, so JSON numbers are tolerated via json.Number).
type ipAPIResponse struct {
	Status      string      `json:"status"`
	Query       string      `json:"query"`
	CountryCode string      `json:"countryCode"`
	Country     string      `json:"country"`
	City        string      `json:"city"`
	RegionName  string      `json:"regionName"`
	Org         string      `json:"org"`
	AS          json.Number `json:"as"`
}

// ipwhoResponse mirrors ipwho.is.
type ipwhoResponse struct {
	Success     bool   `json:"success"`
	IP          string `json:"ip"`
	Country     string `json:"country"`
	CountryCode string `json:"country_code"`
	City        string `json:"city"`
	Region      string `json:"region"`
	Org         string `json:"connection.org"`
}

// ipProvider is a single exit-IP lookup endpoint with its response decoder.
type ipProvider struct {
	name string
	url  string
	// decode parses the provider response into the info fields.
	decode func([]byte) (IPInfo, error)
}

// ipProviders lists exit-IP lookup endpoints in fallback order. Each must
// return a small JSON object; failures (HTTP error, rate limit, decode error)
// fall through to the next one.
var ipProviders = []ipProvider{
	{
		name: "ipinfo",
		url:  "https://ipinfo.io/json",
		decode: func(b []byte) (IPInfo, error) {
			var info IPInfo
			if err := json.Unmarshal(b, &info); err != nil {
				return IPInfo{}, err
			}
			return info, nil
		},
	},
	{
		name: "ip-api",
		url:  "http://ip-api.com/json/?fields=status,query,country,countryCode,city,regionName,org,as",
		decode: func(b []byte) (IPInfo, error) {
			var r ipAPIResponse
			if err := json.Unmarshal(b, &r); err != nil {
				return IPInfo{}, err
			}
			if r.Status != "success" {
				return IPInfo{}, fmt.Errorf("ip-api returned status %q", r.Status)
			}
			return IPInfo{IP: r.Query, City: r.City, Region: r.RegionName, Country: r.CountryCode, Org: r.Org}, nil
		},
	},
	{
		name: "ipwho",
		url:  "https://ipwho.is/",
		decode: func(b []byte) (IPInfo, error) {
			var r ipwhoResponse
			if err := json.Unmarshal(b, &r); err != nil {
				return IPInfo{}, err
			}
			if !r.Success {
				return IPInfo{}, fmt.Errorf("ipwho returned success=false")
			}
			return IPInfo{IP: r.IP, City: r.City, Region: r.Region, Country: r.CountryCode, Org: r.Org}, nil
		},
	},
}

// VerifyExitIP checks the exit IP country via a chain of public IP-lookup
// providers (ipinfo.io first, then ip-api.com, then ipwho.is). The request is
// routed through the SOCKS5 proxy when configured, so the verified IP is the
// tunnel's exit rather than the local machine's.
func VerifyExitIP(ctx context.Context, socks5Addr string, expectedCountry string) (*IPInfo, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	// Route through SOCKS5 proxy when configured, so we verify the exit IP
	// of the tunnel rather than the local machine's IP.
	if socks5Addr != "" {
		dialer, err := proxy.SOCKS5("tcp", socks5Addr, nil, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}
		client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.(proxy.ContextDialer).DialContext(ctx, network, addr)
			},
		}
	}

	var lastErr error
	for _, p := range ipProviders {
		provCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		req, err := http.NewRequestWithContext(provCtx, "GET", p.url, nil)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("create %s request: %w", p.name, err)
			continue
		}
		req.Header.Set("User-Agent", "Rotaze/1.0")

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("%s request failed: %w", p.name, err)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		if readErr != nil {
			lastErr = fmt.Errorf("%s read failed: %w", p.name, readErr)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("%s returned HTTP %d", p.name, resp.StatusCode)
			continue
		}

		info, err := p.decode(body)
		if err != nil {
			lastErr = fmt.Errorf("%s decode failed: %w", p.name, err)
			continue
		}
		if info.Country == "" || info.IP == "" {
			lastErr = fmt.Errorf("%s returned empty fields", p.name)
			continue
		}

		if expectedCountry != "" && expectedCountry != "all" && expectedCountry != "ALL" {
			if info.Country != expectedCountry {
				return &info, fmt.Errorf("country mismatch: expected %s, got %s", expectedCountry, info.Country)
			}
		}
		return &info, nil
	}

	return nil, fmt.Errorf("all exit-IP providers failed: %w", lastErr)
}
