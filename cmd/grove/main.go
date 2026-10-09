package main

import (
	"context"
	"fmt"
	"github.com/torfstack/grove/internal/auth"
	"github.com/torfstack/grove/internal/browser"
	"github.com/torfstack/grove/internal/cli"
	"golang.org/x/oauth2/google"
	"os"
	"os/signal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	service := auth.Service{Flow: auth.Flow{Endpoint: google.Endpoint, OpenBrowser: browser.Open, Output: os.Stdout}}
	command := cli.NewRoot(service.Authenticate)
	if err := command.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "grove: %v\n", err)
		os.Exit(1)
	}
}
