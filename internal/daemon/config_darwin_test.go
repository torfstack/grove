//go:build darwin

package daemon

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestDarwinConfigSecurityAndWatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	data := `{"version":1,"profile_dir":"` + filepath.Join(dir, "profile") + `","mode":"download"}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("private config: %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("public config accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(link); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("directory accepted")
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(fifo); err == nil {
		t.Fatal("FIFO accepted")
	}
	if err := os.WriteFile(path, []byte(strings.TrimSuffix(data, "}")+`,"watch_local":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "watch_local") {
		t.Fatalf("watch request: %v", err)
	}
	called := false
	err := Run(context.Background(), path, Dependencies{AcquireLease: func(string) (io.Closer, error) {
		called = true
		return nil, nil
	}})
	if err == nil || called {
		t.Fatalf("watch request reached lease acquisition: called=%v err=%v", called, err)
	}
}
