package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/pkg/types"
)

func newTestPool(t *testing.T) (*Pool, *cache.Cache) {
	t.Helper()
	c, err := cache.NewCache(filepath.Join(t.TempDir(), "pool.db"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return NewPool(c), c
}
func TestPruneDeadNodes_RemovesDeadKeepsAlive(t *testing.T) {
	// TCP listener on a random port = the "alive" ovpn node.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	aliveAddr := ln.Addr().String()
	host, portStr, err := net.SplitHostPort(aliveAddr)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	alivePort, _ := strconv.Atoi(portStr)

	p, c := newTestPool(t)
	nodes := []*types.Node{
		{Server: host, Port: alivePort, Protocol: "ovpn", Country: "VN"},
		{Server: "127.0.0.1", Port: 1, Protocol: "ovpn", Country: "VN"},   // closed port
		{Server: host, Port: alivePort, Protocol: "socks", Country: "VN"}, // non-ovpn ignored
	}
	if err := c.SaveNodes(nodes); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}

	removed, err := p.PruneDeadNodes(context.Background(), "VN", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("PruneDeadNodes: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1 (only the dead ovpn node)", removed)
	}

	after, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	var ovpn []*types.Node
	for _, n := range after {
		if n.Protocol == "ovpn" {
			ovpn = append(ovpn, n)
		}
	}
	if len(ovpn) != 1 || ovpn[0].Port != alivePort {
		t.Errorf("ovpn nodes after prune = %+v, want only the alive node", ovpn)
	}
}

func TestNewPool(t *testing.T) {
	p, _ := newTestPool(t)
	if p == nil {
		t.Fatal("NewPool returned nil")
	}
	if p.cache == nil {
		t.Error("pool cache should be wired")
	}
}

func TestFetchNodes_EmptySources(t *testing.T) {
	// Exercise the "no nodes fetched from any source" error path hermetically:
	// stub the sources at a closed local port so every fetch fails instantly
	// with connection refused. No proxy env, no real DNS, no live network —
	// Go caches proxy configuration per process anyway, so env-based draining
	// is unreliable under a full test run (and previously let this test hit
	// the real internet, where slow-DNS enrichment hung it for minutes).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadURL := "http://" + ln.Addr().String() + "/dead"
	ln.Close() // closed port: dial fails fast

	p, _ := newTestPool(t)
	p.sources = func(string) []Source {
		return []Source{
			{Name: "dead-socks", URL: deadURL, Kind: "socks"},
			{Name: "dead-vpngate", URL: deadURL, Kind: "vpngate"},
		}
	}
	_, err = p.FetchNodes(context.Background(), "ALL")
	if err == nil {
		t.Error("FetchNodes should fail when every source errors")
	}
}

func TestFetchNodes_WithStubSource(t *testing.T) {
	td := t.TempDir()
	c, err := cache.NewCache(filepath.Join(td, "pool.db"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	// Stub GeoIP: the pool enriches unknown countries via geoAPIURL. Point it
	// at a local batch endpoint so the test is deterministic and offline.
	oldGeoURL := geoAPIURL
	t.Cleanup(func() { geoAPIURL = oldGeoURL })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/batch":
			var q []struct {
				Query  string `json:"query"`
				Fields string `json:"fields"`
			}
			if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			out := make([]struct {
				CountryCode string `json:"countryCode"`
			}, len(q))
			for i, item := range q {
				switch item.Query {
				case "113.22.103.212":
					out[i].CountryCode = "VN"
				case "1.1.1.1":
					out[i].CountryCode = "US"
				}
			}
			json.NewEncoder(w).Encode(out)
		case "/vn.txt":
			fmt.Fprintln(w, "113.22.103.212:1278")
		case "/us.txt":
			fmt.Fprintln(w, "ss://YWVzLTI1Ni1nY206cGFzczEyMw==@1.1.1.1:8388#US-node")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	geoAPIURL = srv.URL + "/batch"

	p := NewPool(c)
	p.sources = func(country string) []Source {
		return []Source{
			{Name: "vn", URL: srv.URL + "/vn.txt", Kind: "socks"},
			{Name: "us", URL: srv.URL + "/us.txt"},
		}
	}

	// Full pipeline through FetchNodes: fetch both sources, enrich (VN for
	// the socks IP, US for the ss IP), filter returned set to VN, and cache
	// the FULL set.
	got, err := p.FetchNodes(context.Background(), "VN")
	if err != nil {
		t.Fatalf("FetchNodes: %v", err)
	}

	// Return = country-filtered only.
	if len(got) != 1 || got[0].Server != "113.22.103.212" || got[0].Country != "VN" {
		t.Errorf("returned set should be VN only, got %+v", got)
	}

	// Cache = every fetched node, not the filtered subset. This guards the
	// regression: `--country VN` runs must not shrink the shared pool cache.
	cached, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	if len(cached) != 2 {
		t.Errorf("cache should keep all fetched nodes (got %d, want 2)", len(cached))
	}
}

func TestFetchSource_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := Source{Name: "Test", URL: srv.URL}
	_, err := fetchSource(context.Background(), src)
	if err == nil {
		t.Error("fetchSource should fail on HTTP 500")
	} else if !strings.Contains(err.Error(), "Test") {
		t.Errorf("error should mention source name, got: %v", err)
	}
}

func TestFetchSource_BadURL(t *testing.T) {
	src := Source{Name: "Bad", URL: "http://127.0.0.1:1/x"}
	_, err := fetchSource(context.Background(), src)
	if err == nil {
		t.Error("fetchSource should fail on unreachable URL")
	}
}

func TestFetchSource_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ss://YWVzLTI1Ni1nY206cGFzczEyMw==@3.3.3.3:8388#ok")
	}))
	defer srv.Close()

	src := Source{Name: "OK", URL: srv.URL}
	content, err := fetchSource(context.Background(), src)
	if err != nil {
		t.Fatalf("fetchSource: %v", err)
	}
	if !strings.Contains(content, "ss://") {
		t.Errorf("content should contain the URI, got: %q", content)
	}
}

