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
		node     string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add new proxy outlets for one or more countries",
		Long: `Add SOCKS5/HTTP outlet(s) to the running daemon. Ports are auto-assigned
starting from 1080, 1081, 1082, ... — one outlet per country, so
--country VN,SG,ID creates three independently verified outlets.
--node "server:port (protocol, country)" pins the outlet to an explicit
upstream, tried before the pool.`,
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
			// One outlet per country: --country VN,SG,ID fans out to three
			// outlets, each with its own port/ID, each verified on its own.
			outlets, created := appendOutlets(outlets, splitCountries(country), protocol, node, primary)
			if err := writeOutletIntent(outlets); err != nil {
				return fmt.Errorf("failed to record outlet: %w", err)
			}
			// Tell the daemon to apply the change.
			proc, err := os.FindProcess(pid)
			if err == nil {
				_ = proc.Signal(syscall.SIGHUP)
			}
			for _, o := range created {
				fmt.Printf("Added outlet #%d on %s (country=%s)\n", o.ID, o.Addr, o.Country)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&protocol, "protocol", "", "Protocol filter for this outlet (optional)")
	cmd.Flags().StringVar(&node, "node", "", `Pin this outlet to an explicit upstream server, "server:port (protocol, country)" — tried before pool candidates`)
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

// splitCountries parses the `--country` value of `daemon proxy add` into one
// entry per outlet: comma-separated, whitespace-trimmed, empty segments
// dropped, case-insensitive duplicates collapsed. An empty value yields
// ["all"] — a single outlet with no country restriction.
func splitCountries(v string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, part := range strings.Split(v, ",") {
		c := strings.TrimSpace(part)
		lower := strings.ToLower(c)
		if c == "" || seen[lower] {
			continue
		}
		seen[lower] = true
		out = append(out, c)
	}
	if len(out) == 0 {
		return []string{"all"}
	}
	return out
}

