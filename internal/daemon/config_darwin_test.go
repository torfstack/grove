//go:build darwin

package daemon

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/torfstack/grove/internal/syncengine"
)

func TestDarwinConfigSecurity(t *testing.T) {
	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.json")
	if err := os.WriteFile(privatePath, []byte(`{"version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := openConfig(privatePath)
	if err != nil {
		t.Fatalf("private regular file rejected: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	publicPath := filepath.Join(dir, "public.json")
	if err := os.WriteFile(publicPath, []byte(`{"version":1}`), 0644); err != nil {
		t.Fatal(err)
	}
	directoryPath := filepath.Join(dir, "directory")
	if err := os.Mkdir(directoryPath, 0700); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(dir, "link.json")
	if err := os.Symlink(privatePath, linkPath); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{publicPath, directoryPath, linkPath} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			_, err := openConfig(path)
			if err == nil {
				t.Fatal("unsafe config file accepted")
			}
			if strings.Contains(err.Error(), path) {
				t.Fatal("config error leaked its path")
			}
		})
	}
}

func TestDarwinLocalWatchRejectedBeforeStartup(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "profile")
	configPath := filepath.Join(dir, "daemon.json")
	config := `{"version":1,"profile_dir":"` + profile + `","mode":"download","watch_local":true}`
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	err := Run(context.Background(), configPath, Dependencies{
		InspectProfile: func(context.Context, string) (syncengine.ProfileSummary, error) {
			t.Fatal("profile inspected before rejecting local watching")
			return syncengine.ProfileSummary{}, nil
		},
		AcquireLease: func(string) (io.Closer, error) {
			t.Fatal("lease acquired before rejecting local watching")
			return nil, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "watch_local is not supported") {
		t.Fatalf("unsupported local watching error = %v", err)
	}
}
