// Package cli - daemon command (background service).
// Works on any Unix-like system without systemd (PID file based).
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/rotaze/rotaze/internal/checker"
	"github.com/rotaze/rotaze/internal/config"
	"github.com/rotaze/rotaze/internal/fetcher"
	"github.com/rotaze/rotaze/internal/rotator"
	"github.com/rotaze/rotaze/pkg/types"
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Background service management (works without systemd)",
		Long: `Run Rotaze as a background daemon using PID file management.
Works on any Unix-like system - no systemd required.

Examples:
  rotaze daemon start --url https://example.com
  rotaze daemon stop
  rotaze daemon status
  rotaze daemon restart --url https://example.com

For systemd systems, use the provided rotaze.service file instead.`,
	}

	cmd.AddCommand(newDaemonStartCmd())
	cmd.AddCommand(newDaemonStopCmd())
	cmd.AddCommand(newDaemonStatusCmd())
	cmd.AddCommand(newDaemonRestartCmd())
	cmd.AddCommand(newDaemonLogsCmd())
	cmd.AddCommand(newDaemonProxyCmd())

	return cmd
}

func getDaemonPIDPath() string {
	return filepath.Join(os.Getenv("HOME"), ".rotaze", "rotaze.pid")
}

func getDaemonLogPath() string {
	return filepath.Join(os.Getenv("HOME"), ".rotaze", "daemon.log")
}

func writePIDFile(pid int) error {
	return os.WriteFile(getDaemonPIDPath(), []byte(fmt.Sprintf("%d", pid)), 0600)
}

