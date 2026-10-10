package privatefs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCanonicalAliases(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	a, err := Canonical(filepath.Join(alias, "absent", "file"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Canonical(filepath.Join(target, "absent", "file"))
	if err != nil || a != b {
		t.Fatalf("canonical aliases differ: %v", err)
	}
}
func TestPrivateRecordRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "record.json")
	value := map[string]int{"version": 1}
	if err := WriteJSON(path, value); err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := ReadJSON(path, &got); err != nil || got["version"] != 1 {
		t.Fatalf("roundtrip: %v", err)
	}
	for p, mode := range map[string]os.FileMode{path: 0600, filepath.Dir(path): 0700} {
		s, err := os.Stat(p)
		if err != nil || s.Mode().Perm() != mode {
			t.Fatalf("mode: %v", err)
		}
	}
}
func TestAtomicRecordFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	if err := WriteJSON(path, map[string]int{"value": 1}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(path, map[string]int{"value": 2}, func(string, string) error { return os.ErrPermission }); err == nil {
		t.Fatal("expected failure")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("old record changed")
	}
}
func TestRejectRecordSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(link, map[string]int{"version": 1}); err == nil {
		t.Fatal("accepted symlink")
	}
	var got any
	if err := ReadJSON(link, &got); err == nil {
		t.Fatal("read symlink")
	}
}
func TestProcessLock(t *testing.T) {
	if path := os.Getenv("GROVE_LOCK_HELPER"); path != "" {
		lock, err := Acquire(path)
		if err != nil {
			os.Exit(10)
		}
		if err := lock.Close(); err != nil {
			os.Exit(11)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "lock")
	lock, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	run := func() error {
		cmd := exec.Command(os.Args[0], "-test.run=^TestProcessLock$")
		cmd.Env = append(os.Environ(), "GROVE_LOCK_HELPER="+path)
		return cmd.Run()
	}
	if err := run(); err == nil {
		t.Fatal("second process acquired held lock")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatal("lock not released", err)
	}
}
