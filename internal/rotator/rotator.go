// Package rotator manages a sing-box process for proxy operations.
// It starts sing-box as a managed subprocess with generated config.
package rotator

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/rotaze/rotaze/pkg/types"
)

// Rotator manages a sing-box subprocess for proxy operations.
type Rotator struct {
	logger     *zap.Logger
	config     *Config
	mu         sync.Mutex
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	listenAddr string
	listenPort int
	configPath string
	running    bool
	waitCh     chan error // closed/produces result when sing-box exits; owned by monitor goroutine
}

// Config holds rotator configuration.
type Config struct {
	ListenAddr string // e.g., "127.0.0.1"
	ListenPort int    // e.g., 1080
	DataDir    string // e.g., ~/.rotaze
}

// New creates a new Rotator instance.
func New(logger *zap.Logger, cfg *Config) *Rotator {
	return &Rotator{
		logger:     logger,
		config:     cfg,
		listenAddr: cfg.ListenAddr,
		listenPort: cfg.ListenPort,
	}
}

// Start initializes and starts the sing-box process with the given node.
func (r *Rotator) Start(ctx context.Context, node *types.Node) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Ensure data directory
	if err := os.MkdirAll(r.config.DataDir, 0700); err != nil {
		return fmt.Errorf("failed to create data dir: %w", err)
	}

	// Build config and write to file
	config := BuildConfig(node, r.listenAddr, r.listenPort)
	configPath := filepath.Join(r.config.DataDir, "singbox.json")
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		return fmt.Errorf("failed to write sing-box config: %w", err)
	}
	r.configPath = configPath
	r.logger.Debug("wrote sing-box config",
		zap.String("path", configPath),
		zap.String("outbound", node.Tag),
		zap.String("protocol", node.Protocol),
		zap.String("server", fmt.Sprintf("%s:%d", node.Server, node.Port)),
		zap.String("dns", "local (system resolver)"),
	)

	// Find sing-box binary
	singboxPath, err := findSingbox()
	if err != nil {
		return fmt.Errorf("sing-box not found: %w", err)
	}
	logSingboxVersion(r.logger, singboxPath)

	// Free the listen port first: a previous (possibly crashed) run may
	// have left a stale sing-box holding it, which would make our child
	// die with "address already in use" and leave the URL check talking
	// to that stale process.
	if err := ensurePortFree(r.listenAddr, r.listenPort); err != nil {
		return err
	}

	// Start sing-box
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	cmd := exec.CommandContext(ctx, singboxPath, "run", "-c", configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start sing-box: %w", err)
	}

	r.cmd = cmd
	r.running = true

	// Monitor process in background. cmd.Wait() must only be called from
	// one place; we store a done channel so Close() can wait on it instead
	// of calling Wait() itself (which would race with this goroutine).
	doneCh := make(chan error, 1)
	go func() {
		myCmd := cmd
		err := cmd.Wait()
		doneCh <- err
		r.mu.Lock()
		// Only clear running if this goroutine's process is still the
		// current one — a stale monitor from a previous process must not
		// clobber the state of the replacement started after it.
		if r.cmd == myCmd {
			r.running = false
		}
		r.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			r.logger.Warn("sing-box process exited", zap.Error(err))
		}
	}()
	r.waitCh = doneCh

	// Wait for sing-box to start listening
	time.Sleep(2 * time.Second)

	// Fail fast if the process died during startup (e.g. config error or
	// port conflict) instead of reporting success for a dead proxy.
	select {
	case err := <-doneCh:
		cancel()
		return fmt.Errorf("sing-box exited during startup: %w", err)
	default:
	}

	r.logger.Info("sing-box started",
		zap.String("listen", fmt.Sprintf("%s:%d", r.listenAddr, r.listenPort)),
		zap.String("outbound", node.Tag),
		zap.Int("pid", cmd.Process.Pid),
	)

	return nil
}

