//go:build linux

package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

type controlledClock struct {
	now  time.Time
	wait func(context.Context, time.Duration) error
}

func (c *controlledClock) Now() time.Time                                  { return c.now }
func (c *controlledClock) Wait(ctx context.Context, d time.Duration) error { return c.wait(ctx, d) }

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func daemonFixture(t *testing.T, mode string) (string, string, syncengine.Binding) {
	t.Helper()
	profile := t.TempDir()
	binding := syncengine.Binding{RemoteRoot: "root", LocalDir: t.TempDir(), TokenFile: filepath.Join(t.TempDir(), "token.json")}
	if err := privatefs.WriteJSON(filepath.Join(profile, "state.json"), syncengine.State{Version: 2, Binding: binding, PopulationComplete: true}); err != nil {
		t.Fatal(err)
	}
	return writeConfig(t, configText(profile, mode, "")), profile, binding
}

func TestDaemonStartupAndScheduling(t *testing.T) {
	path, profile, binding := daemonFixture(t, "download")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &controlledClock{now: time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)}
	calls, waits := 0, 0
	clock.wait = func(_ context.Context, d time.Duration) error {
		waits++
		if d != 5*time.Minute || calls != 1 {
			t.Fatalf("wrong completion wait: %s calls=%d", d, calls)
		}
		r, err := LoadRecord(profile)
		if err != nil || r.NextAttemptAt == nil || !r.NextAttemptAt.Equal(clock.now.Add(d)) {
			t.Fatalf("wrong next attempt: %+v %v", r, err)
		}
		clock.now = clock.now.Add(d)
		return nil
	}
	err := Run(ctx, path, Dependencies{Clock: clock, RunExisting: func(_ context.Context, p string, expected syncengine.Binding, twoWay bool) (syncengine.Result, error) {
		calls++
		if p != profile || expected != binding || twoWay {
			t.Fatal("config/binding changed mid-run")
		}
		if calls == 1 {
			clock.now = clock.now.Add(12 * time.Minute)
			if err := os.WriteFile(path, []byte(configText(profile, "two-way", "1s")), 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			clock.now = clock.now.Add(-time.Hour)
			cancel()
		}
		return syncengine.Result{Downloaded: calls}, nil
	}})
	if err != nil || calls != 2 || waits != 1 {
		t.Fatalf("scheduler result calls=%d waits=%d: %v", calls, waits, err)
	}
	r, err := LoadRecord(profile)
	if err != nil || r.Phase != "stopped" || r.Counts.Downloaded != 2 || r.Outcome != "completed" || r.NextAttemptAt != nil {
		t.Fatalf("wrong stopped observation: %+v %v", r, err)
	}
}

func TestDaemonStartupOwnership(t *testing.T) {
	for _, kind := range []string{"missing-state", "invalid-state", "busy-profile", "busy-daemon", "incomplete-two-way", "cancelled-inspection"} {
		t.Run(kind, func(t *testing.T) {
			path, profile, _ := daemonFixture(t, "two-way")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			deps := Dependencies{RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
				t.Fatal("invalid startup synced")
				return syncengine.Result{}, nil
			}}
			switch kind {
			case "missing-state":
				if err := os.Remove(filepath.Join(profile, "state.json")); err != nil {
					t.Fatal(err)
				}
			case "invalid-state":
				if err := os.WriteFile(filepath.Join(profile, "state.json"), []byte("bad"), 0600); err != nil {
					t.Fatal(err)
				}
			case "busy-profile", "busy-daemon":
				name := "profile.lock"
				if kind == "busy-daemon" {
					name = "daemon.lock"
				}
				lock, err := privatefs.AcquireLease(filepath.Join(profile, name))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = lock.Close() }()
			case "incomplete-two-way":
				var state syncengine.State
				if err := privatefs.ReadJSON(filepath.Join(profile, "state.json"), &state); err != nil {
					t.Fatal(err)
				}
				state.PopulationComplete = false
				if err := privatefs.WriteJSON(filepath.Join(profile, "state.json"), state); err != nil {
					t.Fatal(err)
				}
			case "cancelled-inspection":
				deps.InspectProfile = func(context.Context, string) (syncengine.ProfileSummary, error) {
					cancel()
					return syncengine.ProfileSummary{}, context.Canceled
				}
			}
			err := Run(ctx, path, deps)
			if kind == "cancelled-inspection" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("startup failure ignored")
			}
			if _, err := os.Stat(filepath.Join(profile, "daemon-status.json")); !os.IsNotExist(err) {
				t.Fatal("invalid startup wrote status")
			}
		})
	}
}

