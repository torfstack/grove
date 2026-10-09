package cli

import (
	"context"
	"github.com/spf13/cobra"
	"grove/internal/auth"
)

type AuthenticateFunc func(context.Context, auth.Options) (string, error)

func NewRoot(authenticate AuthenticateFunc) *cobra.Command {
	root := &cobra.Command{Use: "grove", Short: "Synchronize Google Drive", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newAuth(authenticate))
	return root
}
