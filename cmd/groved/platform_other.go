//go:build !linux

package main

import "context"

func platformSupported() bool { return false }
func processContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(ctx)
}
