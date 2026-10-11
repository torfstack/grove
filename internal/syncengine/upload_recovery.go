package syncengine

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/torfstack/grove/internal/drive"
)

var errUploadPending = errors.New("pending upload requires explicit two-way resume")

func (e *executor) recoverUpload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.state.Transaction == nil || (e.state.Transaction.Operation.Kind != OpUpload && e.state.Transaction.Operation.Kind != OpCreate) {
		return errors.New("invalid pending upload")
	}
	journal := e.state.Transaction
	if journal.StagePath != "" && journal.Phase != "committed" {
		if err := verifyPrivateStage(e.profileDir, journal.StagePath, journal.StageIdentity, journal.StageSHA256, journal.StageMD5, journal.StageSize); err != nil {
			return err
		}
	}
	if journal.CloudStagePath != "" && journal.Phase == "conflict-captured" {
		if err := verifyPrivateStage(e.profileDir, journal.CloudStagePath, journal.CloudStageIdentity, journal.CloudSHA256, journal.CloudMD5, journal.CloudSize); err != nil {
			return err
		}
	}
	if !e.resumeUploads {
		return errUploadPending
	}
	if e.mutation == nil {
		return errUploadPending
	}
	j := *journal
	switch j.Phase {
	case "capture-intent":
		if err := removeStageIfPresent(e.profileDir, j.CloudStagePath, j.CloudStageIdentity); err != nil {
			return errUploadPending
		}
		j.Phase = "uploaded"
		j.CloudStagePath, j.CloudStageIdentity, j.CloudSHA256 = "", nil, ""
		j.CloudRevisionID, j.CloudMD5, j.CloudSize = "", "", 0
		if err := e.journal(j); err != nil {
			return err
		}
		return e.captureRecoveredCompetitor(ctx, j)
	case "conflict-captured":
		return e.finishCapturedConflict(ctx, j)
	case "staged", "pin-intent", "pinned":
		if j.Operation.Kind == OpUpload {
			if err := e.currentUploadRemote(ctx, j.Operation.Entry); err != nil {
				return errUploadPending
			}
			pinCreatedByGrove := j.PreflightPinWasKept != nil && !*j.PreflightPinWasKept
			if pinCreatedByGrove {
				if err := e.releasePreflightPinIfPresent(ctx, j.Operation.Entry.Remote.ID, j.PreflightRevisionID); err != nil {
					return errUploadPending
				}
			}
		}
		if err := e.clearJournal(); err != nil {
			return err
		}
		if j.StagePath != "" {
			if err := removeStageIfPresent(e.profileDir, j.StagePath, j.StageIdentity); err != nil {
				return err
			}
		}
		return nil
	case "upload-intent":
		if len(j.CompetingRevisionIDs) > 0 {
			for _, id := range j.CompetingRevisionIDs {
				if err := e.mutation.SetRevisionKeepForever(ctx, j.Operation.Entry.Remote.ID, id, true); err != nil {
					return errUploadPending
				}
			}
			return errUploadPending
		}
		if j.RevisionHistoryUnverified {
			return errUploadPending
		}
		current, err := e.api.Get(ctx, j.Operation.Entry.Remote.ID)
		if err != nil || current.HeadRevisionID == "" || current.HeadRevisionID == j.PreflightRevisionID || current.MD5 != j.StageMD5 || current.Size != j.StageSize {
			return errUploadPending
		}
		return e.finishRecoveredUpdate(ctx, j, current)
	case "uploaded":
		if len(j.CompetingRevisionIDs) > 0 {
			return e.captureRecoveredCompetitor(ctx, j)
		}
		if j.RevisionHistoryUnverified {
			return errUploadPending
		}
		current, err := e.api.Get(ctx, j.Operation.Entry.Remote.ID)
		if err != nil || len(j.After) != 1 {
			return errUploadPending
		}
		if current.HeadRevisionID != j.After[0].HeadRevisionID || current.MD5 != j.StageMD5 || current.Size != j.StageSize {
			return errors.New("pending upload current head no longer matches the staged content")
		}
		return e.finishRecoveredUpdate(ctx, j, current)
	case "create-intent", "created", "verified":
		current, err := e.api.Get(ctx, j.AllocatedID)
		if err != nil || current.Properties["grove_operation"] != j.OperationMarker || current.ID != j.AllocatedID {
			return errUploadPending
		}
		if j.Operation.Local.Kind == "file" && (current.MD5 != j.StageMD5 || current.Size != j.StageSize) {
			return errUploadPending
		}
		if j.Phase == "create-intent" || j.Phase == "created" {
			hash := ""
			if j.Operation.Local.Kind == "file" {
				hash = j.StageSHA256
			}
			j.After = []Completed{{Path: j.Operation.Local.Path, RemoteID: current.ID, Version: current.Version, ModifiedTime: current.ModifiedTime, HeadRevisionID: current.HeadRevisionID, MD5: current.MD5, SHA256: hash, Kind: j.Operation.Local.Kind, Size: current.Size}}
			j.Phase = "verified"
			if err := e.journal(j); err != nil {
				return err
			}
		}
		return e.finishRecoveredCreate(ctx, j)
	case "committed":
		return e.cleanupRecoveredUpload(ctx, j)
	default:
		return errUploadPending
	}
}

