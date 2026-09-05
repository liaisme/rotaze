// Package cli - lock command (prevent auto-rotation).
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newLockCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lock",
		Short: "Lock/unlock to prevent auto-rotation",
		Long:  `Lock to the current node to prevent automatic rotation, or unlock to re-enable it.`,
	}

	cmd.AddCommand(newLockOnCmd())
	cmd.AddCommand(newLockOffCmd())
	cmd.AddCommand(newLockShowCmd())

	return cmd
}

func newLockOnCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "on",
		Short: "Lock to current node (disable auto-rotation)",
		RunE: func(cmd *cobra.Command, args []string) error {
			currentNodeID, err := globalCache.GetCurrentNode()
			if err != nil || currentNodeID == 0 {
				return fmt.Errorf("no current node to lock to")
			}

			if err := globalCache.LockNode(currentNodeID); err != nil {
				return fmt.Errorf("failed to lock node: %w", err)
			}

			node, err := globalCache.GetNodeByID(currentNodeID)
			if err != nil {
				fmt.Printf("✓ Locked to node %d (auto-rotation disabled)\n", currentNodeID)
			} else {
				fmt.Printf("✓ Locked to %s:%d %s (auto-rotation disabled)\n", node.Server, node.Port, node.Country)
			}
			return nil
		},
	}
}

func newLockOffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "off",
		Short: "Unlock (enable auto-rotation)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := globalCache.UnlockNode(); err != nil {
				return fmt.Errorf("failed to unlock: %w", err)
			}
			fmt.Println("✓ Unlocked (auto-rotation enabled)")
			return nil
		},
	}
}

func newLockShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show lock status",
		RunE: func(cmd *cobra.Command, args []string) error {
			locked, err := globalCache.IsNodeLocked()
			if err != nil {
				return fmt.Errorf("failed to check lock status: %w", err)
			}

			if locked {
				nodeID, _ := globalCache.GetLockedNode()
				node, err := globalCache.GetNodeByID(nodeID)
				if err != nil {
					fmt.Printf("Locked to: node %d\n", nodeID)
				} else {
					fmt.Printf("Locked to: %s:%d (%s)\n", node.Server, node.Port, node.Country)
				}
				fmt.Println("Auto-rotation: DISABLED")
			} else {
				fmt.Println("Lock status: UNLOCKED")
				fmt.Println("Auto-rotation: ENABLED")
			}
			return nil
		},
	}
}
