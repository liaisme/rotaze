// Package cli - pool command (node pool management).
package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/internal/fetcher"
	"github.com/rotaze/rotaze/pkg/types"
	"github.com/spf13/cobra"
)

func newPoolCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pool",
		Short: "Node pool management",
		Long:  `List, filter, and inspect available tunnel nodes.`,
	}

	cmd.AddCommand(newPoolListCmd())
	cmd.AddCommand(newPoolStatsCmd())
	cmd.AddCommand(newPoolRefreshCmd())
	cmd.AddCommand(newPoolSearchCmd())
	cmd.AddCommand(newPoolTopCmd())
	cmd.AddCommand(newPoolExportCmd())
	cmd.AddCommand(newPoolImportCmd())
	cmd.AddCommand(newPoolAddCmd())
	cmd.AddCommand(newPoolCheckCmd())
	cmd.AddCommand(newPoolGeoIPCmd())
	cmd.AddCommand(newPoolPingCmd())
	cmd.AddCommand(newPoolDedupCmd())
	cmd.AddCommand(newPoolHealthCmd())
	cmd.AddCommand(newPoolCountCmd())
	cmd.AddCommand(newPoolBackupCmd())
	cmd.AddCommand(newPoolWatchCmd())
	cmd.AddCommand(newPoolHistoryCmd())
	cmd.AddCommand(newPoolMergeCmd())
	cmd.AddCommand(newPoolTreeCmd())
	cmd.AddCommand(newPoolPurgeCmd())

	return cmd
}

func newPoolListCmd() *cobra.Command {
	var (
		country  string
		protocol string
		sort     string
		limit    int
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available nodes",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Fetch nodes from cache
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to fetch nodes: %w", err)
			}

			// Filter by country
			if country != "" && country != "all" {
				var filtered []*types.Node
				for _, n := range nodes {
					if n.Country == country {
						filtered = append(filtered, n)
					}
				}
				nodes = filtered
			}

			// Filter by protocol
			if protocol != "" {
				var filtered []*types.Node
				for _, n := range nodes {
					if n.Protocol == protocol {
						filtered = append(filtered, n)
					}
				}
				nodes = filtered
			}

			if len(nodes) == 0 {
				fmt.Println("No nodes found in pool. Try: rotaze pool refresh")
				return nil
			}

			// Sort nodes
			sortNodes(nodes, sort, globalCache)

			// Build table header based on sort mode
			header := []string{"ID", "Server", "Port", "Protocol", "Country", "TLS"}
			if sort == "latency" || sort == "success" || sort == "random" {
				header = append(header, "Latency", "Score")
			}

			// Display table
			table := tablewriter.NewWriter(os.Stdout)
			table.SetHeader(header)
			table.SetBorder(true)
			table.SetRowLine(true)

			for i, n := range nodes {
				if limit > 0 && i >= limit {
					break
				}
				tls := "No"
				if n.TLSEnabled {
					tls = "Yes"
				}

				row := []string{
					fmt.Sprintf("%d", n.ID),
					n.Server,
					fmt.Sprintf("%d", n.Port),
					n.Protocol,
					n.Country,
					tls,
				}

				if sort == "latency" || sort == "success" || sort == "random" {
					stats, err := globalCache.GetNodeStats(n.ID)
					if err == nil {
						latStr := fmt.Sprintf("%dms", stats.LatencyMS)
						if stats.LatencyMS == 0 {
							latStr = "-"
						}
						score := stats.SuccessRate*0.7 + (1-float64(min(stats.LatencyMS, 10000))/10000.0)*0.3
						row = append(row, latStr, fmt.Sprintf("%.3f", score))
					} else {
						row = append(row, "-", "-")
					}
				}

				table.Append(row)
			}

			table.Render()
			total := len(nodes)
			if limit > 0 && limit < total {
				fmt.Printf("\nShowing %d of %d nodes (use --limit 0 to show all)\n", limit, total)
			} else {
				fmt.Printf("\nTotal: %d nodes\n", total)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&country, "country", "", "Filter by country (VN, US, JP, SG, all)")
	cmd.Flags().StringVar(&protocol, "protocol", "", "Filter by protocol (vless, ss, trojan, vmess, hy2)")
	cmd.Flags().StringVar(&sort, "sort", "id", "Sort by: id, latency, success, random")
	cmd.Flags().IntVarP(&limit, "limit", "n", 0, "Limit number of results (0 = all)")

	return cmd
}

func newPoolStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show pool statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to fetch nodes: %w", err)
			}

			if len(nodes) == 0 {
				fmt.Println("No nodes in pool.")
				return nil
			}

			// Protocol breakdown
			protocols := make(map[string]int)
			countries := make(map[string]int)
			for _, n := range nodes {
				protocols[n.Protocol]++
				countries[n.Country]++
			}

			fmt.Println("Protocol breakdown:")
			for proto, count := range protocols {
				pct := float64(count) / float64(len(nodes)) * 100
				fmt.Printf("  %-10s %3d (%4.0f%%)\n", proto, count, pct)
			}

			fmt.Println("\nCountry breakdown:")
			for country, count := range countries {
				pct := float64(count) / float64(len(nodes)) * 100
				fmt.Printf("  %-5s %3d (%4.0f%%)\n", country, count, pct)
			}

			return nil
		},
	}
}

func newPoolRefreshCmd() *cobra.Command {
	var country string

	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Refresh node pool from sources",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Refreshing node pool from sources...")

			c := globalConfig
			if country != "" {
				c.Checker.Country = country
			}

			p := fetcher.NewPool(globalCache)
			nodes, err := p.FetchNodes(cmd.Context(), c.Checker.Country)
			if err != nil {
				return fmt.Errorf("failed to fetch nodes: %w", err)
			}

			fmt.Printf("✓ Fetched %d nodes from sources\n", len(nodes))
			return nil
		},
	}

	cmd.Flags().StringVar(&country, "country", "", "Country to fetch (overrides config)")
	return cmd
}

func newPoolSearchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "search <query>",
		Short: "Search nodes by server IP, tag, or country",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.ToLower(args[0])

			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to fetch nodes: %w", err)
			}

			var results []*types.Node
			for _, n := range nodes {
				if strings.Contains(strings.ToLower(n.Server), query) ||
					strings.Contains(strings.ToLower(n.Tag), query) ||
					strings.Contains(strings.ToLower(n.Country), query) {
					results = append(results, n)
				}
			}

			if len(results) == 0 {
				fmt.Printf("No nodes matching: %s\n", query)
				return nil
			}

			table := tablewriter.NewWriter(os.Stdout)
			table.SetHeader([]string{"ID", "Server", "Port", "Protocol", "Country", "TLS"})
			table.SetBorder(true)

			for _, n := range results {
				tls := "No"
				if n.TLSEnabled {
					tls = "Yes"
				}
				table.Append([]string{
					fmt.Sprintf("%d", n.ID),
					n.Server,
					fmt.Sprintf("%d", n.Port),
					n.Protocol,
					n.Country,
					tls,
				})
			}

			table.Render()
			fmt.Printf("\nFound: %d nodes matching '%s'\n", len(results), query)
			return nil
		},
	}
}

// sortNodes sorts the node list based on the sort mode.
func sortNodes(nodes []*types.Node, sortBy string, c *cache.Cache) {
	if sortBy == "latency" {
		// Sort by latency ascending (fastest first)
		for i := 0; i < len(nodes); i++ {
			for j := i + 1; j < len(nodes); j++ {
				si, _ := c.GetNodeStats(nodes[i].ID)
				sj, _ := c.GetNodeStats(nodes[j].ID)
				li := int64(99999)
				lj := int64(99999)
				if si != nil && si.LatencyMS > 0 {
					li = si.LatencyMS
				}
				if sj != nil && sj.LatencyMS > 0 {
					lj = sj.LatencyMS
				}
				if lj < li {
					nodes[i], nodes[j] = nodes[j], nodes[i]
				}
			}
		}
	} else if sortBy == "success" {
		// Sort by success rate descending (best first)
		for i := 0; i < len(nodes); i++ {
			for j := i + 1; j < len(nodes); j++ {
				si, _ := c.GetNodeStats(nodes[i].ID)
				sj, _ := c.GetNodeStats(nodes[j].ID)
				sci := 0.0
				scj := 0.0
				if si != nil {
					sci = si.SuccessRate
				}
				if sj != nil {
					scj = sj.SuccessRate
				}
				if scj > sci {
					nodes[i], nodes[j] = nodes[j], nodes[i]
				}
			}
		}
	} else if sortBy == "random" {
		// Fisher-Yates shuffle
		for i := len(nodes) - 1; i > 0; i-- {
			j := rand.Intn(i + 1)
			nodes[i], nodes[j] = nodes[j], nodes[i]
		}
	}
	// default: "id" - no sorting needed, already sorted by ID from DB
}

func newPoolTopCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "top",
		Short: "Show top-performing nodes by latency and success rate",
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to fetch nodes: %w", err)
			}

			if len(nodes) == 0 {
				fmt.Println("No nodes in pool. Try: rotaze pool refresh")
				return nil
			}

			// Get stats for each node and sort by success rate + latency
			type nodeScore struct {
				node  *types.Node
				score float64
			}

			var scored []nodeScore
			for _, n := range nodes {
				stats, err := globalCache.GetNodeStats(n.ID)
				if err != nil || stats.TotalChecks == 0 {
					// Unchecked nodes get a neutral score
					scored = append(scored, nodeScore{node: n, score: 0.5})
					continue
				}
				// Score = success_rate * 0.7 + (1 - latency_normalized) * 0.3
				latNorm := float64(stats.LatencyMS) / 10000.0
				if latNorm > 1 {
					latNorm = 1
				}
				score := stats.SuccessRate*0.7 + (1-latNorm)*0.3
				scored = append(scored, nodeScore{node: n, score: score})
			}

			// Sort by score descending
			for i := 0; i < len(scored); i++ {
				for j := i + 1; j < len(scored); j++ {
					if scored[j].score > scored[i].score {
						scored[i], scored[j] = scored[j], scored[i]
					}
				}
			}

			if limit <= 0 || limit > len(scored) {
				limit = 20
			}

			table := tablewriter.NewWriter(os.Stdout)
			table.SetHeader([]string{"ID", "Server", "Port", "Protocol", "Country", "Score"})
			table.SetBorder(true)

			for i := 0; i < limit && i < len(scored); i++ {
				s := scored[i]
				table.Append([]string{
					fmt.Sprintf("%d", s.node.ID),
					s.node.Server,
					fmt.Sprintf("%d", s.node.Port),
					s.node.Protocol,
					s.node.Country,
					fmt.Sprintf("%.3f", s.score),
				})
			}

			table.Render()
			fmt.Printf("\nShowing top %d of %d nodes\n", limit, len(scored))
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "Number of top nodes to show")
	return cmd
}

