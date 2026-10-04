// Package cli - run command (one-shot URL check).
package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/internal/checker"
	"github.com/rotaze/rotaze/internal/fetcher"
	"github.com/rotaze/rotaze/internal/rotator"
	"github.com/rotaze/rotaze/pkg/types"
)

func newRunCmd() *cobra.Command {
	var (
		url       string
		country   string
		checkIP   bool
		timeout   int
		cached    bool
		attempts  int
		protocol  string
		ovpnFiles []string
	)

	cmd := &cobra.Command{
		Use:   "run",
		Short: "One-shot URL check via proxy",
		Long: `Check if a target URL is accessible through the proxy.
Fetches nodes, starts the proxy, makes one request, and exits with a status code.

Exit codes:
  0 - URL accessible (and country verified if --check-ip)
  1 - URL unreachable or country mismatch
  2 - Configuration error
  3 - No nodes available
  4 - Proxy start/switch failure`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if url == "" {
				return fmt.Errorf("--url is required")
			}

			cfg := globalConfig
			if country != "" {
				cfg.Checker.Country = country
			}
			if checkIP {
				cfg.Checker.CheckIP = true
			}
			if timeout > 0 {
				cfg.Checker.Timeout = timeout
			}
			ctx := cmd.Context()
			var nodes []*types.Node
			var err error
			if len(ovpnFiles) > 0 {
				// Use OpenVPN config(s) directly — no pool import needed.
				nodes, err = loadOVPNNodes(ovpnFiles)
				if err != nil {
					color.Red("✗ Failed to load OpenVPN config: %v\n", err)
					os.Exit(3)
				}
				fmt.Printf("Loaded %d OpenVPN node(s) from config\n", len(nodes))
				// No country filter: these configs were picked explicitly.
				// --check-ip --country VN still enforces the exit IP is VN.
			} else if cached {
				nodes, err = globalCache.GetNodes()
				if err != nil || len(nodes) == 0 {
					color.Red("✗ No cached nodes. Run without --cached first, or: rotaze pool refresh\n")
					os.Exit(3)
				}
				fmt.Printf("Using %d cached nodes\n", len(nodes))
				if cfg.Checker.Country != "" {
					filtered := filterNodesByCountry(nodes, cfg.Checker.Country)
					verbosef("country filter %q: %d of %d cached nodes match", cfg.Checker.Country, len(filtered), len(nodes))
					nodes = filtered
				}
			} else {
				p := fetcher.NewPool(globalCache)
				nodes, err = p.FetchNodes(ctx, cfg.Checker.Country)
				if err != nil {
					color.Red("✗ Failed to fetch nodes: %v\n", err)
					os.Exit(3)
				}
				fmt.Printf("Fetched %d nodes\n", len(nodes))
			}

			// Protocol filter applies to every branch: fetched, cached, and
			// explicit .ovpn configs (whose nodes already carry Protocol=ovpn).
			// With --ovpn the configs were picked explicitly, so a mismatched
			// --protocol simply yields no candidates below.
			if protocol != "" {
				filtered := filterNodesByProtocol(nodes, protocol)
				verbosef("protocol filter %q: %d of %d nodes match", protocol, len(filtered), len(nodes))
				nodes = filtered
			}

			// Step 2: Order usable candidates — previously selected node first,
			// then by protocol/method preference, all from THIS pool so the
			// --country filter actually applies to what gets used.
			preferredID, _ := globalCache.GetCurrentNode()
			candidates := orderedCandidates(nodes, preferredID)
			verbosef("usable candidates: %d of %d nodes (preferred node ID %d)", len(candidates), len(nodes), preferredID)
			if verbose {
				for i, c := range candidates {
					verbosef("  candidate %d/%d: %s", i+1, len(candidates), nodeDebugString(c))
				}
			}
			if len(candidates) == 0 {
				color.Red("✗ No usable nodes available (all missing credentials)\n")
				os.Exit(3)
			}

			r := rotator.New(globalLogger, &rotator.Config{
				ListenAddr: cfg.Proxy.ListenAddr,
				ListenPort: cfg.Proxy.ListenPort,
				DataDir:    getDataDir(cfg),
			})
			defer r.Close()

			// Step 3: Try up to --attempts distinct nodes (0 = all). A dead
			// node should not fail the whole run — rotate to the next candidate.
			maxTries := attempts
			if maxTries <= 0 || maxTries > len(candidates) {
				maxTries = len(candidates)
			}
			var tried []string

			for i := 0; i < maxTries; i++ {
				node := candidates[i]
				fmt.Printf("Using node: %s:%d (%s, %s)\n", node.Server, node.Port, node.Protocol, node.Country)
				verbosef("attempt %d/%d: %s", i+1, maxTries, nodeDebugString(node))
				if err := globalCache.SetCurrentNode(node.ID); err != nil {
					return fmt.Errorf("failed to set current node: %w", err)
				}

				// (Re)start sing-box for this node.
				if i == 0 {
					if err := r.Start(ctx, node); err != nil {
						color.Red("✗ Failed to start proxy: %v\n", err)
						os.Exit(4)
					}
				} else {
					verbosef("node failed, rotating to next candidate")
					if err := r.SwitchOutbound(ctx, node); err != nil {
						color.Red("✗ Failed to switch proxy: %v\n", err)
						os.Exit(4)
					}
				}
				fmt.Printf("Proxy listening on %s\n", r.GetListenAddr())

				// Wait for proxy to be ready
				time.Sleep(2 * time.Second)

				// Step 3.5: Check URL via proxy
				fmt.Printf("Checking URL: %s\n", url)
				timeoutDur := time.Duration(cfg.Checker.Timeout) * time.Second
				result := checker.CheckWithRetryAndProxy(ctx, url, timeoutDur, cfg.Checker.MaxRetries, r.GetListenAddr(), func(a checker.CheckAttempt) {
					if verbose {
						if a.Err != nil {
							verbosef("  check attempt %d/%d: status=%d err=%v [%s]", a.Attempt, cfg.Checker.MaxRetries+1, a.StatusCode, a.Err, a.Latency.Round(time.Millisecond))
						} else {
							verbosef("  check attempt %d/%d: status=%d OK [%s]", a.Attempt, cfg.Checker.MaxRetries+1, a.StatusCode, a.Latency.Round(time.Millisecond))
						}
					}
				})

				if result.Success {
					verbosef("node %s:%d OK (%d) after %s", node.Server, node.Port, result.StatusCode, result.LatencyStr())
					color.Green("✓ URL check: OK (%d) [%v]\n", result.StatusCode, result.LatencyStr())

					// Step 4: Verify exit IP (optional). A node that exits from
					// the wrong country is as useless as a dead one — record it
					// and rotate to the next candidate instead of aborting.
					if cfg.Checker.CheckIP {
						fmt.Printf("Verifying exit IP")
						if cfg.Checker.Country != "" {
							fmt.Printf(" (expected: %s)", cfg.Checker.Country)
						}
						fmt.Printf("...\n")
						info, err := checker.VerifyExitIP(ctx, r.GetListenAddr(), cfg.Checker.Country)
						if err != nil {
							color.Red("✗ Node %s:%d failed country check: %v\n", node.Server, node.Port, err)
							tried = append(tried, fmt.Sprintf("%s:%d", node.Server, node.Port))
							if i < maxTries-1 {
								verbosef("exit IP check failed, trying next candidate (%d left)", maxTries-i-1)
								continue
							}
							if cfg.Checker.Country != "" {
								color.Red("✗ No node verified exiting from %s (tried: %s)\n", cfg.Checker.Country, strings.Join(tried, ", "))
							} else {
								color.Red("✗ Exit IP verification failed through %d node(s): %s\n", len(tried), strings.Join(tried, ", "))
							}
							os.Exit(1)
							return nil
						}
						color.Green("✓ Exit IP: %s (%s) [%s]\n", info.IP, info.Country, info.Org)
					}

					color.Green("✓ Success\n")
					return nil
				}

				color.Red("✗ Node %s:%d failed: %v\n", node.Server, node.Port, result.Error)
				tried = append(tried, fmt.Sprintf("%s:%d", node.Server, node.Port))
				if i < maxTries-1 {
					verbosef("trying next candidate (%d left)", maxTries-i-1)
				}
			}

			color.Red("✗ URL check: FAILED through %d node(s): %s\n", len(tried), strings.Join(tried, ", "))
			os.Exit(1)
			return nil
		},
	}

	cmd.Flags().StringVar(&url, "url", "", "Target URL to check (required)")
	cmd.Flags().StringVar(&country, "country", "", "Exit IP country code (default: from config)")
	cmd.Flags().BoolVar(&checkIP, "check-ip", false, "Verify exit IP country")
	cmd.Flags().IntVar(&timeout, "timeout", 0, "Request timeout in seconds (default: 15)")
	cmd.Flags().BoolVar(&cached, "cached", false, "Use cached nodes only (skip fetch)")
	cmd.Flags().IntVar(&attempts, "attempts", 3, "Max distinct nodes to try (0 = try all candidates)")
	cmd.Flags().StringSliceVar(&ovpnFiles, "ovpn", nil, "OpenVPN config file(s) to use directly (repeatable, or comma-separated)")

	cmd.MarkFlagRequired("url")
	return cmd
}

