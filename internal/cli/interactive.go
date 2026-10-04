// Package cli - interactive command (REPL mode).
package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/internal/checker"
	"github.com/rotaze/rotaze/pkg/types"
	"github.com/spf13/cobra"
)

func newInteractiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "interactive",
		Short: "Interactive REPL mode",
		Long: `Start an interactive shell for manual node control and exploration.

Commands:
  list              Show available nodes
  use <id>          Switch to node
  lock on|off       Lock/unlock to current node
  status            Show current state
  test-url <url>    Test URL via current node
  stats             Pool statistics
  rotate            Force rotation to next node
  help              Show all commands
  exit              Quit`,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Rotaze Interactive Mode")
			fmt.Println("Type 'help' for available commands, 'exit' to quit")
			fmt.Println()

			scanner := bufio.NewScanner(os.Stdin)
			for {
				fmt.Print("rotaze> ")
				if !scanner.Scan() {
					break
				}

				input := strings.TrimSpace(scanner.Text())
				if input == "" {
					continue
				}

				parts := strings.Fields(input)
				command := strings.ToLower(parts[0])

				switch command {
				case "exit", "quit":
					fmt.Println("✓ Goodbye!")
					return nil

				case "help":
					printInteractiveHelp()

				case "list":
					handleListCommand(globalCache, parts[1:])

				case "use":
					if len(parts) < 2 {
						fmt.Println("Usage: use <node-id>")
						continue
					}
					handleUseCommand(globalCache, parts[1])

				case "lock":
					if len(parts) < 2 {
						fmt.Println("Usage: lock on|off")
						continue
					}
					handleLockCommand(globalCache, parts[1])

				case "status":
					handleStatusCommand(globalCache)

				case "stats":
					handleStatsCommand(globalCache)

				case "test-url":
					if len(parts) < 2 {
						fmt.Println("Usage: test-url <url>")
						continue
					}
					handleTestURL(parts[1])

				case "rotate":
					handleRotate(globalCache)

				default:
					fmt.Printf("Unknown command: %s (type 'help' for commands)\n", command)
				}
			}

			return nil
		},
	}
}

func printInteractiveHelp() {
	fmt.Println("Available commands:")
	fmt.Println("  list                    Show available nodes")
	fmt.Println("  list --country VN       Show nodes filtered by country")
	fmt.Println("  list --limit 20         Show at most 20 nodes")
	fmt.Println("  use <id>                Switch to node by ID")
	fmt.Println("  lock on|off             Lock/unlock to current node")
	fmt.Println("  status                  Show current state")
	fmt.Println("  stats                   Pool statistics")
	fmt.Println("  test-url <url>          Test URL via current node")
	fmt.Println("  rotate                  Force rotation to next node")
	fmt.Println("  help                    Show this help")
	fmt.Println("  exit                    Quit")
}

// handleListCommand prints pool nodes, optionally filtered by --country and
// capped by --limit (0 = all).
func handleListCommand(c *cache.Cache, args []string) {
	country := ""
	limit := 0
	for i := 0; i < len(args); {
		arg := args[i]
		switch {
		case arg == "--country" && i+1 < len(args):
			country = args[i+1]
			i += 2
		case arg == "--limit" && i+1 < len(args):
			fmt.Sscanf(args[i+1], "%d", &limit)
			i += 2
		default:
			i++
		}
	}

	nodes, err := c.GetNodes()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	if country != "" && !strings.EqualFold(country, "all") {
		filtered := make([]*types.Node, 0, len(nodes))
		for _, n := range nodes {
			if strings.EqualFold(n.Country, country) {
				filtered = append(filtered, n)
			}
		}
		nodes = filtered
	}
	if len(nodes) == 0 {
		if country != "" {
			fmt.Printf("No nodes matching country %q\n", country)
			return
		}
		fmt.Println("No nodes in pool. Run: rotaze pool refresh")
		return
	}

	total := len(nodes)
	shown := nodes
	if limit > 0 && limit < len(nodes) {
		shown = nodes[:limit]
	}

	fmt.Printf("%-4s %-20s %-6s %-10s %-6s\n", "ID", "Server", "Port", "Protocol", "Country")
	fmt.Println(strings.Repeat("─", 60))
	for _, n := range shown {
		fmt.Printf("%-4d %-20s %-6d %-10s %-6s\n", n.ID, n.Server, n.Port, n.Protocol, n.Country)
	}
	if len(shown) < total {
		fmt.Printf("\nShowing %d of %d nodes\n", len(shown), total)
	} else {
		fmt.Printf("\nTotal: %d nodes\n", total)
	}
}