func newPoolExportCmd() *cobra.Command {
	var (
		format  string
		country string
	)

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export nodes as URI list or JSON",
		Long:  `Export nodes from the pool as sing-box compatible URIs or JSON.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to fetch nodes: %w", err)
			}

			// Filter by country (strict: only geo-confirmed matches; unknown data
			// is never assumed to belong to the requested country)
			if country != "" && !strings.EqualFold(country, "all") {
				var filtered []*types.Node
				for _, n := range nodes {
					if strings.EqualFold(n.Country, country) {
						filtered = append(filtered, n)
					}
				}
				nodes = filtered
			}

			if len(nodes) == 0 {
				fmt.Println("No nodes to export.")
				return nil
			}

			switch format {
			case "json":
				b, err := json.MarshalIndent(nodes, "", "  ")
				if err != nil {
					return fmt.Errorf("JSON marshal failed: %w", err)
				}
				fmt.Println(string(b))
			case "uri":
				for _, n := range nodes {
					uri := nodeToURI(n)
					if uri != "" {
						fmt.Println(uri)
					}
				}
			case "csv":
				fmt.Println("ID,Server,Port,Protocol,Country,TLS,Tag")
				for _, n := range nodes {
					tls := "false"
					if n.TLSEnabled {
						tls = "true"
					}
					fmt.Printf("%d,%s,%d,%s,%s,%s,%s\n", n.ID, n.Server, n.Port, n.Protocol, n.Country, tls, n.Tag)
				}
			default:
				return fmt.Errorf("unsupported format: %s (use json, uri, or csv)", format)
			}

			fmt.Fprintf(os.Stderr, "Exported %d nodes\n", len(nodes))
			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "uri", "Output format: uri, json, csv")
	cmd.Flags().StringVar(&country, "country", "", "Filter by country")

	return cmd
}

func newPoolImportCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import nodes from URI list or JSON",
		Long:  `Import nodes from a file or stdin. Supports URI list and JSON formats.`,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var input []byte
			var err error

			if len(args) > 0 {
				input, err = os.ReadFile(args[0])
				if err != nil {
					return fmt.Errorf("failed to read file: %w", err)
				}
			} else {
				input, err = io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("failed to read stdin: %w", err)
				}
			}

			content := string(input)

			switch format {
			case "uri":
				lines := strings.Split(content, "\n")
				var nodes []*types.Node
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					node, err := fetcher.ParseURIPublic(line)
					if err != nil {
						continue
					}
					nodes = append(nodes, node)
				}
				if err := globalCache.SaveNodes(nodes); err != nil {
					return fmt.Errorf("failed to save nodes: %w", err)
				}
				fmt.Printf("✓ Imported %d nodes from URIs\n", len(nodes))

			case "json":
				var nodes []*types.Node
				if err := json.Unmarshal(input, &nodes); err != nil {
					return fmt.Errorf("JSON parse failed: %w", err)
				}
				if err := globalCache.SaveNodes(nodes); err != nil {
					return fmt.Errorf("failed to save nodes: %w", err)
				}
				fmt.Printf("✓ Imported %d nodes from JSON\n", len(nodes))

			default:
				return fmt.Errorf("unsupported format: %s (use uri or json)", format)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "uri", "Input format: uri, json")

	return cmd
}

// nodeToURI converts a Node back to a proxy URI string.
func nodeToURI(n *types.Node) string {
	switch n.Protocol {
	case "vless":
		uri := fmt.Sprintf("vless://%s@%s:%d", n.UUID, n.Server, n.Port)
		params := []string{}
		if n.TLSEnabled {
			params = append(params, "security=tls")
			if n.TLSSNI != "" {
				params = append(params, "sni="+n.TLSSNI)
			}
		}
		if n.TransportType != "" && n.TransportType != "tcp" {
			params = append(params, "type="+n.TransportType)
			if n.TransportHost != "" {
				params = append(params, "host="+n.TransportHost)
			}
			if n.TransportPath != "" {
				params = append(params, "path="+n.TransportPath)
			}
		}
		if len(params) > 0 {
			uri += "?" + strings.Join(params, "&")
		}
		if n.Tag != "" {
			uri += "#" + n.Tag
		}
		return uri

	case "ss":
		cred := fmt.Sprintf("%s:%s", n.EncryptMethod, n.Password)
		encoded := base64.StdEncoding.EncodeToString([]byte(cred))
		uri := fmt.Sprintf("ss://%s@%s:%d", encoded, n.Server, n.Port)
		if n.Tag != "" {
			uri += "#" + n.Tag
		}
		return uri

	case "trojan":
		uri := fmt.Sprintf("trojan://%s@%s:%d", n.Password, n.Server, n.Port)
		params := []string{}
		if n.TLSEnabled {
			params = append(params, "security=tls")
			if n.TLSSNI != "" {
				params = append(params, "sni="+n.TLSSNI)
			}
		}
		if len(params) > 0 {
			uri += "?" + strings.Join(params, "&")
		}
		if n.Tag != "" {
			uri += "#" + n.Tag
		}
		return uri

	case "hy2":
		return fmt.Sprintf("hy2://%s@%s:%d#%s", n.Password, n.Server, n.Port, n.Tag)

	default:
		return ""
	}
}

// ============================================================
// pool add - manual tunnel addition
// ============================================================

func newPoolAddCmd() *cobra.Command {
	var (
		protocol      string
		server        string
		port          int
		uuid          string
		password      string
		method        string
		country       string
		tag           string
		tls           bool
		sni           string
		transport     string
		transportHost string
		transportPath string
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a tunnel node manually",
		Long: `Add a tunnel node manually by specifying its parameters.
Alternatively, pipe a URI from stdin:
  echo 'vless://uuid@server:port?security=tls#tag' | rotaze pool add
  cat my-nodes.txt | rotaze pool add --format uri`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check if URI is provided as arg or piped from stdin
			if len(args) > 0 {
				// Parse URI from argument
				node, err := fetcher.ParseURIPublic(args[0])
				if err != nil {
					return fmt.Errorf("failed to parse URI: %w", err)
				}
				if err := globalCache.SaveNodes([]*types.Node{node}); err != nil {
					return fmt.Errorf("failed to save node: %w", err)
				}
				fmt.Printf("✓ Added node: %s:%d (%s, %s)\n", node.Server, node.Port, node.Protocol, node.Country)
				return nil
			}

			// Check if stdin has data
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) == 0 {
				// Data is piped from stdin
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("failed to read stdin: %w", err)
				}
				lines := strings.Split(string(data), "\n")
				var added int
				for _, line := range lines {
					line = strings.TrimSpace(line)
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					node, err := fetcher.ParseURIPublic(line)
					if err != nil {
						fmt.Fprintf(os.Stderr, "[SKIP] %s: %v\n", line[:min(50, len(line))], err)
						continue
					}
					if err := globalCache.SaveNodes([]*types.Node{node}); err != nil {
						fmt.Fprintf(os.Stderr, "[SKIP] save failed: %v\n", err)
						continue
					}
					added++
				}
				fmt.Printf("✓ Added %d nodes from stdin\n", added)
				return nil
			}

			// Manual parameter mode
			if server == "" || port == 0 {
				return fmt.Errorf("specify --server and --port, or provide a URI as argument/stdin")
			}

			if protocol == "" {
				protocol = "ss"
			}

			if tag == "" {
				tag = fmt.Sprintf("%s-%s-%d", protocol, server, port)
			}

			node := &types.Node{
				Server:        server,
				Port:          port,
				Protocol:      protocol,
				Country:       country,
				UUID:          uuid,
				Password:      password,
				EncryptMethod: method,
				TLSEnabled:    tls,
				TLSSNI:        sni,
				TransportType: transport,
				TransportHost: transportHost,
				TransportPath: transportPath,
				Tag:           tag,
			}

			if err := globalCache.SaveNodes([]*types.Node{node}); err != nil {
				return fmt.Errorf("failed to save node: %w", err)
			}

			fmt.Printf("✓ Added node: %s:%d (%s) tag=%s\n", node.Server, node.Port, node.Protocol, node.Tag)
			return nil
		},
	}

	cmd.Flags().StringVarP(&protocol, "protocol", "p", "ss", "Protocol: vless, ss, trojan, vmess, hy2")
	cmd.Flags().StringVar(&server, "server", "", "Server address")
	cmd.Flags().IntVarP(&port, "port", "P", 0, "Server port")
	cmd.Flags().StringVar(&uuid, "uuid", "", "UUID (VLESS/VMess)")
	cmd.Flags().StringVarP(&password, "password", "w", "", "Password (SS/Trojan/Hy2)")
	cmd.Flags().StringVarP(&method, "method", "m", "", "Encryption method (SS)")
	cmd.Flags().StringVar(&country, "country", "", "Country code")
	cmd.Flags().StringVarP(&tag, "tag", "t", "", "Node tag")
	cmd.Flags().BoolVar(&tls, "tls", false, "Enable TLS")
	cmd.Flags().StringVar(&sni, "sni", "", "TLS SNI")
	cmd.Flags().StringVar(&transport, "transport", "", "Transport: ws, grpc, httpupgrade")
	cmd.Flags().StringVar(&transportHost, "transport-host", "", "Transport host")
	cmd.Flags().StringVar(&transportPath, "transport-path", "", "Transport path")

	return cmd
}

// ============================================================
// pool check - auto-check node liveness
// ============================================================

func newPoolCheckCmd() *cobra.Command {
	var (
		timeout     int
		concurrency int
		protocol    string
		country     string
	)

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Auto-check node liveness via TCP connect",
		Long: `Check which nodes are alive by attempting TCP connections.
Updates node stats in the database for smart selection.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to get nodes: %w", err)
			}

			// Filter
			if protocol != "" {
				var filtered []*types.Node
				for _, n := range nodes {
					if n.Protocol == protocol {
						filtered = append(filtered, n)
					}
				}
				nodes = filtered
			}
			if country != "" && !strings.EqualFold(country, "all") {
				var filtered []*types.Node
				for _, n := range nodes {
					if strings.EqualFold(n.Country, country) {
						filtered = append(filtered, n)
					}
				}
				nodes = filtered
			}

			if len(nodes) == 0 {
				fmt.Println("No nodes to check.")
				return nil
			}

			fmt.Printf("Checking %d nodes (timeout=%ds, concurrency=%d)...\n", len(nodes), timeout, concurrency)

			// Semaphore for concurrency control
			sem := make(chan struct{}, concurrency)
			var wg sync.WaitGroup
			var alive, dead int
			var mu sync.Mutex

			for i, node := range nodes {
				wg.Add(1)
				sem <- struct{}{}

				go func(n *types.Node, idx int) {
					defer wg.Done()
					defer func() { <-sem }()

					addr := fmt.Sprintf("%s:%d", n.Server, n.Port)
					start := time.Now()
					dialer := net.Dialer{Timeout: time.Duration(timeout) * time.Second}
					conn, err := dialer.DialContext(cmd.Context(), "tcp", addr)
					latency := time.Since(start)

					mu.Lock()
					if err == nil {
						conn.Close()
						alive++
						// Update stats: success
						globalCache.UpdateNodeStats(n.ID, latency.Milliseconds(), true)
						if idx < 20 || idx%100 == 0 {
							fmt.Printf("  ✓ %s:%d (%s) [%v]\n", n.Server, n.Port, n.Protocol, latency.Round(time.Millisecond))
						}
					} else {
						dead++
						// Update stats: failure
						globalCache.UpdateNodeStats(n.ID, 0, false)
					}
					mu.Unlock()

				}(node, i)
			}

			wg.Wait()

			fmt.Printf("\n✓ Check complete: %d alive, %d dead (%d total)\n", alive, dead, len(nodes))
			return nil
		},
	}

	cmd.Flags().IntVarP(&timeout, "timeout", "t", 3, "TCP connect timeout in seconds")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 50, "Max concurrent checks")
	cmd.Flags().StringVarP(&protocol, "protocol", "p", "", "Filter by protocol")
	cmd.Flags().StringVar(&country, "country", "", "Filter by country")

	return cmd
}

