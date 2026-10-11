package syncengine

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/torfstack/grove/internal/drive"
)

var errUploadPending = errors.New("pending upload requires explicit two-way resume")

func (e *executor) recoverUpload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.state.Transaction == nil {
		return errors.New("invalid pending upload")
	}
	j := *e.state.Transaction
	if j.Operation.Kind != OpUpload && j.Operation.Kind != OpCreate {
		return blockedRecovery("unsupported-operation")
	}
	if !e.resumeUploads || e.mutation == nil {
		return errUploadPending
	}
	if j.Phase == "retired" {
		return e.cleanupRetiredUpload(ctx, j)
	}
	if j.Phase == "committed" {
		return e.cleanupRecoveredUpload(ctx, j)
	}
	if j.StagePath != "" {
		if err := verifyPrivateStageContext(ctx, e.profileDir, j.StagePath, j.StageIdentity, j.StageSHA256, j.StageMD5, j.StageSize); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return blockedRecovery("stage-unavailable")
		}
	}
	if j.Phase == "conflict-captured" {
		if len(j.After) != 1 {
			return blockedRecovery("capture-pending")
		}
		current, err := e.api.Get(ctx, j.Operation.Entry.Remote.ID)
		if err != nil {
			return err
		}
		if current.HeadRevisionID != j.After[0].HeadRevisionID {
			return blockedRecovery("remote-changed")
		}
		if _, err = e.verifyRecoveredRemote(ctx, j, current); err != nil {
			return err
		}
		return e.finishCapturedConflict(ctx, j)
	}
	if j.Phase == "capture-intent" {
		if err := removeStageIfPresent(e.profileDir, j.CloudStagePath, j.CloudStageIdentity); err != nil {
			return blockedRecovery("stage-unavailable")
		}
		j.Phase = "uploaded"
		j.CloudStagePath, j.CloudStageIdentity, j.CloudSHA256 = "", nil, ""
		j.CloudRevisionID, j.CloudMD5, j.CloudSize = "", "", 0
		if err := e.journal(j); err != nil {
			return err
		}
	}
	switch j.Phase {
	case "staged", "pin-intent", "pinned":
		return e.recoverPreSend(ctx, j)
	case "upload-intent", "uploaded":
		current, err := e.api.Get(ctx, j.Operation.Entry.Remote.ID)
		if errors.Is(err, drive.ErrNotFound) {
			return blockedRecovery("uncertain-write")
		}
		if err != nil {
			return err
		}
		if j.Phase == "upload-intent" && current.HeadRevisionID == j.PreflightRevisionID {
			return blockedRecovery("uncertain-write")
		}
		if j.Phase == "uploaded" && (len(j.After) != 1 || current.HeadRevisionID != j.After[0].HeadRevisionID) {
			return blockedRecovery("remote-changed")
		}
		return e.finishRecoveredUpdate(ctx, j, current)
	case "create-intent", "created", "verified":
		current, err := e.api.Get(ctx, j.AllocatedID)
		if errors.Is(err, drive.ErrNotFound) {
			return blockedRecovery("uncertain-write")
		}
		if err != nil {
			return err
		}
		completed, err := e.verifyRecoveredRemote(ctx, j, current)
		if err != nil {
			return err
		}
		if j.Phase == "verified" && len(j.After) == 1 && completed.HeadRevisionID != j.After[0].HeadRevisionID {
			return blockedRecovery("remote-changed")
		}
		j.After = []Completed{completed}
		j.Phase = "verified"
		if err = e.journal(j); err != nil {
			return err
		}
		return e.finishRecoveredCreate(ctx, j)
	default:
		return blockedRecovery("unsupported-operation")
	}
}

func (e *executor) recoverPreSend(ctx context.Context, j Journal) error {
	if j.Operation.Kind == OpUpload {
		if err := e.currentUploadRemote(ctx, j.Operation.Entry); err != nil {
			if errors.Is(err, errPendingRemoteChanged) {
				return blockedRecovery("remote-changed")
			}
			return err
		}
		if j.PreflightPinWasKept != nil && !*j.PreflightPinWasKept {
			if err := e.releasePreflightPinIfPresent(ctx, j.Operation.Entry.Remote.ID, j.PreflightRevisionID); err != nil {
				return err
			}
		}
	}
	j.Phase = "retired"
	j.After = nil
	if err := e.journal(j); err != nil {
		return err
	}
	return e.cleanupRetiredUpload(ctx, j)
}

