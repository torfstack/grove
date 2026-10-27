//go:build darwin

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
)

func TestDarwinGrovedCommandRuns(t *testing.T) {
	if !platformSupported() {
		t.Fatal("Darwin daemon runtime is not enabled")
	}
	config := filepath.Join(t.TempDir(), "daemon.json")
	called := false
	cmd := newCommand(func(_ context.Context, got string) error {
		called = true
		if got != config {
			t.Fatalf("config path = %q, want %q", got, config)
		}
		return nil
	})
	cmd.SetArgs([]string{"--config", config})
	cmd.SetOut(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("daemon was not started")
	}
}

func TestDarwinProcessContextCancellation(t *testing.T) {
	ctx, cancel := processContext(context.Background())
	defer cancel()
	cancel()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("process context did not cancel")
	}
}
