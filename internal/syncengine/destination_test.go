package syncengine

import (
	"context"
	"github.com/torfstack/grove/internal/privatefs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestImmutableBinding(t *testing.T) {
	dir := t.TempDir()
	binding := Binding{RemoteRoot: "root", LocalDir: filepath.Join(dir, "local"), TokenFile: filepath.Join(dir, "token")}
	state, err := loadState(dir, binding)
	if err != nil {
		t.Fatal(err)
	}
	if err = saveState(dir, state); err != nil {
		t.Fatal(err)
	}
	binding.RemoteRoot = "other"
	if _, err = loadState(dir, binding); err == nil {
		t.Fatal("binding changed")
	}
}
func TestNewDestinationEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), "local")
	if err := checkInitialDestination(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkInitialDestination(p); err == nil {
		t.Fatal("nonempty destination accepted")
	}
}
func TestRejectStateInsideDestination(t *testing.T) {
	dir := t.TempDir()
	opts := Options{ProfileDir: filepath.Join(dir, "local", "profile"), LocalDir: filepath.Join(dir, "local"), TokenFile: filepath.Join(dir, "token"), RemoteRoot: "root"}
	if _, err := canonicalOptions(opts); err == nil {
		t.Fatal("state inside destination")
	}
}
func TestDestinationAliases(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "local")
	if err := os.Mkdir(p, 0700); err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(dir, "registry")
	lock, err := registerDestination(registry, filepath.Join(dir, "profile"), p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	alias, err := privatefs.Canonical(filepath.Join(p, "..", "local"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registerDestination(registry, filepath.Join(dir, "other-profile"), alias); err == nil {
		t.Fatal("alias acquired destination")
	}
}
func TestNestedDestinationExclusion(t *testing.T) {
	if os.Getenv("GROVE_DEST_HELPER") == "1" {
		_, err := registerDestination(os.Getenv("GROVE_DEST_REGISTRY"), os.Getenv("GROVE_DEST_PROFILE"), os.Getenv("GROVE_DEST_PATH"))
		if err != nil {
			os.Exit(10)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	registry := filepath.Join(dir, "registry")
	p := filepath.Join(dir, "local")
	l, err := registerDestination(registry, filepath.Join(dir, "profile"), p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	cmd := exec.Command(os.Args[0], "-test.run=^TestNestedDestinationExclusion$")
	cmd.Env = append(os.Environ(), "GROVE_DEST_HELPER=1", "GROVE_DEST_REGISTRY="+registry, "GROVE_DEST_PROFILE="+filepath.Join(dir, "other"), "GROVE_DEST_PATH="+filepath.Join(p, "nested"))
	if err = cmd.Run(); err == nil {
		t.Fatal("nested destination acquired")
	}
}
func TestLocalSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if _, err = scanLocal(context.Background(), root, nil); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestFilesystemNameCollision(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	state := State{}
	snapshot := Snapshot{Entries: []Entry{{Path: "a", Remote: file("1", "a")}, {Path: "a", Remote: file("2", "a")}}}
	if err = probeDestination(root, snapshot, &state, func() error { return nil }); err == nil {
		t.Fatal("collision accepted")
	}
}

func TestProbeRejectsUnsupportedPublication(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	state := State{}
	var recorded []string
	save := func() error {
		recorded = nil
		for _, entry := range state.ProbeEntries {
			recorded = append(recorded, filepath.Join(state.ProbePath, entry.Path))
		}
		return nil
	}
	called := false
	link := func(source, target string) error {
		called = true
		for _, path := range []string{source, target} {
			found := false
			for _, owned := range recorded {
				found = found || owned == path
			}
			if !found {
				t.Fatal("link probe path was not journaled before mutation")
			}
		}
		return os.ErrPermission
	}
	snapshot := Snapshot{Entries: []Entry{{Path: "file", Remote: file("1", "file")}}}
	err = probeDestinationWithLink(root, snapshot, &state, save, link)
	if !called || err == nil || !strings.Contains(err.Error(), "safe publication") {
		t.Fatalf("unsupported publication accepted: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 || state.ProbePath != "" || len(state.ProbeEntries) != 0 {
		t.Fatalf("failed preflight left artifacts: %v", err)
	}
}
func TestLocalHashesDetectEdits(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "file")
	if err := os.WriteFile(p, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	a, err := scanLocal(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	b, err := scanLocal(context.Background(), root, nil)
	if err != nil || a[0].SHA256 == b[0].SHA256 {
		t.Fatal("hash missed edit")
	}
}

func TestRejectProfileInsideGit(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	opts := Options{ProfileDir: filepath.Join(repo, "profile"), LocalDir: filepath.Join(dir, "local"), TokenFile: filepath.Join(dir, "token"), RemoteRoot: "root"}
	if _, err := canonicalOptions(opts); err == nil {
		t.Fatal("generated IDs allowed inside Git")
	}
}

func TestCaseAliasDestinationLock(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "Destination")
	alias := filepath.Join(dir, "destination")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	one, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	two, err := os.Stat(alias)
	if err != nil || !os.SameFile(one, two) {
		t.Skip("case-sensitive filesystem")
	}
	lock, err := registerDestination(filepath.Join(dir, "registry1"), filepath.Join(dir, "profile1"), dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	second, err := registerDestination(filepath.Join(dir, "registry2"), filepath.Join(dir, "profile2"), alias)
	if second != nil {
		_ = second.Close()
	}
	if err == nil {
		t.Fatal("case alias acquired destination lease")
	}
}
func TestDestinationExclusionAcrossRegistries(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "destination")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	first, err := registerDestination(filepath.Join(dir, "registry1"), filepath.Join(dir, "profile1"), dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	for _, p := range []string{dest, filepath.Join(dest, "nested")} {
		second, err := registerDestination(filepath.Join(dir, "registry2"), filepath.Join(dir, "profile2"), p)
		if second != nil {
			_ = second.Close()
		}
		if err == nil {
			t.Fatal("registry change bypassed destination lease")
		}
	}
}

func TestCrossRegistryProcessExclusion(t *testing.T) {
	if os.Getenv("GROVE_CROSS_REGISTRY_HELPER") == "1" {
		lock, err := registerDestination(os.Getenv("GROVE_DEST_REGISTRY"), os.Getenv("GROVE_DEST_PROFILE"), os.Getenv("GROVE_DEST_PATH"))
		if err != nil {
			os.Exit(10)
		}
		_ = lock.Close()
		os.Exit(0)
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "destination")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	first, err := registerDestination(filepath.Join(dir, "registry1"), filepath.Join(dir, "profile1"), dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	for _, path := range []string{dest, filepath.Join(dest, "child")} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrossRegistryProcessExclusion$")
		cmd.Env = append(os.Environ(), "GROVE_CROSS_REGISTRY_HELPER=1", "GROVE_DEST_REGISTRY="+filepath.Join(dir, "registry2"), "GROVE_DEST_PROFILE="+filepath.Join(dir, "profile2"), "GROVE_DEST_PATH="+path)
		if err := cmd.Run(); err == nil {
			t.Fatal("second process acquired an overlapping destination across registries")
		}
	}
}
