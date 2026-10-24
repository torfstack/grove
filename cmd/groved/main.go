package main

import (
	"context"
	"fmt"
	"os"

	"github.com/torfstack/grove/internal/daemon"
)

func main() {
	ctx, stop := processContext(context.Background())
	command := newCommand(func(ctx context.Context, path string) error { return daemon.Run(ctx, path, daemon.Dependencies{}) })
	err := command.ExecuteContext(ctx)
	stop()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "groved: %v\n", err)
		os.Exit(1)
	}
}
