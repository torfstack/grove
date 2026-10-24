package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/torfstack/grove/internal/daemon"
)

type statusCommandError struct {
	message string
	cause   error
}

func (e *statusCommandError) Error() string { return e.message }
func (e *statusCommandError) Unwrap() error { return e.cause }

func newStatus(services Services) *cobra.Command {
	var profile string
	var asJSON bool
	cmd := &cobra.Command{Use: "status", Short: "Inspect daemon ownership and last recorded sync status offline", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !filepath.IsAbs(profile) {
			return &statusCommandError{message: "--profile-dir must be absolute"}
		}
		if services.Status == nil {
			return &statusCommandError{message: "status service unavailable"}
		}
		report, inspectErr := services.Status(cmd.Context(), profile)
		var writeErr error
		if asJSON {
			writeErr = json.NewEncoder(cmd.OutOrStdout()).Encode(report)
		} else {
			_, writeErr = io.WriteString(cmd.OutOrStdout(), humanStatus(report))
		}
		if writeErr != nil {
			return &statusCommandError{message: "cannot write status", cause: writeErr}
		}
		if inspectErr != nil {
			return &statusCommandError{message: "status inspection failed", cause: inspectErr}
		}
		return nil
	}}
	cmd.Flags().StringVar(&profile, "profile-dir", "", "Existing profile directory (required)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print machine-readable status")
	return cmd
}

func statusTime(stamp *time.Time) string {
	if stamp == nil {
		return "none"
	}
	return stamp.UTC().Format(time.RFC3339Nano)
}

func humanStatus(r daemon.StatusReport) string {
	lines := []string{fmt.Sprintf("Daemon: %s", r.Owner), fmt.Sprintf("Profile summary: %s", r.ProfileState)}
	if r.Profile != nil {
		lines = append(lines, fmt.Sprintf("Population complete: %t; conflicts: %d; recovery pending: %t", r.Profile.PopulationComplete, r.Profile.ConflictCount, r.Profile.RecoveryPending))
	}
	if r.LastRecorded == nil {
		lines = append(lines, "Last recorded daemon observation: none")
	} else {
		r := r.LastRecorded
		lines = append(lines, fmt.Sprintf("Last recorded local watching: %t", r.WatchLocal))
		outcome := r.Outcome
		if outcome == "never" {
			outcome = "no result yet"
		}
		lines = append(lines, fmt.Sprintf("Last recorded phase: %s; updated: %s", r.Phase, r.UpdatedAt.UTC().Format(time.RFC3339Nano)), fmt.Sprintf("Last recorded mode: %s; interval: %s", r.Mode, r.Interval), fmt.Sprintf("Last attempt: %s; started: %s; finished: %s", outcome, statusTime(r.AttemptStartedAt), statusTime(r.AttemptFinishedAt)), fmt.Sprintf("Last recorded next attempt: %s", statusTime(r.NextAttemptAt)), fmt.Sprintf("Last counts: downloaded=%d uploaded=%d directories=%d skipped=%d updated=%d moved=%d trashed=%d", r.Counts.Downloaded, r.Counts.Uploaded, r.Counts.CreatedDirectories, r.Counts.Skipped, r.Counts.Updated, r.Counts.Moved, r.Counts.Trashed))
		if r.Reason != "" {
			lines = append(lines, "Last recorded reason: "+r.Reason)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