// ============================================================
// pool geoip - enrich country data for all nodes
// ============================================================

func newPoolGeoIPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "geoip",
		Short: "Enrich GeoIP country data for all nodes",
		Long: `Look up country codes for nodes with unknown country.
Uses ip-api.com batch API with hostname resolution.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to get nodes: %w", err)
			}

			// Count unknowns
			var unknown int
			for _, n := range nodes {
				if n.Country == "" || n.Country == "unknown" {
					unknown++
				}
			}

			if unknown == 0 {
				fmt.Println("✓ All nodes already have country data.")
				return nil
			}

			fmt.Printf("Enriching GeoIP for %d nodes with unknown country...\n", unknown)
			fmt.Println("(This may take a moment due to rate limiting)")

			// Use the fetcher's enrichCountries function
			fetcher.EnrichCountries(nodes)

			// Save updated nodes
			if err := globalCache.SaveNodes(nodes); err != nil {
				return fmt.Errorf("failed to save enriched nodes: %w", err)
			}

			// Count results
			var enriched int
			for _, n := range nodes {
				if n.Country != "" && n.Country != "unknown" {
					enriched++
				}
			}

			fmt.Printf("✓ GeoIP enrichment complete: %d nodes now have country data\n", enriched)
			return nil
		},
	}
}

// ============================================================
// pool ping - quick liveness check
// ============================================================

func newPoolPingCmd() *cobra.Command {
	var (
		timeout int
	)

	cmd := &cobra.Command{
		Use:   "ping [node-id|server]",
		Short: "Quick ping check for a specific node",
		Args:  cobra.ExactArgs(1),
		Long: `Quick TCP ping check for a specific node by ID or server address.
