//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestGrovedCommand(t *testing.T) {
	for _, args := range [][]string{nil, {"--config", "relative"}, {"--config", "/config", "extra"}, {"--unknown"}, {"--config", "/config"}} {
		called := 0
		cmd := newCommand(func(_ context.Context, path string) error {
			called++
			if path != "/config" {
				t.Fatal("wrong config")
			}
			return nil
		})
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		err := cmd.ExecuteContext(context.Background())
		valid := len(args) == 2 && args[1] == "/config"
		if (err == nil) != valid || called != map[bool]int{false: 0, true: 1}[valid] {
			t.Fatalf("args %v err=%v calls=%d", args, err, called)
		}
	}
	cmd := newCommand(nil)
	cmd.SetArgs([]string{"--help"})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd = newCommand(func(ctx context.Context, _ string) error { return ctx.Err() })
	cmd.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "config.json")})
	if err := cmd.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("context not forwarded: %v", err)
	}
}