func readPIDFile() (int, error) {
	data, err := os.ReadFile(getDaemonPIDPath())
	if err != nil {
		return 0, err
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil {
		return 0, fmt.Errorf("invalid PID file: %w", err)
	}
	return pid, nil
}

func removePIDFile() {
	os.Remove(getDaemonPIDPath())
}

// daemonState is a small JSON status file (~/.rotaze/daemon.state) written by
// the daemon itself. It lets `daemon status` answer "what is it doing" without
// reading the log file, and lets `start` detect silent early death.
type daemonOutlet struct {
	ID       int    `json:"id"`
	Port     int    `json:"port"`
	Addr     string `json:"addr"`
	Country  string `json:"country"`
	Protocol string `json:"protocol,omitempty"`
	Node     string `json:"node,omitempty"`
	Running  bool   `json:"running"`
	NodeID   int    `json:"node_id,omitempty"`
}

type daemonState struct {
	PID       int            `json:"pid"`
	Status    string         `json:"status"` // starting | running
	Phase     string         `json:"phase,omitempty"`
	Node      string         `json:"node,omitempty"`
	Proxy     string         `json:"proxy,omitempty"`
	StartedAt string         `json:"started_at,omitempty"`
	UpdatedAt string         `json:"updated_at"`
	Outlets   []daemonOutlet `json:"outlets,omitempty"`
}

func getDaemonStatePath() string {
	return filepath.Join(os.Getenv("HOME"), ".rotaze", "daemon.state")
}

func writeDaemonState(s *daemonState) {
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(getDaemonStatePath(), b, 0600)
}

func readDaemonState() *daemonState {
	b, err := os.ReadFile(getDaemonStatePath())
	if err != nil {
		return nil
	}
	var s daemonState
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

func removeDaemonState() {
	os.Remove(getDaemonStatePath())
}

// tailLog returns up to maxLines last lines of the daemon log, joined with
// newlines. Empty when the log does not exist.
func tailLog(maxLines int) string {
	b, err := os.ReadFile(getDaemonLogPath())
	if err != nil || len(b) == 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

func newDaemonStartCmd() *cobra.Command {
	var (
		url        string
		interval   int
		country    string
		foreground bool
		protocol   string
		ovpnFiles  []string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start background daemon",
		Long: `Start Rotaze as a background daemon.

By default, forks to background and writes PID to ~/.rotaze/rotaze.pid.
Use --foreground to run in foreground (useful for systemd or debugging).
Use --ovpn <file> to run on explicit OpenVPN config(s) instead of fetching
nodes, or --protocol <proto> to restrict node selection by protocol.
Status is written to ~/.rotaze/daemon.state and read by 'daemon status'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if url == "" {
				url = "https://api.ipify.org"
			}

			// Check if already running
			if pid, err := readPIDFile(); err == nil && isProcessRunning(pid) {
				return fmt.Errorf("daemon already running (PID %d)", pid)
			}

			// Ensure data directory exists
			home, _ := os.UserHomeDir()
			dataDir := filepath.Join(home, ".rotaze")
			os.MkdirAll(dataDir, 0700)
			if foreground {
				// Run in foreground (for systemd or debugging)
				return runDaemon(url, interval, country, protocol, ovpnFiles)
			}

			// Fork to background
			return forkDaemon(url, interval, country, protocol, ovpnFiles)
		},
	}

	cmd.Flags().StringVar(&url, "url", "", "Target URL to monitor (default: https://api.ipify.org)")
	cmd.Flags().IntVar(&interval, "interval", 0, "Check interval in seconds (default: from config, 30)")
	cmd.Flags().StringVar(&country, "country", "", "Exit IP country code (default: from config)")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run in foreground (don't fork)")
	cmd.Flags().StringVar(&protocol, "protocol", "", "Filter nodes by protocol: vless, ss, trojan, vmess, hy2, socks, ovpn (empty = all)")
	cmd.Flags().StringSliceVar(&ovpnFiles, "ovpn", nil, "OpenVPN config file(s) to use directly (repeatable, or comma-separated)")

	return cmd
}
func forkDaemon(url string, interval int, country string, protocol string, ovpnFiles []string) error {
	// Get current executable path
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Build args for background process
	args := []string{"daemon", "start", "--foreground", "--url", url}
	if interval > 0 {
		args = append(args, "--interval", fmt.Sprintf("%d", interval))
	}
	if country != "" {
		args = append(args, "--country", country)
	}
	if protocol != "" {
		args = append(args, "--protocol", protocol)
	}
	for _, f := range ovpnFiles {
		args = append(args, "--ovpn", f)
	}
	logPath := getDaemonLogPath()
	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}

	cmd := exec.Command(exePath, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil

	// Start detached process
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session
	}

	// Remove old PID file before starting
	removePIDFile()

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	childPID := cmd.Process.Pid

	// Reap the child in the background: a waited process is not a zombie,
	// so the parent's isProcessRunning(childPID) turns false the moment it
	// dies; exited also signals the polling loop below.
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	logFile.Close()

	// Wait for the daemon to signal it is actually running: PID file plus a
	// daemon.state with PID set. When it dies early (config error, missing
	// sing-box, port conflict), surface the log tail so the user does not
	// have to open the file themselves.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if pid, err := readPIDFile(); err == nil && isProcessRunning(pid) {
			if st := readDaemonState(); st != nil && st.PID == pid && st.Status == "running" {
				color.Green("✓ Daemon started in background (PID %d)\n", pid)
				if st.Node != "" {
					fmt.Printf("  Node:   %s\n", st.Node)
				}
				if st.Proxy != "" {
					fmt.Printf("  Proxy:  %s\n", st.Proxy)
				}
				fmt.Printf("  Log:    %s\n", logPath)
				fmt.Println("  Use 'rotaze daemon status' to check status")
				fmt.Println("  Use 'rotaze daemon stop' to stop")
				return nil
			}
		}
		select {
		case <-exited:
			// Child died; report why from the log instead of "check the file".
			msg := "daemon exited during startup"
			if tail := tailLog(30); tail != "" {
				msg += "\n--- daemon.log (last 30 lines) ---\n" + tail
			}
			return fmt.Errorf("%s", msg)
		default:
		}
		if time.Now().After(deadline) {
			color.Green("✓ Daemon process started (PID %d)\n", childPID)
			fmt.Printf("  Still initializing; run 'rotaze daemon status' to check.\n")
			fmt.Printf("  Log: %s\n", logPath)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func runDaemon(url string, interval int, country string, protocol string, ovpnFiles []string) error {
	// Load config
	cfg, err := config.LoadConfig("")
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if country != "" {
		cfg.Checker.Country = country
	}
	if interval > 0 {
		cfg.Checker.Interval = interval
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handler
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	reloadMu := sync.Mutex{}
	var pendingReload *config.Config
	// pendingOutlets is set by every SIGHUP: `daemon proxy add|rm` signals the
	// daemon this way after editing daemon.state. It is tracked separately
	// from pendingReload so outlet changes are still applied when the config
	// file itself fails to reload.
	var pendingOutlets bool
	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGHUP:
				reloadMu.Lock()
				pendingOutlets = true
				reloadMu.Unlock()
				fmt.Println("\nReceived SIGHUP, reloading config...")
				newCfg, err := config.LoadConfig("")
				if err != nil {
					fmt.Fprintf(os.Stderr, "Config reload failed: %v\n", err)
					continue
				}
				reloadMu.Lock()
				pendingReload = newCfg
				reloadMu.Unlock()
				fmt.Printf("Config reloaded (interval=%ds, country=%s)\n", newCfg.Checker.Interval, newCfg.Checker.Country)
			default:
				fmt.Println("\nReceived signal, shutting down...")
				cancel()
				return
			}
		}
	}()

	// Write PID file
	if err := writePIDFile(os.Getpid()); err != nil {
		return fmt.Errorf("failed to write PID file: %w", err)
	}
	defer removePIDFile()

	// Publish lifecycle state so `daemon status` and `start` (parent) can
	// see what phase this daemon is in without opening the log.
	st := &daemonState{
		PID:       os.Getpid(),
		Status:    "starting",
		Phase:     "fetching nodes",
		StartedAt: time.Now().Format(time.RFC3339),
	}
	writeDaemonState(st)
	defer removeDaemonState()
	// Fetch nodes: with --ovpn, load the given configs directly into the
	// pool instead (they get IDs and can be rotated), skipping remote fetch
	// and the 30-min refresh/prune cycle (auto-prune would drop a temporarily
	// dead ovpn config from rotation).
	var p *fetcher.Pool
	var nodes []*types.Node
	if len(ovpnFiles) > 0 {
		nodes, err = loadOVPNNodes(ovpnFiles)
		if err != nil {
			return fmt.Errorf("failed to load OpenVPN config: %w", err)
		}
		if err := globalCache.SaveNodes(nodes); err != nil {
			return fmt.Errorf("failed to save OpenVPN nodes: %w", err)
		}
		fmt.Printf("Loaded %d OpenVPN node(s) from config\n", len(nodes))
	} else {
		fmt.Println("Fetching nodes...")
		p = fetcher.NewPool(globalCache)
		nodes, err = p.FetchNodes(ctx, cfg.Checker.Country)
		if err != nil {
			return fmt.Errorf("failed to fetch nodes: %w", err)
		}
		fmt.Printf("Fetched %d nodes\n", len(nodes))
	}
	if protocol != "" && len(ovpnFiles) == 0 {
		filtered := filterNodesByProtocol(nodes, protocol)
		fmt.Printf("Protocol filter %q: %d of %d nodes match\n", protocol, len(filtered), len(nodes))
		nodes = filtered
	}
	// Auto-refresh pool + prune dead nodes every 30 minutes in background.
	// VPNGate recycles hosts constantly; stale nodes waste rotation attempts
	// (and the default --attempts budget) on servers that no longer answer.
	// Skipped with --ovpn: the configs are explicit, and auto-prune would
	// drop a temporarily dead ovpn config from rotation.
	if p != nil {
		go func() {
			refreshTicker := time.NewTicker(30 * time.Minute)
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
						continue
					}
					removed, err := p.PruneDeadNodes(ctx, cfg.Checker.Country, 3*time.Second)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Auto-prune failed: %v\n", err)
					} else if removed > 0 {
						fmt.Printf("Auto-pruned %d dead nodes (%s)\n", removed, cfg.Checker.Country)
					}
				}
			}
		}()
	}

	// Pick a node. Explicit --ovpn configs bypass the picker (it never
	// returns ovpn) — use the first loaded config directly. The same goes
	// for --protocol: pickWorkingNode never considers ovpn, so pick from
	// the (already protocol-filtered) node set instead.
	var node *types.Node
	if len(ovpnFiles) > 0 {
		if len(nodes) == 0 {
			color.Red("✗ No OpenVPN configs loaded\n")
			os.Exit(3)
		}
		node = nodes[0]
	} else if protocol != "" && protocol != "all" {
		node = pickBestNode(nodes)
	} else {
		node = pickWorkingNode(globalCache, cfg.Checker.Country)
		if node == nil {
			node = pickBestNode(nodes)
		}
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
		return fmt.Errorf("failed to start proxy: %w", err)
	}
	defer r.Close()

	// Set current node
	globalCache.SetCurrentNode(node.ID)
	fmt.Printf("Current node: %s:%d (%s, %s)\n", node.Server, node.Port, node.Protocol, node.Country)
	fmt.Printf("SOCKS5/HTTP proxy: %s\n", r.GetListenAddr())
	fmt.Printf("Monitoring: %s every %ds\n", url, cfg.Checker.Interval)
	fmt.Println("Send SIGHUP to reload config, SIGTERM to stop")
	// Publish ready state; `start` (parent) and `status` read this.
	// stateMu serializes every read-modify-write of the shared state struct:
	// the checker goroutine (rotateFn) and the outlet reconciler both update
	// it, and an unsynchronized write would drop the other's fields.
	stateMu := sync.Mutex{}
	publishState := func() {
		stateMu.Lock()
		defer stateMu.Unlock()
		writeDaemonState(st)
	}

	st.Status = "running"
	st.Phase = "monitoring"
	st.Node = fmt.Sprintf("%s:%d (%s, %s)", node.Server, node.Port, node.Protocol, node.Country)
	st.Proxy = r.GetListenAddr()
	publishState()
	// rotateMu serializes rotations from the checker goroutine (failure) and
	// the SIGHUP watcher goroutine (country change), and guards the shared
	// node/appliedCountry state they both touch.
	rotateMu := sync.Mutex{}
	appliedCountry := cfg.Checker.Country

	// Rotation callback. Picks from the currently-applied country so SIGHUP
	// country changes rotate to a node of the new country.
	rotateFn := func(ctx context.Context, reason string) error {
		rotateMu.Lock()
		defer rotateMu.Unlock()
		newNode, err := pickNextNode(globalCache, node.ID, appliedCountry, protocol)
		if err != nil {
			return fmt.Errorf("no next node: %w", err)
		}
		if err := r.SwitchOutbound(ctx, newNode); err != nil {
			return fmt.Errorf("switch failed: %w", err)
		}
		globalCache.SetCurrentNode(newNode.ID)
		globalCache.RecordRotation(node.ID, newNode.ID, reason, "success")
		fmt.Printf("Rotated to: %s:%d (%s)\n", newNode.Server, newNode.Port, newNode.Country)
		stateMu.Lock()
		st.Node = fmt.Sprintf("%s:%d (%s, %s)", newNode.Server, newNode.Port, newNode.Protocol, newNode.Country)
		stateMu.Unlock()
		publishState()
		node = newNode
		return nil
	}

	// Start checker. Snapshot config values now; the SIGHUP goroutine also
	// writes cfg, so live updates go through checker's thread-safe setters.
	chk := checker.New(globalCache, globalLogger, &checker.Config{
		URL:         url,
		Interval:    time.Duration(cfg.Checker.Interval) * time.Second,
		Timeout:     time.Duration(cfg.Checker.Timeout) * time.Second,
		MaxRetries:  cfg.Checker.MaxRetries,
		MaxAttempts: cfg.Checker.MaxAttempts,
		CheckIP:     cfg.Checker.CheckIP,
		Country:     cfg.Checker.Country,
	})
	chk.OnRotate(rotateFn)

	// applyReload applies a reloaded config to the checker, and rotates to a
	// node of the new country when the country changed.
	applyReload := func(rel *config.Config) {
		chk.UpdateInterval(time.Duration(rel.Checker.Interval) * time.Second)
		chk.UpdateCountry(rel.Checker.Country)
		chk.UpdateCheckIP(rel.Checker.CheckIP)
		rotateMu.Lock()
		countryChanged := appliedCountry != rel.Checker.Country
		if countryChanged {
			appliedCountry = rel.Checker.Country
		}
		rotateMu.Unlock()
		if countryChanged {
			if err := rotateFn(ctx, "reload"); err != nil {
				fmt.Fprintf(os.Stderr, "country rotate failed: %v\n", err)
			}
		}
	}
	// Outlets are the additional proxies declared via `rotaze daemon proxy
	// add`. The CLI writes them to daemon.state and signals us; we own the
	// rotators behind them and reconcile on every reload.
	outlets := newOutletManager(cfg.Proxy.ListenPort)
	defer outlets.Close()

	// pickOutletNode selects a node for an outlet, honouring its country and
	// protocol filter. Outlet "all" means no country restriction.
	pickOutletNode := func(country, protocol string) (*types.Node, error) {
		nodes, err := globalCache.GetNodes()
		if err != nil {
			return nil, err
		}
		nodes = filterNodesByCountry(nodes, country)
		nodes = filterNodesByProtocol(nodes, protocol)
		return pickBestNode(nodes), nil
	}

	// reconcileOutlets applies the outlet intent file to running rotators and
	// publishes the live result into daemon.state. Reading intent (not
	// daemon.state) means a concurrent `daemon proxy add` can never be undone
	// by this write.
	reconcileOutlets := func() {
		result := outlets.Reconcile(ctx, globalLogger, getDataDir(cfg), readOutletIntent(), pickOutletNode)
		stateMu.Lock()
		st.Outlets = result
		stateMu.Unlock()
		publishState()
	}

	// If a SIGHUP arrived before the checker started, apply it now.
	reloadMu.Lock()
	if pendingReload != nil {
		applyReload(pendingReload)
		pendingReload = nil
	}
	reloadMu.Unlock()

	// Apply future reloads: a small watcher goroutine drains pendingReload
	// and pendingOutlets, both set by the SIGHUP handler.
	// Reconcile on SIGHUP and on a fallback cadence: an outlet added while the
	// pool is still empty fails to start, and without a periodic pass it would
	// stay dead forever (nothing else signals the daemon again). PickNode
	// failures are retried here until the pool catches up.
	lastOutletReconcile := time.Now().Add(-time.Hour)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
				reloadMu.Lock()
				if pendingReload != nil {
					applyReload(pendingReload)
					pendingReload = nil
				}
				outletsChanged := pendingOutlets
				pendingOutlets = false
				reloadMu.Unlock()
				if outletsChanged || time.Since(lastOutletReconcile) >= 15*time.Second {
					reconcileOutlets()
					lastOutletReconcile = time.Now()
				}
			}
		}
	}()

	fmt.Println("✓ Daemon started")
	if err := chk.Start(ctx); err != nil {
		return fmt.Errorf("checker error: %w", err)
	}

	fmt.Println("Daemon stopped")
	return nil
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop background daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := readPIDFile()
			if err != nil {
				return fmt.Errorf("daemon not running (no PID file)")
			}

			if !isProcessRunning(pid) {
				removePIDFile()
				return fmt.Errorf("daemon not running (stale PID %d)", pid)
			}

			// Show what we are stopping, from the state file if available.
			if st := readDaemonState(); st != nil && st.PID == pid {
				extra := ""
				if st.Node != "" {
					extra = ", node " + st.Node
				}
				if st.Proxy != "" {
					extra += ", proxy " + st.Proxy
				}
				fmt.Printf("Stopping daemon (PID %d%s)\n", pid, extra)
			}

			// Send SIGTERM
			process, err := os.FindProcess(pid)
			if err != nil {
				return fmt.Errorf("failed to find process: %w", err)
			}

			fmt.Printf("Sending SIGTERM to PID %d...\n", pid)
			if err := process.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("failed to stop daemon: %w", err)
			}

			// Wait for process to exit
			for i := 0; i < 10; i++ {
				time.Sleep(500 * time.Millisecond)
				if !isProcessRunning(pid) {
					removePIDFile()
					removeDaemonState()
					color.Green("✓ Daemon stopped (PID %d)\n", pid)
					return nil
				}
			}

			// Force kill
			fmt.Println("Process still running, sending SIGKILL...")
			process.Signal(syscall.SIGKILL)
			time.Sleep(500 * time.Millisecond)
			removePIDFile()
			removeDaemonState()
			color.Green("✓ Daemon killed (PID %d)\n", pid)
			return nil
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, err := readPIDFile()
			if err != nil {
				fmt.Println("Status: NOT RUNNING")
				fmt.Println("No PID file found.")
				return nil
			}

			if !isProcessRunning(pid) {
				fmt.Println("Status: NOT RUNNING (stale PID)")
				fmt.Printf("Stale PID: %d\n", pid)
				removePIDFile()
				return nil
			}

			color.Green("Status: RUNNING (PID %d)\n", pid)
			st := readDaemonState()
			if st != nil && st.PID == pid {
				if st.StartedAt != "" {
					if started, err := time.Parse(time.RFC3339, st.StartedAt); err == nil {
						fmt.Printf("Up since:  %s (%s)\n", started.Local().Format("2006-01-02 15:04:05"), time.Since(started).Round(time.Second))
					}
				}
				if st.Status == "starting" {
					fmt.Printf("Phase:     %s\n", st.Phase)
				}
				if st.Node != "" {
					fmt.Printf("Node:      %s\n", st.Node)
				} else if currentNodeID, err := globalCache.GetCurrentNode(); err == nil && currentNodeID > 0 {
					if node, err := globalCache.GetNodeByID(currentNodeID); err == nil {
						fmt.Printf("Node:      %s:%d (%s)\n", node.Server, node.Port, node.Country)
					}
				}
				if st.Proxy != "" {
					fmt.Printf("Proxy:     %s (SOCKS5/HTTP)\n", st.Proxy)
				}
				if lastCheck, err := globalCache.GetState(types.StateKeyLastCheckTime); err == nil && lastCheck != "" {
					statusStr, _ := globalCache.GetState(types.StateKeyLastCheckStatus)
					if statusStr == "" {
						statusStr = "pending"
					}
					fmt.Printf("Last check: %s (%s)\n", lastCheck, statusStr)
				}
			} else {
				// Daemon from an older build: fall back to DB node info.
				if currentNodeID, err := globalCache.GetCurrentNode(); err == nil && currentNodeID > 0 {
					if node, err := globalCache.GetNodeByID(currentNodeID); err == nil {
						fmt.Printf("Current node: %s:%d (%s)\n", node.Server, node.Port, node.Country)
					}
				}
			}
			fmt.Printf("Log file:  %s\n", getDaemonLogPath())
			return nil
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	var (
		url       string
		interval  int
		country   string
		protocol  string
		ovpnFiles []string
	)

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart background daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Stop if running
			pid, err := readPIDFile()
			if err == nil && isProcessRunning(pid) {
				process, _ := os.FindProcess(pid)
				process.Signal(syscall.SIGTERM)
				time.Sleep(1 * time.Second)
				if isProcessRunning(pid) {
					process.Signal(syscall.SIGKILL)
					time.Sleep(500 * time.Millisecond)
				}
				removePIDFile()
				removeDaemonState()
				fmt.Println("✓ Stopped")
			}

			// Start
			if url == "" {
				url = "https://api.ipify.org"
			}
			return forkDaemon(url, interval, country, protocol, ovpnFiles)
		},
	}

	cmd.Flags().StringVar(&url, "url", "", "Target URL to monitor (default: https://api.ipify.org)")
	cmd.Flags().IntVar(&interval, "interval", 0, "Check interval in seconds (default: from config, 30)")
	cmd.Flags().StringVar(&country, "country", "", "Exit IP country code (default: from config)")
	cmd.Flags().StringSliceVar(&ovpnFiles, "ovpn", nil, "OpenVPN config file(s) to use directly (repeatable, or comma-separated)")
	cmd.Flags().StringVar(&protocol, "protocol", "", "Filter nodes by protocol: vless, ss, trojan, vmess, hy2, socks, ovpn (empty = all)")

	return cmd
}