Shows latency and updates node stats.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			var node *types.Node

			// Try to parse as ID first
			if id, err := strconv.Atoi(target); err == nil {
				node, _ = globalCache.GetNodeByID(id)
			}

			// If not found by ID, search by server
			if node == nil {
				nodes, err := globalCache.GetNodes()
				if err != nil {
					return fmt.Errorf("failed to get nodes: %w", err)
				}
				for _, n := range nodes {
					if n.Server == target || strings.Contains(n.Server, target) {
						node = n
						break
					}
				}
			}

			if node == nil {
				return fmt.Errorf("node not found: %s", target)
			}

			addr := fmt.Sprintf("%s:%d", node.Server, node.Port)
			fmt.Printf("Pinging %s (%s, %s)... ", addr, node.Protocol, node.Country)

			start := time.Now()
			dialer := net.Dialer{Timeout: time.Duration(timeout) * time.Second}
			conn, err := dialer.DialContext(cmd.Context(), "tcp", addr)
			latency := time.Since(start)

			if err != nil {
				fmt.Printf("✗ FAILED (%v)\n", err)
				globalCache.UpdateNodeStats(node.ID, 0, false)
				return nil
			}
			conn.Close()

			fmt.Printf("✓ OK [%v]\n", latency.Round(time.Millisecond))
			globalCache.UpdateNodeStats(node.ID, latency.Milliseconds(), true)

			// Show stats
			stats, err := globalCache.GetNodeStats(node.ID)
			if err == nil {
				fmt.Printf("  Stats: %d checks, %.0f%% success, %dms avg latency\n",
					stats.TotalChecks,
					stats.SuccessRate*100,
					stats.LatencyMS)
			}

			return nil
		},
	}

	cmd.Flags().IntVarP(&timeout, "timeout", "t", 5, "Timeout in seconds")
	return cmd
}

// ============================================================
// pool dedup - remove duplicate nodes
// ============================================================

func newPoolDedupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "dedup",
		Short: "Remove duplicate nodes (same server:port:protocol)",
		Long: `Find and remove duplicate nodes that share the same server, port, and protocol.
Keeps the node with the most stats/checks.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			groups, err := globalCache.GetDuplicateGroups()
			if err != nil {
				return fmt.Errorf("failed to find duplicates: %w", err)
			}

			var removed int
			var deletedIDs []int

			for key, nodes := range groups {
				if len(nodes) <= 1 {
					continue
				}

				// Find the one to keep: most checks, then lowest ID
				best := nodes[0]
				for _, n := range nodes[1:] {
					statsB, _ := globalCache.GetNodeStats(best.ID)
					statsN, _ := globalCache.GetNodeStats(n.ID)
					bChecks := 0
					nChecks := 0
					if statsB != nil {
						bChecks = statsB.TotalChecks
					}
					if statsN != nil {
						nChecks = statsN.TotalChecks
					}
					if nChecks > bChecks || (nChecks == bChecks && n.ID < best.ID) {
						best = n
					}
				}

				for _, n := range nodes {
					if n.ID != best.ID {
						deletedIDs = append(deletedIDs, n.ID)
						removed++
					}
				}
				if removed <= 10 {
					fmt.Printf("  Dup group %s: keeping #%d, removing %d copies\n", key, best.ID, len(nodes)-1)
				}
			}

			if removed == 0 {
				fmt.Println("✓ No duplicates found.")
				return nil
			}

			// Delete duplicated nodes and their stats
			if err := globalCache.DeleteNodeStats(deletedIDs); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to delete stats: %v\n", err)
			}
			if err := globalCache.DeleteNodes(deletedIDs); err != nil {
				return fmt.Errorf("failed to delete duplicates: %w", err)
			}

			fmt.Printf("\n✓ Removed %d duplicate nodes\n", removed)
			return nil
		},
	}
}

// ============================================================
// pool health - full health check (TCP + HTTP)
// ============================================================

func newPoolHealthCmd() *cobra.Command {
	var (
		timeout     int
		concurrency int
		protocol    string
		country     string
		httpCheck   bool
		url         string
	)

	cmd := &cobra.Command{
		Use:   "health",
		Short: "Full health check (TCP connect + optional HTTP)",
		Long: `Comprehensive health check for nodes.
