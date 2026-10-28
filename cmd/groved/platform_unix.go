//go:build linux || darwin

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func platformSupported() bool { return true }
func processContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
}
