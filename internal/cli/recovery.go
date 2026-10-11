package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/torfstack/grove/internal/syncengine"
)

func newRecovery(services Services) *cobra.Command {
	cmd := &cobra.Command{Use: "recovery", Short: "Inspect, retry, or export interrupted uploads", Args: noArgs}
	for _, name := range []string{"inspect", "retry"} {
		name := name
		sub := &cobra.Command{Use: name, Short: map[string]string{"inspect": "Inspect recorded recovery state offline", "retry": "Retry only the pending transaction"}[name], Args: noArgs}
		var opts syncengine.Options
		addProfileFlags(sub, &opts)
		sub.RunE = func(command *cobra.Command, _ []string) error {
			if err := requireProfileOptions(opts); err != nil {
				return err
			}
			run := services.InspectRecovery
			if name == "retry" {
				run = services.RetryRecovery
			}
			if run == nil {
				return errors.New("recovery service unavailable")
			}
			report, err := run(command.Context(), opts)
			if err == nil || report.Pending {
				if writeErr := writeRecoveryReport(command.OutOrStdout(), report); writeErr != nil {
					return displayRecoveryError(writeErr)
				}
			}
			return displayRecoveryError(err)
		}
		cmd.AddCommand(sub)
	}
	var opts syncengine.Options
	var export syncengine.RecoveryExportOptions
	var candidate string
	sub := &cobra.Command{Use: "export", Short: "Copy verified staged bytes to a new private file", Args: noArgs}
	addProfileFlags(sub, &opts)
	sub.Flags().StringVar(&candidate, "candidate", "", "Recorded candidate: local or cloud (required)")
	sub.Flags().StringVar(&export.Output, "output", "", "New output file outside profiles and sync destinations (required)")
	sub.RunE = func(command *cobra.Command, _ []string) error {
		if err := requireProfileOptions(opts); err != nil {
			return err
		}
		if candidate != "local" && candidate != "cloud" {
			return errors.New("--candidate must be local or cloud")
		}
		if export.Output == "" {
			return errors.New("--output is required")
		}
		if services.ExportRecovery == nil {
			return errors.New("recovery service unavailable")
		}
		export.Candidate = syncengine.ConflictSide(candidate)
		if err := services.ExportRecovery(command.Context(), opts, export); err != nil {
			return displayRecoveryError(err)
		}
		_, err := fmt.Fprintln(command.OutOrStdout(), "Exported verified candidate; pending recovery is unchanged")
		return displayRecoveryError(err)
	}
	cmd.AddCommand(sub)
	return cmd
}

func writeRecoveryReport(w io.Writer, r syncengine.RecoveryReport) error {
	_, err := fmt.Fprintf(w, "Recovery outcome: %s\nPending: %t\nOperation: %s\nPhase: %s\nReason: %s\nLocal stage: %s\nCloud stage: %s\nRecorded competitors: %d\n",
		recoveryField(string(r.Outcome), "none pending completed conflict-pending"), r.Pending,
		recoveryField(string(r.Operation), "upload create download replace move"),
		recoveryField(r.Phase, "intent downloading verified staged pin-intent pinned upload-intent uploaded create-intent created capture-intent conflict-captured committed retired backed-up cancelled"),
		recoveryField(string(r.Reason), "none remote-check-required unsupported-operation stage-unavailable uncertain-write remote-changed history-incomplete multiple-competitors capture-pending cleanup-pending"),
		recoveryField(string(r.Local.Status), "none verified unavailable partial"), recoveryField(string(r.Cloud.Status), "none verified unavailable partial"), r.CompetitorCount)
	if err != nil {
		return err
	}
	for _, action := range r.Actions {
		if _, err = fmt.Fprintf(w, "Next action: %s\n", recoveryField(string(action), "retry export-local export-cloud sync resolve-conflict")); err != nil {
			return err
		}
	}
	return nil
}
func recoveryField(value, allowed string) string {
	for _, known := range strings.Fields(allowed) {
		if value == known {
			return known
		}
	}
	return "unknown"
}

func displayRecoveryError(err error) error {
	if err == nil {
		return nil
	}
	message := "recovery operation failed"
	var export *syncengine.RecoveryExportError
	var blocked *syncengine.RecoveryBlockedError
	switch {
	case errors.As(err, &export) && export.Published:
		message = "export failed; output may already exist"
	case errors.Is(err, context.Canceled):
		message = "recovery cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		message = "recovery deadline exceeded"
	case errors.As(err, &blocked):
		message = "upload recovery remains blocked"
	}
	return &syncengine.RecoveryError{Message: message, Cause: err}
}
