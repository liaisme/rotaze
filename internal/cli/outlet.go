// Outlet manager for the daemon: reconciles `daemon.state` outlets against
// running rotator instances. Each outlet gets its own rotator on the port
// recorded in state; rotators are keyed by outlet ID so add/rm and SIGHUP
// reloads converge.
//
// An outlet only counts as running after it has been verified end-to-end: a
// candidate node is started on its rotator and an HTTP request is probed
// through the outlet's own SOCKS address. Candidates that refuse or time out
// fall through to the next one. Once verified, a monitor goroutine probes
// the outlet periodically and rotates to another node when the connection
// dies.
package cli

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/internal/rotator"
	"github.com/rotaze/rotaze/pkg/types"
)

// outletRot is the rotator surface the outlet manager needs. *rotator.Rotator
// implements it; verification takes it as a parameter so tests can drive the
// candidate loop without starting sing-box.
type outletRot interface {
	Start(ctx context.Context, node *types.Node) error
	SwitchOutbound(ctx context.Context, node *types.Node) error
	GetListenAddr() string
	IsRunning() bool
	Close() error
}

// outletRuntime is a live outlet: its rotator, the node it is serving, the
// cancel func for its verify/monitor goroutine, and whether the end-to-end
// probe has passed yet. Reconcile uses verified to tell "still starting"
// from "serving".
type outletRuntime struct {
	id       int
	rot      outletRot
	node     *types.Node
	cancel   context.CancelFunc
	verified bool
	// port/addr/country/protocol are fixed when the runtime is spawned: the
	// rotator's listen address is always 127.0.0.1:<port>. They are cached
	// here so Reconcile can read them without locking the rotator — a
	// rotator mid-Start holds its own lock for the whole startup, and
	// touching it under m.mu would stall every other outlet.
	port     int
	addr     string
	country  string
	protocol string
}

// outletConfig carries the daemon-side inputs of the manager: the probe
// machinery (injected as a function so tests can script node health), the
// monitor cadence, and the candidate selector shared by initial verification
// and rotation.
type outletConfig struct {
	primaryPort  int
	cache        *cache.Cache
	probe        func(ctx context.Context, addr string) (int, time.Duration, error)
	monitorEvery time.Duration
	maxRetries   int
	candidates   func(country, protocol, preferred string) []*types.Node
	// newRot builds an outlet's rotator. Defaults to the real rotator; tests
	// inject a fake to exercise verification without starting sing-box.
	newRot func(logger *zap.Logger, dataDir string, port int) outletRot
}

// outletManager owns the additional proxy outlets of a running daemon.
// The primary rotator (configured proxy) is not managed here.
type outletManager struct {
	mu      sync.Mutex
	running map[int]*outletRuntime // keyed by outlet ID
	primary int                    // port of the daemon's own proxy
	// lastErr remembers the last failure message per outlet so repeated
	// identical failures (a statically disabled protocol on every reconcile
	// pass, a pool that never yields a working node) degrade to Debug instead
	// of spamming WARN.
	lastErr map[int]string

	cache        *cache.Cache
	probe        func(ctx context.Context, addr string) (int, time.Duration, error)
	monitorEvery time.Duration
	maxRetries   int
	candidates   func(country, protocol, preferred string) []*types.Node
	newRot       func(logger *zap.Logger, dataDir string, port int) outletRot

	// onUpdate is called after an outlet becomes verified or rotates, so the
	// daemon can republish state without waiting for the reconcile cadence.
	onUpdate func()
}

func newOutletManager(cfg outletConfig) *outletManager {
	if cfg.monitorEvery <= 0 {
		cfg.monitorEvery = 30 * time.Second
	}
	if cfg.maxRetries < 0 {
		cfg.maxRetries = 0
	}
	if cfg.newRot == nil {
		cfg.newRot = func(logger *zap.Logger, dataDir string, port int) outletRot {
			return rotator.New(logger, &rotator.Config{
				ListenAddr: "127.0.0.1",
				ListenPort: port,
				DataDir:    dataDir,
			})
		}
	}
	return &outletManager{
		running:      make(map[int]*outletRuntime),
		primary:      cfg.primaryPort,
		lastErr:      make(map[int]string),
		cache:        cfg.cache,
		probe:        cfg.probe,
		monitorEvery: cfg.monitorEvery,
		maxRetries:   cfg.maxRetries,
		candidates:   cfg.candidates,
		newRot:       cfg.newRot,
	}
}

