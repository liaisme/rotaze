// Package cli - watch command (continuous monitoring).
package cli

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/internal/checker"
	"github.com/rotaze/rotaze/internal/config"
	"github.com/rotaze/rotaze/internal/fetcher"
	"github.com/rotaze/rotaze/internal/health"
	"github.com/rotaze/rotaze/internal/metrics"
	"github.com/rotaze/rotaze/internal/rotator"
	"github.com/rotaze/rotaze/pkg/types"
)

func newWatchCmd() *cobra.Command {
	var (
		url         string
		interval    int
		timeout     int
		country     string
		checkIP     bool
		attempts    int
		healthPort  int
		metricsAddr string
		cached      bool
	)

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Continuous monitoring with auto-rotation",
		Long: `Run in foreground, continuously checking a target URL via the SOCKS5 proxy.
Automatically rotates to a new tunnel when the URL becomes unreachable.

Press Ctrl+C to stop.`,
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
			if interval > 0 {
				cfg.Checker.Interval = interval
			}
			if attempts > 0 {
				cfg.Checker.MaxAttempts = attempts
			}

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			var chk *checker.Checker

			// Set up signal handler
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
			go func() {
				for sig := range sigChan {
					switch sig {
					case syscall.SIGHUP:
						fmt.Println("\nReceived SIGHUP, reloading config...")
						newCfg, err := config.LoadConfig(configPath)
						if err != nil {
							fmt.Fprintf(os.Stderr, "Config reload failed: %v\n", err)
							continue
						}
						cfg = newCfg
						if chk != nil {
							// Apply hot-reloadable fields via thread-safe setters.
							// (Direct struct writes here would race with the main
							// goroutine reading cfg during rotation setup.)
							chk.UpdateInterval(time.Duration(newCfg.Checker.Interval) * time.Second)
							chk.UpdateCountry(newCfg.Checker.Country)
							chk.UpdateURL(url)
						}
						fmt.Printf("Config reloaded (interval=%ds, country=%s)\n", newCfg.Checker.Interval, newCfg.Checker.Country)
					default:
						fmt.Println("\nReceived signal, shutting down...")
						cancel()
						return
					}
				}
			}()

			// Step 1: Fetch nodes (or skip when --cached)
			var nodes []*types.Node
			var err error
			p := fetcher.NewPool(globalCache)
			if cached {
				fmt.Println("Using cached nodes (--cached)")
				nodes, err = globalCache.GetNodes()
				if err != nil {
					return fmt.Errorf("failed to load cached nodes: %w", err)
				}
				fmt.Printf("Loaded %d cached nodes\n", len(nodes))
			} else {
				fmt.Println("Fetching nodes...")
				nodes, err = p.FetchNodes(ctx, cfg.Checker.Country)
				if err != nil {
					return fmt.Errorf("failed to fetch nodes: %w", err)
				}
				fmt.Printf("Fetched %d nodes\n", len(nodes))
			}

			// Auto-refresh pool every hour in background
			go func() {
				refreshTicker := time.NewTicker(1 * time.Hour)
				defer refreshTicker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-refreshTicker.C:
						fmt.Println("Auto-refreshing node pool...")
						_, err := p.FetchNodes(ctx, cfg.Checker.Country)
						if err != nil {
							fmt.Fprintf(os.Stderr, "Auto-refresh failed: %v\n", err)
						} else {
							fmt.Println("Pool refreshed successfully")
						}
					}
				}
			}()

			// Step 2: Pick first node and start rotator
			node := pickWorkingNode(globalCache)
			if node == nil {
				node = pickBestNode(nodes)
			}
			if node == nil {
				color.Red("✗ No usable nodes available\n")
				os.Exit(3)
			}

			// Start sing-box
			r := rotator.New(globalLogger, &rotator.Config{
				ListenAddr: cfg.Proxy.ListenAddr,
				ListenPort: cfg.Proxy.ListenPort,
				DataDir:    getDataDir(cfg),
			})

			if err := r.Start(ctx, node); err != nil {
				color.Red("✗ Failed to start sing-box: %v\n", err)
				os.Exit(4)
			}
			defer r.Close()

			// Start health server
			if healthPort > 0 {
				hs := health.NewWithPort(healthPort)
				if err := hs.Start(ctx); err != nil {
					fmt.Fprintf(os.Stderr, "Health server failed: %v\n", err)
				} else {
					defer hs.Stop(ctx)
					hs.SetNodeInfo(map[string]interface{}{
						"server":  node.Server,
						"port":    node.Port,
						"country": node.Country,
						"proxy":   r.GetListenAddr(),
					})
					fmt.Printf("Health check: http://127.0.0.1:%d/health\n", hs.GetPort())
				}
			}

			// Start metrics server
			var mc *metrics.Collector
			if metricsAddr != "" {
				mc = metrics.New()
				mc.SetProxyRunning(true)
				mc.SetNodesFetched(int64(len(nodes)))
				mc.SetCheckInterval(cfg.Checker.Interval)
				mc.SetCurrentNode(node.ID, node.Server, node.Country)
				ms, err := metrics.StartMetricsServer(metricsAddr, mc)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Metrics server failed: %v\n", err)
				} else {
					defer ms.Close()
					fmt.Printf("Metrics: http://%s/metrics\n", metricsAddr)
				}
			}

			// Set current node
			if err := globalCache.SetCurrentNode(node.ID); err != nil {
				return fmt.Errorf("failed to set current node: %w", err)
			}

			fmt.Printf("Current node: %s:%d (%s, %s)\n", node.Server, node.Port, node.Protocol, node.Country)
			fmt.Printf("SOCKS5 proxy: %s\n", r.GetListenAddr())

			// Step 3: Set up rotation callback
			rotateFn := func(ctx context.Context, reason string) error {
				// Pick next node
				newNode, err := pickNextNode(globalCache, node.ID)
				if err != nil {
					return fmt.Errorf("no next node available: %w", err)
				}

				// Switch outbound
				if err := r.SwitchOutbound(ctx, newNode); err != nil {
					return fmt.Errorf("failed to switch outbound: %w", err)
				}

				// Update state
				if err := globalCache.SetCurrentNode(newNode.ID); err != nil {
					return fmt.Errorf("failed to update current node: %w", err)
				}

				// Record rotation
				if err := globalCache.RecordRotation(node.ID, newNode.ID, reason, "success"); err != nil {
					globalLogger.Warn("failed to record rotation", zap.Error(err))
				}

				// Update metrics
				if mc != nil {
					mc.IncRotations()
					mc.SetCurrentNode(newNode.ID, newNode.Server, newNode.Country)
				}

				fmt.Printf("Rotated to: %s:%d (%s, %s)\n", newNode.Server, newNode.Port, newNode.Protocol, newNode.Country)
				node = newNode
				return nil
			}

			// Step 4: Start checker
			chk = checker.New(globalCache, globalLogger, &checker.Config{
				URL:         url,
				Interval:    time.Duration(cfg.Checker.Interval) * time.Second,
				Timeout:     time.Duration(cfg.Checker.Timeout) * time.Second,
				MaxRetries:  cfg.Checker.MaxRetries,
				MaxAttempts: cfg.Checker.MaxAttempts,
				CheckIP:     cfg.Checker.CheckIP,
				Country:     cfg.Checker.Country,
			})
			chk.OnRotate(rotateFn)

			fmt.Printf("Monitoring %s every %ds\n", url, cfg.Checker.Interval)
			fmt.Println("Press Ctrl+C to stop")

			// Run checker (blocks until context cancelled)
			if err := chk.Start(ctx); err != nil {
				return fmt.Errorf("checker error: %w", err)
			}

			// Shutdown
			fmt.Println("Shutting down...")
			r.Close()
			fmt.Println("✓ Shutdown complete")
			return nil
		},
	}

	cmd.Flags().StringVar(&url, "url", "", "Target URL to monitor (required)")
	cmd.Flags().IntVar(&interval, "interval", 0, "Check interval in seconds (default: 30)")
	cmd.Flags().IntVar(&timeout, "timeout", 0, "Request timeout in seconds (default: 15)")
	cmd.Flags().StringVar(&country, "country", "", "Exit IP country code (default: from config)")
	cmd.Flags().BoolVar(&checkIP, "check-ip", false, "Verify exit IP country")
	cmd.Flags().IntVar(&attempts, "attempts", 0, "Consecutive failures before rotate (default: 3)")
	cmd.Flags().IntVar(&healthPort, "health-port", 0, "Health check HTTP port (0 = disabled)")
	cmd.Flags().StringVar(&metricsAddr, "metrics", "", "Prometheus metrics listen address (e.g., :9090)")
	cmd.Flags().BoolVar(&cached, "cached", false, "Use cached nodes only (skip fetch)")
	cmd.MarkFlagRequired("url")

	return cmd
}

