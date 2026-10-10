//go:build linux || darwin

package syncengine

import (
	"os"
	"testing"
)

func TestMoveNoReplace(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err = root.Mkdir("parent", 0700); err != nil {
		t.Fatal(err)
	}
	if err = root.WriteFile("a", []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = moveNoReplace(root, "a", "parent/b"); err != nil {
		t.Fatal(err)
	}
	if err = root.WriteFile("a", []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = moveNoReplace(root, "parent/b", "a"); err == nil {
		t.Fatal("occupied target replaced")
	}
	b, err := root.ReadFile("a")
	if err != nil || string(b) != "user" {
		t.Fatal("target lost")
	}
	if err = root.Mkdir("folder", 0700); err != nil {
		t.Fatal(err)
	}
	if err = moveNoReplace(root, "folder", "parent/folder"); err != nil {
		t.Fatal(err)
	}
	if err = root.Symlink("parent", "alias"); err != nil {
		t.Fatal(err)
	}
	if err = moveNoReplace(root, "a", "alias/c"); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestProbeMoveUnsupported(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	s := State{Version: 2}
	err = probeMoveWith(root, &s, func() error { return nil }, func(string, string) error { return os.ErrPermission })
	if err == nil {
		t.Fatal("unsupported move accepted")
	}
	entries, err := os.ReadDir(root.Name())
	if err != nil || len(entries) != 0 {
		t.Fatal("probe artifacts left", err)
	}
}
func TestProbeMoveRecovery(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	s := State{Version: 2}
	saved := s
	calls := 0
	err = probeMoveWith(root, &s, func() error {
		calls++
		if calls == 2 {
			return os.ErrPermission
		}
		saved = s
		return nil
	}, func(a, b string) error { return moveNoReplace(root, a, b) })
	if err == nil {
		t.Fatal("save fault ignored")
	}
	s = saved
	if err = clearProbe(root, &s, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root.Name())
	if err != nil || len(entries) != 0 {
		t.Fatal("recovery left artifacts", err)
	}
}