func (e *executor) cleanupRetiredUpload(ctx context.Context, j Journal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.StagePath != "" {
		if err := removeVerifiedStageIfPresent(ctx, e.profileDir, j.StagePath, j.StageIdentity, j.StageSHA256, j.StageMD5, j.StageSize); err != nil {
			return err
		}
	}
	return e.clearJournal()
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
	completed, err := e.verifyRecoveredRemote(ctx, j, current)
	if err != nil {
		return err
	}
	revisions, err := e.mutation.ListRevisions(ctx, current.ID)
	if err != nil {
		return err
	}
	foundPreflight, foundHead := false, false
	seen := map[string]bool{}
	for _, revision := range revisions {
		if revision.ID == "" || seen[revision.ID] {
			return blockedRecovery("history-incomplete")
		}
		seen[revision.ID] = true
		foundPreflight = foundPreflight || revision.ID == j.PreflightRevisionID
		foundHead = foundHead || revision.ID == current.HeadRevisionID
	}
	if !foundPreflight || !foundHead {
		return blockedRecovery("history-incomplete")
	}
	competitors, verified := competingRevisionIDs(revisions, j.Operation.Entry.Remote.ModifiedTime, j.PreflightRevisionID, current.HeadRevisionID)
	obligations := append([]string(nil), j.CompetingRevisionIDs...)
	known := map[string]bool{}
	for _, id := range obligations {
		known[id] = true
	}
	for _, id := range competitors {
		if !known[id] {
			obligations = append(obligations, id)
			known[id] = true
		}
	}
	j.After = []Completed{completed}
	j.Phase = "uploaded"
	j.CompetingRevisionIDs = obligations
	j.RevisionHistoryUnverified = !verified
	if err = e.journal(j); err != nil {
		return err
	}
	for _, id := range obligations {
		if !seen[id] {
			return blockedRecovery("history-incomplete")
		}
		if err = e.mutation.SetRevisionKeepForever(ctx, current.ID, id, true); err != nil {
			return err
		}
	}
	if !verified {
		return blockedRecovery("history-incomplete")
	}
	if len(obligations) > 1 {
		return blockedRecovery("multiple-competitors")
	}
	if len(obligations) == 1 {
		return e.captureRecoveredCompetitor(ctx, j)
	}
	// Recheck the head after the history read before recording the baseline.
	after, err := e.api.Get(ctx, current.ID)
	if err != nil {
		return err
	}
	if !recoveredStructure(j, after) || after.HeadRevisionID != completed.HeadRevisionID || after.ModifiedTime != completed.ModifiedTime || after.MD5 != completed.MD5 || after.Size != completed.Size {
		return blockedRecovery("remote-changed")
	}
	if err = e.commitJournal(j); err != nil {
		return err
	}
	return e.cleanupRecoveredUpload(ctx, *e.state.Transaction)
}

func (e *executor) captureRecoveredCompetitor(ctx context.Context, j Journal) error {
	if len(j.CompetingRevisionIDs) != 1 {
		return blockedRecovery("multiple-competitors")
	}
	revisions, err := e.mutation.ListRevisions(ctx, j.Operation.Entry.Remote.ID)
	if err != nil {
		return err
	}
	for _, revision := range revisions {
		if revision.ID == j.CompetingRevisionIDs[0] {
			return e.captureCompetingRevision(ctx, j, revision)
		}
	}
	return blockedRecovery("history-incomplete")
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

func (e *executor) cleanupRecoveredUpload(ctx context.Context, j Journal) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.StagePath != "" && !conflictUsesStage(e.state.Conflicts, j.StagePath) {
		if err := removeVerifiedStageIfPresent(ctx, e.profileDir, j.StagePath, j.StageIdentity, j.StageSHA256, j.StageMD5, j.StageSize); err != nil {
			return err
		}
	}
	if j.CloudStagePath != "" && j.CloudStageIdentity != nil && !conflictUsesStage(e.state.Conflicts, j.CloudStagePath) {
		if err := removeVerifiedStageIfPresent(ctx, e.profileDir, j.CloudStagePath, j.CloudStageIdentity, j.CloudSHA256, j.CloudMD5, j.CloudSize); err != nil {
			return err
		}
	}
	if j.Operation.Conflict != nil {
		if err := e.cleanupResolvedConflict(ctx, *j.Operation.Conflict); err != nil {
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

func removeVerifiedStageIfPresent(ctx context.Context, profile, path string, identity *FileIdentity, sha, md5 string, size int64) error {
	root, err := os.OpenRoot(profile)
	if err != nil {
		return err
	}
	_, statErr := root.Lstat(path)
	closeErr := root.Close()
	if errors.Is(statErr, os.ErrNotExist) && closeErr == nil {
		return nil
	}
	if statErr != nil {
		return statErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err = verifyPrivateStageContext(ctx, profile, path, identity, sha, md5, size); err != nil {
		return err
	}
	return removeStage(profile, path, identity)
}
