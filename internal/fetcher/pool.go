package fetcher

import (
	"context"
	"fmt"
	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/pkg/types"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Pool manages the node fetching and caching lifecycle.
type Pool struct {
	cache *cache.Cache

	// sources returns the subscription sources for a country. It is a field
	// (not a direct call) so tests can point FetchNodes at a local server.
	sources func(country string) []Source
}

// NewPool creates a new fetcher pool.
func NewPool(c *cache.Cache) *Pool {
	return &Pool{cache: c, sources: Sources}
}

// FetchNodes fetches nodes from all sources concurrently, deduplicates, and caches.
func (p *Pool) FetchNodes(ctx context.Context, country string) ([]*types.Node, error) {
	sources := p.sources(country)

	// Fetch from all sources concurrently
	type result struct {
		source Source
		nodes  []*types.Node
		err    error
	}

	results := make(chan result, len(sources))
	var wg sync.WaitGroup

	for _, src := range sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			nodes, err := p.fetchFromSource(ctx, s)
			results <- result{source: s, nodes: nodes, err: err}
		}(src)
	}

	// Close results channel when all goroutines finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect all nodes
	var nodes []*types.Node
	for r := range results {
		if r.err != nil {
			fmt.Printf("[WARN] Source %s failed: %v\n", r.source.Name, r.err)
			continue
		}
		nodes = append(nodes, r.nodes...)
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes fetched from any source")
	}

	// Deduplicate
	nodes = DeduplicateNodes(nodes)

	// Cheap prefilter: drop socks proxies whose first octet cannot be a VN
	// allocation before the slower GeoIP enrichment (ip-api.com free tier is
	// limited to 15 requests/min, which caps how many batch lookups a run may
	// make). The proxy's own address is its exit address, so the octet check
	// is a sound prefilter — only octets present in VN RIR allocations remain.
	// For other countries (or "all") this is a no-op.
	//
	// NOTE: prefilterCountry filters in-place (nodes[:0]); capture the cached
	// set only after it, or the old slice header would alias stale entries.
	nodes = prefilterCountry(nodes, country)

	// Enrich country data for nodes with unknown country
	EnrichCountries(ctx, nodes)

	// Cache the enriched, deduplicated set — BEFORE the country filter. Saving
	// the country-filtered subset would let `--country VN` runs refresh only
	// VN nodes while the rest of the pool ages out. The country filter below
	// only affects the returned set; the cache keeps every node.
	allNodes := nodes

	// Filter by country (if not "all")
	if country != "" && country != "all" && country != "ALL" {
		nodes = FilterByCountry(nodes, country)
	}

	// Cache to database (upsert; stale nodes are pruned separately)
	if err := p.cache.SaveNodes(allNodes); err != nil {
		return nil, fmt.Errorf("failed to cache nodes: %w", err)
	}

	return nodes, nil
}

// PruneDeadNodes probes TCP liveness of cached nodes and removes the dead
// ones. VPNGate and free proxy lists recycle hosts constantly; a stale pool
// wastes rotation attempts (and the default --attempts budget) on servers
// that no longer answer. TCP is probed because every protocol in the pool
// except hy2 (QUIC/UDP-only) carries its traffic over TCP; a UDP "dial"
// always succeeds (no handshake) so it cannot prove liveness anyway.
// Returns the number of nodes removed.
func (p *Pool) PruneDeadNodes(ctx context.Context, country string, timeout time.Duration) (int, error) {
	nodes, err := p.cache.GetNodes()
	if err != nil {
		return 0, err
	}
	var targets []*types.Node
	for _, n := range nodes {
		if n.Protocol == "hy2" {
			// QUIC transport — no TCP port to probe; always keep.
			continue
		}
		if country != "" && !strings.EqualFold(n.Country, country) {
			continue
		}
		targets = append(targets, n)
	}
	if len(targets) == 0 {
		return 0, nil
	}

	var dead []int
	var alive int
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64) // bounded concurrency for TCP probes
	for _, n := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(n *types.Node) {
			defer wg.Done()
			defer func() { <-sem }()
			d := net.Dialer{Timeout: timeout}
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(n.Server, strconv.Itoa(n.Port)))
			if err != nil {
				mu.Lock()
				dead = append(dead, n.ID)
				mu.Unlock()
				return
			}
			conn.Close()
			mu.Lock()
			alive++
			mu.Unlock()
		}(n)
	}
	wg.Wait()

	if len(dead) > 0 {
		if err := p.cache.DeleteNodes(dead); err != nil {
			return 0, fmt.Errorf("failed to delete dead nodes: %w", err)
		}
		if err := p.cache.DeleteNodeStats(dead); err != nil {
			return 0, fmt.Errorf("failed to delete dead node stats: %w", err)
		}
	}
	return len(dead), nil
}

