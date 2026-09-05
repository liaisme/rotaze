package fetcher

import (
	"context"
	"fmt"
	"sync"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/pkg/types"
)

// Pool manages the node fetching and caching lifecycle.
type Pool struct {
	cache *cache.Cache
}

// NewPool creates a new fetcher pool.
func NewPool(c *cache.Cache) *Pool {
	return &Pool{cache: c}
}

// FetchNodes fetches nodes from all sources concurrently, deduplicates, and caches.
func (p *Pool) FetchNodes(ctx context.Context, country string) ([]*types.Node, error) {
	sources := Sources(country)

	// Fetch from all sources concurrently
	type result struct {
		source Source
		uris   []string
		err    error
	}

	results := make(chan result, len(sources))
	var wg sync.WaitGroup

	for _, src := range sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			uris, err := p.fetchFromSource(ctx, s)
			results <- result{source: s, uris: uris, err: err}
		}(src)
	}

	// Close results channel when all goroutines finish
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect all URIs
	var allURIs []string
	for r := range results {
		if r.err != nil {
			fmt.Printf("[WARN] Source %s failed: %v\n", r.source.Name, r.err)
			continue
		}
		allURIs = append(allURIs, r.uris...)
	}

	if len(allURIs) == 0 {
		return nil, fmt.Errorf("no nodes fetched from any source")
	}

	// Parse all URIs
	var nodes []*types.Node
	var parseErrors int
	for _, uri := range allURIs {
		node, err := parseURI(uri)
		if err != nil {
			parseErrors++
			continue
		}
		nodes = append(nodes, node)
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("failed to parse any nodes (%d parse errors)", parseErrors)
	}

	// Deduplicate
	nodes = DeduplicateNodes(nodes)

	// Enrich country data for nodes with unknown country
	EnrichCountries(nodes)

	// Filter by country (if not "all")
	if country != "" && country != "all" && country != "ALL" {
		nodes = FilterByCountry(nodes, country)
	}

	// Cache to database
	if err := p.cache.SaveNodes(nodes); err != nil {
		return nil, fmt.Errorf("failed to cache nodes: %w", err)
	}

	return nodes, nil
}

// fetchFromSource fetches URIs from a single source.
func (p *Pool) fetchFromSource(ctx context.Context, source Source) ([]string, error) {
	content, err := fetchSource(ctx, source)
	if err != nil {
		return nil, err
	}

	uris := parseSourceContent(source, content)
	return uris, nil
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
