package cli

import (
	"bytes"
	"context"
	"errors"
	"github.com/torfstack/grove/internal/fixture"
	"github.com/torfstack/grove/internal/syncengine"
	"strings"
	"testing"
)

func TestSyncFlags(t *testing.T) {
	for _, args := range [][]string{{"sync"}, {"sync", "--profile-dir", "p"}, {"sync", "--profile-dir", "p", "--remote-root", "r", "--local-dir", "l", "--token-file", "t"}, {"sync", "--profile-dir", "p", "--remote-root", "r", "--local-dir", "l", "--token-file", "t", "extra"}} {
		called := false
		services := Services{Sync: func(_ context.Context, o syncengine.Options) (syncengine.Result, error) {
			called = true
			if o.ProfileDir != "p" || o.RemoteRoot != "r" || o.LocalDir != "l" || o.TokenFile != "t" {
				t.Fatal("wrong options")
			}
			return syncengine.Result{Downloaded: 2}, nil
		}}
		root := NewRoot(services)
		root.SetArgs(args)
		root.SetOut(&bytes.Buffer{})
		err := root.ExecuteContext(context.Background())
		valid := len(args) == 9
		if (err == nil) != valid || called != valid {
			t.Fatalf("args %v err %v", args, err)
		}
	}
}
func TestFixtureFlags(t *testing.T) {
	called := false
	root := NewRoot(Services{Verify: func(_ context.Context, o FixtureOptions) (fixture.Report, error) {
		called = true
		if o.Manifest != "manifest" || o.LocalDir != "local" || o.TokenFile != "" {
			t.Fatal("wrong verify options")
		}
		return fixture.Report{Files: 5}, nil
	}})
	root.SetArgs([]string{"fixture", "verify", "--manifest", "manifest", "--local-dir", "local"})
	root.SetOut(&bytes.Buffer{})
	if err := root.ExecuteContext(context.Background()); err != nil || !called {
		t.Fatal("verify flags", err)
	}
	for _, command := range []string{"seed", "inspect", "cleanup"} {
		root := NewRoot(Services{})
		root.SetArgs([]string{"fixture", command})
		root.SetOut(&bytes.Buffer{})
		if err := root.ExecuteContext(context.Background()); err == nil {
			t.Fatal("missing required flags accepted")
		}
	}
}
func TestHelpNoCredentials(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"sync", "--help"}, {"fixture", "seed", "--help"}} {
		root := NewRoot(Services{})
		root.SetArgs(args)
		root.SetOut(&bytes.Buffer{})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
	}
}
func TestCommandCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := NewRoot(Services{Sync: func(ctx context.Context, _ syncengine.Options) (syncengine.Result, error) {
		return syncengine.Result{}, ctx.Err()
	}})
	root.SetArgs([]string{"sync", "--profile-dir", "p", "--remote-root", "r", "--local-dir", "l", "--token-file", "t"})
	if !errors.Is(root.ExecuteContext(ctx), context.Canceled) {
		t.Fatal("context not propagated")
	}
}
func TestCLIOutputRedacted(t *testing.T) {
	root := NewRoot(Services{Sync: func(context.Context, syncengine.Options) (syncengine.Result, error) {
		return syncengine.Result{Skipped: 2}, nil
	}})
	root.SetArgs([]string{"sync", "--profile-dir", "p", "--remote-root", "private-id-sentinel", "--local-dir", "l", "--token-file", "t"})
	var out bytes.Buffer
	root.SetOut(&out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "private-id-sentinel") || !strings.Contains(out.String(), "Skipped: 2") {
		t.Fatal("unsafe output")
	}
}
