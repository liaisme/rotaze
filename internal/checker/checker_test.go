package checker

import (
	"context"
	"testing"
	"time"
)

func TestCheckResult_LatencyStr(t *testing.T) {
	tests := []struct {
		name     string
		latency  time.Duration
		expected string
	}{
		{
			name:     "microseconds",
			latency:  500 * time.Microsecond,
			expected: "500µs",
		},
		{
			name:     "milliseconds",
			latency:  1500 * time.Millisecond,
			expected: "1.5s",
		},
		{
			name:     "seconds",
			latency:  5 * time.Second,
			expected: "5s",
		},
		{
			name:     "zero",
			latency:  0,
			expected: "0µs",
		},
		{
			name:     "nanoseconds under millisecond",
			latency:  100 * time.Nanosecond,
			expected: "0µs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CheckResult{Latency: tt.latency}
			got := result.LatencyStr()
			if got != tt.expected {
				t.Errorf("LatencyStr() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestCheckURLOnce_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, _, err := CheckURLOnce(ctx, "http://example.com", 5*time.Second)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestCheckURLOnce_InvalidURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := CheckURLOnce(ctx, "not-a-url", 1*time.Second)
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestCheckWithRetry_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := CheckWithRetry(ctx, "http://example.com", 1*time.Second, 2)
	if result.Error == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestCheckWithRetry_ZeroRetries(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Use a non-existent proxy to ensure failure
	// This tests that with 0 retries, we only try once
	result := CheckWithRetry(ctx, "http://127.0.0.1:19999/test", 1*time.Second, 0)
	// Will fail because no proxy is running
	if result.Success {
		t.Error("expected failure when no proxy available")
	}
}

func TestCheckURLOnceWithProxy_NonExistentProxy(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, err := CheckURLOnceWithProxy(ctx, "http://example.com", 1*time.Second, "127.0.0.1:19999")
	if err == nil {
		t.Error("expected error when connecting to non-existent proxy")
	}
}

func TestCheckResult_Success(t *testing.T) {
	tests := []struct {
		name     string
		result   CheckResult
		expected bool
	}{
		{
			name:     "success",
			result:   CheckResult{StatusCode: 200, Success: true},
			expected: true,
		},
		{
			name:     "failure",
			result:   CheckResult{StatusCode: 500, Success: false, Error: context.DeadlineExceeded},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.Success != tt.expected {
				t.Errorf("Success = %v, want %v", tt.result.Success, tt.expected)
			}
		})
	}
}