// Reconcile brings running outlets in line with the outlets recorded in
// daemon state: outlets that disappeared are stopped, and new ones get a
// verify-and-monitor goroutine. The returned list carries live status
// (Running/Addr/Node/NodeID) so the caller can persist it; an outlet still
// being verified reports Running=false until its end-to-end probe passes.
//
// Lock discipline: m.mu guards the bookkeeping maps and the cached runtime
// fields only. Rotator methods (Start/SwitchOutbound/IsRunning/Close) are
// never called while it is held: a rotator holds its own lock for the whole
// sing-box startup, so touching one under m.mu would stall every other
// outlet and the daemon's reconcile cadence. Liveness of a running outlet is
// therefore checked by its own goroutine (monitorOutlet), and a removed
// outlet's rotator is closed after the lock is released.
func (m *outletManager) Reconcile(ctx context.Context, logger *zap.Logger, dataDir string, outlets []daemonOutlet) []daemonOutlet {
	desired := make(map[int]daemonOutlet, len(outlets))
	for _, o := range outlets {
		desired[o.ID] = o
	}

	var needStop []outletStop
	m.mu.Lock()

	// Stop outlets that are no longer wanted. Their goroutines are cancelled
	// now; the rotator itself is closed outside the lock.
	for id, rt := range m.running {
		if _, ok := desired[id]; !ok {
			rt.cancel()
			needStop = append(needStop, outletStop{id, rt})
			delete(m.running, id)
			delete(m.lastErr, id)
		}
	}

	// Ports held by live outlets. State is edited by a separate CLI process,
	// so a port recorded there can already be taken by a sibling outlet.
	// Ports come from the cached runtime fields, never from the rotator.
	ports := map[int]int{m.primary: 0}
	for id, rt := range m.running {
		ports[rt.port] = id
	}

	out := make([]daemonOutlet, 0, len(desired))
	for _, o := range desired {
		rt, ok := m.running[o.ID]
		if !ok {
			if owner, clash := ports[o.Port]; clash {
				logger.Warn("outlet port already in use, skipping",
					zap.Int("outlet", o.ID), zap.Int("port", o.Port), zap.Int("used_by", owner))
				o.Running, o.Node, o.NodeID = false, "", 0
				out = append(out, o)
				continue
			}

			m.startOutlet(ctx, logger, dataDir, o)
			ports[o.Port] = o.ID

			o.Running, o.Node, o.NodeID = false, "", 0
			out = append(out, o)
			logger.Info("outlet queued for verification",
				zap.Int("outlet", o.ID), zap.Int("port", o.Port),
				zap.String("country", o.Country), zap.String("protocol", o.Protocol))
			continue
		}

		if !rt.verified {
			// Verification in flight: report pending, its goroutine will
			// publish the outcome when it finishes.
			o.Running, o.Node, o.NodeID = false, "", 0
			out = append(out, o)
			continue
		}

		// Serving: report the cached status. If the embedded sing-box died,
		// its own goroutine notices within one monitor interval, unregisters
		// itself, and the next reconcile pass builds a fresh runtime.
		o.Running = true
		o.Addr = rt.addr
		o.NodeID = rt.node.ID
		o.Node = describeNode(rt.node)
		// Show the node's last measured probe latency. probeOnce writes it
		// to the stats cache immediately after each verify/monitor probe,
		// so a verified outlet has a row; missing stats leave MS at 0,
		// printed as "-".
		if st, err := m.cache.GetNodeStats(rt.node.ID); err == nil {
			o.MS = st.LatencyMS
		}
		out = append(out, o)
	}
	m.mu.Unlock()

	// Close the rotators of removed outlets outside the lock: Close waits on
	// the rotator's own lock, which a slow startup may hold for seconds. Each
	// close runs in its own goroutine so one removal cannot stall this pass
	// (and with it the status publish) for every other outlet.
	for _, s := range needStop {
		go stopOutlet(logger, s.id, s.rt.rot)
	}
	return out
}

