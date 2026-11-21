package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/torfstack/grove/internal/syncengine"
)

func newFolders(discover func(context.Context, string, string) ([]syncengine.SelectionFolder, error)) *cobra.Command {
	var rootID, token string
	cmd := &cobra.Command{
		Use:   "folders",
		Short: "List selectable folders under a Drive root",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if rootID == "" || token == "" {
				return errors.New("--remote-root and --token-file are required")
			}
			if discover == nil {
				return errors.New("folder discovery service unavailable")
			}
			folders, err := discover(cmd.Context(), rootID, token)
			if err != nil {
				return err
			}
			for _, folder := range folders {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", folder.ID, folder.Path); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&rootID, "remote-root", "", "Drive root folder ID (required)")
	cmd.Flags().StringVar(&token, "token-file", "", "Authentication record path (required)")
	return cmd
}

func newSelection(replace func(context.Context, string, []string) error) *cobra.Command {
	var profile string
	var ids []string
	var all, empty bool
	cmd := &cobra.Command{
		Use:   "select-folders",
		Short: "Replace the folder selection of a stopped sync profile",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if profile == "" {
				return errors.New("--profile-dir is required")
			}
			if (!all && !empty && len(ids) == 0) || (all && empty) || ((all || empty) && len(ids) != 0) {
				return errors.New("choose --all, --include-no-folders, or one or more --include-folder flags")
			}
			if replace == nil {
				return errors.New("selection service unavailable")
			}
			if empty {
				ids = []string{}
			}
			return replace(cmd.Context(), profile, ids)
		},
	}
	cmd.Flags().StringVar(&profile, "profile-dir", "", "Private sync profile directory (required)")
	cmd.Flags().StringArrayVar(&ids, "include-folder", nil, "Include a Drive folder ID (repeatable)")
	cmd.Flags().BoolVar(&all, "all", false, "Select the whole Drive root")
	cmd.Flags().BoolVar(&empty, "include-no-folders", false, "Select no folders")
	return cmd
}