// loadOVPNNodes reads one or more OpenVPN config files and parses them into
// nodes. Used by run and daemon to use .ovpn files directly without first
// importing them into the pool.
func loadOVPNNodes(files []string) ([]*types.Node, error) {
	var nodes []*types.Node
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", f, err)
		}
		node, err := fetcher.ParseOVPN(data)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", f, err)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// nodeDebugString returns a detailed one-line description of a node for
// --verbose output, showing the fields that actually matter per protocol
// (method for ss, credential presence for trojan/vless/vmess/hy2, TLS and
// transport when configured).
func nodeDebugString(n *types.Node) string {
	var parts []string
	switch n.Protocol {
	case "ss":
		parts = append(parts, fmt.Sprintf("method=%s", n.EncryptMethod))
		if n.Password != "" {
			parts = append(parts, "password=***")
		}
	case "trojan", "hy2":
		if n.Password != "" {
			parts = append(parts, "password=***")
		} else {
			parts = append(parts, "password=missing")
		}
	case "vless", "vmess":
		if n.UUID != "" {
			parts = append(parts, "uuid=***")
		} else {
			parts = append(parts, "uuid=missing")
		}
	case "http":
		if n.Password != "" {
			parts = append(parts, "auth=***")
		}
	}
	if n.TLSEnabled {
		parts = append(parts, "tls=yes")
		if n.TLSSNI != "" {
			parts = append(parts, fmt.Sprintf("sni=%s", n.TLSSNI))
		}
	}
	if n.TransportType != "" && n.TransportType != "tcp" {
		parts = append(parts, fmt.Sprintf("transport=%s", n.TransportType))
		if n.TransportPath != "" {
			parts = append(parts, fmt.Sprintf("path=%s", n.TransportPath))
		}
	}
	return fmt.Sprintf("tag=%s server=%s:%d protocol=%s country=%s %s",
		n.Tag, n.Server, n.Port, n.Protocol, n.Country, strings.Join(parts, " "))
}