func (e *executor) releasePreflightPinIfPresent(ctx context.Context, fileID, revisionID string) error {
	revisions, err := e.mutation.ListRevisions(ctx, fileID)
	if err != nil {
		return err
	}
	for _, revision := range revisions {
		if revision.ID == revisionID {
			if !revision.KeepForever {
				return nil
			}
			return e.mutation.SetRevisionKeepForever(ctx, fileID, revisionID, false)
		}
	}
	return errors.New("preflight revision is missing during pin recovery")
}

func (e *executor) finishRecoveredUpdate(ctx context.Context, j Journal, current drive.File) error {
	if current.HeadRevisionID == "" || current.ModifiedTime == "" {
		return errUploadPending
	}
	revisions, err := e.mutation.ListRevisions(ctx, current.ID)
	if err != nil {
		return fmt.Errorf("pending upload revision history cannot be listed: %w", err)
	}
	foundPreflight, foundHead := false, false
	for _, revision := range revisions {
		foundPreflight = foundPreflight || revision.ID == j.PreflightRevisionID
		foundHead = foundHead || revision.ID == current.HeadRevisionID
	}
	if !foundPreflight || !foundHead {
		return errors.New("pending upload revision history is missing an anchor")
	}
	competitors, historyVerified := competingRevisionIDs(revisions, j.Operation.Entry.Remote.ModifiedTime, j.PreflightRevisionID, current.HeadRevisionID)
	if len(competitors) > 0 || !historyVerified {
		j.CompetingRevisionIDs = competitors
		j.RevisionHistoryUnverified = !historyVerified
		if err = e.journal(j); err != nil {
			return err
		}
		if !historyVerified || len(competitors) != 1 {
			return errUploadPending
		}
		for _, revision := range revisions {
			if revision.ID == competitors[0] {
				return e.captureCompetingRevision(ctx, j, revision)
			}
		}
		return errUploadPending
	}
	j.After = []Completed{{Path: j.Operation.Entry.Path, RemoteID: current.ID, Version: current.Version, ModifiedTime: current.ModifiedTime, HeadRevisionID: current.HeadRevisionID, MD5: current.MD5, SHA256: j.StageSHA256, Kind: "file", Size: j.StageSize}}
	if err = e.commitJournal(j); err != nil {
		return err
	}
	return e.cleanupRecoveredUpload(ctx, *e.state.Transaction)
}

func (e *executor) captureRecoveredCompetitor(ctx context.Context, j Journal) error {
	if len(j.CompetingRevisionIDs) != 1 {
		return errUploadPending
	}
	revisions, err := e.mutation.ListRevisions(ctx, j.Operation.Entry.Remote.ID)
	if err != nil {
		return errUploadPending
	}
	for _, revision := range revisions {
		if revision.ID == j.CompetingRevisionIDs[0] {
			return e.captureCompetingRevision(ctx, j, revision)
		}
	}
	return errUploadPending
}

