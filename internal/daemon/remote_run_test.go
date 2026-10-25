//go:build linux

package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

func remoteConfig(t *testing.T) string {
	t.Helper()
	path, _, _ := daemonFixture(t, "download")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSuffix(string(data), "}")+`,"remote_poll_interval":"30s"}`), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func timerDuration(t *testing.T, c *watchTestClock, want time.Duration) *watchTestTimer {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case timer := <-c.timers:
			if timer.duration == want {
				return timer
			}
		case <-deadline.C:
			t.Fatalf("missing %s timer", want)
			return nil
		}
	}
}
func remoteRecord(t *testing.T, records <-chan Record, predicate func(Record) bool) Record {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case r := <-records:
			if predicate(r) {
				return r
			}
		case <-deadline.C:
			t.Fatal("missing remote status observation")
			return Record{}
		}
	}
}
func TestDaemonRemotePolling(t *testing.T) {
	path := remoteConfig(t)
	clock := &watchTestClock{now: time.Now(), timers: make(chan *watchTestTimer, 30)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	records := make(chan Record, 100)
	done := make(chan error, 1)
	var initialized atomic.Bool
	var calls atomic.Int32
	go func() {
		done <- Run(ctx, path, Dependencies{Clock: clock, OpenWatcher: func(context.Context, string, Clock) (Watcher, error) {
			t.Error("remote-only mode opened local watcher")
			return nil, errors.New("unwanted watcher")
		}, SaveRecord: func(_ string, r Record) error { records <- r; return nil }, CheckRemoteChanges: func(_ context.Context, _ string, _ syncengine.Binding, cursor string) (syncengine.RemoteChangesResult, error) {
			if !initialized.Load() {
				return syncengine.RemoteChangesResult{}, privatefs.ErrLocked
			}
			if cursor != "" {
				t.Error("unexpected initial cursor")
			}
			return syncengine.RemoteChangesResult{Cursor: "initial", Changed: true}, nil
		}, RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
			calls.Add(1)
			return syncengine.Result{Downloaded: 7}, nil
		}})
	}()
	poll := timerDuration(t, clock, 30*time.Second)
	first := remoteRecord(t, records, func(r Record) bool {
		return r.Phase == "waiting" && r.RemoteOutcome == "busy" && r.Counts.Downloaded == 7
	})
	if first.RemotePollInterval != 30*time.Second || first.RemoteCheckedAt == nil {
		t.Fatal("remote history omitted")
	}
	initialized.Store(true)
	clock.advance(30 * time.Second)
	poll.ch <- clock.Now()
	debounce := timerDuration(t, clock, time.Second)
	observed := remoteRecord(t, records, func(r Record) bool { return r.Phase == "waiting" && r.RemoteOutcome == "completed" })
	if observed.Counts.Downloaded != 7 || calls.Load() != 1 {
		t.Fatal("remote observation reset sync history or overlapped work")
	}
	clock.advance(time.Second)
	debounce.ch <- clock.Now()
	remoteRecord(t, records, func(r Record) bool {
		return r.Phase == "waiting" && r.AttemptStartedAt != nil && r.AttemptStartedAt.After(*first.AttemptStartedAt)
	})
	if calls.Load() != 2 {
		t.Fatal("cursor initialization did not request a later scan", calls.Load())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("remote shutdown hung")
	}
}
func TestDaemonRemoteStatus(t *testing.T) {
	path := remoteConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	records := make(chan Record, 100)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, path, Dependencies{SaveRecord: func(_ string, r Record) error { records <- r; return nil }, CheckRemoteChanges: func(context.Context, string, syncengine.Binding, string) (syncengine.RemoteChangesResult, error) {
			return syncengine.RemoteChangesResult{}, errors.New("private-auth-error")
		}, RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
			return syncengine.Result{Skipped: 3}, nil
		}})
	}()
	r := remoteRecord(t, records, func(r Record) bool {
		return r.Phase == "waiting" && r.RemoteOutcome == "failed" && r.Counts.Skipped == 3
	})
	if r.RemoteCheckedAt == nil || r.Reason != "" || r.Outcome != "completed" {
		t.Fatal("remote failure corrupted normal attempt history")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown")
	}
}
func TestDaemonRemoteGate(t *testing.T) {
	path := remoteConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan string, 10)
	release := make(chan struct{})
	done := make(chan error, 1)
	var active atomic.Int32
	work := func(ctx context.Context, kind string) {
		if active.Add(1) != 1 {
			t.Error("overlapping check/sync work")
		}
		defer active.Add(-1)
		entered <- kind
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	go func() {
		done <- Run(ctx, path, Dependencies{CheckRemoteChanges: func(ctx context.Context, _ string, _ syncengine.Binding, _ string) (syncengine.RemoteChangesResult, error) {
			work(ctx, "check")
			return syncengine.RemoteChangesResult{Cursor: "initial", Changed: true}, nil
		}, RunExisting: func(ctx context.Context, _ string, _ syncengine.Binding, _ bool) (syncengine.Result, error) {
			work(ctx, "sync")
			return syncengine.Result{}, nil
		}})
	}()
	var first string
	select {
	case first = <-entered:
	case err := <-done:
		t.Fatal("startup", err)
	case <-time.After(3 * time.Second):
		t.Fatal("no work")
	}
	select {
	case second := <-entered:
		t.Fatalf("concurrent jobs: %s %s", first, second)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case second := <-entered:
		if second == first {
			t.Fatal("other job did not progress")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gate did not release after cleanup")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("gate shutdown")
	}
}
func TestDaemonRemoteShutdown(t *testing.T) {
	path := remoteConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, cleaned := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	fault := errors.New("private-poll-cleanup")
	go func() {
		done <- Run(ctx, path, Dependencies{AcquireLease: func(string) (io.Closer, error) {
			return closeFunc(func() error {
				select {
				case <-cleaned:
				default:
					t.Error("ownership released before poll cleanup")
				}
				return nil
			}), nil
		}, CheckRemoteChanges: func(ctx context.Context, _ string, _ syncengine.Binding, _ string) (syncengine.RemoteChangesResult, error) {
			close(started)
			<-ctx.Done()
			close(cleaned)
			return syncengine.RemoteChangesResult{}, errors.Join(ctx.Err(), syncengine.ErrRemoteCheckCleanup, fault)
		}, RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
			return syncengine.Result{}, nil
		}})
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatal("startup", err)
	case <-time.After(3 * time.Second):
		t.Fatal("poll not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, fault) || strings.Contains(err.Error(), "private") {
			t.Fatalf("late cleanup failure lost/exposed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("remote cleanup hung")
	}
}
