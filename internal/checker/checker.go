// Package checker provides URL monitoring, exit IP verification, and retry logic.
package checker

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/net/proxy"

	"github.com/rotaze/rotaze/internal/cache"
	"github.com/rotaze/rotaze/pkg/types"
)

// Checker monitors a target URL via SOCKS5 proxy and triggers rotation on failure.
type Checker struct {
	cache  *cache.Cache
	logger *zap.Logger

	// Configuration
	url         string
	interval    time.Duration
	timeout     time.Duration
	maxRetries  int
	maxAttempts int
	checkIP     bool
	country     string
	socks5Addr  string

	// Callbacks
	onRotate func(ctx context.Context, reason string) error

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
}

// Config holds checker configuration.
type Config struct {
	URL         string
	Interval    time.Duration
	Timeout     time.Duration
	MaxRetries  int
	MaxAttempts int
	CheckIP     bool
	Country     string
	SOCKS5Addr  string
}

// New creates a new Checker.
func New(c *cache.Cache, logger *zap.Logger, cfg *Config) *Checker {
	socks5Addr := cfg.SOCKS5Addr
	if socks5Addr == "" {
		socks5Addr = "127.0.0.1:1080"
	}

	return &Checker{
		cache:       c,
		logger:      logger,
		url:         cfg.URL,
		interval:    cfg.Interval,
		timeout:     cfg.Timeout,
		maxRetries:  cfg.MaxRetries,
		maxAttempts: cfg.MaxAttempts,
		checkIP:     cfg.CheckIP,
		country:     cfg.Country,
		socks5Addr:  socks5Addr,
		stopCh:      make(chan struct{}),
	}
}

// OnRotate sets the callback function called when rotation is needed.
func (ch *Checker) OnRotate(fn func(ctx context.Context, reason string) error) {
	ch.onRotate = fn
}

// Start begins the monitoring loop.
func (ch *Checker) Start(ctx context.Context) error {
	ch.mu.Lock()
	if ch.running {
		ch.mu.Unlock()
		return fmt.Errorf("checker already running")
	}
	ch.running = true
	currentInterval := ch.interval
	ch.mu.Unlock()

	ch.logger.Info("starting URL checker",
		zap.String("url", ch.url),
		zap.Duration("interval", currentInterval),
	)

	ticker := time.NewTicker(currentInterval)
	defer ticker.Stop()

	// Wait for proxy to be fully ready before first check
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(3 * time.Second):
	}

	// Run first check
	if err := ch.check(ctx); err != nil {
		ch.logger.Warn("initial check failed", zap.Error(err))
	}

	for {
		select {
		case <-ctx.Done():
			ch.logger.Info("checker stopped (context cancelled)")
			return nil
		case <-ch.stopCh:
			ch.logger.Info("checker stopped")
			return nil
		case <-ticker.C:
			// Pick up interval changes made via UpdateInterval (SIGHUP reload)
			ch.mu.Lock()
			newInterval := ch.interval
			ch.mu.Unlock()
			if newInterval != currentInterval && newInterval > 0 {
				currentInterval = newInterval
				ticker.Reset(currentInterval)
				ch.logger.Info("check interval updated", zap.Duration("interval", currentInterval))
			}
			if err := ch.check(ctx); err != nil {
				ch.logger.Warn("check failed", zap.Error(err))
			}
		}
	}
}

// Stop stops the monitoring loop.
func (ch *Checker) Stop() {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.running {
		close(ch.stopCh)
		ch.running = false
	}
}

// UpdateInterval changes the check interval on the fly.
func (ch *Checker) UpdateInterval(d time.Duration) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.interval = d
}

// UpdateCountry changes the expected exit-IP country on the fly (SIGHUP reload).
func (ch *Checker) UpdateCountry(c string) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.country = c
}

// UpdateCheckIP enables/disables exit-IP country verification on the fly (SIGHUP reload).
func (ch *Checker) UpdateCheckIP(v bool) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.checkIP = v
}

// UpdateURL changes the monitored URL on the fly (SIGHUP reload).
func (ch *Checker) UpdateURL(u string) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.url = u
}