// filterNodesByCountry filters cached nodes to the requested country code,
// dropping nodes with no country data (they cannot be verified). "all" passes through.
func filterNodesByCountry(nodes []*types.Node, country string) []*types.Node {
	if country == "" || strings.EqualFold(country, "all") {
		return nodes
	}
	var out []*types.Node
	for _, n := range nodes {
		if n.Country != "" && strings.EqualFold(n.Country, country) {
			out = append(out, n)
		}
	}
	return out
}

// filterNodesByProtocol filters nodes to the requested protocol
// (vless, ss, trojan, vmess, hy2, socks, ovpn). Empty or "all" passes through.
func filterNodesByProtocol(nodes []*types.Node, protocol string) []*types.Node {
	if protocol == "" || strings.EqualFold(protocol, "all") {
		return nodes
	}
	// Accept the common user spelling "--protocol openvpn".
	if strings.EqualFold(protocol, "openvpn") {
		protocol = "ovpn"
	}
	var out []*types.Node
	for _, n := range nodes {
		if strings.EqualFold(n.Protocol, protocol) {
			out = append(out, n)
		}
	}
	return out
}

// orderedCandidates returns viable nodes from the pool ordered by preference:
// the currently-selected node first, then SS with a supported cipher, VLESS,
// Trojan, Hy2, VMess, then any remaining node with valid credentials.
func orderedCandidates(nodes []*types.Node, preferredID int) []*types.Node {
	viable := make([]*types.Node, 0, len(nodes))
	for _, n := range nodes {
		if nodeWithValidCredentials(n) != nil {
			viable = append(viable, n)
		}
	}
	if len(viable) == 0 {
		return nil
	}

	seenID := make(map[int]bool)
	seenPtr := make(map[*types.Node]bool)
	var out []*types.Node
	pick := func(match func(*types.Node) bool) {
		for _, n := range viable {
			if !match(n) {
				continue
			}
			// Only mark nodes that were actually selected, so later
			// preference passes can still pick the rest of the pool.
			if n.ID != 0 {
				if seenID[n.ID] {
					continue
				}
				seenID[n.ID] = true
			} else {
				if seenPtr[n] {
					continue
				}
				seenPtr[n] = true
			}
			out = append(out, n)
		}
	}

	if preferredID != 0 {
		pick(func(n *types.Node) bool { return n.ID == preferredID })
	}
	pick(func(n *types.Node) bool { return n.Protocol == "socks" })
	pick(func(n *types.Node) bool { return n.Protocol == "ss" && supportedMethods[n.EncryptMethod] })
	pick(func(n *types.Node) bool { return n.Protocol == "vless" && n.UUID != "" })
	pick(func(n *types.Node) bool { return n.Protocol == "trojan" && n.Password != "" })
	pick(func(n *types.Node) bool { return n.Protocol == "hy2" && n.Password != "" })
	pick(func(n *types.Node) bool { return n.Protocol == "vmess" && n.UUID != "" })
	pick(func(n *types.Node) bool { return n.Protocol == "ovpn" && nodeWithValidCredentials(n) != nil })
	pick(func(n *types.Node) bool { return true })
	return out
}

