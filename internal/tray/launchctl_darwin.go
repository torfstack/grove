//go:build darwin

package tray

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
)

const launchctlPath = "/bin/launchctl"

type systemLaunchctl struct{}

func (systemLaunchctl) Print(ctx context.Context, target string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, launchctlPath, "print", target)
	var output limitedLaunchctlOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if output.overflow {
		return nil, errors.New("launchctl output limit exceeded")
	}
	if err != nil {
		return output.Bytes(), errors.New("launchctl print failed")
	}
	return output.Bytes(), nil
}

func (systemLaunchctl) Bootout(ctx context.Context, target string) error {
	return runLaunchctl(ctx, "bootout", target)
}

func (systemLaunchctl) Kickstart(ctx context.Context, target string) error {
	return runLaunchctl(ctx, "kickstart", target)
}

func runLaunchctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, launchctlPath, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return errors.New("launchctl control failed")
	}
	return nil
}

type limitedLaunchctlOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *limitedLaunchctlOutput) Write(data []byte) (int, error) {
	const limit = 1024 * 1024
	remaining := limit - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return len(data), nil
	}
	if len(data) > remaining {
		b.overflow = true
		_, _ = b.Buffer.Write(data[:remaining])
		return len(data), nil
	}
	return b.Buffer.Write(data)
}

var _ launchctlRunner = systemLaunchctl{}
