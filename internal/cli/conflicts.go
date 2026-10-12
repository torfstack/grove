package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/torfstack/grove/internal/syncengine"
)

func addProfileFlags(cmd *cobra.Command, opts *syncengine.Options) {
	cmd.Flags().StringVar(&opts.ProfileDir, "profile-dir", "", "Private sync profile directory (required)")
	cmd.Flags().StringVar(&opts.RemoteRoot, "remote-root", "", "Selected Drive folder ID (required)")
	cmd.Flags().StringVar(&opts.LocalDir, "local-dir", "", "Destination directory (required)")
	cmd.Flags().StringVar(&opts.TokenFile, "token-file", "", "Authentication record path (required)")
}

func newConfig(services Services) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Configure a sync profile", Args: noArgs}
	set := &cobra.Command{Use: "set", Short: "Set a profile option", Args: cobra.ExactArgs(2)}
	var opts syncengine.Options
	addProfileFlags(set, &opts)
	set.RunE = func(cmd *cobra.Command, args []string) error {
		if args[0] != "conflict-policy" {
			return errors.New("supported setting is conflict-policy")
		}
		if opts.ProfileDir == "" || opts.RemoteRoot == "" || opts.LocalDir == "" || opts.TokenFile == "" {
			return errors.New("--profile-dir, --remote-root, --local-dir, and --token-file are required")
		}
		if services.SetConflictPolicy == nil {
			return errors.New("configuration service unavailable")
		}
		policy := syncengine.ConflictPolicy(args[1])
		if err := services.SetConflictPolicy(cmd.Context(), opts, policy); err != nil {
			return err
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "Conflict policy set to %s\n", policy)
		return err
	}
	cmd.AddCommand(set)
	return cmd
}

func newConflicts(services Services) *cobra.Command {
	cmd := &cobra.Command{Use: "conflicts", Short: "Inspect and resolve sync conflicts", Args: noArgs}
	list := &cobra.Command{Use: "list", Short: "List pending conflicts", Args: noArgs}
	var listOpts syncengine.Options
	addProfileFlags(list, &listOpts)
	list.RunE = func(command *cobra.Command, _ []string) error {
		if err := requireProfileOptions(listOpts); err != nil {
			return err
		}
		if services.ListConflicts == nil {
			return errors.New("conflict service unavailable")
		}
		conflicts, err := services.ListConflicts(command.Context(), listOpts)
		if err != nil {
			return err
		}
		for _, conflict := range conflicts {
			if _, err = fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\n", conflict.ID, conflict.Path, conflict.RemoteVersion); err != nil {
				return err
			}
		}
		return nil
	}
	show := &cobra.Command{Use: "show <id>", Short: "Show conflict candidate details", Args: cobra.ExactArgs(1)}
	var showOpts syncengine.Options
	addProfileFlags(show, &showOpts)
	show.RunE = func(command *cobra.Command, args []string) error {
		if err := requireProfileOptions(showOpts); err != nil {
			return err
		}
		if services.ConflictDetails == nil {
			return errors.New("conflict service unavailable")
		}
		detail, err := services.ConflictDetails(command.Context(), showOpts, args[0])
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", detail.ID, detail.Path); err != nil {
			return err
		}
		for _, item := range detail.Candidates {
			selected := ""
			if item.Selected {
				selected = "\tselected"
			}
			if _, err = fmt.Fprintf(command.OutOrStdout(), "%d\t%s\t%s\t%d%s\n", item.Ordinal, item.Side, item.ModifiedTime, item.Size, selected); err != nil {
				return err
			}
		}
		return nil
	}
	cmd.AddCommand(show)
	resolve := &cobra.Command{Use: "resolve <id>", Short: "Choose a conflict candidate", Args: cobra.ExactArgs(1)}
	var resolveOpts syncengine.Options
	var keep string
	var candidate int
	addProfileFlags(resolve, &resolveOpts)
	resolve.Flags().StringVar(&keep, "keep", "", "Candidate to keep: local or cloud")
	resolve.Flags().IntVar(&candidate, "candidate", 0, "Cloud candidate ordinal shown by conflicts show")
	resolve.RunE = func(command *cobra.Command, args []string) error {
		if err := requireProfileOptions(resolveOpts); err != nil {
			return err
		}
		if services.ResolveConflict == nil {
			return errors.New("conflict service unavailable")
		}
		if keep != "" && keep != "local" && keep != "cloud" {
			return errors.New("--keep must be local or cloud")
		}
		side := syncengine.ConflictSide(keep)
		if candidate > 0 {
			if keep == "local" {
				return errors.New("--candidate cannot be used with --keep local")
			}
			side = syncengine.SideCloud
		}
		if side == "" {
			return errors.New("provide --keep local|cloud or --candidate <ordinal>")
		}
		if candidate < 0 {
			return errors.New("--candidate must be a positive ordinal")
		}
		if err := services.ResolveConflict(command.Context(), resolveOpts, args[0], side, candidate); err != nil {
			return err
		}
		choice := string(side)
		if side == syncengine.SideCloud && candidate > 0 {
			choice = fmt.Sprintf("cloud candidate %d", candidate)
		}
		_, err := fmt.Fprintf(command.OutOrStdout(), "Resolved conflict %s using %s\n", args[0], choice)
		return err
	}
	cmd.AddCommand(list, resolve)
	return cmd
}

func requireProfileOptions(opts syncengine.Options) error {
	if opts.ProfileDir == "" || opts.RemoteRoot == "" || opts.LocalDir == "" || opts.TokenFile == "" {
		return errors.New("--profile-dir, --remote-root, --local-dir, and --token-file are required")
	}
	return nil
}
