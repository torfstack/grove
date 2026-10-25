//go:build linux

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/daemon"
	"github.com/torfstack/grove/internal/privatefs"
)

func remoteProcessConfig(t *testing.T, f *processFixture, local bool) {
	t.Helper()
	if err := privatefs.WriteJSON(f.config, map[string]any{"version": 1, "profile_dir": f.opts.ProfileDir, "mode": "download", "interval": "10m", "remote_poll_interval": "10s", "watch_local": local}); err != nil {
		t.Fatal(err)
	}
}
func waitRemoteProcess(t *testing.T, child *daemonChild, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("remote process condition timed out")
		case <-child.done:
			t.Fatalf("remote daemon exited: %v", child.err)
		case <-tick.C:
		}
	}
}
func TestGrovedRemoteDownload(t *testing.T) {
	f := newProcessFixture(t)
	remoteProcessConfig(t, f, false)
	child := startDaemonChild(t, f, f.config)
	var initialized time.Time
	waitRemoteProcess(t, child, func() bool {
		r, err := daemon.LoadRecord(f.opts.ProfileDir)
		if err != nil || r == nil || r.RemoteOutcome != "completed" || r.RemoteCheckedAt == nil {
			return false
		}
		initialized = *r.RemoteCheckedAt
		return true
	})
	waitRemoteProcess(t, child, func() bool {
		r, err := daemon.LoadRecord(f.opts.ProfileDir)
		return err == nil && r != nil && r.Phase == "waiting" && r.Outcome == "completed" && r.AttemptStartedAt != nil && r.AttemptStartedAt.After(initialized) && r.NextAttemptAt != nil && time.Until(*r.NextAttemptAt) > time.Minute
	})
	changed := time.Now()
	f.remote.mu.Lock()
	f.remote.data = "remote feed update"
	f.remote.version = "2"
	f.remote.mu.Unlock()
	waitRemoteProcess(t, child, func() bool {
		data, err := os.ReadFile(filepath.Join(f.opts.LocalDir, "a"))
		return err == nil && string(data) == "remote feed update"
	})
	if time.Since(changed) >= time.Minute {
		t.Fatal("remote detection waited for periodic sync")
	}
	var downloaded *daemon.Record
	waitRemoteProcess(t, child, func() bool {
		r, err := daemon.LoadRecord(f.opts.ProfileDir)
		if err != nil || r == nil || r.Phase != "waiting" || r.Outcome != "completed" || r.Counts.Downloaded != 1 || r.AttemptStartedAt == nil || !r.AttemptStartedAt.After(changed) {
			return false
		}
		downloaded = r
		return true
	})
	waitRemoteProcess(t, child, func() bool {
		r, err := daemon.LoadRecord(f.opts.ProfileDir)
		if err != nil || r == nil || r.RemoteCheckedAt == nil || !r.RemoteCheckedAt.After(*downloaded.AttemptFinishedAt) {
			return false
		}
		if r.RemoteOutcome != "completed" || !r.AttemptStartedAt.Equal(*downloaded.AttemptStartedAt) || r.Counts.Downloaded != 1 {
			t.Fatal("empty feed changed sync history or repeated engine work")
		}
		return true
	})
	if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitProcess(t, child); err != nil {
		t.Fatal(err)
	}
}
func TestGrovedRemoteSignals(t *testing.T) {
	for _, kind := range []string{"remote-only", "combined", "during-check"} {
		t.Run(kind, func(t *testing.T) {
			f := newProcessFixture(t)
			remoteProcessConfig(t, f, kind == "combined")
			var started chan struct{}
			if kind == "during-check" {
				started = make(chan struct{})
				f.remote.mu.Lock()
				f.remote.changesGate = make(chan struct{})
				f.remote.changesStarted = started
				f.remote.mu.Unlock()
			}
			child := startDaemonChild(t, f, f.config)
			if started != nil {
				select {
				case <-started:
				case <-child.done:
					t.Fatalf("startup failed: %v", child.err)
				case <-time.After(10 * time.Second):
					t.Fatal("poll did not start")
				}
			} else {
				waitRemoteProcess(t, child, func() bool { return waiting(f.opts.ProfileDir) })
			}
			if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			if err := waitProcess(t, child); err != nil {
				t.Fatal(err)
			}
			lock, err := privatefs.AcquireLease(filepath.Join(f.opts.ProfileDir, "daemon.lock"))
			if err != nil {
				t.Fatal("daemon retained lifetime ownership")
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
