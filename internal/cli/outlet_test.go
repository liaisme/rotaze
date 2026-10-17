package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/pkg/types"
)

// testCache builds a file-backed cache for the outlet tests: verification
// runs in a goroutine while the test asserts, which an in-memory SQLite DB
// cannot serve (each pooled connection would get its own database).
func testCache(t *testing.T) *cache.Cache {
	t.Helper()
	c, err := cache.NewCache(filepath.Join(t.TempDir(), "outlet.db"))
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// fakeOutletRot is an outletRot that records lifecycle calls instead of
// starting sing-box.
type fakeOutletRot struct {
	mu        sync.Mutex
	port      int
	addr      string
	running   bool
	node      *types.Node
	startIDs  []int
	switchIDS []int
	closed    bool
	// gate, when non-nil, makes IsRunning and Close hold the rotator lock
	// until the channel is closed — the way the real rotator holds its lock
	// while sing-box starts or a switch is in flight. A test opens it to
	// emulate a rotator busy with a long operation.
	gate chan struct{}
}

func newFakeOutletRot(port int) *fakeOutletRot {
	return &fakeOutletRot{port: port, addr: fmt.Sprintf("127.0.0.1:%d", port)}
}

func (f *fakeOutletRot) Start(_ context.Context, node *types.Node) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.node = node
	f.running = true
	f.startIDs = append(f.startIDs, node.ID)
	return nil
}

func (f *fakeOutletRot) SwitchOutbound(_ context.Context, node *types.Node) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.node = node
	f.running = true
	f.switchIDS = append(f.switchIDS, node.ID)
	return nil
}

func (f *fakeOutletRot) GetListenAddr() string { return f.addr }

func (f *fakeOutletRot) IsRunning() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gate != nil {
		<-f.gate
	}
	return f.running
}

func (f *fakeOutletRot) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gate != nil {
		<-f.gate
	}
	f.closed = true
	f.running = false
	return nil
}

func (f *fakeOutletRot) startedIDs() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.startIDs...)
}

func (f *fakeOutletRot) switchedIDs() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.switchIDS...)
}

func (f *fakeOutletRot) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// probeScript feeds the manager's probe function a fixed sequence of
// results, then repeats the last one. Verification and rotation are
// sequential in these tests, so ordering is deterministic.
type probeScript struct {
	mu    sync.Mutex
	steps []probeResult
	idx   int
	last  probeResult
}

type probeResult struct {
	status  int
	latency time.Duration
	err     error
}

func (p *probeScript) fn(_ context.Context, _ string) (int, time.Duration, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.idx < len(p.steps) {
		r := p.steps[p.idx]
		p.idx++
		p.last = r
		return r.status, r.latency, r.err
	}
	return p.last.status, p.last.latency, p.last.err
}

func okProbe(_ context.Context, _ string) (int, time.Duration, error) {
	return 200, 5 * time.Millisecond, nil
}

