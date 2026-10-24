//go:build linux || darwin

package privatefs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLeaseExclusiveAndSharedProbe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	lock, err := AcquireLease(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if other, err := AcquireLease(path); !errors.Is(err, ErrLocked) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("exclusive contention: %v", err)
	}
	if active, err := ProbeLease(path); !active || err != nil {
		t.Fatalf("active lease: %t %v", active, err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestLeaseChild$")
	child.Env = append(os.Environ(), "GROVE_LEASE_CHILD="+path)
	if data, err := child.CombinedOutput(); err != nil {
		t.Fatalf("child exclusion: %s %v", data, err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if active, err := ProbeLease(path); active || err != nil {
		t.Fatalf("inactive lease: %t %v", active, err)
	}
	missing := filepath.Join(t.TempDir(), "absent", "daemon.lock")
	if active, err := ProbeLease(missing); active || err != nil {
		t.Fatalf("missing lease: %t %v", active, err)
	}
	if _, err := os.Stat(filepath.Dir(missing)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("probe created a directory")
	}
	if lock, err := AcquireLease(missing); err == nil {
		_ = lock.Close()
		t.Fatal("lease created its parent")
	}
}

func TestLeaseChild(t *testing.T) {
	path := os.Getenv("GROVE_LEASE_CHILD")
	if path == "" {
		return
	}
	lock, err := AcquireLease(path)
	if lock != nil {
		_ = lock.Close()
	}
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("cross-process writer allowed: %v", err)
	}
}

func TestLeaseProbeReadersAndFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.lock")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if active, err := ProbeLease(path); active || err != nil {
		t.Fatalf("reader mistaken for daemon: %t %v", active, err)
	}
	alias := filepath.Join(t.TempDir(), "profile")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if active, err := ProbeLease(filepath.Join(alias, "daemon.lock")); active || err != nil {
		t.Fatalf("parent alias: %t %v", active, err)
	}
	leaf := filepath.Join(dir, "alias.lock")
	if err := os.Symlink(path, leaf); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{leaf, dir, filepath.Join(path, "child")} {
		if active, err := ProbeLease(bad); err == nil || active {
			t.Fatalf("invalid probe accepted: %t %v", active, err)
		}
		if lock, err := AcquireLease(bad); err == nil || errors.Is(err, ErrLocked) {
			if lock != nil {
				_ = lock.Close()
			}
			t.Fatalf("invalid lease mistaken for contention: %v", err)
		}
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(path, 0000); err != nil {
			t.Fatal(err)
		}
		if active, err := ProbeLease(path); err == nil || active {
			t.Fatalf("permission failure mistaken for inactivity: %t %v", active, err)
		}
	}
}

func TestLockBusyClassification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.lock")
	lock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	if other, err := Acquire(path); !errors.Is(err, ErrLocked) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("file contention untyped: %v", err)
	}
	directory, err := AcquireDirectory(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = directory.Close() }()
	if other, err := AcquireDirectory(dir, false); !errors.Is(err, ErrLocked) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("directory contention untyped: %v", err)
	}
	if other, err := AcquireDirectory(path, false); err == nil || errors.Is(err, ErrLocked) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("invalid directory mistaken for contention: %v", err)
	}
}
