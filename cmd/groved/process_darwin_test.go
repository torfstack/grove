//go:build darwin

package main

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/torfstack/grove/internal/daemon"
	"github.com/torfstack/grove/internal/privatefs"
)

func TestDarwinDaemonProcessOwnershipAndShutdown(t *testing.T) {
	f := newProcessFixture(t)
	first := startDaemonChild(t, f, f.config)
	waitReady(t, first, func() bool { return waiting(f.opts.ProfileDir) })
	second := startDaemonChild(t, f, f.config)
	if err := waitProcess(t, second); err == nil {
		t.Fatal("second daemon acquired the active profile")
	}
	if err := first.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitProcess(t, first); err != nil {
		t.Fatalf("SIGTERM did not shut down cleanly: %v", err)
	}
	if active, err := privatefs.ProbeLease(filepath.Join(f.opts.ProfileDir, "daemon.lock")); err != nil || active {
		t.Fatalf("daemon lease remains active after shutdown: active=%t err=%v", active, err)
	}
	record, err := daemon.LoadRecord(f.opts.ProfileDir)
	if err != nil || record == nil || record.Phase != "waiting" {
		t.Fatalf("expected retained waiting status: %+v %v", record, err)
	}
}