func ssNode(id int, server, country string) *types.Node {
	return &types.Node{
		ID:            id,
		Server:        server,
		Port:          8388,
		Protocol:      "ss",
		EncryptMethod: "aes-256-gcm",
		Password:      "pw",
		Country:       country,
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func (m *outletManager) runtimeFor(id int) *outletRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[id]
}

func statsOf(t *testing.T, c *cache.Cache, nodeID int) *types.NodeStats {
	t.Helper()
	s, err := c.GetNodeStats(nodeID)
	if err != nil {
		t.Fatalf("GetNodeStats(%d): %v", nodeID, err)
	}
	return s
}

// ============================================================
// verifyOutlet
// ============================================================

// TestVerifyOutletFallsThroughToNextCandidate: a candidate whose probe fails
// must not stop verification — the next candidate is started and probed, and
// the failed candidate is recorded in the node stats.
func TestVerifyOutletFallsThroughToNextCandidate(t *testing.T) {
	c := testCache(t)
	a := ssNode(0, "10.0.0.1", "VN")
	b := ssNode(0, "10.0.0.2", "VN")
	a.ID = seedNode(t, c, a)
	b.ID = seedNode(t, c, b)

	rot := newFakeOutletRot(1081)
	script := &probeScript{steps: []probeResult{
		{err: errors.New("connection refused")},
		{status: 200, latency: 7 * time.Millisecond},
	}}
	m := newOutletManager(outletConfig{cache: c, probe: script.fn})

	got := m.verifyOutlet(context.Background(), zap.NewNop(), rot,
		daemonOutlet{ID: 1, Port: 1081, Country: "VN"}, []*types.Node{a, b})

	if got == nil || got.ID != b.ID {
		t.Fatalf("verifyOutlet = %v, want node %d", got, b.ID)
	}
	if ids := rot.startedIDs(); len(ids) != 1 || ids[0] != a.ID {
		t.Errorf("started nodes = %v, want [%d]", ids, a.ID)
	}
	if ids := rot.switchedIDs(); len(ids) != 1 || ids[0] != b.ID {
		t.Errorf("switched nodes = %v, want [%d]", ids, b.ID)
	}
	if s := statsOf(t, c, a.ID); s.TotalFailures != 1 || s.TotalChecks != 1 {
		t.Errorf("node %d stats = %+v, want 1 failed check", a.ID, s)
	}
	if s := statsOf(t, c, b.ID); s.TotalChecks != 1 || s.SuccessRate != 1 {
		t.Errorf("node %d stats = %+v, want 1 successful check", b.ID, s)
	}
}

// TestVerifyOutletAllCandidatesFail: every candidate is tried once and nil is
// returned when none carries traffic — the caller then unregisters the outlet
// so the next reconcile pass retries.
func TestVerifyOutletAllCandidatesFail(t *testing.T) {
	c := testCache(t)
	a := ssNode(0, "10.0.0.1", "VN")
	b := ssNode(0, "10.0.0.2", "VN")
	a.ID = seedNode(t, c, a)
	b.ID = seedNode(t, c, b)

	rot := newFakeOutletRot(1081)
	probe := func(context.Context, string) (int, time.Duration, error) {
		return 0, 0, errors.New("unreachable")
	}
	m := newOutletManager(outletConfig{cache: c, probe: probe})

	got := m.verifyOutlet(context.Background(), zap.NewNop(), rot,
		daemonOutlet{ID: 1, Port: 1081, Country: "VN"}, []*types.Node{a, b})

	if got != nil {
		t.Fatalf("verifyOutlet = %v, want nil", got)
	}
	if ids := rot.startedIDs(); len(ids) != 1 {
		t.Errorf("started nodes = %v, want the first candidate", ids)
	}
	if ids := rot.switchedIDs(); len(ids) != 1 || ids[0] != b.ID {
		t.Errorf("switched nodes = %v, want [%d]", ids, b.ID)
	}
	if m.lastErr[1] != "no node passed end-to-end verification" {
		t.Errorf("lastErr = %q, want verification failure", m.lastErr[1])
	}
}

// TestVerifyOutletNoCandidates: an empty candidate list (country/protocol
// filter matches nothing) never touches the rotator.
func TestVerifyOutletNoCandidates(t *testing.T) {
	c := testCache(t)
	rot := newFakeOutletRot(1081)
	m := newOutletManager(outletConfig{cache: c, probe: okProbe})

	if got := m.verifyOutlet(context.Background(), zap.NewNop(), rot,
		daemonOutlet{ID: 1, Port: 1081}, nil); got != nil {
		t.Fatalf("verifyOutlet = %v, want nil", got)
	}
	if ids := rot.startedIDs(); len(ids) != 0 {
		t.Errorf("started nodes = %v, want none", ids)
	}
	if m.lastErr[1] != "no usable node in pool" {
		t.Errorf("lastErr = %q, want pool-empty failure", m.lastErr[1])
	}
}

// TestVerifyOutletCancelledContext: shutdown must return immediately without
// probing or recording a failure.
func TestVerifyOutletCancelledContext(t *testing.T) {
	c := testCache(t)
	node := ssNode(0, "10.0.0.1", "VN")
	node.ID = seedNode(t, c, node)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rot := newFakeOutletRot(1081)
	m := newOutletManager(outletConfig{cache: c, probe: okProbe})

	if got := m.verifyOutlet(ctx, zap.NewNop(), rot,
		daemonOutlet{ID: 1, Port: 1081}, []*types.Node{node}); got != nil {
		t.Fatalf("verifyOutlet = %v, want nil", got)
	}
	if ids := rot.startedIDs(); len(ids) != 0 {
		t.Errorf("started nodes = %v, want none", ids)
	}
	if _, ok := m.lastErr[1]; ok {
		t.Errorf("lastErr = %v, want no recorded failure", m.lastErr)
	}
}

// ============================================================
// rotateOutlet
// ============================================================

// TestRotateOutletSwitchesUntilProbeSucceeds: rotation skips the node being
// served, tries each candidate once, and adopts the first that carries
// traffic.
func TestRotateOutletSwitchesUntilProbeSucceeds(t *testing.T) {
	c := testCache(t)
	a := ssNode(0, "10.0.0.1", "VN")
	b := ssNode(0, "10.0.0.2", "VN")
	d := ssNode(0, "10.0.0.3", "VN")
	a.ID = seedNode(t, c, a)
	b.ID = seedNode(t, c, b)
	d.ID = seedNode(t, c, d)

	rot := newFakeOutletRot(1081)
	script := &probeScript{steps: []probeResult{
		{err: errors.New("unreachable")},
		{status: 204},
	}}
	m := newOutletManager(outletConfig{
		cache:      c,
		probe:      script.fn,
		candidates: func(string, string) []*types.Node { return []*types.Node{a, b, d} },
	})
	rt := &outletRuntime{rot: rot, node: a, verified: true}

	m.rotateOutlet(context.Background(), zap.NewNop(), daemonOutlet{ID: 1, Port: 1081, Country: "VN"}, rt)

	if ids := rot.switchedIDs(); len(ids) != 2 || ids[0] != b.ID || ids[1] != d.ID {
		t.Errorf("switched nodes = %v, want [%d %d]", ids, b.ID, d.ID)
	}
	if rt.node == nil || rt.node.ID != d.ID {
		t.Errorf("runtime node = %v, want %d", rt.node, d.ID)
	}
	if s := statsOf(t, c, d.ID); s.TotalChecks != 1 || s.SuccessRate != 1 {
		t.Errorf("node %d stats = %+v, want 1 successful check", d.ID, s)
	}
}

// TestRotateOutletNoReachableNode: when no candidate connects the outlet keeps
// the previous node and records the outage for deduplication.
func TestRotateOutletNoReachableNode(t *testing.T) {
	c := testCache(t)
	a := ssNode(0, "10.0.0.1", "VN")
	b := ssNode(0, "10.0.0.2", "VN")
	a.ID = seedNode(t, c, a)
	b.ID = seedNode(t, c, b)

	rot := newFakeOutletRot(1081)
	probe := func(context.Context, string) (int, time.Duration, error) {
		return 0, 0, errors.New("unreachable")
	}
	m := newOutletManager(outletConfig{
		cache:      c,
		probe:      probe,
		candidates: func(string, string) []*types.Node { return []*types.Node{a, b} },
	})
	rt := &outletRuntime{rot: rot, node: a, verified: true}

	m.rotateOutlet(context.Background(), zap.NewNop(), daemonOutlet{ID: 1, Port: 1081, Country: "VN"}, rt)

	if rt.node.ID != a.ID {
		t.Errorf("runtime node = %d, want %d (unchanged)", rt.node.ID, a.ID)
	}
	if m.lastErr[1] != "no reachable node to rotate to" {
		t.Errorf("lastErr = %q, want rotation failure", m.lastErr[1])
	}
}

// ============================================================
// Reconcile
// ============================================================

// TestReconcileVerifiesThenReportsRunning pins the asynchronous contract: the
// pass that creates the runtime reports it as pending, and a later pass — or
// the onUpdate callback fired by the verifier — reports the live node.
func TestReconcileVerifiesThenReportsRunning(t *testing.T) {
	c := testCache(t)
	node := ssNode(0, "10.0.0.1", "VN")
	node.ID = seedNode(t, c, node)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rot := newFakeOutletRot(1081)
	m := newOutletManager(outletConfig{
		primaryPort:  1080,
		cache:        c,
		probe:        okProbe,
		monitorEvery: 5 * time.Millisecond,
		candidates:   func(string, string) []*types.Node { return []*types.Node{node} },
		newRot:       func(*zap.Logger, string, int) outletRot { return rot },
	})
	verified := make(chan struct{}, 1)
	m.onUpdate = func() {
		select {
		case verified <- struct{}{}:
		default:
		}
	}
	outlet := daemonOutlet{ID: 1, Port: 1081, Country: "VN"}

	out := m.Reconcile(ctx, zap.NewNop(), t.TempDir(), []daemonOutlet{outlet})
	if len(out) != 1 || out[0].Running {
		t.Fatalf("first pass = %+v, want pending (Running=false)", out)
	}

	select {
	case <-verified:
	case <-time.After(3 * time.Second):
		t.Fatal("outlet never verified")
	}

	out = m.Reconcile(ctx, zap.NewNop(), t.TempDir(), []daemonOutlet{outlet})
	if len(out) != 1 || !out[0].Running || out[0].NodeID != node.ID {
		t.Fatalf("second pass = %+v, want running on node %d", out, node.ID)
	}

	m.Close()
	if rt := m.runtimeFor(1); rt != nil {
		t.Errorf("runtime still registered after Close: %+v", rt)
	}
	// Close does not wait for the rotator (a close can block on a startup),
	// so the rotator is closed just after Close returns.
	waitFor(t, "outlet rotator to close", rot.isClosed)
}

// TestReconcileRetriesAfterVerificationFailure: a failed verification
// unregisters the runtime so the next pass starts a new attempt instead of
// leaving the outlet dead forever.
func TestReconcileRetriesAfterVerificationFailure(t *testing.T) {
	c := testCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	probe := func(context.Context, string) (int, time.Duration, error) {
		return 0, 0, errors.New("unreachable")
	}
	var mu sync.Mutex
	created := 0
	m := newOutletManager(outletConfig{
		primaryPort:  1080,
		cache:        c,
		probe:        probe,
		monitorEvery: time.Second,
		candidates:   func(string, string) []*types.Node { return nil },
		newRot: func(*zap.Logger, string, int) outletRot {
			mu.Lock()
			created++
			mu.Unlock()
			return newFakeOutletRot(1081)
		},
	})
	outlet := daemonOutlet{ID: 1, Port: 1081, Country: "VN"}

	m.Reconcile(ctx, zap.NewNop(), t.TempDir(), []daemonOutlet{outlet})
	waitFor(t, "runtime to be dropped", func() bool { return m.runtimeFor(1) == nil })

	out := m.Reconcile(ctx, zap.NewNop(), t.TempDir(), []daemonOutlet{outlet})
	if len(out) != 1 || out[0].Running {
		t.Fatalf("retry pass = %+v, want pending", out)
	}
	mu.Lock()
	n := created
	mu.Unlock()
	if n != 2 {
		t.Errorf("rotators created = %d, want 2 (one per attempt)", n)
	}
	m.Close()
}

// TestReconcileSkipsClashingPort: state is edited by another process, so two
// outlets can name the same port; the second must not steal the first's
// listener.
func TestReconcileSkipsClashingPort(t *testing.T) {
	c := testCache(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	created := 0
	m := newOutletManager(outletConfig{
		primaryPort:  1080,
		cache:        c,
		probe:        okProbe,
		monitorEvery: time.Second,
		candidates:   func(string, string) []*types.Node { return nil },
		newRot: func(*zap.Logger, string, int) outletRot {
			mu.Lock()
			created++
			mu.Unlock()
			return newFakeOutletRot(1081)
		},
	})
	defer m.Close()

	out := m.Reconcile(ctx, zap.NewNop(), t.TempDir(), []daemonOutlet{
		{ID: 1, Port: 1081, Country: "VN"},
		{ID: 2, Port: 1081, Country: "SG"},
	})
	outletsByID := map[int]daemonOutlet{}
	for _, o := range out {
		outletsByID[o.ID] = o
	}
	if len(out) != 2 {
		t.Fatalf("got %d outlets, want 2 (clashing one reported)", len(out))
	}
	if outletsByID[2].Running {
		t.Error("clashing outlet reported running")
	}
	mu.Lock()
	n := created
	mu.Unlock()
	if n != 1 {
		t.Errorf("rotators created = %d, want 1 (second port is taken)", n)
	}
}

// TestReconcileStopsRemovedOutlet: an outlet missing from the intent list is
// stopped, its goroutine cancelled, and its port released for reuse.
func TestReconcileStopsRemovedOutlet(t *testing.T) {
	c := testCache(t)
	node := ssNode(0, "10.0.0.1", "VN")
	node.ID = seedNode(t, c, node)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rot := newFakeOutletRot(1081)
	m := newOutletManager(outletConfig{
		primaryPort:  1080,
		cache:        c,
		probe:        okProbe,
		monitorEvery: 5 * time.Millisecond,
		candidates:   func(string, string) []*types.Node { return []*types.Node{node} },
		newRot:       func(*zap.Logger, string, int) outletRot { return rot },
	})
	outlet := daemonOutlet{ID: 1, Port: 1081, Country: "VN"}

	m.Reconcile(ctx, zap.NewNop(), t.TempDir(), []daemonOutlet{outlet})
	waitFor(t, "outlet to verify", func() bool {
		rt := m.runtimeFor(1)
		return rt != nil && rt.verified
	})

	out := m.Reconcile(ctx, zap.NewNop(), t.TempDir(), nil)
	if len(out) != 0 {
		t.Fatalf("got %d outlets after removal, want 0", len(out))
	}
	// The rotator is closed off the reconcile path (a close can wait on a
	// slow startup), so the runtime is gone at once and the close follows.
	if rt := m.runtimeFor(1); rt != nil {
		t.Errorf("removed outlet still registered: %+v", rt)
	}
	waitFor(t, "removed outlet rotator to close", rot.isClosed)
}

// TestReconcileDoesNotStallOnBusyRotator: a rotator holds its own lock while
// sing-box starts or a switch is in flight. A concurrent reconcile pass — the
// path onUpdate takes right after a verification — must not block behind that
// lock, or the daemon stops publishing state for every outlet while one is
// busy. Reconcile therefore never calls a rotator method.
func TestReconcileDoesNotStallOnBusyRotator(t *testing.T) {
	c := testCache(t)
	node := ssNode(0, "10.0.0.1", "VN")
	node.ID = seedNode(t, c, node)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gate := make(chan struct{})
	rot := newFakeOutletRot(1081)
	rot.gate = gate
	m := newOutletManager(outletConfig{
		primaryPort:  1080,
		cache:        c,
		probe:        okProbe,
		monitorEvery: time.Hour,
		candidates:   func(string, string) []*types.Node { return []*types.Node{node} },
		newRot:       func(*zap.Logger, string, int) outletRot { return rot },
	})
	outlets := []daemonOutlet{{ID: 1, Port: 1081, Country: "VN"}}

	m.Reconcile(ctx, zap.NewNop(), t.TempDir(), outlets)
	waitFor(t, "outlet to verify", func() bool {
		rt := m.runtimeFor(1)
		return rt != nil && rt.verified
	})

	// The rotator is busy: every lock-taking call blocks until the gate
	// opens. A reconcile pass must still return and report the outlet.
	done := make(chan []daemonOutlet, 1)
	go func() { done <- m.Reconcile(ctx, zap.NewNop(), t.TempDir(), outlets) }()
	select {
	case out := <-done:
		if len(out) != 1 || !out[0].Running || out[0].NodeID != node.ID {
			t.Fatalf("reconcile pass = %+v, want the verified outlet still running", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconcile stalled behind a busy rotator")
	}

	close(gate)
	m.Close()
}

// TestManagerCloseDoesNotWaitForBusyRotator: Close runs on the daemon's
// shutdown path, so it must not block on a rotator that is mid-operation —
// doing so kept the process alive (still holding a readable PID file) long
// after it had reported that it stopped.
func TestManagerCloseDoesNotWaitForBusyRotator(t *testing.T) {
	c := testCache(t)
	node := ssNode(0, "10.0.0.1", "VN")
	node.ID = seedNode(t, c, node)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	gate := make(chan struct{})
	rot := newFakeOutletRot(1081)
	rot.gate = gate
	m := newOutletManager(outletConfig{
		primaryPort:  1080,
		cache:        c,
		probe:        okProbe,
		monitorEvery: time.Hour,
		candidates:   func(string, string) []*types.Node { return []*types.Node{node} },
		newRot:       func(*zap.Logger, string, int) outletRot { return rot },
	})
	m.Reconcile(ctx, zap.NewNop(), t.TempDir(), []daemonOutlet{{ID: 1, Port: 1081, Country: "VN"}})
	waitFor(t, "outlet to verify", func() bool {
		rt := m.runtimeFor(1)
		return rt != nil && rt.verified
	})

	done := make(chan struct{})
	go func() {
		m.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(gate)
		t.Fatal("Close waited for a busy rotator")
	}

	// The close still happens, just after Close returned.
	close(gate)
	waitFor(t, "rotator to close", rot.isClosed)
}

// ============================================================
// monitorOutlet
// ============================================================

// TestMonitorRotatesAfterRepeatedProbeFailures: a burst of failures equal to
// the retry budget triggers rotation to the next node that passes a probe.
func TestMonitorRotatesAfterRepeatedProbeFailures(t *testing.T) {
	c := testCache(t)
	a := ssNode(0, "10.0.0.1", "VN")
	b := ssNode(0, "10.0.0.2", "VN")
	a.ID = seedNode(t, c, a)
	b.ID = seedNode(t, c, b)

	rot := newFakeOutletRot(1081)
	// verify a (ok), monitor: maxRetries=1 → two failures, then rotate to b (ok).
	script := &probeScript{steps: []probeResult{
		{status: 200, latency: time.Millisecond},
		{err: errors.New("unreachable")},
		{err: errors.New("unreachable")},
		{status: 200, latency: time.Millisecond},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := newOutletManager(outletConfig{
		primaryPort:  1080,
		cache:        c,
		probe:        script.fn,
		monitorEvery: 5 * time.Millisecond,
		maxRetries:   1,
		candidates:   func(string, string) []*types.Node { return []*types.Node{a, b} },
		newRot:       func(*zap.Logger, string, int) outletRot { return rot },
	})
	m.Reconcile(ctx, zap.NewNop(), t.TempDir(), []daemonOutlet{{ID: 1, Port: 1081, Country: "VN"}})

	waitFor(t, "outlet to rotate to node b", func() bool {
		rt := m.runtimeFor(1)
		return rt != nil && rt.node != nil && rt.node.ID == b.ID
	})
	if ids := rot.switchedIDs(); len(ids) != 1 || ids[0] != b.ID {
		t.Errorf("switched nodes = %v, want [%d]", ids, b.ID)
	}
	m.Close()
}

// ============================================================
// outletCandidates
// ============================================================

// TestOutletCandidatesFiltersBadStats: nodes with a poor success record are
// left out of the candidate list; nodes the outlet must still be able to try
// when every node is bad.
func TestOutletCandidatesFiltersBadStats(t *testing.T) {
	c := testCache(t)
	good := ssNode(0, "10.0.0.1", "VN")
	bad := ssNode(0, "10.0.0.2", "VN")
	other := ssNode(0, "10.0.0.3", "SG")
	good.ID = seedNode(t, c, good)
	bad.ID = seedNode(t, c, bad)
	other.ID = seedNode(t, c, other)

	for i := 0; i < 6; i++ {
		if err := c.UpdateNodeStats(bad.ID, 0, false); err != nil {
			t.Fatalf("UpdateNodeStats: %v", err)
		}
	}

	got := outletCandidates(c, "VN", "")
	if len(got) != 1 || got[0].ID != good.ID {
		t.Fatalf("VN candidates = %v, want only node %d", got, good.ID)
	}
	got = outletCandidates(c, "all", "")
	if len(got) != 2 {
		t.Fatalf("all candidates = %d nodes, want 2 (bad-stats node excluded)", len(got))
	}
	for _, n := range got {
		if n.ID == bad.ID {
			t.Errorf("bad-stats node %d still a candidate", bad.ID)
		}
	}
	got = outletCandidates(c, "SG", "")
	if len(got) != 1 || got[0].ID != other.ID {
		t.Fatalf("SG candidates = %v, want only node %d", got, other.ID)
	}
}

// TestOutletCandidatesFallsBackWhenAllBad: stale stats must not starve an
// outlet — with every node marked bad the full list is returned.
func TestOutletCandidatesFallsBackWhenAllBad(t *testing.T) {
	c := testCache(t)
	node := ssNode(0, "10.0.0.1", "VN")
	node.ID = seedNode(t, c, node)
	for i := 0; i < 6; i++ {
		c.UpdateNodeStats(node.ID, 0, false)
	}

	got := outletCandidates(c, "", "")
	if len(got) != 1 || got[0].ID != node.ID {
		t.Fatalf("candidates = %v, want fallback to node %d", got, node.ID)
	}
}

// TestOutletCandidatesEmptyPool: no nodes means no candidates (not a panic).
func TestOutletCandidatesEmptyPool(t *testing.T) {
	if got := outletCandidates(testCache(t), "all", ""); got != nil {
		t.Errorf("candidates = %v, want nil", got)
	}
}

// TestOutletCandidatesDropsInvalidCredentials: orderedCandidates only keeps
// nodes it can actually dial, so an ss node without a password never reaches
// the outlet.
func TestOutletCandidatesDropsInvalidCredentials(t *testing.T) {
	c := testCache(t)
	broken := &types.Node{Server: "10.0.0.9", Port: 8388, Protocol: "ss", Country: "VN"}
	broken.ID = seedNode(t, c, broken)

	if got := outletCandidates(c, "VN", ""); got != nil {
		t.Errorf("candidates = %v, want nil (no usable credentials)", got)
	}
}
