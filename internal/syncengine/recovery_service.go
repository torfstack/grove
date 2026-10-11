package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/torfstack/grove/internal/privatefs"
)

func (s Service) recoveryState(opts Options) (Options, State, *privatefs.Lock, error) {
	canonical, err := canonicalOptions(opts)
	if err != nil {
		return Options{}, State{}, nil, err
	}
	info, err := os.Lstat(filepath.Join(canonical.ProfileDir, "state.json"))
	if err != nil || !info.Mode().IsRegular() {
		return Options{}, State{}, nil, errors.New("recovery requires an existing profile")
	}
	return s.lockedState(canonical)
}

func (s Service) InspectRecovery(ctx context.Context, opts Options) (report RecoveryReport, err error) {
	defer func() { err = safeRecoveryError(err) }()
	if err = ctx.Err(); err != nil {
		return report, err
	}
	canonical, state, lock, err := s.recoveryState(opts)
	if err != nil {
		return report, err
	}
	defer closeResult(lock.Close, &err)
	return inspectRecoveryState(ctx, canonical.ProfileDir, state)
}

func inspectRecoveryState(ctx context.Context, profile string, state State) (RecoveryReport, error) {
	r := RecoveryReport{Reason: "none", Outcome: "none", Local: RecoveryStage{Status: "none"}, Cloud: RecoveryStage{Status: "none"}}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if state.Pending != nil || state.ProbePath != "" {
		r.Pending = true
		r.Outcome = "pending"
		r.Operation = OpDownload
		r.Reason = "unsupported-operation"
		r.Actions = []RecoveryAction{"sync"}
		if state.Pending != nil {
			r.Phase = state.Pending.Phase
		}
		return r, nil
	}
	j := state.Transaction
	if j == nil {
		return r, nil
	}
	r.Pending = true
	r.Outcome = "pending"
	r.Operation = j.Operation.Kind
	r.Phase = j.Phase
	r.CompetitorCount = len(j.CompetingRevisionIDs)
	if j.Operation.Kind != OpUpload && j.Operation.Kind != OpCreate {
		r.Reason = "unsupported-operation"
		r.Actions = []RecoveryAction{"sync"}
		return r, nil
	}
	r.Reason = "remote-check-required"
	r.Actions = []RecoveryAction{"retry"}
	var err error
	r.Local.Status, err = inspectRecoveryStage(ctx, profile, j.StagePath, j.StageIdentity, j.StageSHA256, j.StageMD5, j.StageSize)
	if err != nil {
		return r, err
	}
	if j.CloudStagePath != "" && j.CloudSHA256 == "" {
		r.Cloud.Status = "partial"
	} else {
		r.Cloud.Status, err = inspectRecoveryStage(ctx, profile, j.CloudStagePath, j.CloudStageIdentity, j.CloudSHA256, j.CloudMD5, j.CloudSize)
		if err != nil {
			return r, err
		}
	}
	if r.Local.Status == "verified" {
		r.Actions = append(r.Actions, "export-local")
	}
	if r.Cloud.Status == "verified" {
		r.Actions = append(r.Actions, "export-cloud")
	}
	switch {
	case j.Phase == "committed" || j.Phase == "retired":
		r.Reason = "cleanup-pending"
	case r.Local.Status == "unavailable" || r.Cloud.Status == "unavailable":
		r.Reason = "stage-unavailable"
	case len(j.CompetingRevisionIDs) > 1:
		r.Reason = "multiple-competitors"
	case j.RevisionHistoryUnverified:
		r.Reason = "history-incomplete"
	case j.Phase == "capture-intent" || j.Phase == "conflict-captured":
		r.Reason = "capture-pending"
	case j.Phase == "upload-intent" || j.Phase == "create-intent":
		r.Reason = "uncertain-write"
	}
	return r, nil
}

func inspectRecoveryStage(ctx context.Context, profile, path string, identity *FileIdentity, sha, md5 string, size int64) (RecoveryStageStatus, error) {
	if path == "" {
		return "none", nil
	}
	if err := verifyPrivateStageContext(ctx, profile, path, identity, sha, md5, size); err != nil {
		if ctx.Err() != nil {
			return "unavailable", ctx.Err()
		}
		return "unavailable", nil
	}
	return "verified", nil
}
