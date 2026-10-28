package main

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newCommand(run func(context.Context, string) error) *cobra.Command {
	var config string
	cmd := &cobra.Command{Use: "groved", Short: "Periodically synchronize one existing Grove profile", SilenceErrors: true, SilenceUsage: true, Args: func(_ *cobra.Command, args []string) error {
		if len(args) != 0 {
			return errors.New("positional arguments are not supported")
		}
		return nil
	}, RunE: func(cmd *cobra.Command, _ []string) error {
		if !filepath.IsAbs(config) {
			return errors.New("--config must be an absolute path")
		}
		if !platformSupported() {
			return errors.New("groved requires Linux or macOS")
		}
		if run == nil {
			return errors.New("daemon service unavailable")
		}
		return run(cmd.Context(), config)
	}}
	cmd.Flags().StringVar(&config, "config", "", "Private daemon configuration file (required)")
	return cmd
}