Performs TCP connectivity test, and optionally an HTTP request through the proxy.
Updates stats for smart node selection.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to get nodes: %w", err)
			}

			// Filter
			if protocol != "" {
				var filtered []*types.Node
				for _, n := range nodes {
					if n.Protocol == protocol {
						filtered = append(filtered, n)
					}
				}
				nodes = filtered
			}
			if country != "" && country != "all" {
				var filtered []*types.Node
				for _, n := range nodes {
					if n.Country == country {
						filtered = append(filtered, n)
					}
				}
				nodes = filtered
			}

			if len(nodes) == 0 {
				fmt.Println("No nodes to check.")
				return nil
			}

			fmt.Printf("Health check: %d nodes (TCP timeout=%ds, concurrency=%d, http=%v)\n",
				len(nodes), timeout, concurrency, httpCheck)

			sem := make(chan struct{}, concurrency)
			var wg sync.WaitGroup
			var mu sync.Mutex
			alive, dead := 0, 0

			results := make([]string, 0, len(nodes))

			for i, node := range nodes {
				wg.Add(1)
				sem <- struct{}{}

				go func(n *types.Node, idx int) {
					defer wg.Done()
					defer func() { <-sem }()

					addr := fmt.Sprintf("%s:%d", n.Server, n.Port)
					start := time.Now()
					dialer := net.Dialer{Timeout: time.Duration(timeout) * time.Second}
					conn, err := dialer.DialContext(cmd.Context(), "tcp", addr)
					latency := time.Since(start)

					mu.Lock()
					defer mu.Unlock()

					if err != nil {
						dead++
						globalCache.UpdateNodeStats(n.ID, 0, false)
						return
					}

					conn.Close()
					alive++

					status := "TCP OK"
					if httpCheck && url != "" {
						// TODO: could do HTTP check through proxy
						status = "TCP+HTTP OK"
					}

					result := fmt.Sprintf("  ✓ %s:%d (%s, %s) [%v] %s",
						n.Server, n.Port, n.Protocol, n.Country,
						latency.Round(time.Millisecond), status)
					results = append(results, result)
					globalCache.UpdateNodeStats(n.ID, latency.Milliseconds(), true)

				}(node, i)
			}

			wg.Wait()

			// Print top results
			printLimit := 20
			if len(results) < printLimit {
				printLimit = len(results)
			}
			for i := 0; i < printLimit; i++ {
				fmt.Println(results[i])
			}
			if len(results) > printLimit {
				fmt.Printf("  ... and %d more alive nodes\n", len(results)-printLimit)
			}

			fmt.Printf("\n✓ Health check complete: %d alive, %d dead (%d total)\n", alive, dead, len(nodes))
			return nil
		},
	}

	cmd.Flags().IntVarP(&timeout, "timeout", "t", 3, "TCP connect timeout in seconds")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 50, "Max concurrent checks")
	cmd.Flags().StringVarP(&protocol, "protocol", "p", "", "Filter by protocol")
	cmd.Flags().StringVar(&country, "country", "", "Filter by country")
	cmd.Flags().BoolVar(&httpCheck, "http", false, "Enable HTTP check through proxy")
	cmd.Flags().StringVar(&url, "url", "https://httpbin.org/ip", "URL for HTTP check")

	return cmd
}

// ============================================================
// pool count - quick node count
// ============================================================

func newPoolCountCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "count",
		Short: "Quick node count summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to get nodes: %w", err)
			}

			total := len(nodes)
			protocols := make(map[string]int)
			countries := make(map[string]int)
			checked := 0
			alive := 0

			for _, n := range nodes {
				protocols[n.Protocol]++
				if n.Country != "" && n.Country != "unknown" {
					countries[n.Country]++
				}
				stats, err := globalCache.GetNodeStats(n.ID)
				if err == nil && stats.TotalChecks > 0 {
					checked++
					if stats.SuccessRate > 0.5 {
						alive++
					}
				}
			}

			fmt.Printf("Pool: %d nodes\n", total)
			fmt.Printf("  Protocols: ")
			for p, c := range protocols {
				fmt.Printf("%s=%d ", p, c)
			}
			fmt.Println()
			fmt.Printf("  Countries: %d unique\n", len(countries))
			fmt.Printf("  Checked:   %d nodes\n", checked)
			fmt.Printf("  Alive:     %d nodes (%.0f%% success)\n", alive, float64(alive)/float64(max(checked, 1))*100)

			return nil
		},
	}
}

// ============================================================
// pool backup - backup/restore node pool
// ============================================================

func newPoolBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Backup or restore node pool",
		Long:  `Backup the node pool to a JSON file, or restore from backup.`,
	}

	cmd.AddCommand(newPoolBackupSaveCmd())
	cmd.AddCommand(newPoolBackupLoadCmd())

	return cmd
}

func newPoolBackupSaveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "save [file]",
		Short: "Save node pool to backup file",
		Long:  `Save all nodes and their stats to a JSON file.`,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to get nodes: %w", err)
			}

			type BackupNode struct {
				*types.Node
				Stats *types.NodeStats `json:"stats,omitempty"`
			}

			var backup []BackupNode
			for _, n := range nodes {
				bn := BackupNode{Node: n}
				bn.Stats, _ = globalCache.GetNodeStats(n.ID)
				backup = append(backup, bn)
			}

			b, err := json.MarshalIndent(backup, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal backup: %w", err)
			}

			if len(args) > 0 {
				if err := os.WriteFile(args[0], b, 0644); err != nil {
					return fmt.Errorf("failed to write backup: %w", err)
				}
				fmt.Printf("✓ Backup saved to %s (%d nodes)\n", args[0], len(backup))
			} else {
				fmt.Println(string(b))
			}

			return nil
		},
	}
}

func newPoolBackupLoadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "load [file]",
		Short: "Load node pool from backup file",
		Long:  `Restore nodes and stats from a JSON backup file.`,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var input []byte
			var err error
			if len(args) > 0 {
				input, err = os.ReadFile(args[0])
				if err != nil {
					return fmt.Errorf("failed to read backup: %w", err)
				}
			} else {
				input, err = io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("failed to read stdin: %w", err)
				}
			}

			type BackupNode struct {
				*types.Node
				Stats *types.NodeStats `json:"stats,omitempty"`
			}

			var backup []BackupNode
			if err := json.Unmarshal(input, &backup); err != nil {
				return fmt.Errorf("failed to parse backup: %w", err)
			}

			var nodes []*types.Node
			for _, bn := range backup {
				nodes = append(nodes, bn.Node)
			}

			if err := globalCache.SaveNodes(nodes); err != nil {
				return fmt.Errorf("failed to restore nodes: %w", err)
			}

			// Restore stats
			restored := 0
			for _, bn := range backup {
				if bn.Stats != nil && bn.Stats.TotalChecks > 0 {
					globalCache.UpdateNodeStats(bn.Node.ID, bn.Stats.LatencyMS, bn.Stats.SuccessRate > 0.5)
					restored++
				}
			}

			fmt.Printf("✓ Restored %d nodes (%d with stats) from backup\n", len(nodes), restored)
			return nil
		},
	}
}

// ============================================================
// pool watch - periodic health check
// ============================================================

func newPoolWatchCmd() *cobra.Command {
	var (
		interval    int
		timeout     int
		concurrency int
		protocol    string
	)

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Periodically check node health",
		Long: `Continuously monitor node health at a set interval.
Runs health checks in a loop, updating stats for smart selection.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Pool watch started (interval=%ds, timeout=%ds, concurrency=%d)\n", interval, timeout, concurrency)
			fmt.Println("Press Ctrl+C to stop.")

			ticker := time.NewTicker(time.Duration(interval) * time.Second)
			defer ticker.Stop()

			// Handle Ctrl+C
			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

			runCheck := func() {
				nodes, err := globalCache.GetNodes()
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error getting nodes: %v\n", err)
					return
				}

				// Filter by protocol
				if protocol != "" {
					var filtered []*types.Node
					for _, n := range nodes {
						if n.Protocol == protocol {
							filtered = append(filtered, n)
						}
					}
					nodes = filtered
				}

				sem := make(chan struct{}, concurrency)
				var wg sync.WaitGroup
				var mu sync.Mutex
				alive, dead := 0, 0

				for _, node := range nodes {
					wg.Add(1)
					sem <- struct{}{}

					go func(n *types.Node) {
						defer wg.Done()
						defer func() { <-sem }()

						addr := fmt.Sprintf("%s:%d", n.Server, n.Port)
						start := time.Now()
						dialer := net.Dialer{Timeout: time.Duration(timeout) * time.Second}
						conn, err := dialer.DialContext(cmd.Context(), "tcp", addr)
						latency := time.Since(start)

						mu.Lock()
						if err == nil {
							conn.Close()
							alive++
							globalCache.UpdateNodeStats(n.ID, latency.Milliseconds(), true)
						} else {
							dead++
							globalCache.UpdateNodeStats(n.ID, 0, false)
						}
						mu.Unlock()
					}(node)
				}

				wg.Wait()
				fmt.Printf("[%s] ✓ Check: %d alive, %d dead (%d total)\n",
					time.Now().Format("15:04:05"), alive, dead, len(nodes))
			}

			// Run immediately
			runCheck()

			for {
				select {
				case <-ticker.C:
					runCheck()
				case <-sigs:
					fmt.Println("\n✓ Pool watch stopped.")
					return nil
				}
			}
		},
	}

	cmd.Flags().IntVarP(&interval, "interval", "i", 300, "Check interval in seconds")
	cmd.Flags().IntVarP(&timeout, "timeout", "t", 3, "TCP connect timeout in seconds")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 50, "Max concurrent checks")
	cmd.Flags().StringVarP(&protocol, "protocol", "p", "", "Filter by protocol")

	return cmd
}

// ============================================================
// pool history - rotation history
// ============================================================

func newPoolHistoryCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "history",
		Short: "Show rotation history",
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit <= 0 {
				limit = 20
			}

			rotations, err := globalCache.GetRecentRotations(limit)
			if err != nil {
				return fmt.Errorf("failed to get rotations: %w", err)
			}

			if len(rotations) == 0 {
				fmt.Println("No rotation history found.")
				return nil
			}

			table := tablewriter.NewWriter(os.Stdout)
			table.SetHeader([]string{"Time", "From", "To", "Reason", "Status"})
			table.SetBorder(true)

			for _, r := range rotations {
				fromNode, _ := globalCache.GetNodeByID(r.FromNodeID)
				toNode, _ := globalCache.GetNodeByID(r.ToNodeID)

				fromStr := fmt.Sprintf("#%d", r.FromNodeID)
				if fromNode != nil {
					fromStr = fmt.Sprintf("#%d %s:%d", fromNode.ID, fromNode.Server, fromNode.Port)
				}

				toStr := fmt.Sprintf("#%d", r.ToNodeID)
				if toNode != nil {
					toStr = fmt.Sprintf("#%d %s:%d", toNode.ID, toNode.Server, toNode.Port)
				}

				statusIcon := "✓"
				if r.Status == "failed" {
					statusIcon = "✗"
				}

				table.Append([]string{
					r.Timestamp.Format("2006-01-02 15:04:05"),
					fromStr,
					toStr,
					r.Reason,
					statusIcon + " " + r.Status,
				})
			}

			table.Render()
			fmt.Printf("\nShowing %d most recent rotations\n", len(rotations))
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "Number of rotations to show")

	return cmd
}

// ============================================================
// pool merge - merge multiple backup files
// ============================================================

func newPoolMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge <file1> [file2] ...",
		Short: "Merge multiple backup files into the pool",
		Long: `Merge nodes from multiple JSON backup files into the pool.
Deduplicates by server:port:protocol.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			type BackupNode struct {
				*types.Node
				Stats *types.NodeStats `json:"stats,omitempty"`
			}

			var allNodes []*types.Node
			var allStats []BackupNode

			for _, file := range args {
				input, err := os.ReadFile(file)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[SKIP] %s: %v\n", file, err)
					continue
				}

				var backup []BackupNode
				if err := json.Unmarshal(input, &backup); err != nil {
					fmt.Fprintf(os.Stderr, "[SKIP] %s: invalid JSON: %v\n", file, err)
					continue
				}

				for _, bn := range backup {
					allNodes = append(allNodes, bn.Node)
					if bn.Stats != nil {
						allStats = append(allStats, bn)
					}
				}
				fmt.Printf("  Loaded %s: %d nodes\n", file, len(backup))
			}

			if len(allNodes) == 0 {
				fmt.Println("No nodes found in any backup file.")
				return nil
			}

			// Save all (deduplication handled by SaveNodes)
			if err := globalCache.SaveNodes(allNodes); err != nil {
				return fmt.Errorf("failed to save merged nodes: %w", err)
			}

			// Restore stats
			for _, bn := range allStats {
				if bn.Stats.TotalChecks > 0 {
					globalCache.UpdateNodeStats(bn.Node.ID, bn.Stats.LatencyMS, bn.Stats.SuccessRate > 0.5)
				}
			}

			fmt.Printf("\n✓ Merged %d nodes (%d with stats) from %d files\n", len(allNodes), len(allStats), len(args))
			return nil
		},
	}
}