func TestDaemonFailureAndStop(t *testing.T) {
	for _, busy := range []bool{false, true} {
		path, profile, _ := daemonFixture(t, "two-way")
		ctx, cancel := context.WithCancel(context.Background())
		clock := &controlledClock{now: time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)}
		clock.wait = func(_ context.Context, d time.Duration) error {
			if d != 5*time.Minute {
				t.Fatal("failure retried without interval")
			}
			r, err := LoadRecord(profile)
			if err != nil {
				t.Fatal(err)
			}
			want := "failed"
			if busy {
				want = "busy"
			}
			if r.Outcome != want || r.Counts.Downloaded != 1 {
				t.Fatalf("wrong failure record: %+v", r)
			}
			cancel()
			return context.Canceled
		}
		calls := 0
		err := Run(ctx, path, Dependencies{Clock: clock, RunExisting: func(_ context.Context, _ string, _ syncengine.Binding, twoWay bool) (syncengine.Result, error) {
			if !twoWay {
				t.Fatal("two-way not forwarded")
			}
			calls++
			if busy {
				return syncengine.Result{Downloaded: 1}, privatefs.ErrLocked
			}
			return syncengine.Result{Downloaded: 1}, errors.New("private-token-sentinel")
		}})
		if err != nil || calls != 1 {
			t.Fatalf("error scheduling/stop: %v", err)
		}
	}
	path, profile, _ := daemonFixture(t, "download")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, path, Dependencies{RunExisting: func(ctx context.Context, _ string, _ syncengine.Binding, _ bool) (syncengine.Result, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			<-release
			return syncengine.Result{}, ctx.Err()
		}})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("attempt did not start")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("attempt did not receive cancellation")
	}
	if active, err := privatefs.ProbeLease(filepath.Join(profile, "daemon.lock")); err != nil || !active {
		t.Fatalf("lease released before active attempt returned: %t %v", active, err)
	}
	select {
	case err := <-done:
		t.Fatalf("returned before active cleanup: %v", err)
	default:
	}
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish")
	}
}

func TestDaemonRecordFailures(t *testing.T) {
	for _, phase := range []string{"starting", "syncing", "waiting", "stopping", "close"} {
		t.Run(phase, func(t *testing.T) {
			path, profile, _ := daemonFixture(t, "download")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			clock := &controlledClock{now: time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC), wait: func(context.Context, time.Duration) error { cancel(); return context.Canceled }}
			deps := Dependencies{Clock: clock, RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
				calls++
				return syncengine.Result{Downloaded: 1}, nil
			}, SaveRecord: func(profile string, r Record) error {
				if r.Phase == phase {
					return errors.New("private-save-sentinel")
				}
				return SaveRecord(profile, r)
			}}
			if phase == "close" {
				deps.AcquireLease = func(path string) (io.Closer, error) {
					lock, err := privatefs.AcquireLease(path)
					if err != nil {
						return nil, err
					}
					return closeFunc(func() error { _ = lock.Close(); return errors.New("private-close-sentinel") }), nil
				}
			}
			err := Run(ctx, path, deps)
			if err == nil {
				t.Fatal("persistence/cleanup failure ignored")
			}
			if (phase == "starting" || phase == "syncing") && calls != 0 {
				t.Fatal("sync started without durable observation")
			}
			if calls > 1 {
				t.Fatal("completed attempt repeated after status failure")
			}
			if active, probeErr := privatefs.ProbeLease(filepath.Join(profile, "daemon.lock")); probeErr != nil || active {
				t.Fatal("fatal exit leaked lifetime lease")
			}
		})
	}
}

func TestDaemonMissingProfileDoesNotRecreate(t *testing.T) {
	path, profile, _ := daemonFixture(t, "download")
	err := Run(context.Background(), path, Dependencies{RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
		if err := os.RemoveAll(profile); err != nil {
			t.Fatal(err)
		}
		return syncengine.Result{Downloaded: 1}, nil
	}})
	if err == nil {
		t.Fatal("missing status parent accepted")
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatal("advisory status recreated the deleted profile")
	}
}

func TestDaemonCancellationPreservesOtherFailures(t *testing.T) {
	path, profile, _ := daemonFixture(t, "download")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := Run(ctx, path, Dependencies{RunExisting: func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error) {
		cancel()
		return syncengine.Result{}, errors.Join(context.Canceled, errors.New("private-cleanup-secret"))
	}})
	if err == nil || strings.Contains(err.Error(), "private-cleanup-secret") {
		t.Fatalf("shutdown error ignored or leaked: %v", err)
	}
	r, loadErr := LoadRecord(profile)
	if loadErr != nil || r.Outcome != "failed" || r.Phase != "stopped" {
		t.Fatalf("mixed shutdown outcome lost: %+v %v", r, loadErr)
	}
}
