// Package rotator runs sing-box in-process and owns the proxy listener.
// sing-box is linked as a library (github.com/sagernet/sing-box): there is no
// external binary to install, no config file on disk and no child process to
// supervise — each Rotator owns one *singbox.Box instance for its port.
package rotator

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	singbox "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
	"go.uber.org/zap"

	"github.com/rotaze/rotaze/pkg/types"
)

// singBoxModulePath identifies the linked sing-box library in the build info.
const singBoxModulePath = "github.com/sagernet/sing-box"

// Rotator manages an embedded sing-box instance for proxy operations.
type Rotator struct {
	logger     *zap.Logger
	config     *Config
	mu         sync.Mutex
	instance   *singbox.Box
	cancel     context.CancelFunc
	listenAddr string
	listenPort int
	running    bool
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

// Start initializes and starts an embedded sing-box instance for the node.
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

	if err := r.startLocked(ctx, node); err != nil {
		return err
	}

	r.logger.Info("sing-box started (embedded)",
		zap.String("listen", net.JoinHostPort(r.listenAddr, strconv.Itoa(r.listenPort))),
		zap.String("outbound", node.Tag),
		zap.String("protocol", node.Protocol),
	)
	return nil
}

// SwitchOutbound changes the active outbound node by replacing the sing-box
// instance. A new box is built rather than mutated: sing-box has no supported
// in-place outbound swap, and rebuilding is cheap (in-process start is
// milliseconds) compared to re-establishing the tunnel itself.
func (r *Rotator) SwitchOutbound(ctx context.Context, node *types.Node) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	node, err := r.materializeOVPNCerts(node)
	if err != nil {
		return fmt.Errorf("failed to materialize OpenVPN certs: %w", err)
	}

	if err := r.startLocked(ctx, node); err != nil {
		return err
	}

	r.logger.Info("switched outbound",
		zap.String("outbound", node.Tag),
		zap.String("server", node.Server),
		zap.Int("port", node.Port),
	)
	return nil
}

// startLocked builds and starts a fresh box, replacing any running instance.
// Caller must hold r.mu.
func (r *Rotator) startLocked(ctx context.Context, node *types.Node) error {
	// Tear the old instance down first so it releases the listen port.
	if err := r.stopLocked(); err != nil {
		return fmt.Errorf("failed to stop previous instance: %w", err)
	}

	// sing-box parses its own JSON schema into typed options; building the
	// config as JSON keeps one source of truth for every protocol and stays
	// correct when sing-box's schema moves, instead of pinning rotaze to
	// sing-box's Go structs.
	configJSON := BuildConfig(node, r.listenAddr, r.listenPort)

	// include.Context registers every protocol/dns/endpoint type; without it
	// sing-box rejects unknown types while parsing.
	boxCtx, cancel := context.WithCancel(include.Context(service.ExtendContext(ctx)))

	options, err := json.UnmarshalExtendedContext[option.Options](boxCtx, configJSON)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to build sing-box options: %w", err)
	}

	instance, err := singbox.New(singbox.Options{
		Context: boxCtx,
		Options: options,
	})
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create sing-box instance: %w", err)
	}

	if err := instance.Start(); err != nil {
		_ = instance.Close()
		cancel()
		return fmt.Errorf("failed to start sing-box: %w", err)
	}

	r.instance = instance
	r.cancel = cancel
	r.running = true

	r.logger.Debug("sing-box instance started",
		zap.String("outbound", node.Tag),
		zap.String("protocol", node.Protocol),
		zap.String("server", net.JoinHostPort(node.Server, strconv.Itoa(node.Port))),
		zap.Int("listen_port", r.listenPort),
	)

	// The listener is up once Start returns, but an OpenVPN tunnel needs
	// seconds more before it can carry traffic, so wait for the port to accept
	// before reporting success.
	if !waitForReady(r.listenAddr, r.listenPort, 15*time.Second) {
		if err := r.stopLocked(); err != nil {
			r.logger.Warn("cleanup after failed readiness check", zap.Error(err))
		}
		return fmt.Errorf("sing-box did not accept connections on %s",
			net.JoinHostPort(r.listenAddr, strconv.Itoa(r.listenPort)))
	}

	return nil
}

// Close shuts down the embedded sing-box instance.
func (r *Rotator) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.stopLocked(); err != nil {
		return err
	}
	r.logger.Info("sing-box closed (embedded)")
	return nil
}

// stopLocked closes the running instance and releases the listen port.
// Caller must hold r.mu.
func (r *Rotator) stopLocked() error {
	instance := r.instance
	r.instance = nil
	r.cancel = nil
	r.running = false
	if instance == nil {
		return nil
	}

	// Close before cancelling the context: Close drains in-flight work, while
	// a cancelled context would abort that shutdown mid-flight.
	err := instance.Close()
	if r.cancel != nil {
		r.cancel()
	}
	if err != nil {
		return fmt.Errorf("failed to close sing-box: %w", err)
	}
	return nil
}

// waitForReady polls the given listen address until a TCP connection is
// accepted or the timeout elapses. Returns true if the port is up.
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

// IsRunning returns whether the embedded sing-box instance is running.
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
// Filenames are keyed by listen port: several outlets may run side by side,
// and shared names would let one outlet's certs be overwritten while another
// outlet's sing-box still references them.
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

// logEmbeddedSingboxVersion reports which sing-box library version is linked,
// so a behavior difference can be traced to the dependency instead of guessed.
func logEmbeddedSingboxVersion(logger *zap.Logger) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		logger.Debug("build info unavailable; cannot report sing-box version")
		return
	}
	for _, dep := range info.Deps {
		if dep.Path == singBoxModulePath {
			logger.Info("using embedded sing-box", zap.String("version", dep.Version))
			return
		}
	}
	logger.Info("using embedded sing-box",
		zap.String("version", strings.TrimSpace(info.Main.Version)),
		zap.String("module", "unknown"),
	)
}