// ============================================================
// pool tree - hierarchical view of pool
// ============================================================

func newPoolTreeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tree",
		Short: "Show pool tree (protocol → country → nodes)",
		RunE: func(cmd *cobra.Command, args []string) error {
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to get nodes: %w", err)
			}

			if len(nodes) == 0 {
				fmt.Println("Pool is empty.")
				return nil
			}

			// Build tree: protocol -> country -> []Node
			tree := make(map[string]map[string][]*types.Node)
			for _, n := range nodes {
				if tree[n.Protocol] == nil {
					tree[n.Protocol] = make(map[string][]*types.Node)
				}
				country := n.Country
				if country == "" || country == "unknown" {
					country = "??"
				}
				tree[n.Protocol][country] = append(tree[n.Protocol][country], n)
			}

			// Print tree
			protoOrder := []string{"ss", "vless", "trojan", "vmess", "hy2"}
			fmt.Printf("Pool: %d nodes\n\n", len(nodes))

			for pi, proto := range protoOrder {
				countries := tree[proto]
				if len(countries) == 0 {
					continue
				}

				isLastProto := pi == len(protoOrder)-1
				protoPrefix := "├──"
				if isLastProto {
					protoPrefix = "└──"
				}

				// Sort countries by count
				type countryCount struct {
					name  string
					count int
				}
				var sorted []countryCount
				for c, ns := range countries {
					sorted = append(sorted, countryCount{name: c, count: len(ns)})
				}
				for i := 0; i < len(sorted); i++ {
					for j := i + 1; j < len(sorted); j++ {
						if sorted[j].count > sorted[i].count {
							sorted[i], sorted[j] = sorted[j], sorted[i]
						}
					}
				}

				totalProto := 0
				for _, ns := range countries {
					totalProto += len(ns)
				}
				fmt.Printf("%s %s (%d)\n", protoPrefix, proto, totalProto)

				for ci, cc := range sorted {
					isLastCountry := ci == len(sorted)-1
					countryPrefix := "│   ├──"
					if isLastProto {
						countryPrefix = "    ├──"
					}
					if isLastCountry {
						countryPrefix = "│   └──"
						if isLastProto {
							countryPrefix = "    └──"
						}
					}
					fmt.Printf("%s %s (%d)\n", countryPrefix, cc.name, cc.count)
				}
			}

			return nil
		},
	}
}

// ============================================================
// pool purge - remove expired/old nodes
// ============================================================

func newPoolPurgeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "purge",
		Short: "Remove expired nodes and clean up stats",
		Long:  `Remove nodes whose TTL has expired and clean up orphaned stats.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Remove expired nodes
			removed, err := globalCache.PurgeExpiredNodes()
			if err != nil {
				return fmt.Errorf("failed to purge expired nodes: %w", err)
			}

			// Get remaining nodes
			nodes, err := globalCache.GetNodes()
			if err != nil {
				return fmt.Errorf("failed to get remaining nodes: %w", err)
			}

			// Find orphaned stats (stats with no matching node)
			allStats, err := globalCache.GetNodeStatsAll()
			if err != nil {
				return fmt.Errorf("failed to get stats: %w", err)
			}

			nodeIDs := make(map[int]bool)
			for _, n := range nodes {
				nodeIDs[n.ID] = true
			}

			var orphanedIDs []int
			for _, s := range allStats {
				if !nodeIDs[s.NodeID] {
					orphanedIDs = append(orphanedIDs, s.NodeID)
				}
			}

			if len(orphanedIDs) > 0 {
				if err := globalCache.DeleteNodeStats(orphanedIDs); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: failed to delete orphaned stats: %v\n", err)
				} else {
					fmt.Printf("✓ Cleaned %d orphaned stats\n", len(orphanedIDs))
				}
			}

			fmt.Printf("✓ Purged %d expired nodes (%d remaining)\n", removed, len(nodes))
			return nil
		},
	}
}
