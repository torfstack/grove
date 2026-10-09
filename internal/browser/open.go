package browser

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
)

func Open(ctx context.Context, url string) error {
	return openOn(ctx, runtime.GOOS, url, func(ctx context.Context, name string, args ...string) error {
		return exec.CommandContext(ctx, name, args...).Run()
	})
}

func openOn(ctx context.Context, platform, url string, run func(context.Context, string, ...string) error) error {
	switch platform {
	case "linux":
		return run(ctx, "xdg-open", url)
	case "darwin":
		return run(ctx, "open", url)
	default:
		return errors.New("automatic browser launch is unsupported on this platform")
	}
}