// pickSelectedNode returns the currently selected node from the DB (set by 'use' command).
func pickSelectedNode(c *cache.Cache) *types.Node {
	currentNodeID, err := c.GetCurrentNode()
	if err != nil || currentNodeID == 0 {
		return nil
	}
	node, err := c.GetNodeByID(currentNodeID)
	if err != nil {
		return nil
	}
	// Use any node with valid credentials for its protocol
	return nodeWithValidCredentials(node)
}

// pickBestNode selects the best node from the pool.
func pickBestNode(nodes []*types.Node) *types.Node {
	if len(nodes) == 0 {
		return nil
	}
	// Prefer SS with supported methods
	for _, n := range nodes {
		if n.Protocol == "ss" && n.Password != "" && supportedMethods[n.EncryptMethod] {
			return n
		}
	}
	// Then VLESS with UUID
	for _, n := range nodes {
		if n.Protocol == "vless" && n.UUID != "" {
			return n
		}
	}
	// Then Trojan with password
	for _, n := range nodes {
		if n.Protocol == "trojan" && n.Password != "" {
			return n
		}
	}
	// Then Hy2 with password
	for _, n := range nodes {
		if n.Protocol == "hy2" && n.Password != "" {
			return n
		}
	}
	// Then any SS with password
	for _, n := range nodes {
		if n.Protocol == "ss" && n.Password != "" {
			return n
		}
	}
	// Fall back to first node with any credentials
	for _, n := range nodes {
		if nodeWithValidCredentials(n) != nil {
			return n
		}
	}
	return nodes[0]
}

// supportedMethods lists SS methods compatible with this sing-box version.
var supportedMethods = map[string]bool{
	"aes-128-gcm":             true,
	"aes-256-gcm":             true,
	"chacha20-ietf-poly1305":  true,
	"2022-blake3-aes-128-gcm": true,
	"2022-blake3-aes-256-gcm": true,
}

// pickWorkingNode picks a known-working node from the cache.
// When country is set (and not "all"), only nodes matching that country are considered.
func pickWorkingNode(c *cache.Cache, country string) *types.Node {
	nodes, err := c.GetNodes()
	if err != nil || len(nodes) == 0 {
		return nil
	}
	nodes = filterNodesByCountry(nodes, country)
	// Prefer SS nodes with supported encryption methods
	for _, n := range nodes {
		if n.Protocol == "ss" && n.Password != "" && supportedMethods[n.EncryptMethod] {
			return n
		}
	}
	// Then VLESS with UUID
	for _, n := range nodes {
		if n.Protocol == "vless" && n.UUID != "" {
			return n
		}
	}
	// Then Trojan with password
	for _, n := range nodes {
		if n.Protocol == "trojan" && n.Password != "" {
			return n
		}
	}
	// Then Hy2 with password
	for _, n := range nodes {
		if n.Protocol == "hy2" && n.Password != "" {
			return n
		}
	}
	// Fall back to any SS with password
	for _, n := range nodes {
		if n.Protocol == "ss" && n.Password != "" {
			return n
		}
	}
	return nil
}

// nodeWithValidCredentials returns the node if it has valid credentials for its protocol, nil otherwise.
func nodeWithValidCredentials(n *types.Node) *types.Node {
	switch n.Protocol {
	case "ss":
		if n.Password != "" && n.EncryptMethod != "" {
			return n
		}
	case "vless":
		if n.UUID != "" {
			return n
		}
	case "trojan":
		if n.Password != "" {
			return n
		}
	case "vmess":
		if n.UUID != "" {
			return n
		}
	case "hy2":
		if n.Password != "" {
			return n
		}
	case "http":
		// HTTP proxy always valid (no auth required for free proxies)
		return n
	case "socks":
		// SOCKS proxy always valid (no auth required for free proxies)
		return n
	case "ovpn":
		// OpenVPN needs the full client certificate material.
		if n.CACert != "" && n.ClientCert != "" && n.ClientKey != "" && n.Password != "" {
			return n
		}
	}
	return nil
}
