//go:build linux

package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

type fakeWatcher struct {
	box      *watchMailbox
	closed   chan struct{}
	closeErr error
}

func newFakeWatcher() *fakeWatcher {
	return &fakeWatcher{box: newWatchMailbox(), closed: make(chan struct{})}
}
func (w *fakeWatcher) Ready() <-chan struct{}          { return w.box.ready }
func (w *fakeWatcher) Take() WatchBatch                { return w.box.take() }
func (w *fakeWatcher) Reconcile(context.Context) error { return nil }
func (w *fakeWatcher) Close() error                    { close(w.closed); return w.closeErr }
func watchConfig(t *testing.T) string {
	t.Helper()
	path, _, _ := daemonFixture(t, "download")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSuffix(string(data), "}")+`,"watch_local":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestDaemonWatchFailureDuringSync(t *testing.T) {
	path := watchConfig(t)
	w := newFakeWatcher()
	started := make(chan struct{})
	done := make(chan error, 1)
	cleaned := false
	go func() {
		done <- Run(context.Background(), path, Dependencies{OpenWatcher: func(context.Context, string, Clock) (Watcher, error) { return w, nil }, RunExisting: func(ctx context.Context, _ string, _ syncengine.Binding, _ bool) (syncengine.Result, error) {
			close(started)
			<-ctx.Done()
			cleaned = true
			return syncengine.Result{}, context.Cause(ctx)
		}})
	}()
	<-started
	w.box.fail(errors.New("private-watch-error"))
	select {
	case err := <-done:
		if err == nil || strings.Contains(err.Error(), "private-watch-error") {
			t.Fatal(err)
		}
		if !cleaned {
			t.Fatal("worker not joined")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch failure hung")
	}
	select {
	case <-w.closed:
	default:
		t.Fatal("watcher not closed")
	}
}
func TestDaemonWatchShutdownJoins(t *testing.T) {
	path := watchConfig(t)
	w := newFakeWatcher()
	w.closeErr = errors.New("private-close-error")
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, path, Dependencies{OpenWatcher: func(context.Context, string, Clock) (Watcher, error) { return w, nil }, AcquireLease: func(string) (io.Closer, error) {
			return closeFunc(func() error {
				select {
				case <-w.closed:
				default:
					t.Error("lease closed before watcher")
				}
				return nil
			}), nil
		}, RunExisting: func(ctx context.Context, _ string, _ syncengine.Binding, _ bool) (syncengine.Result, error) {
			close(started)
			<-ctx.Done()
			return syncengine.Result{}, ctx.Err()
		}})
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, w.closeErr) || strings.Contains(err.Error(), "private-close-error") {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown hung")
	}
}

type watchTestClock struct {
	mu     sync.Mutex
	now    time.Time
	timers chan *watchTestTimer
}
type watchTestTimer struct {
	ch       chan time.Time
	duration time.Duration
}

func (t *watchTestTimer) C() <-chan time.Time                       { return t.ch }
func (t *watchTestTimer) Stop() bool                                { return true }
func (c *watchTestClock) Now() time.Time                            { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *watchTestClock) Wait(context.Context, time.Duration) error { panic("unselectable wait") }
func (c *watchTestClock) NewTimer(d time.Duration) Timer {
	t := &watchTestTimer{make(chan time.Time, 1), d}
	c.timers <- t
	return t
}
func (c *watchTestClock) advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }
func nextWatchTimer(t *testing.T, c *watchTestClock) *watchTestTimer {
	t.Helper()
	select {
	case timer := <-c.timers:
		return timer
	case <-time.After(3 * time.Second):
		t.Fatal("missing timer")
		return nil
	}
}
func TestDaemonWatchScheduling(t *testing.T) {
	path := watchConfig(t)
	w := newFakeWatcher()
	clock := &watchTestClock{now: time.Now(), timers: make(chan *watchTestTimer, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan int, 10)
	release := make(chan struct{})
	done := make(chan error, 1)
	n := 0
	go func() {
		done <- Run(ctx, path, Dependencies{Clock: clock, OpenWatcher: func(context.Context, string, Clock) (Watcher, error) { return w, nil }, RunExisting: func(ctx context.Context, _ string, _ syncengine.Binding, _ bool) (syncengine.Result, error) {
			n++
			calls <- n
			if n == 1 {
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return syncengine.Result{}, nil
		}})
	}()
	if got := <-calls; got != 1 {
		t.Fatal(got)
	}
	w.box.hint(clock.Now())
	close(release)
	timer := nextWatchTimer(t, clock)
	if timer.duration != time.Second {
		t.Fatalf("debounce %s", timer.duration)
	}
	clock.advance(time.Second)
	timer.ch <- clock.Now()
	select {
	case n := <-calls:
		if n != 2 {
			t.Fatal(n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no follow-up")
	}
	timer = nextWatchTimer(t, clock)
	if timer.duration != 299*time.Second {
		t.Fatalf("periodic drift: %s", timer.duration)
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

func TestDaemonWatchRetry(t *testing.T) {
	path := watchConfig(t)
	w := newFakeWatcher()
	clock := &watchTestClock{now: time.Now(), timers: make(chan *watchTestTimer, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	calls := 0
	go func() {
		done <- Run(ctx, path, Dependencies{Clock: clock, OpenWatcher: func(context.Context, string, Clock) (Watcher, error) { return w, nil }, RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
			calls++
			w.box.hint(clock.Now())
			if calls == 1 {
				return syncengine.Result{}, privatefs.ErrLocked
			}
			cancel()
			return syncengine.Result{}, nil
		}})
	}()
	timer := nextWatchTimer(t, clock)
	if timer.duration != 5*time.Minute {
		t.Fatalf("busy retry loop: %s", timer.duration)
	}
	clock.advance(5 * time.Minute)
	timer.ch <- clock.Now()
	select {
	case err := <-done:
		if err != nil || calls != 2 {
			t.Fatal(err, calls)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not finish")
	}
}
func TestDaemonWatchStatusDeadline(t *testing.T) {
	path := watchConfig(t)
	w := newFakeWatcher()
	clock := &watchTestClock{now: time.Now(), timers: make(chan *watchTestTimer, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	records := make(chan Record, 20)
	go func() {
		done <- Run(ctx, path, Dependencies{Clock: clock, OpenWatcher: func(context.Context, string, Clock) (Watcher, error) { return w, nil }, SaveRecord: func(_ string, r Record) error { records <- r; return nil }, RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
			return syncengine.Result{}, nil
		}})
	}()
	_ = nextWatchTimer(t, clock)
	w.box.hint(clock.Now())
	timer := nextWatchTimer(t, clock)
	if timer.duration != time.Second {
		t.Fatal(timer.duration)
	}
	var last Record
	for len(records) > 0 {
		last = <-records
	}
	if last.Phase != "waiting" || !last.WatchLocal || last.NextAttemptAt == nil || !last.NextAttemptAt.Equal(clock.Now().Add(time.Second)) {
		t.Fatalf("wrong deadline: %+v", last)
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
func TestDaemonWatchCompletionBoundary(t *testing.T) {
	path := watchConfig(t)
	w := newFakeWatcher()
	clock := &watchTestClock{now: time.Now(), timers: make(chan *watchTestTimer, 20)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	calls := 0
	go func() {
		done <- Run(ctx, path, Dependencies{Clock: clock, OpenWatcher: func(context.Context, string, Clock) (Watcher, error) { return w, nil }, RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
			calls++
			if calls == 1 {
				w.box.hint(clock.Now())
			} else {
				cancel()
			}
			return syncengine.Result{}, nil
		}})
	}()
	timer := nextWatchTimer(t, clock)
	if timer.duration != time.Second {
		t.Fatal(timer.duration)
	}
	clock.advance(time.Second)
	timer.ch <- clock.Now()
	select {
	case err := <-done:
		if err != nil || calls != 2 {
			t.Fatal(err, calls)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lost completion event")
	}
}

func TestDaemonWatchFailureSurvivesSignal(t *testing.T) {
	path := watchConfig(t)
	w := newFakeWatcher()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fault := errors.New("private-watch-failure")
	err := Run(ctx, path, Dependencies{OpenWatcher: func(context.Context, string, Clock) (Watcher, error) { return w, nil }, SaveRecord: func(_ string, r Record) error {
		if r.Phase == "starting" {
			w.box.fail(fault)
			cancel()
		}
		return nil
	}, RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
		t.Fatal("cancelled startup ran engine")
		return syncengine.Result{}, nil
	}})
	if !errors.Is(err, fault) || strings.Contains(err.Error(), "private-watch-failure") {
		t.Fatalf("signal masked watcher fault: %v", err)
	}
}

type reconcileWatcher struct {
	*fakeWatcher
	reconcile func(context.Context) error
}

func (w *reconcileWatcher) Reconcile(ctx context.Context) error { return w.reconcile(ctx) }
func TestDaemonWatchSignalDuringReconcile(t *testing.T) {
	path := watchConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &reconcileWatcher{fakeWatcher: newFakeWatcher(), reconcile: func(ctx context.Context) error { cancel(); return ctx.Err() }}
	err := Run(ctx, path, Dependencies{OpenWatcher: func(context.Context, string, Clock) (Watcher, error) { return w, nil }, RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
		return syncengine.Result{}, nil
	}})
	if err != nil {
		t.Fatalf("graceful signal became coverage failure: %v", err)
	}
}

func TestDaemonWatchSignalPreservesCompletionCleanupError(t *testing.T) {
	path := watchConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanupErr := errors.New("private-engine-cleanup")
	w := &reconcileWatcher{fakeWatcher: newFakeWatcher(), reconcile: func(ctx context.Context) error { cancel(); return ctx.Err() }}
	err := Run(ctx, path, Dependencies{OpenWatcher: func(context.Context, string, Clock) (Watcher, error) { return w, nil }, RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
		return syncengine.Result{}, errors.Join(context.Canceled, cleanupErr)
	}})
	if !errors.Is(err, cleanupErr) || strings.Contains(err.Error(), "private-engine-cleanup") {
		t.Fatalf("completion lost cleanup error: %v", err)
	}
}

type lateFailureWatcher struct {
	*fakeWatcher
	fault error
}

func (w *lateFailureWatcher) Close() error { w.box.fail(w.fault); return w.fakeWatcher.Close() }
func TestDaemonWatchFailureDuringReaderCleanup(t *testing.T) {
	path := watchConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fault := errors.New("private-reader-fault")
	w := &lateFailureWatcher{newFakeWatcher(), fault}
	err := Run(ctx, path, Dependencies{OpenWatcher: func(context.Context, string, Clock) (Watcher, error) { return w, nil }, SaveRecord: func(_ string, r Record) error {
		if r.Phase == "starting" {
			cancel()
		}
		return nil
	}})
	if !errors.Is(err, fault) || strings.Contains(err.Error(), "private-reader-fault") {
		t.Fatalf("reader cleanup lost fault: %v", err)
	}
}

func TestDaemonAttemptCancellationClassification(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err, cause error
		want       string
	}{
		{"attempt-cancelled", context.Canceled, nil, "failed"},
		{"attempt-timeout", context.DeadlineExceeded, nil, "failed"},
		{"daemon-cancelled", context.Canceled, context.Canceled, "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Record{}
			recordAttempt(&r, syncengine.Result{}, tc.err, tc.cause)
			if r.Outcome != tc.want {
				t.Fatalf("outcome=%s want=%s", r.Outcome, tc.want)
			}
		})
	}
}
