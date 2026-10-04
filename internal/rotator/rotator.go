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
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/rotaze/rotaze/pkg/types"
	"syscall"
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
		// Per-instance config file. Several rotators may run side by side
		// (one per proxy port), so the path is keyed by listen port —
		// a shared filename would have one instance rewrite the config
		// another instance is currently running.
		configPath: filepath.Join(cfg.DataDir, fmt.Sprintf("singbox-%d.json", cfg.ListenPort)),
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

	// OpenVPN client certs are stored PEM strings in the DB: sing-box's
	// openvpn-client endpoint requires *paths*, so materialize them under the
	// data dir before building the config.
	node, err := r.materializeOVPNCerts(node)
	if err != nil {
		return fmt.Errorf("failed to materialize OpenVPN certs: %w", err)
	}

	// Build config and write to file (path assigned in New, unique per port)
	config := BuildConfig(node, r.listenAddr, r.listenPort)
	if err := os.WriteFile(r.configPath, config, 0600); err != nil {
		return fmt.Errorf("failed to write sing-box config: %w", err)
	}
	r.logger.Debug("wrote sing-box config",
		zap.String("path", r.configPath),
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
	if err := ensurePortFree(r.listenAddr, r.listenPort, r.configPath); err != nil {
		return err
	}

	// Start sing-box
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel

	cmd := exec.CommandContext(ctx, singboxPath, "run", "-c", r.configPath)
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

	// Wait for sing-box to accept connections. OpenVPN endpoints take
	// seconds to establish the tunnel (control handshake + TLS), so a fixed
	// sleep would report "started" before the route can carry traffic.
	waitForReady(r.listenAddr, r.listenPort, 15*time.Second)

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
	// OpenVPN certs must be materialized to files for the endpoint config.
	node, err := r.materializeOVPNCerts(node)
	if err != nil {
		return fmt.Errorf("failed to materialize OpenVPN certs: %w", err)
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
		zap.String("server", net.JoinHostPort(node.Server, strconv.Itoa(node.Port))),
	)

	// Find sing-box binary
	singboxPath, err := findSingbox()
	if err != nil {
		return fmt.Errorf("sing-box not found: %w", err)
	}

	// Free the listen port (stale process cleanup) before starting the
	// replacement; our old process was stopped above.
	if err := ensurePortFree(r.listenAddr, r.listenPort, r.configPath); err != nil {
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

	// Wait for sing-box to accept connections (OpenVPN tunnel may need
	// several seconds to establish before traffic can be routed).
	waitForReady(r.listenAddr, r.listenPort, 15*time.Second)

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

// waitForReady polls the given listen address until a TCP connection is
// accepted or the timeout elapses. A SOCKS inbound accepts immediately once
// sing-box is up, so a successful dial means the proxy can be used (the
// OpenVPN endpoint may still be establishing its tunnel, but sing-box will
// queue/retry the connection until it is). Returns true if the port is up.
func waitForReady(addr string, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(addr, strconv.Itoa(port)), time.Second)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// IsRunning returns whether the sing-box process is running.
func (r *Rotator) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// GetListenAddr returns the proxy listen address.
func (r *Rotator) GetListenAddr() string {
	return net.JoinHostPort(r.listenAddr, strconv.Itoa(r.listenPort))
}

// GetPort returns the proxy listen port.
func (r *Rotator) GetPort() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listenPort
}

// materializeOVPNCerts writes OpenVPN PEM material to disk under the data
// dir. sing-box's openvpn-client endpoint takes certificate *paths*, not
// inline text, so the DB strings must become files. Returns a node copy with
// paths filled in; the original is untouched (no DB write here).
//
// Filenames are keyed by listen port, like configPath: several outlets may
// run side by side, and shared names would let one outlet's certs be
// overwritten while another outlet's sing-box still references them.
func (r *Rotator) materializeOVPNCerts(node *types.Node) (*types.Node, error) {
	if node.Protocol != "ovpn" {
		return node, nil
	}
	if node.CACert == "" || node.ClientCert == "" || node.ClientKey == "" {
		return nil, fmt.Errorf("OpenVPN node %d missing certificate material", node.ID)
	}
	copy := *node
	writePEM := func(name, content string) (string, error) {
		p := filepath.Join(r.config.DataDir, fmt.Sprintf("ovpn-%d-%s", r.listenPort, name))
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			return "", err
		}
		return p, nil
	}
	var err error
	if copy.CACert, err = writePEM("ca.pem", node.CACert); err != nil {
		return nil, fmt.Errorf("write ca: %w", err)
	}
	if copy.ClientCert, err = writePEM("client.pem", node.ClientCert); err != nil {
		return nil, fmt.Errorf("write client cert: %w", err)
	}
	if copy.ClientKey, err = writePEM("client.key", node.ClientKey); err != nil {
		return nil, fmt.Errorf("write client key: %w", err)
	}
	return &copy, nil
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

// ensurePortFree verifies that the proxy listen port is bindable, killing a
// stale sing-box that is holding *that specific port* if one is found.
//
// The kill must stay scoped to the port: when several rotators run side by
// side (one per proxy port) a global `pkill -f sing-box` would take down the
// healthy live outlets as well. Only a sing-box process whose command line
// references this rotator's own config file is treated as ours to reap; a
// foreign holder yields a clear error instead.
func ensurePortFree(addr string, port int, configPath string) error {
	probe := func() bool {
		ln, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
		if err != nil {
			return false
		}
		ln.Close()
		return true
	}

	if probe() {
		return nil
	}

	// Port occupied. Reap only our own stale sing-box for this port.
	if pid, ok := findStaleSingboxPID(port, configPath); ok {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGKILL)
		}
		// Give the kernel a moment to release the listening socket.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if probe() {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	if probe() {
		return nil
	}
	return fmt.Errorf("listen port %s is in use by another process; free it and retry",
		net.JoinHostPort(addr, strconv.Itoa(port)))
}

