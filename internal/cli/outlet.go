// Outlet manager for the daemon: reconciles `daemon.state` outlets against
// running rotator instances. Each outlet gets its own rotator on the port
// recorded in state; rotators are keyed by outlet ID so add/rm and SIGHUP
// reloads converge.
package cli

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"github.com/rotaze/rotaze/internal/rotator"
	"github.com/rotaze/rotaze/pkg/types"
)

// outletRuntime is a live outlet: its rotator plus the node it is serving,
// so the reported state stays accurate without re-reading the DB.
type outletRuntime struct {
	rot  *rotator.Rotator
	node *types.Node
}

// outletManager owns the additional proxy outlets of a running daemon.
// The primary rotator (configured proxy) is not managed here.
type outletManager struct {
	mu      sync.Mutex
	running map[int]*outletRuntime // keyed by outlet ID
	primary int                    // port of the daemon's own proxy
}

func newOutletManager(primaryPort int) *outletManager {
	return &outletManager{running: make(map[int]*outletRuntime), primary: primaryPort}
}

// Reconcile brings running outlets in line with the outlets recorded in
// daemon state: outlets that disappeared are stopped, new ones are started,
// and outlets whose sing-box process died are restarted. The returned list
// carries live status (Running/Addr/Node/NodeID) so the caller can persist it.
//
// A single outlet failing to start does not abort the reconcile: it is
// reported as not running and retried on the next pass.
func (m *outletManager) Reconcile(ctx context.Context, logger *zap.Logger, dataDir string, outlets []daemonOutlet, pickNode func(country, protocol string) (*types.Node, error)) []daemonOutlet {
	m.mu.Lock()
	defer m.mu.Unlock()

	desired := make(map[int]daemonOutlet, len(outlets))
	for _, o := range outlets {
		desired[o.ID] = o
	}

	// Stop outlets that are no longer wanted.
	for id, rt := range m.running {
		if _, ok := desired[id]; !ok {
			stopOutlet(logger, id, rt.rot)
			delete(m.running, id)
		}
	}

	// Ports held by live outlets. State is edited by a separate CLI process,
	// so a port recorded there can already be taken by a sibling outlet.
	ports := map[int]int{m.primary: 0}
	for id, rt := range m.running {
		ports[rt.rot.GetPort()] = id
	}

	out := make([]daemonOutlet, 0, len(desired))
	for _, o := range desired {
		if rt, ok := m.running[o.ID]; ok && rt.rot.IsRunning() {
			// Already serving: report live status, leave the process alone.
			o.Running = true
			o.Addr = rt.rot.GetListenAddr()
			o.NodeID = rt.node.ID
			o.Node = describeNode(rt.node)
			out = append(out, o)
			continue
		}
		// Either new, or its process died since the last pass.
		if rt, ok := m.running[o.ID]; ok {
			stopOutlet(logger, o.ID, rt.rot)
			delete(m.running, o.ID)
			delete(ports, rt.rot.GetPort())
		}
		if owner, clash := ports[o.Port]; clash {
			logger.Warn("outlet port already in use, skipping",
				zap.Int("outlet", o.ID), zap.Int("port", o.Port), zap.Int("used_by", owner))
			o.Running, o.Node, o.NodeID = false, "", 0
			out = append(out, o)
			continue
		}
		node, err := pickNode(o.Country, o.Protocol)
		if err != nil || node == nil {
			logger.Warn("outlet has no usable node",
				zap.Int("outlet", o.ID), zap.Int("port", o.Port),
				zap.String("country", o.Country), zap.String("protocol", o.Protocol), zap.Error(err))
			o.Running, o.Node, o.NodeID = false, "", 0
			out = append(out, o)
			continue
		}
		r := rotator.New(logger, &rotator.Config{
			ListenAddr: "127.0.0.1",
			ListenPort: o.Port,
			DataDir:    dataDir,
		})
		if err := r.Start(ctx, node); err != nil {
			logger.Warn("outlet start failed",
				zap.Int("outlet", o.ID), zap.Int("port", o.Port), zap.Error(err))
			o.Running, o.Node, o.NodeID = false, "", 0
			out = append(out, o)
			continue
		}
		m.running[o.ID] = &outletRuntime{rot: r, node: node}
		ports[o.Port] = o.ID
		o.Running = true
		o.Addr = r.GetListenAddr()
		o.NodeID = node.ID
		o.Node = describeNode(node)
		logger.Info("outlet started", zap.Int("outlet", o.ID), zap.String("addr", o.Addr),
			zap.String("country", o.Country), zap.String("node", o.Node))
		out = append(out, o)
	}
	return out
}

// Close stops every managed outlet.
func (m *outletManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rt := range m.running {
		stopOutlet(globalLogger, id, rt.rot)
		delete(m.running, id)
	}
}

func stopOutlet(logger *zap.Logger, id int, r *rotator.Rotator) {
	if err := r.Close(); err != nil && logger != nil {
		logger.Warn("outlet close failed", zap.Int("outlet", id), zap.Error(err))
		return
	}
	if logger != nil {
		logger.Info("outlet stopped", zap.Int("outlet", id))
	}
}

func describeNode(n *types.Node) string {
	return fmt.Sprintf("%s:%d (%s, %s)", n.Server, n.Port, n.Protocol, n.Country)
}