// pickNextNode selects the next node with smarter selection.
// It skips bad nodes (high failure rate), prefers SS with supported methods,
// and adds randomization to avoid getting stuck.
func pickNextNode(c *cache.Cache, currentID int) (*types.Node, error) {
	nodes, err := c.GetNodes()
	if err != nil {
		return nil, err
	}

	if len(nodes) == 0 {
		return nil, fmt.Errorf("no nodes available")
	}

	// Build candidate list (skip current node and bad nodes)
	var candidates []*types.Node
	for _, n := range nodes {
		if n.ID == currentID {
			continue
		}

		// Check stats for this node
		stats, err := c.GetNodeStats(n.ID)
		if err == nil && stats.TotalChecks > 5 {
			// Skip nodes with less than 25% success rate
			if stats.SuccessRate < 0.25 {
				continue
			}
		}

		// Rank candidates by protocol priority
		if n.Protocol == "ss" && n.Password != "" && supportedMethods[n.EncryptMethod] {
			// Highest priority: SS with supported methods
			candidates = append([]*types.Node{n}, candidates...)
		} else if n.Protocol == "vless" && n.UUID != "" {
			// Second priority: VLESS with UUID
			if len(candidates) > 0 && candidates[0].Protocol == "ss" {
				candidates = append([]*types.Node{candidates[0]}, append([]*types.Node{n}, candidates[1:]...)...)
			} else {
				candidates = append([]*types.Node{n}, candidates...)
			}
		} else if (n.Protocol == "trojan" && n.Password != "") || (n.Protocol == "hy2" && n.Password != "") || (n.Protocol == "ss" && n.Password != "") {
			candidates = append(candidates, n)
		} else {
			candidates = append(candidates, n)
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no viable nodes available")
	}

	// Pick from candidates with some randomization
	// If we have multiple good SS nodes, pick randomly from top 10
	goodSS := make([]*types.Node, 0)
	for _, n := range candidates {
		if n.Protocol == "ss" && n.Password != "" && supportedMethods[n.EncryptMethod] {
			goodSS = append(goodSS, n)
		}
	}

	if len(goodSS) > 1 {
		// Random pick from good SS nodes (up to 10)
		limit := 10
		if len(goodSS) < limit {
			limit = len(goodSS)
		}
		return goodSS[rand.Intn(limit)], nil
	}

	if len(candidates) > 0 {
		return candidates[0], nil
	}

	return nil, fmt.Errorf("no viable nodes available")
}
