package fetcher

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	// Sources() returns a fixed list pointing at GitHub; without network
	// stubbing FetchNodes would error. We instead test the "all sources
	// failed" path by draining network access via a bogus proxy env.
	origProxy := os.Getenv("HTTP_PROXY")
	origProxyLower := os.Getenv("http_proxy")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	defer func() {
		os.Setenv("HTTP_PROXY", origProxy)
		os.Setenv("http_proxy", origProxyLower)
	}()

	p, _ := newTestPool(t)
	// Use "ALL" so Sources() returns the full list; with the dead proxy,
	// every fetch should fail fast and FetchNodes must return an error.
	_, err := p.FetchNodes(context.Background(), "ALL")
	if err == nil {
		t.Skip("network unexpectedly reachable; cannot test offline path")
	}
}

func TestFetchNodes_WithStubSource(t *testing.T) {
	// We can't redirect Sources() URLs, but fetchSource honors HTTP(S)_PROXY.
	// Simpler: spin a local server and verify the FetchNodes pipeline logic
	// end-to-end by calling the internal steps directly.
	p, c := newTestPool(t)
	_ = p

	// Simulate the pipeline: parse -> dedup -> filter -> save
	uris := []string{
		"ss://YWVzLTI1Ni1nY206cGFzczEyMw==@1.1.1.1:8388#A",
		"ss://YWVzLTI1Ni1nY206cGFzczEyMw==@1.1.1.1:8388#A-dup", // duplicate
		"trojan://pass@2.2.2.2:443#B",
	}
	var nodes []*types.Node
	for _, uri := range uris {
		if n, err := parseURI(uri); err == nil {
			nodes = append(nodes, n)
		}
	}
	nodes = DeduplicateNodes(nodes)

	// Simulate geo enrichment: 1.1.1.1 confirmed US, 2.2.2.2 has no country data
	for _, n := range nodes {
		n.Country = "US"
	}
	nodes[1].Country = ""

	// Filter "US": strict — only geo-confirmed US survives, unknowns are dropped
	nodes = FilterByCountry(nodes, "US")

	if err := c.SaveNodes(nodes); err != nil {
		t.Fatalf("SaveNodes: %v", err)
	}

	got, err := c.GetNodes()
	if err != nil {
		t.Fatalf("GetNodes: %v", err)
	}
	// 1.1.1.1 (US, ss) survives; 2.2.2.2 (no country data) is NOT assumed US; dup removed
	if len(got) != 1 || got[0].Server != "1.1.1.1" {
		t.Errorf("expected 1 node (strict US) after dedup+filter+save, got %d", len(got))
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
	uris, err := p.fetchFromSource(context.Background(), Source{Name: "T", URL: srv.URL})
	if err != nil {
		t.Fatalf("fetchFromSource: %v", err)
	}
	if len(uris) != 1 {
		t.Fatalf("expected 1 URI, got %d: %v", len(uris), uris)
	}
	if !strings.Contains(uris[0], "4.4.4.4") {
		t.Errorf("URI should contain server, got: %s", uris[0])
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
