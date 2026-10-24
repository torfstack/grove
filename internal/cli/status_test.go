package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/torfstack/grove/internal/daemon"
)

func TestStatusCommand(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "profile")
		called := 0
		root := NewRoot(Services{Status: func(_ context.Context, profile string) (daemon.StatusReport, error) {
			called++
			if profile != path {
				t.Fatal("wrong profile")
			}
			return daemon.StatusReport{Owner: "inactive", ProfileState: "busy"}, nil
		}})
		args := []string{"status", "--profile-dir", path}
		if asJSON {
			args = append(args, "--json")
		}
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetArgs(args)
		if err := root.Execute(); err != nil || called != 1 {
			t.Fatalf("status command: %v", err)
		}
		if asJSON {
			var report map[string]any
			if err := json.Unmarshal(out.Bytes(), &report); err != nil || report["owner"] != "inactive" || report["profile"] != nil {
				t.Fatalf("wrong JSON: %s %v", out.String(), err)
			}
		} else if !strings.Contains(out.String(), "Daemon: inactive") || !strings.Contains(out.String(), "Last recorded") {
			t.Fatal("human status omitted historical labeling")
		}
	}
	for _, args := range [][]string{{"status"}, {"status", "--profile-dir", "relative"}, {"status", "--profile-dir", "/profile", "extra"}} {
		root := NewRoot(Services{Status: func(context.Context, string) (daemon.StatusReport, error) {
			t.Fatal("invalid flags called service")
			return daemon.StatusReport{}, nil
		}})
		root.SetArgs(args)
		root.SetOut(&bytes.Buffer{})
		if err := root.Execute(); err == nil {
			t.Fatal("invalid status flags accepted")
		}
	}
	root := NewRoot(Services{})
	root.SetArgs([]string{"status", "--help"})
	root.SetOut(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestStatusCommandPartialError(t *testing.T) {
	root := NewRoot(Services{Status: func(context.Context, string) (daemon.StatusReport, error) {
		return daemon.StatusReport{Owner: "unknown", ProfileState: "unavailable"}, errors.New("private-token-error")
	}})
	root.SetArgs([]string{"status", "--profile-dir", t.TempDir(), "--json"})
	var out bytes.Buffer
	root.SetOut(&out)
	err := root.Execute()
	if err == nil || strings.Contains(err.Error(), "private-token-error") || !strings.Contains(out.String(), `"owner":"unknown"`) {
		t.Fatalf("partial error ignored or exposed: %s %v", out.String(), err)
	}
}
