//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/daemon"
	"github.com/torfstack/grove/internal/privatefs"
)

func processWatchConfig(t *testing.T, f *processFixture) {
	t.Helper()
	if err := privatefs.WriteJSON(f.config, map[string]any{"version": 1, "profile_dir": f.opts.ProfileDir, "mode": "download", "interval": "5m", "watch_local": true}); err != nil {
		t.Fatal(err)
	}
}
func TestGrovedWatchDownloadSettles(t *testing.T) {
	f := newProcessFixture(t)
	processWatchConfig(t, f)
	f.remote.mu.Lock()
	f.remote.data = "remote update"
	f.remote.version = "2"
	f.remote.mu.Unlock()
	child := startDaemonChild(t, f, f.config)
	waitReady(t, child, func() bool {
		data, err := os.ReadFile(filepath.Join(f.opts.LocalDir, "a"))
		return err == nil && string(data) == "remote update"
	})
	waitReady(t, child, func() bool {
		r, err := daemon.LoadRecord(f.opts.ProfileDir)
		return err == nil && r != nil && r.Phase == "waiting" && r.Outcome == "completed" && r.Counts.Updated == 0 && r.Counts.Downloaded == 0 && r.AttemptStartedAt != nil
	})
	before, err := daemon.LoadRecord(f.opts.ProfileDir)
	if err != nil {
		t.Fatal(err)
	}
	// More than two quiet windows must pass without an engine-generated loop.
	time.Sleep(2200 * time.Millisecond)
	after, err := daemon.LoadRecord(f.opts.ProfileDir)
	if err != nil || !after.AttemptStartedAt.Equal(*before.AttemptStartedAt) {
		t.Fatalf("self-triggering loop: %v", err)
	}
	if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitProcess(t, child); err != nil {
		t.Fatal(err)
	}
}
func TestGrovedWatchEditDuringSync(t *testing.T) {
	f := newProcessFixture(t)
	processWatchConfig(t, f)
	gate, started := make(chan struct{}), make(chan struct{})
	f.remote.mu.Lock()
	f.remote.data = "remote update"
	f.remote.version = "2"
	f.remote.gate = gate
	f.remote.started = started
	f.remote.mu.Unlock()
	child := startDaemonChild(t, f, f.config)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("transfer not started")
	}
	edit := filepath.Join(f.opts.LocalDir, "untracked")
	if err := os.WriteFile(edit, []byte("local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	close(gate)
	waitReady(t, child, func() bool {
		r, err := daemon.LoadRecord(f.opts.ProfileDir)
		return err == nil && r != nil && r.Phase == "waiting" && r.Outcome == "failed"
	})
	data, err := os.ReadFile(edit)
	if err != nil || string(data) != "local edit" {
		t.Fatal("local edit lost")
	}
	if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitProcess(t, child); err != nil {
		t.Fatal(err)
	}
}
func TestGrovedWatchSignalsAndFailure(t *testing.T) {
	for _, kind := range []string{"idle-int", "active-term", "root-replaced"} {
		t.Run(kind, func(t *testing.T) {
			f := newProcessFixture(t)
			processWatchConfig(t, f)
			var gate chan struct{}
			if kind == "active-term" {
				gate = make(chan struct{})
				f.remote.mu.Lock()
				f.remote.data = "remote update"
				f.remote.version = "2"
				f.remote.gate = gate
				f.remote.started = make(chan struct{})
				f.remote.mu.Unlock()
			}
			child := startDaemonChild(t, f, f.config)
			if gate != nil {
				select {
				case <-f.remote.started:
				case <-time.After(5 * time.Second):
					t.Fatal("transfer not started")
				}
			} else {
				waitReady(t, child, func() bool { return waiting(f.opts.ProfileDir) })
			}
			switch kind {
			case "root-replaced":
				if err := os.Rename(f.opts.LocalDir, f.opts.LocalDir+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(f.opts.LocalDir, 0700); err != nil {
					t.Fatal(err)
				}
			case "idle-int":
				if err := child.cmd.Process.Signal(os.Interrupt); err != nil {
					t.Fatal(err)
				}
			default:
				if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			err := waitProcess(t, child)
			if kind == "root-replaced" {
				if err == nil {
					t.Fatal("root replacement accepted")
				}
				data, _ := os.ReadFile(child.log)
				if strings.Contains(string(data), f.opts.LocalDir) {
					t.Fatal("watch error exposed destination")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			lease, err := privatefs.AcquireLease(filepath.Join(f.opts.ProfileDir, "daemon.lock"))
			if err != nil {
				t.Fatal("ownership not released")
			}
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
