package cli

import (
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"grove/internal/auth"
)

func newAuth(authenticate AuthenticateFunc) *cobra.Command {
	opts := auth.Options{}
	cmd := &cobra.Command{Use: "auth", Short: "Authorize Google Drive access in your browser", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.ClientSecret == "" {
				return errors.New("--client-secret is required")
			}
			if _, err := auth.Scope(opts.Access); err != nil {
				return err
			}
			path, err := authenticate(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Authentication saved to %s\n", path)
			return err
		}}
	cmd.Flags().StringVar(&opts.ClientSecret, "client-secret", "", "Google Desktop client JSON path (required)")
	cmd.Flags().StringVar(&opts.Access, "access", "read-only", "Drive access: read-only or read-write")
	cmd.Flags().StringVar(&opts.TokenFile, "token-file", "", "Override the token storage path")
	return cmd
}
