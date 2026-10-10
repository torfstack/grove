package live

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiveConfiguration(t *testing.T) {
	dir := t.TempDir()
	defaultToken := filepath.Join(dir, "default", "token.json")
	for _, tc := range []struct {
		name, enabled, token, runs string
		ok, active                 bool
	}{{"disabled", "", "", "", true, false}, {"missing", "1", "", "", false, true}, {"relative", "1", "relative", filepath.Join(dir, "runs"), false, true}, {"default", "1", defaultToken, filepath.Join(dir, "runs"), false, true}, {"overlap", "1", filepath.Join(dir, "runs", "token.json"), filepath.Join(dir, "runs"), false, true}, {"valid", "1", filepath.Join(dir, "test", "token.json"), filepath.Join(dir, "runs"), true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			_, active, err := liveConfig(tc.enabled, tc.token, tc.runs, defaultToken)
			if (err == nil) != tc.ok || active != tc.active {
				t.Fatalf("validation: active=%v err=%v", active, err)
			}
		})
	}
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := liveConfig("1", filepath.Join(dir, "token"), filepath.Join(repo, "runs"), defaultToken); err == nil {
		t.Fatal("repository run dir accepted")
	}
}