// appendOutlets allocates one outlet per country on top of the existing
// intent list, passing the growing slice to nextOutlet each time so ports
// and IDs stay distinct across the batch.
func appendOutlets(outlets []daemonOutlet, countries []string, protocol, node string, primaryPort int) ([]daemonOutlet, []daemonOutlet) {
	created := make([]daemonOutlet, 0, len(countries))
	for _, c := range countries {
		o := nextOutlet(outlets, primaryPort)
		o.Country = c
		o.Protocol = protocol
		o.Node = node
		o.Addr = fmt.Sprintf("127.0.0.1:%d", o.Port)
		outlets = append(outlets, o)
		created = append(created, o)
	}
	return outlets, created
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

// writeOutletIntent replaces the intent file atomically (write a sibling temp
// file, then rename). The daemon reads this file on every reconcile, so a
// truncating write would let it observe a partial list — outlets missing from
// that read are torn down and started again a moment later.
func writeOutletIntent(outlets []daemonOutlet) error {
	b, err := json.MarshalIndent(outlets, "", "  ")
	if err != nil {
		return err
	}
	path := getOutletIntentPath()
	tmp, err := os.CreateTemp(filepath.Dir(path), ".outlets-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// clearOutletIntent drops every requested outlet by deleting the intent file.
// It is called when the daemon stops gracefully: a stopped daemon owns no
// outlets, so neither a later `daemon proxy list` nor a later `daemon start`
// may resurrect them. `daemon restart` snapshots the intent before stopping
// and writes it back afterwards, which is what keeps outlets across a restart;
// a killed daemon (SIGKILL, crash) never calls this, so its outlets come back
// on the next start.
func clearOutletIntent() {
	_ = os.Remove(getOutletIntentPath())
}

func newDaemonProxyListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List proxy outlets on the running daemon",
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
	fmt.Printf("%-5s %-10s %-15s %-10s %-8s %-30s %-8s\n", "ID", "PORT", "ADDR", "COUNTRY", "RUNNING", "NODE", "MS")
	// Include default proxy if present. Its MS column is always "-": the
	// primary proxy is the daemon's own listener, not a remote node.
	if st.Proxy != "" {
		fmt.Printf("%-5d %-10d %-15s %-10s %-8t %-30s %-8s\n", 0, extractPort(st.Proxy), st.Proxy, "", true, nodeOrDash(st.Node), "-")
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
		ms := "-"
		if o.MS > 0 {
			ms = fmt.Sprintf("%dms", o.MS)
		}
		fmt.Printf("%-5d %-10d %-15s %-10s %-8s %-30s %-8s\n", o.ID, o.Port, o.Addr, c, running, nodeOrDash(o.Node), ms)
	}
}

// nodeOrDash renders a node description, falling back to "-" when the
// outlet has no live node (still verifying, pending, or stopped).
func nodeOrDash(node string) string {
	if node == "" {
		return "-"
	}
	return node
}

// parseOutletIDs parses the arguments of `daemon proxy rm`. Every argument is
// either the word "all" or a comma-separated list of IDs ("1,2"), and the two
// may be mixed. It returns the requested ID set and whether every outlet is to
// be removed.
func parseOutletIDs(args []string) (map[int]bool, bool, error) {
	ids := make(map[int]bool)
	all := false
	for _, arg := range args {
		for _, part := range strings.Split(arg, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if strings.EqualFold(part, "all") {
				all = true
				continue
			}
			id, err := strconv.Atoi(part)
			if err != nil || id <= 0 {
				return nil, false, fmt.Errorf("invalid id: %s", part)
			}
			ids[id] = true
		}
	}
	if !all && len(ids) == 0 {
		return nil, false, fmt.Errorf("no outlet id given")
	}
	return ids, all, nil
}

// removeOutlets partitions the current outlet intent: keep holds the outlets
// that survive, removed (ascending) the IDs that will be dropped, missing the
// requested IDs that are not outlets. The caller refuses the whole command when
// missing is non-empty, so a single bad ID leaves the intent untouched. When
// all is true every outlet is removed, whatever ids contains.
func removeOutlets(outlets []daemonOutlet, ids map[int]bool, all bool) (keep []daemonOutlet, removed, missing []int) {
	keep = make([]daemonOutlet, 0, len(outlets))
	found := make(map[int]bool, len(outlets))
	for _, o := range outlets {
		if all || ids[o.ID] {
			found[o.ID] = true
			removed = append(removed, o.ID)
			continue
		}
		keep = append(keep, o)
	}
	if !all {
		for id := range ids {
			if !found[id] {
				missing = append(missing, id)
			}
		}
	}
	sort.Ints(removed)
	sort.Ints(missing)
	return keep, removed, missing
}

func newDaemonProxyRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <id>...|all",
		Short: "Remove one or more proxy outlets",
		Long: `Remove proxy outlets by ID. Accepts several IDs, a comma-separated
list, or the word "all":

  rotaze daemon proxy rm 1 2 3
  rotaze daemon proxy rm 1,2
  rotaze daemon proxy rm all

The command is atomic: if any requested ID is not an outlet, nothing is
removed.`,
		Args: cobra.MinimumNArgs(1),
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
			ids, all, err := parseOutletIDs(args)
			if err != nil {
				return err
			}
			outlets := readOutletIntent()
			if all && len(outlets) == 0 {
				return fmt.Errorf("no outlets to remove")
			}
			keep, removed, missing := removeOutlets(outlets, ids, all)
			if len(missing) > 0 {
				parts := make([]string, 0, len(missing))
				for _, id := range missing {
					parts = append(parts, strconv.Itoa(id))
				}
				return fmt.Errorf("outlet not found: %s", strings.Join(parts, ", "))
			}
			if err := writeOutletIntent(keep); err != nil {
				return fmt.Errorf("failed to record removal: %w", err)
			}
			proc, err := os.FindProcess(pid)
			if err == nil {
				_ = proc.Signal(syscall.SIGHUP)
			}
			for _, id := range removed {
				fmt.Printf("Removed outlet #%d\n", id)
			}
			return nil
		},
	}
	return cmd
}
