package rotator

import (
	"testing"
	"time"
)

// blockingCloseBox implements boxServer with a Close that never returns,
// simulating the sing-box drain stall behind the monitor freeze: an in-flight
// handler stuck dialing a blackholed remote (the box sets no dial timeout), so
// Close never finishes draining.
type blockingCloseBox struct {
	closeStarted chan struct{}
}

func (b *blockingCloseBox) Start() error { return nil }

func (b *blockingCloseBox) Close() error {
	close(b.closeStarted)
	select {} // hang forever, like a stuck drain
}

// TestRotator_stopLocked_CloseHangCapped is the regression test for the
// monitor freeze: when instance.Close never returns, stopLocked must give up
// after closeTimeout, force-abort via cancel(), leave no live instance
// behind, and return nil — an error here would fail SwitchOutbound and stall
// outlet recovery until the next reconcile pass.
func TestRotator_stopLocked_CloseHangCapped(t *testing.T) {
	old := closeTimeout
	closeTimeout = 50 * time.Millisecond
	defer func() { closeTimeout = old }()

	r := newTestRotator(t, 0) // port is irrelevant: nothing is started
	closeStarted := make(chan struct{})
	cancelled := make(chan struct{})
	r.instance = &blockingCloseBox{closeStarted: closeStarted}
	r.cancel = func() { close(cancelled) }
	r.running = true

	done := make(chan error, 1)
	go func() { done <- r.stopLocked() }()

	select {
	case <-closeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("instance.Close was never called")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stopLocked errored on hung close: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stopLocked outlived closeTimeout with a hung close")
	}

	select {
	case <-cancelled:
	default:
		t.Error("cancel() was not called after the close timeout")
	}
	if r.instance != nil {
		t.Error("stopLocked left instance set")
	}
	if r.cancel != nil {
		t.Error("stopLocked left cancel set")
	}
	if r.running {
		t.Error("stopLocked left running=true")
	}
}