// check performs a single URL check with retry logic.
func (ch *Checker) check(ctx context.Context) error {
	// Get current node
	currentNodeID, err := ch.cache.GetCurrentNode()
	if err != nil || currentNodeID == 0 {
		return fmt.Errorf("no current node set")
	}

	// Update last check time
	ch.cache.SetState(types.StateKeyLastCheckTime, time.Now().Format(time.RFC3339))
	// Reset status; final "ok"/"fail" is written below so `daemon status`
	// can report the last check result without parsing logs.
	ch.cache.SetState(types.StateKeyLastCheckStatus, "checking")

	// Try the URL check with retries
	var lastErr error
	for attempt := 0; attempt <= ch.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s, ...
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
			ch.logger.Debug("retrying check", zap.Int("attempt", attempt), zap.Duration("backoff", backoff))

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		statusCode, latency, err := ch.checkURL(ctx)
		if err == nil && statusCode >= 200 && statusCode < 400 {
			// Verify the exit IP country when verification is enabled and a
			// specific country is expected. A mismatch counts as a failed
			// attempt so the retry/rotation path picks a different node.
			ch.mu.Lock()
			checkIP := ch.checkIP
			country := ch.country
			ch.mu.Unlock()
			if checkIP && country != "" && country != "all" && country != "ALL" {
				ch.logger.Info("URL check passed, verifying exit IP country",
					zap.Int("status", statusCode),
					zap.Duration("latency", latency),
					zap.String("country", country),
				)
				_, verr := VerifyExitIP(ctx, ch.socks5Addr, country)
				if verr != nil {
					err = verr
				}
			}
			if err == nil {
				// Success
				ch.cache.UpdateNodeStats(currentNodeID, latency.Milliseconds(), true)
				ch.cache.SetState(types.StateKeyLastCheckStatus,
					fmt.Sprintf("ok (%d) in %s via node %d",
						statusCode, latency.Round(time.Millisecond), currentNodeID),
				)
				ch.logger.Info("URL check passed",
					zap.Int("status", statusCode),
					zap.Duration("latency", latency),
				)
				return nil
			}
		}

		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("HTTP %d", statusCode)
		}

		ch.cache.UpdateNodeStats(currentNodeID, latency.Milliseconds(), false)
		ch.logger.Warn("URL check failed",
			zap.Int("attempt", attempt+1),
			zap.Error(lastErr),
		)
	}

	// All retries exhausted - need rotation
	ch.cache.SetState(types.StateKeyLastCheckStatus, fmt.Sprintf("fail: %v", lastErr))
	ch.logger.Error("all retries exhausted, triggering rotation",
		zap.Int("max_retries", ch.maxRetries),
		zap.Error(lastErr),
	)

	// Check if locked
	locked, _ := ch.cache.IsNodeLocked()
	if locked {
		ch.logger.Warn("node is locked, cannot auto-rotate")
		return fmt.Errorf("node locked, rotation blocked: %w", lastErr)
	}

	// Trigger rotation
	if ch.onRotate != nil {
		if err := ch.onRotate(ctx, "failure"); err != nil {
			ch.logger.Error("rotation failed", zap.Error(err))
			return fmt.Errorf("rotation failed: %w", err)
		}
	}

	return lastErr
}

// checkURL performs the actual HTTP request via SOCKS5 proxy.
func (ch *Checker) checkURL(ctx context.Context) (int, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, ch.timeout)
	defer cancel()

	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, "GET", ch.url, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("create request: %w", err)
	}

	// Route through SOCKS5 proxy
	dialer, err := proxy.SOCKS5("tcp", ch.socks5Addr, nil, proxy.Direct)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
	}

	client := &http.Client{
		Timeout: ch.timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.(proxy.ContextDialer).DialContext(ctx, network, addr)
			},
		},
	}

	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return 0, latency, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	return resp.StatusCode, latency, nil
}

// CheckURLOnce performs a single URL check via SOCKS5 proxy (for run mode).
func CheckURLOnce(ctx context.Context, url string, timeout time.Duration) (int, time.Duration, error) {
	return CheckURLOnceWithProxy(ctx, url, timeout, "127.0.0.1:1080")
}

// CheckURLOnceWithProxy performs a single URL check via specified SOCKS5 proxy.
func CheckURLOnceWithProxy(ctx context.Context, url string, timeout time.Duration, socks5Addr string) (int, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("create request: %w", err)
	}

	// Route through SOCKS5 proxy
	dialer, err := proxy.SOCKS5("tcp", socks5Addr, nil, proxy.Direct)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
	}

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.(proxy.ContextDialer).DialContext(ctx, network, addr)
			},
		},
	}

	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return 0, latency, err
	}
	defer resp.Body.Close()

	return resp.StatusCode, latency, nil
}

// CheckResult is a convenience type for check results.
type CheckResult struct {
	StatusCode int
	Latency    time.Duration
	Error      error
	Success    bool
}

// LatencyStr returns a human-readable latency string.
func (r CheckResult) LatencyStr() string {
	if r.Latency < time.Millisecond {
		return fmt.Sprintf("%dµs", r.Latency.Microseconds())
	}
	return fmt.Sprintf("%v", r.Latency.Round(time.Millisecond))
}

// CheckWithRetry performs a URL check with retry logic and returns the result.
func CheckWithRetry(ctx context.Context, url string, timeout time.Duration, maxRetries int) CheckResult {
	return CheckWithRetryAndProxy(ctx, url, timeout, maxRetries, "127.0.0.1:1080")
}

// CheckAttempt reports a single check attempt so callers can surface
// per-attempt progress in verbose mode.
type CheckAttempt struct {
	Attempt    int
	StatusCode int
	Latency    time.Duration
	Err        error
}

// CheckWithRetryAndProxy performs a URL check with retry logic using specified
// proxy. An optional observer callback is invoked after every attempt (success
// or failure) — used by --verbose to show retries as they happen.
func CheckWithRetryAndProxy(ctx context.Context, url string, timeout time.Duration, maxRetries int, socks5Addr string, observers ...func(CheckAttempt)) CheckResult {
	var lastErr error
	var lastLatency time.Duration
	var lastStatus int

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
			select {
			case <-ctx.Done():
				return CheckResult{Error: ctx.Err()}
			case <-time.After(backoff):
			}
		}

		status, latency, err := CheckURLOnceWithProxy(ctx, url, timeout, socks5Addr)
		lastStatus = status
		lastLatency = latency
		lastErr = err

		for _, observe := range observers {
			if observe != nil {
				observe(CheckAttempt{
					Attempt:    attempt + 1,
					StatusCode: status,
					Latency:    latency,
					Err:        err,
				})
			}
		}

		if err == nil && status >= 200 && status < 400 {
			return CheckResult{
				StatusCode: status,
				Latency:    latency,
				Success:    true,
			}
		}
	}

	return CheckResult{
		StatusCode: lastStatus,
		Latency:    lastLatency,
		Error:      lastErr,
		Success:    false,
	}
}
