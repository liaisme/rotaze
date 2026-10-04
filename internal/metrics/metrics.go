// Package metrics provides Prometheus-compatible metrics for Rotaze.
package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Collector holds all metrics for Rotaze.
type Collector struct {
	mu sync.RWMutex

	// Counters (atomic)
	rotationsTotal    atomic.Int64
	checksTotal       atomic.Int64
	checksSuccess     atomic.Int64
	checksFailed      atomic.Int64
	nodesFetchedTotal atomic.Int64
	nodesActive       atomic.Int64

	// Gauges (atomic)
	currentNodeID    atomic.Int64
	proxyRunning     atomic.Int64
	checkInterval    atomic.Int64
	lastCheckUnix    atomic.Int64
	lastRotationUnix atomic.Int64

	// Labels (protected by mu)
	currentNodeServer  string
	currentNodeCountry string
}

// New creates a new metrics collector.
func New() *Collector {
	return &Collector{}
}

// IncRotations increments the rotation counter.
func (c *Collector) IncRotations() {
	c.rotationsTotal.Add(1)
	c.lastRotationUnix.Store(time.Now().Unix())
}

// IncChecks increments the check counter.
func (c *Collector) IncChecks(success bool) {
	c.checksTotal.Add(1)
	if success {
		c.checksSuccess.Add(1)
	} else {
		c.checksFailed.Add(1)
	}
	c.lastCheckUnix.Store(time.Now().Unix())
}

// SetNodesFetched sets the total nodes fetched.
func (c *Collector) SetNodesFetched(n int64) {
	c.nodesFetchedTotal.Store(n)
}

// SetNodesActive sets the active node count.
func (c *Collector) SetNodesActive(n int64) {
	c.nodesActive.Store(n)
}

// SetCurrentNode updates the current node info.
func (c *Collector) SetCurrentNode(id int, server, country string) {
	c.currentNodeID.Store(int64(id))
	c.mu.Lock()
	c.currentNodeServer = server
	c.currentNodeCountry = country
	c.mu.Unlock()
}

// SetProxyRunning sets the proxy running state.
func (c *Collector) SetProxyRunning(running bool) {
	if running {
		c.proxyRunning.Store(1)
	} else {
		c.proxyRunning.Store(0)
	}
}

// SetCheckInterval sets the check interval in seconds.
func (c *Collector) SetCheckInterval(seconds int) {
	c.checkInterval.Store(int64(seconds))
}

// ServeHTTP implements http.Handler for /metrics endpoint.
func (c *Collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	c.mu.RLock()
	server := c.currentNodeServer
	country := c.currentNodeCountry
	c.mu.RUnlock()

	fmt.Fprintf(w, "# HELP rotaze_rotations_total Total number of node rotations.\n")
	fmt.Fprintf(w, "# TYPE rotaze_rotations_total counter\n")
	fmt.Fprintf(w, "rotaze_rotations_total %d\n", c.rotationsTotal.Load())

	fmt.Fprintf(w, "# HELP rotaze_checks_total Total number of URL checks.\n")
	fmt.Fprintf(w, "# TYPE rotaze_checks_total counter\n")
	fmt.Fprintf(w, "rotaze_checks_total %d\n", c.checksTotal.Load())

	fmt.Fprintf(w, "# HELP rotaze_checks_success_total Total successful URL checks.\n")
	fmt.Fprintf(w, "# TYPE rotaze_checks_success_total counter\n")
	fmt.Fprintf(w, "rotaze_checks_success_total %d\n", c.checksSuccess.Load())

	fmt.Fprintf(w, "# HELP rotaze_checks_failed_total Total failed URL checks.\n")
	fmt.Fprintf(w, "# TYPE rotaze_checks_failed_total counter\n")
	fmt.Fprintf(w, "rotaze_checks_failed_total %d\n", c.checksFailed.Load())

	fmt.Fprintf(w, "# HELP rotaze_nodes_fetched_total Total nodes fetched from sources.\n")
	fmt.Fprintf(w, "# TYPE rotaze_nodes_fetched_total counter\n")
	fmt.Fprintf(w, "rotaze_nodes_fetched_total %d\n", c.nodesFetchedTotal.Load())

	fmt.Fprintf(w, "# HELP rotaze_nodes_active Number of nodes in pool.\n")
	fmt.Fprintf(w, "# TYPE rotaze_nodes_active gauge\n")
	fmt.Fprintf(w, "rotaze_nodes_active %d\n", c.nodesActive.Load())

	fmt.Fprintf(w, "# HELP rotaze_proxy_running Whether the proxy is running.\n")
	fmt.Fprintf(w, "# TYPE rotaze_proxy_running gauge\n")
	fmt.Fprintf(w, "rotaze_proxy_running %d\n", c.proxyRunning.Load())

	fmt.Fprintf(w, "# HELP rotaze_check_interval_seconds Current check interval.\n")
	fmt.Fprintf(w, "# TYPE rotaze_check_interval_seconds gauge\n")
	fmt.Fprintf(w, "rotaze_check_interval_seconds %d\n", c.checkInterval.Load())

	fmt.Fprintf(w, "# HELP rotaze_current_node_id Current active node ID.\n")
	fmt.Fprintf(w, "# TYPE rotaze_current_node_id gauge\n")
	fmt.Fprintf(w, "rotaze_current_node_id %d\n", c.currentNodeID.Load())

	fmt.Fprintf(w, "# HELP rotaze_last_check_timestamp Unix timestamp of last check.\n")
	fmt.Fprintf(w, "# TYPE rotaze_last_check_timestamp gauge\n")
	fmt.Fprintf(w, "rotaze_last_check_timestamp %d\n", c.lastCheckUnix.Load())

	fmt.Fprintf(w, "# HELP rotaze_last_rotation_timestamp Unix timestamp of last rotation.\n")
	fmt.Fprintf(w, "# TYPE rotaze_last_rotation_timestamp gauge\n")
	fmt.Fprintf(w, "rotaze_last_rotation_timestamp %d\n", c.lastRotationUnix.Load())

	fmt.Fprintf(w, "# HELP rotaze_node_info Current node information.\n")
	fmt.Fprintf(w, "# TYPE rotaze_node_info gauge\n")
	fmt.Fprintf(w, "rotaze_node_info{server=%q,country=%q} 1\n", server, country)
}

// StartMetricsServer starts a Prometheus metrics HTTP server.
func StartMetricsServer(addr string, collector *Collector) (*http.Server, error) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", collector)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Metrics server error: %v\n", err)
		}
	}()

	return server, nil
}
