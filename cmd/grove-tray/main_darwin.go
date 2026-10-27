//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"fyne.io/systray"
	"github.com/torfstack/grove/internal/tray"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	start, end := systray.RunWithExternalLoop(func() {
		go func() {
			cmd := newCommand(func(runCtx context.Context, opts tray.Options) error {
				return tray.Run(runCtx, opts, tray.Dependencies{})
			})
			errCh <- cmd.ExecuteContext(ctx)
			stop()
			end()
		}()
	}, stop)
	start()
	select {
	case err := <-errCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "grove-tray: %v\n", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		err := <-errCh
		if err != nil {
			fmt.Fprintf(os.Stderr, "grove-tray: %v\n", err)
			os.Exit(1)
		}
	}
}
