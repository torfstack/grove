package cli

import (
	"errors"
	"fmt"
	"github.com/spf13/cobra"
	"github.com/torfstack/grove/internal/fixture"
)

func newFixture(services Services) *cobra.Command {
	parent := &cobra.Command{Use: "fixture", Short: "Manage disposable Drive test fixtures", Args: noArgs}
	for _, name := range []string{"seed", "inspect", "verify", "cleanup"} {
		opts := FixtureOptions{}
		cmd := &cobra.Command{Use: name, Short: map[string]string{"seed": "Create a fresh owned fixture", "inspect": "Inspect an owned remote fixture", "verify": "Verify local content against the manifest", "cleanup": "Trash only recorded fixture objects"}[name], Args: noArgs}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error {
			if (name == "seed" || name == "verify") && opts.Manifest == "" {
				return errors.New("--manifest is required")
			}
			if name == "verify" {
				if opts.LocalDir == "" {
					return errors.New("--local-dir is required")
				}
			} else if opts.RunDir == "" || opts.TokenFile == "" {
				return errors.New("--run-dir and --token-file are required")
			}
			var report fixture.Report
			var err error
			switch name {
			case "seed":
				if services.Seed == nil {
					return errors.New("fixture service unavailable")
				}
				var path string
				path, err = services.Seed(cmd.Context(), opts)
				if err == nil {
					_, err = fmt.Fprintf(cmd.OutOrStdout(), "Fixture run saved to %s\n", path)
				}
				return err
			case "inspect":
				if services.Inspect == nil {
					return errors.New("fixture service unavailable")
				}
				report, err = services.Inspect(cmd.Context(), opts)
			case "verify":
				if services.Verify == nil {
					return errors.New("fixture service unavailable")
				}
				report, err = services.Verify(cmd.Context(), opts)
			case "cleanup":
				if services.Cleanup == nil {
					return errors.New("fixture service unavailable")
				}
				if err = services.Cleanup(cmd.Context(), opts); err != nil {
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Fixture cleanup complete")
				return err
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Verified files: %d; directories: %d\n", report.Files, report.Directories)
			return err
		}
		if name == "seed" || name == "verify" {
			cmd.Flags().StringVar(&opts.Manifest, "manifest", "", "Fixture manifest path (required)")
		}
		if name == "verify" {
			cmd.Flags().StringVar(&opts.LocalDir, "local-dir", "", "Downloaded fixture directory (required)")
		} else {
			cmd.Flags().StringVar(&opts.RunDir, "run-dir", "", "Private fixture run directory (required)")
			cmd.Flags().StringVar(&opts.TokenFile, "token-file", "", "Dedicated test authentication record (required)")
		}
		parent.AddCommand(cmd)
	}
	return parent
}
