package cli

import (
	"context"
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/torfstack/grove/internal/syncengine"
)

func newSync(run func(context.Context, syncengine.Options) (syncengine.Result, error)) *cobra.Command {
	opts := syncengine.Options{}
	cmd := &cobra.Command{Use: "sync", Short: "Download and reconcile an ordinary-file tree from Drive", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if opts.ProfileDir == "" || opts.RemoteRoot == "" || opts.LocalDir == "" || opts.TokenFile == "" {
			return errors.New("--profile-dir, --remote-root, --local-dir, and --token-file are required")
		}
		if run == nil {
			return errors.New("sync service unavailable")
		}
		result, err := run(cmd.Context(), opts)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Downloaded: %d; directories created: %d; Skipped: %d; Updated: %d; Moved: %d\n", result.Downloaded, result.CreatedDirectories, result.Skipped, result.Updated, result.Moved)
		return err
	}}
	cmd.Flags().StringVar(&opts.ProfileDir, "profile-dir", "", "Private sync profile directory (required)")
	cmd.Flags().StringVar(&opts.RemoteRoot, "remote-root", "", "Selected Drive folder ID (required)")
	cmd.Flags().StringVar(&opts.LocalDir, "local-dir", "", "Destination directory (required)")
	cmd.Flags().StringVar(&opts.TokenFile, "token-file", "", "Authentication record path (required)")
	return cmd
}