func handleUseCommand(c *cache.Cache, idStr string) {
	var id int
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil {
		fmt.Printf("Invalid node ID: %s\n", idStr)
		return
	}

	node, err := c.GetNodeByID(id)
	if err != nil {
		fmt.Printf("Node not found: %d\n", id)
		return
	}

	previousNodeID, _ := c.GetCurrentNode()
	if err := c.SetCurrentNode(id); err != nil {
		fmt.Printf("Error updating state: %v\n", err)
		return
	}
	c.RecordRotation(previousNodeID, id, "manual", "success")

	fmt.Printf("✓ Switched to node %d: %s:%d (%s)\n", node.ID, node.Server, node.Port, node.Country)
}

func handleLockCommand(c *cache.Cache, action string) {
	switch strings.ToLower(action) {
	case "on":
		currentNodeID, _ := c.GetCurrentNode()
		if currentNodeID == 0 {
			fmt.Println("No current node to lock to")
			return
		}
		if err := c.LockNode(currentNodeID); err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		fmt.Println("✓ Locked (auto-rotation disabled)")

	case "off":
		if err := c.UnlockNode(); err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		fmt.Println("✓ Unlocked (auto-rotation enabled)")

	default:
		fmt.Println("Usage: lock on|off")
	}
}

func handleStatusCommand(c *cache.Cache) {
	currentNodeID, _ := c.GetCurrentNode()
	locked, _ := c.IsNodeLocked()
	totalNodes, _ := c.CountNodes()

	if currentNodeID > 0 {
		node, err := c.GetNodeByID(currentNodeID)
		if err != nil {
			fmt.Printf("Current: node %d\n", currentNodeID)
		} else {
			fmt.Printf("Current: %s:%d (%s)\n", node.Server, node.Port, node.Country)
		}
	} else {
		fmt.Println("Current: (none)")
	}

	if locked {
		fmt.Println("Lock: LOCKED")
	} else {
		fmt.Println("Lock: UNLOCKED")
	}
	fmt.Printf("Pool: %d nodes\n", totalNodes)
}

func handleStatsCommand(c *cache.Cache) {
	nodes, err := c.GetNodes()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

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
}

func handleTestURL(url string) {
	fmt.Printf("Testing %s...\n", url)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	socks5Addr := "127.0.0.1:1080"
	if globalConfig != nil {
		socks5Addr = fmt.Sprintf("%s:%d", globalConfig.Proxy.ListenAddr, globalConfig.Proxy.ListenPort)
	}

	status, latency, err := checker.CheckURLOnceWithProxy(ctx, url, 15*time.Second, socks5Addr)
	if err != nil {
		// Try direct connection as fallback
		status, latency, err = checker.CheckURLOnce(ctx, url, 15*time.Second)
		if err != nil {
			color.Red("✗ Failed: %v\n", err)
			return
		}
		fmt.Println("  (direct connection - proxy not running)")
	}

	if status >= 200 && status < 400 {
		color.Green("✓ OK (HTTP %d) [%v]\n", status, latency.Round(time.Millisecond))
	} else {
		color.Yellow("⚠ HTTP %d [%v]\n", status, latency.Round(time.Millisecond))
	}
}

func handleRotate(c *cache.Cache) {
	currentNodeID, _ := c.GetCurrentNode()

	locked, _ := c.IsNodeLocked()
	if locked {
		fmt.Println("✗ Node is locked. Run 'lock off' first.")
		return
	}

	// Get all nodes
	nodes, err := c.GetNodes()
	if err != nil || len(nodes) == 0 {
		fmt.Println("✗ No nodes available")
		return
	}

	// Pick next node (different from current)
	var nextNode *types.Node
	for _, n := range nodes {
		if n.ID != currentNodeID {
			nextNode = n
			break
		}
	}

	if nextNode == nil {
		fmt.Println("✗ No alternative nodes available")
		return
	}

	// Update state
	if err := c.SetCurrentNode(nextNode.ID); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	c.RecordRotation(currentNodeID, nextNode.ID, "manual", "success")

	fmt.Printf("✓ Rotated to: %s:%d (%s, %s)\n", nextNode.Server, nextNode.Port, nextNode.Protocol, nextNode.Country)
}
