package tray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func optionsFixture(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	return Options{ProfileDir: dir}
}

func TestNormalizeOptions(t *testing.T) {
	opts := optionsFixture(t)
	got, err := NormalizeOptions(opts)
	if err != nil || got.Name != "Grove" || got.ProfileDir != opts.ProfileDir {
		t.Fatalf("normalized: %+v %v", got, err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(opts.ProfileDir, alias); err != nil {
		t.Fatal(err)
	}
	got, err = NormalizeOptions(Options{ProfileDir: alias, Name: "Persönlich <&_>", ServiceInstance: "personal-2_X"})
	if err != nil || got.ProfileDir != opts.ProfileDir {
		t.Fatalf("alias: %+v %v", got, err)
	}
	for _, change := range []func(*Options){
		func(o *Options) { o.ProfileDir = "relative" }, func(o *Options) { o.ProfileDir = filepath.Join(t.TempDir(), "missing") },
		func(o *Options) { o.Name = "bad\nname" }, func(o *Options) { o.Name = string([]byte{255}) }, func(o *Options) { o.Name = strings.Repeat("x", 81) },
		func(o *Options) { o.ServiceInstance = "../private" }, func(o *Options) { o.ServiceInstance = "other.service" },
	} {
		bad := opts
		change(&bad)
		if _, err := NormalizeOptions(bad); err == nil || strings.Contains(err.Error(), opts.ProfileDir) {
			t.Fatalf("invalid options error: %v", err)
		}
	}
	if err := os.Mkdir(filepath.Join(opts.ProfileDir, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeOptions(opts); err == nil {
		t.Fatal("profile in git accepted")
	}
	if err := os.Remove(filepath.Join(opts.ProfileDir, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(opts.ProfileDir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeOptions(opts); err == nil {
		t.Fatal("missing state accepted")
	}
	if err := os.Mkdir(filepath.Join(opts.ProfileDir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeOptions(opts); err == nil {
		t.Fatal("nonregular state accepted")
	}
}