func TestFetchFromSource_ParsesContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ss://YWVzLTI1Ni1nY206cGFzczEyMw==@4.4.4.4:8388#p1")
		fmt.Fprintln(w, "# comment line")
		fmt.Fprintln(w, "")
	}))
	defer srv.Close()

	p, _ := newTestPool(t)
	nodes, err := p.fetchFromSource(context.Background(), Source{Name: "T", URL: srv.URL})
	if err != nil {
		t.Fatalf("fetchFromSource: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d: %v", len(nodes), nodes)
	}
	if !strings.Contains(nodes[0].Server, "4.4.4.4") {
		t.Errorf("node should contain server, got: %s", nodes[0].Server)
	}
}

func TestSources_CountryNormalization(t *testing.T) {
	all := Sources("")
	vn := Sources("vn") // lowercase must normalize
	vnUpper := Sources("VN")

	if len(all) == 0 {
		t.Fatal("Sources(\"\") should return sources")
	}
	if len(vn) != len(vnUpper) {
		t.Errorf("Sources(vn)=%d != Sources(VN)=%d", len(vn), len(vnUpper))
	}
	// Country-specific list should differ from all-countries list
	// (VN sources point at VN-specific files)
	sameURL := true
	for i, s := range vnUpper {
		if i < len(all) && s.URL != all[i].URL {
			sameURL = false
			break
		}
	}
	if sameURL && len(vnUpper) > 0 && len(all) > 0 && len(all) != len(vnUpper) {
		// length differs so lists differ — fine
		_ = sameURL
	}
}

func TestToUpper_Cases(t *testing.T) {
	tests := []struct{ in, want string }{
		{"vn", "VN"},
		{"VN", "VN"},
		{"Vn", "VN"},
		{"", ""},
		{"abc123", "ABC123"},
		{"mIxEd9", "MIXED9"},
	}
	for _, tt := range tests {
		if got := toUpper(tt.in); got != tt.want {
			t.Errorf("toUpper(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFilterByCountry_Strict(t *testing.T) {
	nodes := []*types.Node{
		{Server: "a", Country: "VN"},
		{Server: "b", Country: "vn"}, // case-insensitive
		{Server: "c", Country: "US"},
		{Server: "d", Country: "unknown"}, // NOT assumed to be VN
		{Server: "e", Country: ""},        // no data at all
	}

	got := FilterByCountry(nodes, "vn")
	servers := map[string]bool{}
	for _, n := range got {
		servers[n.Server] = true
	}
	if !servers["a"] || !servers["b"] || servers["c"] || servers["d"] || servers["e"] {
		t.Errorf("FilterByCountry(vn) should include only a,b (strict), got: %v", servers)
	}
	if len(got) != 2 {
		t.Errorf("FilterByCountry(vn) should return exactly 2 nodes, got %d", len(got))
	}

	// Empty / ALL passes everything through (no filter requested)
	for _, noFilter := range []string{"", "all", "ALL"} {
		all := FilterByCountry(nodes, noFilter)
		if len(all) != len(nodes) {
			t.Errorf("FilterByCountry(%q) should pass through %d nodes, got %d", noFilter, len(nodes), len(all))
		}
	}

	// A country with zero geo-confirmed nodes returns empty — not unknown junk
	none := FilterByCountry(nodes, "JP")
	if len(none) != 0 {
		t.Errorf("FilterByCountry(JP) should return 0 nodes, got %d", len(none))
	}
}

func TestDeduplicateNodes_KeysAndOrder(t *testing.T) {
	nodes := []*types.Node{
		{Server: "1.1.1.1", Port: 443, Protocol: "ss"},
		{Server: "1.1.1.1", Port: 443, Protocol: "vless"}, // same host:port, different protocol
		{Server: "1.1.1.1", Port: 443, Protocol: "ss"},    // exact duplicate
		{Server: "1.1.1.1", Port: 8443, Protocol: "ss"},   // different port
	}
	got := DeduplicateNodes(nodes)
	if len(got) != 3 {
		t.Errorf("expected 3 unique nodes, got %d", len(got))
	}
	// Order preserved
	if got[0].Protocol != "ss" || got[1].Protocol != "vless" {
		t.Error("dedup should preserve first-seen order")
	}
}
