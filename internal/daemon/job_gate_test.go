package daemon

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestJobGateCancellationAndCleanup(t *testing.T) {
	gate := newJobGate()
	if err := gate.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- gate.acquire(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("gate did not wait: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("gate wait leaked")
	}
	acquired := make(chan struct{})
	release := make(chan struct{})
	go func() {
		if err := gate.acquire(context.Background()); err != nil {
			done <- err
			return
		}
		close(acquired)
		<-release
		gate.release()
		done <- nil
	}()
	select {
	case <-acquired:
		t.Fatal("overlapping work entered gate")
	default:
	}
	gate.release()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("released gate unavailable")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