// startOutlet registers a runtime for o and launches the goroutine that
// verifies, then monitors it. The caller must hold m.mu.
func (m *outletManager) startOutlet(ctx context.Context, logger *zap.Logger, dataDir string, o daemonOutlet) {
	vctx, cancel := context.WithCancel(ctx)
	rt := &outletRuntime{
		id:       o.ID,
		rot:      m.newRot(logger, dataDir, o.Port),
		cancel:   cancel,
		port:     o.Port,
		addr:     fmt.Sprintf("127.0.0.1:%d", o.Port),
		country:  o.Country,
		protocol: o.Protocol,
	}
	m.running[o.ID] = rt
	go m.runOutlet(vctx, logger, o, rt)
}

// unregister drops rt from the running set if it is still the registered
// runtime for id; a concurrent reconcile may already have replaced it.
func (m *outletManager) unregister(id int, rt *outletRuntime) {
	m.mu.Lock()
	if m.running[id] == rt {
		delete(m.running, id)
	}
	m.mu.Unlock()
}

// runOutlet owns one outlet for the lifetime of its context: it verifies the
// outlet end-to-end, then monitors it. On verification failure it
// unregisters itself so the next reconcile pass retries with a fresh
// candidate list.
func (m *outletManager) runOutlet(ctx context.Context, logger *zap.Logger, o daemonOutlet, rt *outletRuntime) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("outlet goroutine panicked",
				zap.Int("outlet", o.ID),
				zap.String("country", o.Country),
				zap.Any("panic", r),
				zap.Stack("stack"))
			_ = rt.rot.Close()
			m.unregister(o.ID, rt)
		}
	}()
	node := m.verifyOutlet(ctx, logger, rt.rot, o, m.candidates(o.Country, o.Protocol, o.Node))
	if node == nil {
		_ = rt.rot.Close()
		m.unregister(o.ID, rt)
		return
	}

	m.mu.Lock()
	rt.node = node
	rt.verified = true
	delete(m.lastErr, o.ID)
	m.mu.Unlock()
	logger.Info("outlet verified",
		zap.Int("outlet", o.ID),
		zap.String("addr", rt.rot.GetListenAddr()),
		zap.String("country", o.Country),
		zap.String("node", describeNode(node)))
	if m.onUpdate != nil {
		m.onUpdate()
	}
	m.monitorOutlet(ctx, logger, o, rt)
}

// verifyOutlet starts r on the first candidate that actually carries
// traffic through the outlet: every candidate is started (or switched to)
// and then probed over the outlet's local SOCKS address. Candidates that
// fail are recorded as failed checks in the node stats and the next one is
// tried. Returns the connected node, or nil when no candidate connects or
// the context is cancelled.
func (m *outletManager) verifyOutlet(ctx context.Context, logger *zap.Logger, r outletRot, o daemonOutlet, candidates []*types.Node) *types.Node {
	fail := func(msg string, detail error) *types.Node {
		if ctx.Err() != nil {
			// Shutdown or `proxy rm`: expected, not a failure worth logging.
			return nil
		}
		if detail != nil {
			logger.Debug("outlet verification detail", zap.Int("outlet", o.ID), zap.Error(detail))
		}
		// WARN with candidate count to surface exhausted-pool failures
		logger.Warn("outlet exhausted all candidates",
			zap.Int("outlet", o.ID),
			zap.String("listen", o.Addr),
			zap.String("country", o.Country),
			zap.Int("candidates", len(candidates)),
			zap.String("reason", msg))
		m.noteDown(logger, o.ID, msg)
		return nil
	}

	if len(candidates) == 0 {
		return fail("no usable node in pool", nil)
	}

	started := false
	var lastErr error
	for _, node := range candidates {
		if ctx.Err() != nil {
			return nil
		}
		if !started {
			lastErr = r.Start(ctx, node)
		} else {
			lastErr = r.SwitchOutbound(ctx, node)
		}
		if lastErr != nil {
			// This candidate cannot even be configured on the rotator; log and
			// give the next one its own chance.
			logger.Info("outlet candidate start failed",
				zap.Int("outlet", o.ID),
				zap.String("node", describeNode(node)),
				zap.Bool("first", !started),
				zap.Error(lastErr))
			continue
		}
		started = true
		status, latency, perr := m.probe(ctx, r.GetListenAddr())
		if perr == nil && status >= 200 && status < 400 {
			m.cache.UpdateNodeStats(node.ID, latency.Milliseconds(), true)
			return node
		}
		m.cache.UpdateNodeStats(node.ID, latency.Milliseconds(), false)
		if perr != nil {
			lastErr = perr
		} else {
			lastErr = fmt.Errorf("HTTP %d", status)
		}
	}
	return fail("no node passed end-to-end verification", lastErr)
}

