//go:build linux

package daemon

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
	"testing"
	"time"
)

func TestRemoteSourcePolling(t *testing.T) {
	clock := &watchTestClock{now: time.Now(), timers: make(chan *watchTestTimer, 10)}
	calls := 0
	source := openRemoteSource(context.Background(), 30*time.Second, clock, func(ctx context.Context, cursor string) (syncengine.RemoteChangesResult, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) < 9*time.Second {
			t.Error("whole-check budget missing")
		}
		if calls == 1 {
			if cursor != "" {
				t.Error("initial cursor not empty")
			}
			return syncengine.RemoteChangesResult{Cursor: "first", Changed: true}, nil
		}
		if cursor != "first" {
			t.Error("cursor did not advance")
		}
		return syncengine.RemoteChangesResult{Cursor: "second"}, nil
	})
	defer func() {
		if err := source.Close(); err != nil {
			t.Error(err)
		}
	}()
	timer := nextWatchTimer(t, clock)
	if timer.duration != 30*time.Second {
		t.Fatal(timer.duration)
	}
	b := source.Take()
	if b.First.IsZero() || b.Remote == nil || b.Remote.Outcome != "completed" {
		t.Fatalf("initial observation: %+v", b)
	}
	if source.Take().Remote != nil {
		t.Fatal("remote observation was not drained")
	}
	clock.advance(30 * time.Second)
	timer.ch <- clock.Now()
	_ = nextWatchTimer(t, clock)
	b = source.Take()
	if !b.First.IsZero() || b.Remote == nil || b.Remote.Outcome != "completed" {
		t.Fatalf("empty feed observation: %+v", b)
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestRemoteSourceFailureRetry(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial-error", true: "busy"}[busy], func(t *testing.T) {
			clock := &watchTestClock{now: time.Now(), timers: make(chan *watchTestTimer, 10)}
			calls := 0
			source := openRemoteSource(context.Background(), 30*time.Second, clock, func(_ context.Context, cursor string) (syncengine.RemoteChangesResult, error) {
				calls++
				if calls == 1 {
					return syncengine.RemoteChangesResult{Cursor: "old", Changed: true}, nil
				}
				if cursor != "old" {
					t.Error("failed check advanced cursor")
				}
				if busy {
					return syncengine.RemoteChangesResult{Cursor: "wrong"}, privatefs.ErrLocked
				}
				return syncengine.RemoteChangesResult{Cursor: "wrong", Changed: true}, errors.New("private-api-error")
			})
			defer func() { _ = source.Close() }()
			timer := nextWatchTimer(t, clock)
			_ = source.Take()
			clock.advance(30 * time.Second)
			timer.ch <- clock.Now()
			timer = nextWatchTimer(t, clock)
			b := source.Take()
			want := "failed"
			if busy {
				want = "busy"
			}
			if b.Err != nil || b.Remote == nil || b.Remote.Outcome != want || (!busy && b.First.IsZero()) || (busy && !b.First.IsZero()) {
				t.Fatalf("failure observation: %+v", b)
			}
			clock.advance(30 * time.Second)
			timer.ch <- clock.Now()
			_ = nextWatchTimer(t, clock)
		})
	}
}
func TestRemoteSourceShutdownFaults(t *testing.T) {
	for _, fault := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled", true: "cleanup"}[fault], func(t *testing.T) {
			clock := &watchTestClock{now: time.Now(), timers: make(chan *watchTestTimer, 10)}
			started := make(chan struct{})
			cleanup := errors.New("private-close-fault")
			source := openRemoteSource(context.Background(), 30*time.Second, clock, func(ctx context.Context, _ string) (syncengine.RemoteChangesResult, error) {
				close(started)
				<-ctx.Done()
				if fault {
					return syncengine.RemoteChangesResult{}, errors.Join(ctx.Err(), syncengine.ErrRemoteCheckCleanup, cleanup)
				}
				return syncengine.RemoteChangesResult{}, ctx.Err()
			})
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("poll did not start")
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			if err := source.Close(); err != nil {
				t.Fatal(err)
			}
			b := source.Take()
			if fault {
				if !errors.Is(b.Err, cleanup) {
					t.Fatal("cleanup fault lost after join")
				}
			} else if b.Err != nil {
				t.Fatal(b.Err)
			}
		})
	}
}