// fetchFromSource fetches nodes from a single source.
func (p *Pool) fetchFromSource(ctx context.Context, source Source) ([]*types.Node, error) {
	content, err := fetchSource(ctx, source)
	if err != nil {
		return nil, err
	}

	if source.Kind == "vpngate" {
		nodes := parseVPNGateContent(content)
		if len(nodes) == 0 {
			return nil, fmt.Errorf("no OpenVPN nodes in VPNGate CSV")
		}
		return nodes, nil
	}

	uris := parseSourceContent(source, content)
	var nodes []*types.Node
	for _, uri := range uris {
		node, err := parseURI(uri)
		if err != nil {
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// DeduplicateNodes removes duplicate nodes based on (server, port, protocol).
func DeduplicateNodes(nodes []*types.Node) []*types.Node {
	seen := make(map[string]bool)
	var result []*types.Node

	for _, node := range nodes {
		key := fmt.Sprintf("%s:%d:%s", node.Server, node.Port, node.Protocol)
		if !seen[key] {
			seen[key] = true
			result = append(result, node)
		}
	}

	return result
}

// FilterByCountry filters nodes by country code.
// Strict: only nodes whose geo-confirmed country matches are kept. Nodes with
// unknown or missing country data are never assumed to belong to the requested
// country, so a "--country VN" pool cannot silently contain nodes whose real
// exit is, say, TR or JP. "" or "ALL" passes everything through.
func FilterByCountry(nodes []*types.Node, country string) []*types.Node {
	country = toUpper(country)
	if country == "" || country == "ALL" {
		return nodes
	}

	var result []*types.Node
	for _, node := range nodes {
		if toUpper(node.Country) == country {
			result = append(result, node)
		}
	}

	return result
}

// prefilterCountry drops socks/proxy nodes whose IP cannot be in the requested
// country before the expensive GeoIP enrichment. Tunnel (vless/ss/trojan/...)
// server addresses are not exit addresses, so they are never filtered here —
// only plain SOCKS5 proxies, where server == exit.
func prefilterCountry(nodes []*types.Node, country string) []*types.Node {
	c := toUpper(country)
	if c == "" || c == "ALL" {
		return nodes
	}
	var octets map[string]bool
	switch c {
	case "VN":
		octets = vnPrefixes
	default:
		// No octet map for this country yet — keep all nodes, GeoIP decides.
		return nodes
	}
	out := nodes[:0]
	for _, n := range nodes {
		if n.Protocol == "socks" {
			ip := net.ParseIP(n.Server)
			if ip4 := ip.To4(); ip4 != nil && !octets[strconv.Itoa(int(ip4[0]))] {
				continue
			}
		}
		out = append(out, n)
	}
	return out
}

// vnPrefixes holds IPv4 first octets present in Vietnam's RIR allocation
// (ipverse/rir-ip country/vn aggregated.json). First octets outside this set
// cannot be a VN exit, so the host is dropped before GeoIP.
var vnPrefixes = map[string]bool{
	"1": true, "14": true, "23": true, "27": true, "31": true, "36": true,
	"42": true, "43": true, "45": true, "49": true, "58": true, "59": true,
	"61": true, "64": true, "79": true, "85": true, "101": true, "103": true,
	"109": true, "110": true, "111": true, "112": true, "113": true, "115": true,
	"116": true, "117": true, "118": true, "119": true, "120": true, "121": true,
	"122": true, "123": true, "124": true, "125": true, "137": true, "138": true,
	"144": true, "146": true, "148": true, "150": true, "151": true, "157": true,
	"160": true, "161": true, "162": true, "163": true, "165": true, "167": true,
	"171": true, "175": true, "180": true, "182": true, "183": true, "192": true,
	"194": true, "195": true, "198": true, "202": true, "203": true, "210": true,
	"213": true, "218": true, "220": true, "221": true, "222": true, "223": true,
}

// toUpper is a simple string uppercase helper.
func toUpper(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		result[i] = c
	}
	return string(result)
}