func (e *executor) finishRecoveredCreate(ctx context.Context, j Journal) error {
	if len(j.After) != 1 {
		return errUploadPending
	}
	if err := e.commitJournal(j); err != nil {
		return err
	}
	return e.cleanupRecoveredUpload(ctx, *e.state.Transaction)
}

func (e *executor) cleanupRecoveredUpload(_ context.Context, j Journal) error {
	if j.StagePath != "" && !conflictUsesStage(e.state.Conflicts, j.StagePath) {
		if err := removeStageIfPresent(e.profileDir, j.StagePath, j.StageIdentity); err != nil {
			return err
		}
	}
	if j.CloudStagePath != "" && j.CloudStageIdentity != nil && !conflictUsesStage(e.state.Conflicts, j.CloudStagePath) {
		if err := removeStageIfPresent(e.profileDir, j.CloudStagePath, j.CloudStageIdentity); err != nil {
			return err
		}
	}
	return e.clearJournal()
}

func conflictUsesStage(conflicts []Conflict, path string) bool {
	for _, conflict := range conflicts {
		if conflict.LocalStagePath == path || conflict.CloudStagePath == path {
			return true
		}
	}
	return false
}

func removeStageIfPresent(profileDir, path string, identity *FileIdentity) error {
	root, err := os.OpenRoot(profileDir)
	if err != nil {
		return errors.New("cannot open upload profile")
	}
	info, err := root.Lstat(path)
	closeErr := root.Close()
	if errors.Is(err, os.ErrNotExist) && closeErr == nil {
		return nil
	}
	if err != nil || closeErr != nil {
		return errors.New("cannot inspect upload stage during recovery")
	}
	if info == nil {
		return errors.New("cannot inspect upload stage during recovery")
	}
	return removeStage(profileDir, path, identity)
}

func verifyPrivateStage(profileDir, path string, identity *FileIdentity, shaExpected, md5Expected string, sizeExpected int64) error {
	return verifyPrivateStageContext(context.Background(), profileDir, path, identity, shaExpected, md5Expected, sizeExpected)
}

func verifyPrivateStageContext(ctx context.Context, profileDir, path string, identity *FileIdentity, shaExpected, md5Expected string, sizeExpected int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !safeRelative(path) || identity == nil || identity.Inode == 0 {
		return errors.New("invalid private upload stage record")
	}
	root, err := os.OpenRoot(profileDir)
	if err != nil {
		return errors.New("cannot open upload profile")
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || (sizeExpected >= 0 && info.Size() != sizeExpected) {
		return errors.New("private upload stage is missing or changed")
	}
	actual, err := fileIdentity(info)
	if err != nil || actual != *identity {
		return errors.New("private upload stage ownership changed")
	}
	f, err := root.Open(path)
	if err != nil {
		return errors.New("cannot open private upload stage")
	}
	opened, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return errors.New("cannot inspect private upload stage")
	}
	openedIdentity, err := fileIdentity(opened)
	if err != nil || !opened.Mode().IsRegular() || openedIdentity != *identity {
		_ = f.Close()
		return errors.New("private upload stage ownership changed")
	}
	sha, md5sum := sha256.New(), md5.New()
	_, readErr := io.Copy(io.MultiWriter(sha, md5sum), &contextReader{ctx: ctx, reader: f})
	after, statErr := root.Lstat(path)
	if statErr != nil {
		_ = f.Close()
		return errors.New("private upload stage ownership changed")
	}
	afterIdentity, identityErr := fileIdentity(after)
	if identityErr != nil || afterIdentity != *identity || !after.Mode().IsRegular() {
		_ = f.Close()
		return errors.New("private upload stage ownership changed")
	}
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || (shaExpected != "" && hex.EncodeToString(sha.Sum(nil)) != shaExpected) || (md5Expected != "" && hex.EncodeToString(md5sum.Sum(nil)) != md5Expected) {
		return errors.New("private upload stage content changed")
	}
	return nil
}
