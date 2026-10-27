package main

import (
	"context"
	"errors"

	"github.com/spf13/cobra"
	"github.com/torfstack/grove/internal/tray"
)

func newCommand(run func(context.Context, tray.Options) error) *cobra.Command {
	opts := tray.Options{}
	cmd := &cobra.Command{Use: "grove-tray", Short: "Show one Grove profile in a Freedesktop system tray", SilenceErrors: true, SilenceUsage: true, Args: cobra.NoArgs}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Context().Err() != nil {
			return nil
		}
		if cmd.Flags().Changed("name") && opts.Name == "" {
			return errors.New("display name cannot be empty")
		}
		if cmd.Flags().Changed("service-instance") && opts.ServiceInstance == "" {
			return errors.New("service instance cannot be empty")
		}
		normalized, err := tray.ValidateArguments(opts)
		if err != nil {
			return err
		}
		if run == nil {
			return errors.New("tray service unavailable")
		}
		err = run(cmd.Context(), normalized)
		if err != nil {
			var failure *tray.Failure
			if errors.As(err, &failure) {
				return failure
			}
			return errors.New("tray service failed")
		}
		return nil
	}
	cmd.Flags().StringVar(&opts.ProfileDir, "profile-dir", "", "Existing profile directory (required)")
	cmd.Flags().StringVar(&opts.Name, "name", "Grove", "Public tray display name")
	cmd.Flags().StringVar(&opts.ServiceInstance, "service-instance", "", "Optional service-manager instance for groved")
	return cmd
}
