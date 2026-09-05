package checker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/proxy"
)

// IPInfo represents the response from ipinfo.io.
type IPInfo struct {
	IP      string `json:"ip"`
	City    string `json:"city"`
	Region  string `json:"region"`
	Country string `json:"country"`
	Org     string `json:"org"`
}

// VerifyExitIP checks the exit IP country via ipinfo.io.
func VerifyExitIP(ctx context.Context, socks5Addr string, expectedCountry string) (*IPInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://ipinfo.io/json", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

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

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ipinfo request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ipinfo returned HTTP %d", resp.StatusCode)
	}

	var info IPInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("failed to decode ipinfo response: %w", err)
	}

	if expectedCountry != "" && expectedCountry != "all" && expectedCountry != "ALL" {
		if info.Country != expectedCountry {
			return &info, fmt.Errorf("country mismatch: expected %s, got %s", expectedCountry, info.Country)
		}
	}

	return &info, nil
}