// monitorOutlet probes a verified outlet every monitorEvery. A successful
// probe records the node stats; after maxRetries failed probes in a row (the
// same retry budget the checker uses before rotating) the outlet rotates to
// another node until one carries traffic again. It also owns liveness: when
// the embedded sing-box has stopped it unregisters the runtime (after closing
// the rotator) so the next reconcile pass re-verifies from scratch.
func (m *outletManager) monitorOutlet(ctx context.Context, logger *zap.Logger, o daemonOutlet, rt *outletRuntime) {
	ticker := time.NewTicker(m.monitorEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// The embedded sing-box stopped underneath us (crashed, or the
			// rotator was torn down): there is nothing to probe or rotate.
			// Unregister so the next reconcile builds a fresh runtime and
			// verifies again. IsRunning only ever touches this goroutine's
			// own rotator, so it cannot block on a sibling's startup.
			if !rt.rot.IsRunning() {
				logger.Warn("outlet sing-box stopped, will be re-verified", zap.Int("outlet", o.ID))
				m.unregister(o.ID, rt)
				if err := rt.rot.Close(); err != nil {
					logger.Warn("outlet close failed", zap.Int("outlet", o.ID), zap.Error(err))
				}
				return
			}

			m.mu.Lock()
			node := rt.node
			m.mu.Unlock()
			healthy := false
			for attempt := 0; attempt <= m.maxRetries; attempt++ {
				if attempt > 0 {
					backoff := time.Duration(1<<uint(attempt-1)) * time.Second
					if backoff > 4*time.Second {
						backoff = 4 * time.Second
					}
					select {
					case <-ctx.Done():
						return
					case <-time.After(backoff):
					}
				}
				if m.probeOnce(ctx, rt, node) {
					healthy = true
					break
				}
			}
			if healthy {
				continue
			}
			logger.Warn("outlet connection lost, rotating",
				zap.Int("outlet", o.ID),
				zap.String("addr", rt.rot.GetListenAddr()),
				zap.Int("failed_probes", m.maxRetries+1))
			m.rotateOutlet(ctx, logger, o, rt)
		}
	}
}

// probeOnce performs one end-to-end HTTP probe through the outlet and
// records the outcome against node.
func (m *outletManager) probeOnce(ctx context.Context, rt *outletRuntime, node *types.Node) bool {
	status, latency, err := m.probe(ctx, rt.rot.GetListenAddr())
	ok := err == nil && status >= 200 && status < 400
	if node != nil {
		m.cache.UpdateNodeStats(node.ID, latency.Milliseconds(), ok)
	}
	return ok
}

