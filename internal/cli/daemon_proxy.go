// New proxy commands for multi-outlet daemon management.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
)

func newDaemonProxyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Manage multiple proxy outlets on the running daemon",
	}
	cmd.AddCommand(newDaemonProxyAddCmd())
	cmd.AddCommand(newDaemonProxyListCmd())
	cmd.AddCommand(newDaemonProxyRmCmd())
	return cmd
}
func newDaemonProxyAddCmd() *cobra.Command {
	var (
		country  string
		protocol string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add a new proxy outlet for a given country",
		Long:  `Add a new SOCKS5/HTTP outlet to the running daemon. The port is auto-assigned starting from 1080, 1081, 1082, ...`,
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := readPIDFile()
			if err != nil {
				return fmt.Errorf("daemon not running")
			}
			if !isProcessRunning(pid) {
				removePIDFile()
				removeDaemonState()
				return fmt.Errorf("daemon not running")
			}
			st := readDaemonState()
			if st == nil {
				return fmt.Errorf("daemon state unavailable")
			}
			// The outlet list lives in its own file, written only by this
			// command. The daemon writes daemon.state, so sharing one file
			// would let the daemon's periodic state publish (which happens
			// while outlets are being started) overwrite a just-added entry.
			outlets := readOutletIntent()
			// Reserve the daemon's own primary port. It must come from the
			// config, not from daemon.state: the state's Proxy field is only
			// populated once the daemon has a live proxy, so during startup
			// it is empty and the primary port would look free.
			primary := globalConfig.Proxy.ListenPort
			if p := extractPort(st.Proxy); p > 0 {
				primary = p
			}
			outlet := nextOutlet(outlets, primary)
			outlet.Country = country
			outlet.Protocol = protocol
			outlet.Addr = fmt.Sprintf("127.0.0.1:%d", outlet.Port)

			outlets = append(outlets, outlet)
			if err := writeOutletIntent(outlets); err != nil {
				return fmt.Errorf("failed to record outlet: %w", err)
			}
			// Tell the daemon to apply the change.
			proc, err := os.FindProcess(pid)
			if err == nil {
				_ = proc.Signal(syscall.SIGHUP)
			}
			fmt.Printf("Added outlet #%d on %s (country=%s)\n", outlet.ID, outlet.Addr, outlet.Country)
			return nil
		},
	}
	cmd.Flags().StringVar(&country, "country", "all", "Country filter for this outlet")
	cmd.Flags().StringVar(&protocol, "protocol", "", "Protocol filter for this outlet (optional)")
	return cmd
}

// nextOutlet allocates the ID and port for a new outlet. Ports are assigned
// from the lowest free number at or above 1080, skipping ports held by
// existing outlets and by the daemon's own primary proxy. IDs are allocated
// above the highest existing one: the daemon keys live rotators by outlet
// ID, so reusing an id after `rm` would collapse two outlets into one.
func nextOutlet(outlets []daemonOutlet, primaryPort int) daemonOutlet {
	used := make(map[int]bool, len(outlets)+1)
	for _, o := range outlets {
		used[o.Port] = true
	}
	if primaryPort > 0 {
		used[primaryPort] = true
	}
	port := 1080
	for used[port] {
		port++
	}
	id := 1
	for _, o := range outlets {
		if o.ID >= id {
			id = o.ID + 1
		}
	}
	return daemonOutlet{ID: id, Port: port}
}

func extractPort(addr string) int {
	if i := strings.LastIndex(addr, ":"); i >= 0 && i+1 < len(addr) {
		if v, err := strconv.Atoi(addr[i+1:]); err == nil {
			return v
		}
	}
	return 0
}

// getOutletIntentPath returns the outlet intent file. It is written only by
// `daemon proxy add|rm`; the daemon reads it and publishes results into
// daemon.state, so the two never write the same file.
func getOutletIntentPath() string {
	return filepath.Join(os.Getenv("HOME"), ".rotaze", "outlets.json")
}

func readOutletIntent() []daemonOutlet {
	b, err := os.ReadFile(getOutletIntentPath())
	if err != nil {
		return nil
	}
	var outlets []daemonOutlet
	if json.Unmarshal(b, &outlets) != nil {
		return nil
	}
	return outlets
}

func writeOutletIntent(outlets []daemonOutlet) error {
	b, err := json.MarshalIndent(outlets, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(getOutletIntentPath(), b, 0600)
}

func newDaemonProxyListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List proxy outlets on the running daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			st := readDaemonState()
			if st == nil {
				return fmt.Errorf("daemon not running")
			}
			printOutlets(st, readOutletIntent())
			return nil
		},
	}
	return cmd
}

// printOutlets renders the live outlets from daemon state, then adds any
// outlet that has been requested (recorded in the outlet intent file) but is
// not yet reflected in state — it may still be waiting for the daemon to
// reconcile it, so it is shown as pending rather than silently missing.
func printOutlets(st *daemonState, desired []daemonOutlet) {
	fmt.Printf("%-5s %-10s %-15s %-10s %-8s\n", "ID", "PORT", "ADDR", "COUNTRY", "RUNNING")
	// Include default proxy if present
	if st.Proxy != "" {
		fmt.Printf("%-5d %-10d %-15s %-10s %-8t\n", 0, extractPort(st.Proxy), st.Proxy, "", true)
	}
	live := make(map[int]daemonOutlet, len(st.Outlets))
	for _, o := range st.Outlets {
		live[o.ID] = o
	}
	outs := append([]daemonOutlet(nil), st.Outlets...)
	for _, d := range desired {
		if _, ok := live[d.ID]; !ok {
			d.Running = false
			outs = append(outs, d)
		}
	}
	sort.Slice(outs, func(i, j int) bool { return outs[i].ID < outs[j].ID })
	for _, o := range outs {
		running := "false"
		if o.Running {
			running = "true"
		}
		c := o.Country
		if c == "" {
			c = "all"
		}
		fmt.Printf("%-5d %-10d %-15s %-10s %-8s\n", o.ID, o.Port, o.Addr, c, running)
	}
}

func newDaemonProxyRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm [id]",
		Short: "Remove a proxy outlet by ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := readPIDFile()
			if err != nil {
				return fmt.Errorf("daemon not running")
			}
			if !isProcessRunning(pid) {
				removePIDFile()
				removeDaemonState()
				return fmt.Errorf("daemon not running")
			}
			id, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid id")
			}
			outlets := readOutletIntent()
			newOuts := make([]daemonOutlet, 0, len(outlets))
			removed := false
			for _, o := range outlets {
				if o.ID == id {
					removed = true
					continue
				}
				newOuts = append(newOuts, o)
			}
			if !removed {
				return fmt.Errorf("outlet not found")
			}
			if err := writeOutletIntent(newOuts); err != nil {
				return fmt.Errorf("failed to record removal: %w", err)
			}
			proc, err := os.FindProcess(pid)
			if err == nil {
				_ = proc.Signal(syscall.SIGHUP)
			}
			fmt.Printf("Removed outlet #%d\n", id)
			return nil
		},
	}
	return cmd
}
