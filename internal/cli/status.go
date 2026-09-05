// Package cli - status command (show current state).
package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current state",
		Long:  `Display current node, pool stats, lock status, and recent rotations.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Current node
			currentNodeID, _ := globalCache.GetCurrentNode()
			locked, _ := globalCache.IsNodeLocked()

			if currentNodeID > 0 {
				node, err := globalCache.GetNodeByID(currentNodeID)
				if err != nil {
					fmt.Printf("Current Node:    (unknown - ID %d)\n", currentNodeID)
				} else {
					fmt.Printf("Current Node:    %s:%d (%s)\n", node.Server, node.Port, node.Country)
				}
			} else {
				fmt.Println("Current Node:    (none)")
			}

			// Lock status
			if locked {
				fmt.Println("Lock state:      LOCKED (auto-rotation disabled)")
			} else {
				fmt.Println("Lock state:      AUTO-ROTATION ENABLED")
			}

			// Pool stats
			totalNodes, _ := globalCache.CountNodes()
			fmt.Printf("\nPool Stats:\n")
			fmt.Printf("  Total nodes:   %d\n", totalNodes)

			// Recent rotations
			rotations, err := globalCache.GetRecentRotations(5)
			if err == nil && len(rotations) > 0 {
				fmt.Printf("\nRecent Rotations:\n")
				for _, r := range rotations {
					fmt.Printf("  %s → Node %d → Node %d (%s, %s)\n",
						r.Timestamp.Format("15:04"),
						r.FromNodeID,
						r.ToNodeID,
						r.Reason,
						r.Status)
				}
			}

			fmt.Printf("\nLast updated: %s\n", time.Now().Format("2006-01-02 15:04:05"))
			return nil
		},
	}
}
