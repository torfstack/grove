package cli

import (
	"context"
	"errors"
	"github.com/spf13/cobra"
	"github.com/torfstack/grove/internal/auth"
	"github.com/torfstack/grove/internal/fixture"
	"github.com/torfstack/grove/internal/syncengine"
)

type AuthenticateFunc func(context.Context, auth.Options) (string, error)
type FixtureOptions struct{ Manifest, RunDir, LocalDir, TokenFile string }
type Services struct {
	Authenticate AuthenticateFunc
	Seed         func(context.Context, FixtureOptions) (string, error)
	Inspect      func(context.Context, FixtureOptions) (fixture.Report, error)
	Verify       func(context.Context, FixtureOptions) (fixture.Report, error)
	Cleanup      func(context.Context, FixtureOptions) error
	Sync         func(context.Context, syncengine.Options) (syncengine.Result, error)
}

func NewRoot(services Services) *cobra.Command {
	root := &cobra.Command{Use: "grove", Short: "Synchronize Google Drive", SilenceErrors: true, SilenceUsage: true}
	if services.Authenticate == nil {
		services.Authenticate = func(context.Context, auth.Options) (string, error) {
			return "", errors.New("authentication service unavailable")
		}
	}
	root.AddCommand(newAuth(services.Authenticate), newFixture(services), newSync(services.Sync))
	return root
}
func noArgs(_ *cobra.Command, args []string) error {
	if len(args) != 0 {
		return errors.New("positional arguments are not supported")
	}
	return nil
}