// rotateOutlet switches the outlet to the next candidate that passes an
// end-to-end probe, trying each candidate once. It returns after the first
// candidate that carries traffic; when none does, the next monitor tick
// probes again and retries the cycle.
func (m *outletManager) rotateOutlet(ctx context.Context, logger *zap.Logger, o daemonOutlet, rt *outletRuntime) {
	m.mu.Lock()
	current := rt.node
	m.mu.Unlock()

	for _, node := range m.candidates(o.Country, o.Protocol, o.Node) {
		if current != nil && node.ID == current.ID {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if err := rt.rot.SwitchOutbound(ctx, node); err != nil {
			logger.Debug("outlet switch failed",
				zap.Int("outlet", o.ID),
				zap.String("node", describeNode(node)),
				zap.Error(err))
			continue
		}
		if m.probeOnce(ctx, rt, node) {
			if current != nil {
				m.cache.RecordRotation(current.ID, node.ID, "outlet-failure", "success")
			}
			m.mu.Lock()
			rt.node = node
			m.mu.Unlock()
			logger.Info("outlet rotated",
				zap.Int("outlet", o.ID),
				zap.String("node", describeNode(node)))
			if m.onUpdate != nil {
				m.onUpdate()
			}
			return
		}
	}
	m.noteDown(logger, o.ID, "no reachable node to rotate to")
}

// noteDown records an outlet-level failure. The first occurrence of a
// message logs at WARN; repeats (the reconcile cadence retried the same
// failure) degrade to Debug.
func (m *outletManager) noteDown(logger *zap.Logger, id int, msg string) {
	m.mu.Lock()
	dup := m.lastErr[id] == msg
	m.lastErr[id] = msg
	m.mu.Unlock()
	if dup {
		logger.Debug("outlet down", zap.Int("outlet", id), zap.String("reason", msg))
		return
	}
	logger.Warn("outlet down", zap.Int("outlet", id), zap.String("reason", msg))
}

// outletCandidates returns the ordered nodes an outlet may try for its
// country/protocol filter: nodes with a poor success record are left out
// (they already burned their checks), falling back to the full ordered list
// when every node has bad stats so the outlet keeps retrying instead of
// starving on stale data. Order is the shared preference order (socks, ss,
// vless, trojan, hy2, ...) and only nodes with usable credentials are
// considered.
func outletCandidates(c *cache.Cache, country, protocol string) []*types.Node {
	nodes, err := c.GetNodes()
	if err != nil || len(nodes) == 0 {
		return nil
	}
	nodes = filterNodesByCountry(nodes, country)
	nodes = filterNodesByProtocol(nodes, protocol)

	// One stats pass instead of per-node queries (same pattern as pool's
	// statsIndex): this runs on every verification and rotation.
	stats := make(map[int]*types.NodeStats)
	if all, err := c.GetNodeStatsAll(); err == nil {
		for _, s := range all {
			stats[s.NodeID] = s
		}
	}
	usable := make([]*types.Node, 0, len(nodes))
	for _, n := range nodes {
		if s, ok := stats[n.ID]; ok && s.TotalChecks > 5 && s.SuccessRate < 0.25 {
			continue
		}
		usable = append(usable, n)
	}
	if len(usable) == 0 {
		usable = nodes
	}
	return orderedCandidates(usable, 0)
}

// outletStop is a runtime scheduled for shutdown: the manager cancels it
// under m.mu and closes its rotator afterwards, outside the lock.
type outletStop struct {
	id int
	rt *outletRuntime
}

// Close stops every managed outlet. It cancels the verify/monitor goroutines
// under m.mu and closes the rotators off the lock, without waiting: a close
// waits on the rotator's own lock, which a sing-box startup may hold for
// minutes, and Close runs on the daemon's shutdown path — waiting there kept
// the process alive long after it had printed that it stopped. The rotators
// still get closed, just not before the caller returns.
func (m *outletManager) Close() {
	var stops []outletStop
	m.mu.Lock()
	for id, rt := range m.running {
		rt.cancel()
		stops = append(stops, outletStop{id, rt})
		delete(m.running, id)
	}
	m.mu.Unlock()
	for _, s := range stops {
		go stopOutlet(globalLogger, s.id, s.rt.rot)
	}
}

func stopOutlet(logger *zap.Logger, id int, r outletRot) {
	if err := r.Close(); err != nil {
		if logger != nil {
			logger.Warn("outlet close failed", zap.Int("outlet", id), zap.Error(err))
		}
		return
	}
	if logger != nil {
		logger.Info("outlet stopped", zap.Int("outlet", id))
	}
}

func describeNode(n *types.Node) string {
	return fmt.Sprintf("%s:%d (%s, %s)", n.Server, n.Port, n.Protocol, n.Country)
}

// parseNodeString parses a daemonOutlet.Node string (format:
// "server:port (protocol, country)") into a *types.Node.
// Returns nil if parsing fails or format is invalid.
func parseNodeString(s string) *types.Node {
	if s == "" {
		return nil
	}
	// Expected format: "server:port (protocol, country)"
	// Find the opening parenthesis
	parenIdx := strings.LastIndex(s, " (")
	if parenIdx == -1 {
		return nil
	}
	// Extract server:port part
	serverPort := strings.TrimSpace(s[:parenIdx])
	// Extract (protocol, country) part
	protoCountry := strings.TrimSpace(s[parenIdx+2:])
	if !strings.HasSuffix(protoCountry, ")") {
		return nil
	}
	protoCountry = protoCountry[:len(protoCountry)-1] // remove trailing )

	// Parse server:port
	host, portStr, err := net.SplitHostPort(serverPort)
	if err != nil {
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil
	}

	// Parse protocol, country
	parts := strings.Split(protoCountry, ", ")
	if len(parts) != 2 {
		return nil
	}
	protocol := strings.TrimSpace(parts[0])
	country := strings.TrimSpace(parts[1])

	return &types.Node{
		Server:   host,
		Port:     port,
		Protocol: protocol,
		Country:  country,
	}
}
