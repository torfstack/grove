package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/torfstack/grove/internal/tray"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := newCommand(func(ctx context.Context, opts tray.Options) error { return tray.Run(ctx, opts, tray.Dependencies{}) })
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "grove-tray: %v\n", err)
		os.Exit(1)
	}
}