// SwitchOutbound changes the active outbound node by restarting sing-box.
func (r *Rotator) SwitchOutbound(ctx context.Context, node *types.Node) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Stop existing process
	if r.cmd != nil && r.running {
		if r.cancel != nil {
			r.cancel()
		}
		// Wait for process to exit with timeout. The monitor goroutine from
		// Start() owns cmd.Wait(); drain its result channel instead of
		// calling Wait() again (which would race).
		if r.waitCh != nil {
			select {
			case <-r.waitCh:
			case <-time.After(3 * time.Second):
				// Force kill if still running
				r.cmd.Process.Kill()
				select {
				case <-r.waitCh:
				case <-time.After(2 * time.Second):
				}
			}
		}
		r.running = false
		// Extra grace period for port release
		time.Sleep(500 * time.Millisecond)
	}

	// Build new config
	config := BuildConfig(node, r.listenAddr, r.listenPort)
	if err := os.WriteFile(r.configPath, config, 0600); err != nil {
		return fmt.Errorf("failed to write new config: %w", err)
	}
	r.logger.Debug("wrote sing-box config (switch)",
		zap.String("path", r.configPath),
		zap.String("outbound", node.Tag),
		zap.String("protocol", node.Protocol),
		zap.String("server", fmt.Sprintf("%s:%d", node.Server, node.Port)),
	)

	// Find sing-box binary
	singboxPath, err := findSingbox()
	if err != nil {
		return fmt.Errorf("sing-box not found: %w", err)
	}

	// Free the listen port (stale process cleanup) before starting the
	// replacement; our old process was stopped above.
	if err := ensurePortFree(r.listenAddr, r.listenPort); err != nil {
		return err
	}

	// Start new process
	ctx2, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	cmd := exec.CommandContext(ctx2, singboxPath, "run", "-c", r.configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("failed to start sing-box: %w", err)
	}

	r.cmd = cmd
	r.running = true

	// Monitor process (single owner of cmd.Wait() for this process)
	newWaitCh := make(chan error, 1)
	go func() {
		myCmd := cmd
		err := cmd.Wait()
		newWaitCh <- err
		r.mu.Lock()
		// Only clear running if this goroutine's process is still the
		// current one — a stale monitor from a previous process must not
		// clobber the state of the replacement started after it.
		if r.cmd == myCmd {
			r.running = false
		}
		r.mu.Unlock()
		if err != nil && ctx2.Err() == nil {
			r.logger.Warn("sing-box process exited", zap.Error(err))
		}
	}()
	r.waitCh = newWaitCh

	// Wait for sing-box to start
	time.Sleep(2 * time.Second)

	// Fail fast if the process died during startup.
	select {
	case err := <-newWaitCh:
		cancel()
		return fmt.Errorf("sing-box exited during startup: %w", err)
	default:
	}

	r.logger.Info("switched outbound",
		zap.String("outbound", node.Tag),
		zap.String("server", node.Server),
		zap.Int("port", node.Port),
	)

	return nil
}

// Close shuts down the sing-box process.
func (r *Rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cancel != nil {
		r.cancel()
	}

	if r.cmd != nil && r.running {
		r.cmd.Process.Signal(os.Interrupt)
		// Wait up to 2 seconds for graceful shutdown. The monitor goroutine
		// in Start() owns cmd.Wait(); we just listen for its result.
		if r.waitCh != nil {
			select {
			case <-r.waitCh:
			case <-time.After(2 * time.Second):
				r.cmd.Process.Kill()
				// Give the monitor goroutine a moment to reap the killed process
				select {
				case <-r.waitCh:
				case <-time.After(2 * time.Second):
				}
			}
		}
		r.running = false
	}

	// Clean up config file
	if r.configPath != "" {
		os.Remove(r.configPath)
	}

	r.logger.Info("sing-box closed")
	return nil
}

// IsRunning returns whether the sing-box process is running.
func (r *Rotator) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// GetListenAddr returns the proxy listen address.
func (r *Rotator) GetListenAddr() string {
	return fmt.Sprintf("%s:%d", r.listenAddr, r.listenPort)
}

// logSingboxVersion logs which sing-box binary and version is in use, so
// version-specific issues (e.g. DNS schema changes) are easy to diagnose.
func logSingboxVersion(logger *zap.Logger, singboxPath string) {
	out, err := exec.Command(singboxPath, "version").Output()
	if err != nil {
		logger.Debug("could not read sing-box version", zap.String("path", singboxPath), zap.Error(err))
		return
	}
	logger.Info("using sing-box",
		zap.String("version", strings.TrimSpace(string(out))),
		zap.String("path", singboxPath),
	)
}

// ensurePortFree verifies the proxy listen port is available, killing any
// stale sing-box left over from a previous (possibly crashed) run that
// still holds it. Returns a clear error only if the port is still taken
// after cleanup — e.g. by an unrelated process.
func ensurePortFree(addr string, port int) error {
	probe := func() bool {
		ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", addr, port))
		if err != nil {
			return false
		}
		ln.Close()
		return true
	}

	if probe() {
		return nil
	}

	// Port occupied — almost certainly a stale sing-box from a run that
	// crashed or was killed before its child was reaped. rotaze is the
	// only manager of sing-box on the device, so killing sing-box
	// processes is safe here. Retry once after cleanup.
	exec.Command("pkill", "-9", "-f", "sing-box").Run()
	time.Sleep(500 * time.Millisecond)

	if probe() {
		return nil
	}
	return fmt.Errorf("listen port %s:%d is in use by another process; free it and retry", addr, port)
}

// findSingbox locates the sing-box binary.
func findSingbox() (string, error) {
	// Check common locations
	locations := []string{
		"sing-box",
		"/usr/local/bin/sing-box",
		"/usr/bin/sing-box",
		filepath.Join(os.Getenv("HOME"), ".local/bin/sing-box"),
	}

	for _, loc := range locations {
		path, err := exec.LookPath(loc)
		if err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("sing-box binary not found in PATH or common locations")
}