// newDaemonLogsCmd implements `rotaze daemon logs`: show the tail of the
// daemon log file, or follow it like `tail -f`. The daemon writes to a fixed
// path (getDaemonLogPath) regardless of --log-file, so this reads that file.
func newDaemonLogsCmd() *cobra.Command {
	var (
		follow bool
		lines  int
	)

	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show daemon log (last lines, or follow)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if lines <= 0 {
				lines = 20
			}
			path := getDaemonLogPath()

			if !follow {
				out := tailLog(lines)
				if out == "" {
					fmt.Printf("No daemon log at %s\n", path)
					return nil
				}
				fmt.Println(out)
				return nil
			}

			// Follow mode: start at the last `lines` lines, then poll for
			// appended data. Exit on Ctrl+C.
			f, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open log file: %w", err)
			}
			defer f.Close()

			size, err := f.Seek(0, io.SeekEnd)
			if err != nil {
				return fmt.Errorf("failed to seek log file: %w", err)
			}
			offset := size
		scan:
			for i := 0; i < lines && offset > 0; i++ {
				chunk := int64(512)
				if chunk > offset {
					chunk = offset
				}
				offset -= chunk
				buf := make([]byte, chunk)
				if _, err := f.ReadAt(buf, offset); err != nil {
					return fmt.Errorf("failed to read log file: %w", err)
				}
				for j := len(buf) - 1; j >= 0; j-- {
					if buf[j] != '\n' {
						continue
					}
					if j == len(buf)-1 && offset+chunk == size {
						continue // trailing newline of the last line
					}
					offset += int64(j) + 1
					break scan
				}
			}
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return fmt.Errorf("failed to seek log file: %w", err)
			}

			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
			defer signal.Stop(sigChan)

			buf := make([]byte, 4096)
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-sigChan:
					fmt.Println()
					return nil
				case <-cmd.Context().Done():
					return nil
				case <-ticker.C:
					n, err := f.Read(buf)
					if n > 0 {
						os.Stdout.Write(buf[:n])
					}
					if err != nil && err != io.EOF {
						return fmt.Errorf("failed to read log file: %w", err)
					}
				}
			}
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output (like tail -f)")
	cmd.Flags().IntVar(&lines, "lines", 20, "Number of lines to show")

	return cmd
}
