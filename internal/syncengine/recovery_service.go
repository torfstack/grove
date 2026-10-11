package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/torfstack/grove/internal/drive"
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

func (s Service) RetryRecovery(ctx context.Context, opts Options) (report RecoveryReport, err error) {
	defer func() { err = safeRecoveryError(err) }()
	if err = ctx.Err(); err != nil {
		return report, err
	}
	canonical, state, lock, err := s.recoveryState(opts)
	if err != nil {
		return report, err
	}
	defer closeRecoveryResource(lock.Close, &report, &err)
	report, err = inspectRecoveryState(ctx, canonical.ProfileDir, state)
	if err != nil {
		return report, err
	}
	if !report.Pending {
		return report, nil
	}
	if report.Reason == "unsupported-operation" {
		return report, blockedRecovery(report.Reason)
	}
	registry := s.RegistryDir
	if registry == "" {
		registry, err = registryPath()
		if err != nil {
			return report, err
		}
	}
	lease, err := registerDestination(registry, canonical.ProfileDir, canonical.LocalDir)
	if err != nil {
		return report, err
	}
	defer closeRecoveryResource(lease.Close, &report, &err)
	root, err := os.OpenRoot(canonical.LocalDir)
	if err != nil {
		return report, err
	}
	defer closeRecoveryResource(root.Close, &report, &err)
	open := s.OpenAPI
	if open == nil {
		open = defaultOpenAPI
	}
	api, closeAPI, err := open(ctx, canonical.TokenFile, "read-write")
	if err != nil {
		return report, err
	}
	defer closeRecoveryResource(closeAPI, &report, &err)
	mutation, ok := api.(drive.SyncMutationAPI)
	if !ok {
		return report, errors.New("recovery requires mutation-capable Drive API")
	}
	priorConflicts := map[string]bool{}
	for _, c := range state.Conflicts {
		priorConflicts[c.ID] = true
	}
	ex := executor{root: root, profileDir: canonical.ProfileDir, remoteRoot: canonical.RemoteRoot, api: api, mutation: mutation, state: &state, save: func() error { return saveState(canonical.ProfileDir, state) }, resumeUploads: true}
	recoveryErr := ex.recoverUpload(ctx)
	report, err = inspectRecoveryState(ctx, canonical.ProfileDir, state)
	if err != nil {
		return report, err
	}
	if recoveryErr != nil {
		var blocked *RecoveryBlockedError
		if errors.As(recoveryErr, &blocked) {
			report.Reason = blocked.Reason
		}
		return report, recoveryErr
	}
	if report.Pending {
		return report, blockedRecovery(report.Reason)
	}
	report.Outcome = "completed"
	for _, c := range state.Conflicts {
		if !priorConflicts[c.ID] {
			report.Outcome = "conflict-pending"
			report.Actions = []RecoveryAction{"resolve-conflict"}
			break
		}
	}
	return report, nil
}

func closeRecoveryResource(close func() error, report *RecoveryReport, err *error) {
	if closeErr := close(); closeErr != nil {
		*err = errors.Join(*err, closeErr)
		report.Outcome = "none"
	}
}
