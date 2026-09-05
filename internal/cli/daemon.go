// Package cli - daemon command (background service).
// Works on any Unix-like system without systemd (PID file based).
package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/rotaze/rotaze/internal/checker"
	"github.com/rotaze/rotaze/internal/config"
	"github.com/rotaze/rotaze/internal/fetcher"
	"github.com/rotaze/rotaze/internal/rotator"
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
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start background daemon",
		Long: `Start Rotaze as a background daemon.

By default, forks to background and writes PID to ~/.rotaze/rotaze.pid.
Use --foreground to run in foreground (useful for systemd or debugging).`,
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
				return runDaemon(url, interval, country)
			}

			// Fork to background
			return forkDaemon(url, interval, country)
		},
	}

	cmd.Flags().StringVar(&url, "url", "", "Target URL to monitor (default: https://api.ipify.org)")
	cmd.Flags().IntVar(&interval, "interval", 0, "Check interval in seconds")
	cmd.Flags().StringVar(&country, "country", "", "Exit IP country code")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run in foreground (don't fork)")

	return cmd
}

// forkDaemon starts the daemon in background by re-executing itself.
func forkDaemon(url string, interval int, country string) error {
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

	// Fork the process
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

	// Release the process handle so parent doesn't block
	cmd.Process.Release()
	logFile.Close()

	// Wait for PID file to appear (child writes it in runDaemon)
	for i := 0; i < 10; i++ {
		time.Sleep(300 * time.Millisecond)
		if pid, err := readPIDFile(); err == nil && isProcessRunning(pid) {
			color.Green("✓ Daemon started in background (PID %d)\n", pid)
			fmt.Printf("  Log file: %s\n", logPath)
			fmt.Printf("  PID file: %s\n", getDaemonPIDPath())
			fmt.Println("  Use 'rotaze daemon status' to check status")
			fmt.Println("  Use 'rotaze daemon stop' to stop")
			return nil
		}
	}

	// Fallback: report child PID even if we can't confirm via PID file
	if childPID > 0 && isProcessRunning(childPID) {
		color.Green("✓ Daemon started in background (PID %d)\n", childPID)
		fmt.Printf("  Log file: %s\n", logPath)
		return nil
	}

	return fmt.Errorf("daemon failed to start (check %s)", logPath)
}

// runDaemon runs the daemon in foreground.
func runDaemon(url string, interval int, country string) error {
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
	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGHUP:
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

	// Fetch nodes
	fmt.Println("Fetching nodes...")
	p := fetcher.NewPool(globalCache)
	nodes, err := p.FetchNodes(ctx, cfg.Checker.Country)
	if err != nil {
		return fmt.Errorf("failed to fetch nodes: %w", err)
	}
	fmt.Printf("Fetched %d nodes\n", len(nodes))

	// Pick a node
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
		return fmt.Errorf("failed to start proxy: %w", err)
	}
	defer r.Close()

	// Set current node
	globalCache.SetCurrentNode(node.ID)
	fmt.Printf("Current node: %s:%d (%s, %s)\n", node.Server, node.Port, node.Protocol, node.Country)
	fmt.Printf("SOCKS5 proxy: %s\n", r.GetListenAddr())
	fmt.Printf("Monitoring: %s every %ds\n", url, cfg.Checker.Interval)
	fmt.Println("Send SIGHUP to reload config, SIGTERM to stop")

	// Rotation callback
	rotateFn := func(ctx context.Context, reason string) error {
		newNode, err := pickNextNode(globalCache, node.ID)
		if err != nil {
			return fmt.Errorf("no next node: %w", err)
		}
		if err := r.SwitchOutbound(ctx, newNode); err != nil {
			return fmt.Errorf("switch failed: %w", err)
		}
		globalCache.SetCurrentNode(newNode.ID)
		globalCache.RecordRotation(node.ID, newNode.ID, reason, "success")
		fmt.Printf("Rotated to: %s:%d (%s)\n", newNode.Server, newNode.Port, newNode.Country)
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

	// If a SIGHUP arrived before the checker started, apply it now.
	reloadMu.Lock()
	if pendingReload != nil {
		chk.UpdateInterval(time.Duration(pendingReload.Checker.Interval) * time.Second)
		chk.UpdateCountry(pendingReload.Checker.Country)
		pendingReload = nil
	}
	reloadMu.Unlock()

	// Apply future reloads: a small watcher goroutine drains pendingReload.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
				reloadMu.Lock()
				if pendingReload != nil {
					chk.UpdateInterval(time.Duration(pendingReload.Checker.Interval) * time.Second)
					chk.UpdateCountry(pendingReload.Checker.Country)
					pendingReload = nil
				}
				reloadMu.Unlock()
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
					color.Green("✓ Daemon stopped (PID %d)\n", pid)
					return nil
				}
			}

			// Force kill
			fmt.Println("Process still running, sending SIGKILL...")
			process.Signal(syscall.SIGKILL)
			time.Sleep(500 * time.Millisecond)
			removePIDFile()
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

			if isProcessRunning(pid) {
				color.Green("Status: RUNNING (PID %d)\n", pid)
				// Show current node
				currentNodeID, _ := globalCache.GetCurrentNode()
				if currentNodeID > 0 {
					node, err := globalCache.GetNodeByID(currentNodeID)
					if err == nil {
						fmt.Printf("Current node: %s:%d (%s)\n", node.Server, node.Port, node.Country)
					}
				}
				// Show log file
				fmt.Printf("Log file: %s\n", getDaemonLogPath())
			} else {
				fmt.Println("Status: NOT RUNNING (stale PID)")
				fmt.Printf("Stale PID: %d\n", pid)
				removePIDFile()
			}
			return nil
		},
	}
}

func newDaemonRestartCmd() *cobra.Command {
	var (
		url      string
		interval int
		country  string
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
				fmt.Println("✓ Stopped")
			}

			// Start
			if url == "" {
				url = "https://api.ipify.org"
			}

			fmt.Println("Starting daemon...")
			return forkDaemon(url, interval, country)
		},
	}

	cmd.Flags().StringVar(&url, "url", "", "Target URL to monitor")
	cmd.Flags().IntVar(&interval, "interval", 0, "Check interval in seconds")
	cmd.Flags().StringVar(&country, "country", "", "Exit IP country code")

	return cmd
}
