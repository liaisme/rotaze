// Package cli - use command (manual node selection).
package cli

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func newUseCmd() *cobra.Command {
	var (
		server string
		tag    string
		random bool
		best   bool
	)

	cmd := &cobra.Command{
		Use:   "use [ID]",
		Short: "Switch to a specific tunnel node",
		Long: `Switch to a specific node immediately.
You can specify a node by ID, server IP, tag, or use --random/--best.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var nodeID int

			switch {
			case len(args) > 0:
				id, err := strconv.Atoi(args[0])
				if err != nil {
					return fmt.Errorf("invalid node ID: %s", args[0])
				}
				nodeID = id

			case server != "":
				// Find node by server IP
				nodes, err := globalCache.GetNodes()
				if err != nil {
					return fmt.Errorf("failed to get nodes: %w", err)
				}
				found := false
				for _, n := range nodes {
					if strings.Contains(n.Server, server) {
						nodeID = n.ID
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("no node found with server matching: %s", server)
				}

			case tag != "":
				// Find node by tag
				nodes, err := globalCache.GetNodes()
				if err != nil {
					return fmt.Errorf("failed to get nodes: %w", err)
				}
				found := false
				for _, n := range nodes {
					if strings.Contains(n.Tag, tag) {
						nodeID = n.ID
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("no node found with tag matching: %s", tag)
				}

			case random:
				// Pick random node
				nodes, err := globalCache.GetNodes()
				if err != nil || len(nodes) == 0 {
					return fmt.Errorf("no nodes available")
				}
				nodeID = nodes[rand.Intn(len(nodes))].ID

			case best:
				// Pick best node (prefer SS with supported methods)
				nodes, err := globalCache.GetNodes()
				if err != nil || len(nodes) == 0 {
					return fmt.Errorf("no nodes available")
				}
				bestNode := pickBestNode(nodes)
				if bestNode == nil {
					return fmt.Errorf("no good nodes available")
				}
				nodeID = bestNode.ID

			default:
				return fmt.Errorf("specify a node ID, --server, --tag, --random, or --best")
			}

			// Lookup node
			node, err := globalCache.GetNodeByID(nodeID)
			if err != nil {
				return fmt.Errorf("node not found: %w", err)
			}

			// Get previous node for rotation record
			previousNodeID, _ := globalCache.GetCurrentNode()

			// Update current node in DB
			if err := globalCache.SetCurrentNode(nodeID); err != nil {
				return fmt.Errorf("failed to update current node: %w", err)
			}

			// Log rotation
			if err := globalCache.RecordRotation(previousNodeID, nodeID, "manual", "success"); err != nil {
				globalLogger.Warn("failed to record rotation", zap.Error(err))
			}

			fmt.Printf("✓ Switched to node %d: %s:%d (%s, %s)\n", node.ID, node.Server, node.Port, node.Protocol, node.Country)
			return nil
		},
	}

	cmd.Flags().StringVar(&server, "server", "", "Select by server IP (partial match)")
	cmd.Flags().StringVar(&tag, "tag", "", "Select by sing-box tag (partial match)")
	cmd.Flags().BoolVar(&random, "random", false, "Pick random node")
	cmd.Flags().BoolVar(&best, "best", false, "Pick best performer")

	return cmd
}