// findStaleSingboxPID looks for a running sing-box process bound to the given
// port whose command line references configPath. Matching the config file is
// what makes a kill safe: it proves the process belongs to this rotator
// instance (and its port) and not to another live outlet.
//
// Port ownership is resolved through the kernel rather than by parsing tool
// output: /proc/net/tcp{,6} gives the socket inode of the listening port, and
// /proc/<pid>/fd symlinks map that inode back to the owning PID. Where /proc
// is unavailable the config-file match still bounds the kill to this
// instance, so a sibling outlet on another port is never touched.
func findStaleSingboxPID(port int, configPath string) (int, bool) {
	base := filepath.Base(configPath)
	self := os.Getpid()

	for _, pid := range singboxProcessIDs() {
		if pid == self {
			continue
		}
		cmdline, ok := readCmdline(pid)
		if !ok || !strings.Contains(cmdline, "sing-box") {
			continue
		}
		if !strings.Contains(cmdline, base) {
			// Some sing-box, but not the one serving this port.
			continue
		}
		if holders, ok := pidsListeningOn(port); ok && holders[pid] {
			return pid, true
		}
	}
	return 0, false
}

// singboxProcessIDs lists PIDs whose executable name is sing-box.
func singboxProcessIDs() []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		comm, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(comm)) != "sing-box" {
			continue
		}
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

// readCmdline returns the NUL-joined command line of a process as a string.
func readCmdline(pid int) (string, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " ")), true
}

// pidsListeningOn returns the set of PIDs listening on the given TCP port,
// resolved via /proc socket inodes. ok is false on platforms without /proc.
func pidsListeningOn(port int) (map[int]bool, bool) {
	inodes, ok := listeningSocketInodes(port)
	if !ok || len(inodes) == 0 {
		return nil, false
	}
	want := make(map[string]bool, len(inodes))
	for _, in := range inodes {
		want[in] = true
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, false
	}
	holders := make(map[int]bool)
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join("/proc", e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if ino, ok := socketInode(link); ok && want[ino] {
				holders[pid] = true
				break
			}
		}
	}
	if len(holders) == 0 {
		return nil, false
	}
	return holders, true
}

// listeningSocketInodes returns socket inodes in LISTEN state on the port.
func listeningSocketInodes(port int) ([]string, bool) {
	var inodes []string
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			fields := strings.Fields(line)
			// sl local_address rem_address st tx* rx* tr* tm* when retrnsmt uid timeout inode
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			_, portHex, ok := strings.Cut(fields[1], ":")
			if !ok {
				continue
			}
			p, err := strconv.ParseInt(portHex, 16, 32)
			if err != nil || int(p) != port {
				continue
			}
			inodes = append(inodes, fields[9])
		}
	}
	if len(inodes) == 0 {
		return nil, true
	}
	return inodes, true
}

// socketInode extracts the inode from a /proc fd symlink like "socket:[12345]".
func socketInode(link string) (string, bool) {
	rest, ok := strings.CutPrefix(link, "socket:[")
	if !ok {
		return "", false
	}
	ino, ok := strings.CutSuffix(rest, "]")
	return ino, ok
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
