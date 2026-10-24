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
	"path/filepath"
	"sort"
	"time"

	"github.com/torfstack/grove/internal/drive"
)

func (e *executor) recordConflict(ctx context.Context, op Operation) error {
	if op.Conflict == nil {
		return errors.New("conflict operation has no conflict record")
	}
	conflict := *op.Conflict
	if conflict.Deletion {
		for _, existing := range e.state.Conflicts {
			if existing.ID == conflict.ID {
				return nil
			}
		}
		next := *e.state
		filtered := make([]Conflict, 0, len(next.Conflicts)+1)
		for _, existing := range next.Conflicts {
			if existing.Deletion && existing.Path == conflict.Path {
				continue
			}
			filtered = append(filtered, existing)
		}
		next.Conflicts = append(filtered, conflict)
		return e.persist(next)
	}
	if conflict.Baseline.RemoteID != "" {
		if err := e.requireFile(ctx, conflict.Path, Completed{Kind: "file", Size: conflict.LocalSize, SHA256: conflict.LocalSHA256}); err != nil {
			return failedPrivately("local conflict candidate changed", err)
		}
	}
	for _, existing := range e.state.Conflicts {
		if existing.ID == conflict.ID {
			return nil
		}
	}
	local, err := e.stageUpload(ctx, op.Local)
	if err != nil {
		return err
	}
	conflict.LocalStagePath = local.Path
	conflict.LocalStageIdentity = &local.Identity
	if op.Entry.Remote.MIMEType == drive.FolderMIME {
		return errors.New("folder conflict candidates are unsupported")
	}
	cloudPath := ".grove-cloud-conflict-" + nonce()
	if cloudPath == ".grove-cloud-conflict-" {
		return errors.New("cannot create cloud conflict stage identity")
	}
	profileRoot, err := os.OpenRoot(e.profileDir)
	if err != nil {
		return errors.New("cannot open private conflict profile")
	}
	cloudFile, err := profileRoot.OpenFile(cloudPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = profileRoot.Close()
		return errors.New("cannot create private cloud conflict stage")
	}
	info, err := cloudFile.Stat()
	if err != nil {
		_ = cloudFile.Close()
		_ = profileRoot.Close()
		return errors.New("cannot identify cloud conflict stage")
	}
	identity, err := fileIdentity(info)
	if err != nil {
		_ = cloudFile.Close()
		_ = profileRoot.Close()
		return err
	}
	sha, md5sum := sha256.New(), md5.New()
	w := io.MultiWriter(cloudFile, sha, md5sum)
	count := &countWriter{writer: w}
	err = e.api.Download(ctx, conflict.RemoteID, count)
	syncErr := cloudFile.Sync()
	closeErr := cloudFile.Close()
	rootSyncErr := syncProfileRoot(profileRoot)
	rootCloseErr := profileRoot.Close()
	cloudSHA := hex.EncodeToString(sha.Sum(nil))
	cloudMD5 := hex.EncodeToString(md5sum.Sum(nil))
	if err != nil || syncErr != nil || closeErr != nil || rootSyncErr != nil || rootCloseErr != nil || count.count != conflict.RemoteSize || cloudMD5 != conflict.RemoteMD5 {
		return failedPrivately("cannot verify cloud conflict candidate", err, syncErr, closeErr, rootSyncErr, rootCloseErr)
	}
	conflict.CloudStagePath = cloudPath
	conflict.CloudStageIdentity = &identity
	conflict.CloudSHA256 = cloudSHA
	conflict.RemoteRevisionID = op.Entry.Remote.HeadRevisionID
	conflict.CloudRevisionID = conflict.RemoteRevisionID
	conflict.CloudMD5 = conflict.RemoteMD5
	conflict.CloudSize = conflict.RemoteSize
	conflict.ID = conflictID(conflict)
	*op.Conflict = conflict
	e.state.Conflicts = append(e.state.Conflicts, conflict)
	if err = e.save(); err != nil {
		return err
	}
	return nil
}

func (e *executor) upload(ctx context.Context, op Operation) error {
	if e.mutation == nil {
		return errors.New("two-way sync requires a mutation-capable Drive API")
	}
	if op.Kind == OpCreate {
		ids, err := e.mutation.GenerateIDs(ctx, 1)
		if err != nil {
			return err
		}
		if len(ids) != 1 || ids[0] == "" {
			return errors.New("drive returned an invalid create ID")
		}
		parentID := e.remoteRoot
		parentPath := filepath.Dir(op.Local.Path)
		if parentPath != "." {
			found := false
			for _, baseline := range e.state.Completed {
				if baseline.Path == parentPath && baseline.Kind == "folder" {
					parentID, found = baseline.RemoteID, true
					break
				}
			}
			if !found {
				return errors.New("local upload parent is not tracked")
			}
		}
		marker := nonce()
		if marker == "" {
			return errors.New("cannot create upload operation identity")
		}
		name := filepath.Base(op.Local.Path)
		mime := "application/octet-stream"
		if op.Local.Kind == "folder" {
			mime = drive.FolderMIME
		}
		op.Entry = Entry{Path: op.Local.Path, Remote: drive.File{ID: ids[0], Name: name, MIMEType: mime, Parents: []string{parentID}, OwnedByMe: true}}
		op.OperationMarker = marker
	}
	if op.Kind == OpUpload {
		if len(op.Before) != 1 || op.Entry.Remote.HeadRevisionID == "" || op.Entry.Remote.ModifiedTime == "" {
			return errors.New("remote revision metadata is required before upload")
		}
		if err := e.currentUploadRemote(ctx, op.Entry); err != nil {
			return err
		}
	}
	if err := e.prepareUpload(ctx, op); err != nil {
		return err
	}
	j := *e.state.Transaction
	var input drive.Create
	input.Name = filepath.Base(op.Local.Path)
	input.MIMEType = op.Entry.Remote.MIMEType
	if op.Kind == OpCreate {
		input.ParentID = op.Entry.Remote.Parents[0]
		input.Properties = map[string]string{"grove_operation": op.OperationMarker}
	}
	var result drive.File
	if op.Local.Kind == "file" {
		stageRoot, err := os.OpenRoot(e.profileDir)
		if err != nil {
			return errors.New("cannot open upload stage")
		}
		stage, err := stageRoot.Open(j.StagePath)
		if err != nil {
			_ = stageRoot.Close()
			return errors.New("cannot open upload stage")
		}
		if op.Kind == OpUpload {
			wasKept, pinErr := e.preflightRevisionWasKept(ctx, op.Entry.Remote.ID, j.PreflightRevisionID)
			if pinErr != nil {
				_ = stage.Close()
				_ = stageRoot.Close()
				return pinErr
			}
			j.PreflightPinWasKept = &wasKept
			j.Phase = "pin-intent"
			if err = e.journal(j); err != nil {
				_ = stage.Close()
				_ = stageRoot.Close()
				return err
			}
			if err = e.mutation.SetRevisionKeepForever(ctx, op.Entry.Remote.ID, j.PreflightRevisionID, true); err != nil {
				_ = stage.Close()
				_ = stageRoot.Close()
				return err
			}
			j.PinnedRevisionID = j.PreflightRevisionID
			j.Phase = "pinned"
			if err = e.journal(j); err != nil {
				_ = stage.Close()
				_ = stageRoot.Close()
				return err
			}
			j.Phase = "upload-intent"
			if err = e.journal(j); err != nil {
				_ = stage.Close()
				_ = stageRoot.Close()
				return err
			}
			result, err = e.mutation.UpdateContent(ctx, op.Entry.Remote.ID, drive.Update{ContentChanged: true}, stage, j.StageSize)
		} else {
			j.Phase = "create-intent"
			if err = e.journal(j); err != nil {
				_ = stage.Close()
				_ = stageRoot.Close()
				return err
			}
			result, err = e.mutation.CreateWithID(ctx, j.AllocatedID, input, stage, j.StageSize)
		}
		stageCloseErr := stage.Close()
		rootCloseErr := stageRoot.Close()
		if err != nil || stageCloseErr != nil || rootCloseErr != nil {
			return failedPrivately("remote upload outcome is uncertain", err, stageCloseErr, rootCloseErr)
		}
	} else {
		j.Phase = "create-intent"
		if err := e.journal(j); err != nil {
			return err
		}
		created, createErr := e.mutation.CreateWithID(ctx, j.AllocatedID, input, nil, 0)
		if createErr != nil {
			return failedPrivately("remote folder creation outcome is uncertain", createErr)
		}
		result = created
	}
	if result.ID != op.Entry.Remote.ID || result.Name != input.Name || result.MIMEType != input.MIMEType || (op.Local.Kind == "file" && (result.Size != j.StageSize || result.MD5 != j.StageMD5)) {
		return errors.New("remote upload result did not match staged content")
	}
	if op.Kind == OpUpload {
		if result.HeadRevisionID == "" || result.ModifiedTime == "" {
			return errors.New("remote upload result has no revision identity")
		}
		j.Phase = "uploaded"
		j.After = []Completed{{Path: op.Entry.Path, RemoteID: result.ID, Version: result.Version, ModifiedTime: result.ModifiedTime, HeadRevisionID: result.HeadRevisionID, MD5: result.MD5, SHA256: j.StageSHA256, Kind: "file", Size: j.StageSize}}
	} else {
		j.Phase = "verified"
		hash := ""
		if op.Local.Kind == "file" {
			hash = j.StageSHA256
		}
		j.After = []Completed{{Path: op.Local.Path, RemoteID: result.ID, Version: result.Version, ModifiedTime: result.ModifiedTime, HeadRevisionID: result.HeadRevisionID, MD5: result.MD5, SHA256: hash, Kind: op.Local.Kind, Size: result.Size}}
	}
	if err := e.journal(j); err != nil {
		return err
	}
	if op.Kind == OpUpload {
		revisions, err := e.mutation.ListRevisions(ctx, result.ID)
		if err != nil {
			return fmt.Errorf("cannot verify uploaded revision history: %w", err)
		}
		foundOld, foundHead := false, false
		competitors, historyVerified := competingRevisionIDs(revisions, j.Operation.Entry.Remote.ModifiedTime, j.PreflightRevisionID, result.HeadRevisionID)
		for _, revision := range revisions {
			foundOld = foundOld || revision.ID == j.PreflightRevisionID
			foundHead = foundHead || revision.ID == result.HeadRevisionID
		}
		if !foundOld || !foundHead || result.HeadRevisionID == "" {
			return errors.New("uploaded revision history is incomplete")
		}
		if len(competitors) > 0 || !historyVerified {
			j.CompetingRevisionIDs = competitors
			j.RevisionHistoryUnverified = !historyVerified
			if err = e.journal(j); err != nil {
				return err
			}
			for _, revisionID := range competitors {
				if err = e.mutation.SetRevisionKeepForever(ctx, result.ID, revisionID, true); err != nil {
					return &RecoveryError{Message: "cannot preserve concurrent remote revision; upload recovery is pending", Cause: err}
				}
			}
			if !historyVerified {
				return blockedRecovery("history-incomplete")
			}
			return e.captureCompetingRevisions(ctx, j, revisions)
		}
	}
	if err := e.commitJournal(j); err != nil {
		return err
	}
	return e.cleanupRecoveredUpload(ctx, *e.state.Transaction)
}

func (e *executor) preflightRevisionWasKept(ctx context.Context, fileID, revisionID string) (bool, error) {
	revisions, err := e.mutation.ListRevisions(ctx, fileID)
	if err != nil {
		return false, err
	}
	for _, revision := range revisions {
		if revision.ID == revisionID {
			return revision.KeepForever, nil
		}
	}
	return false, errors.New("preflight revision is missing before pinning")
}

func competingRevisionIDs(revisions []drive.Revision, boundary, preflight, head string) ([]string, bool) {
	boundaryTime, boundaryErr := time.Parse(time.RFC3339Nano, boundary)
	verified := boundaryErr == nil
	var ids []string
	for _, revision := range revisions {
		modified, parseErr := time.Parse(time.RFC3339Nano, revision.ModifiedTime)
		if parseErr != nil {
			verified = false
		}
		if revision.ID == preflight || revision.ID == head {
			continue
		}
		if revision.ID == "" {
			verified = false
			continue
		}
		if parseErr != nil || boundaryErr != nil {
			verified = false
			ids = append(ids, revision.ID)
		} else if !modified.Before(boundaryTime) {
			ids = append(ids, revision.ID)
		}
	}
	byID := make(map[string]drive.Revision, len(revisions))
	for _, revision := range revisions {
		byID[revision.ID] = revision
	}
	sort.Slice(ids, func(i, j int) bool {
		return revisionLess(byID[ids[i]], byID[ids[j]])
	})
	return ids, verified
}

func revisionLess(left, right drive.Revision) bool {
	return cloudCandidateLess(
		CloudCandidate{RevisionID: left.ID, ModifiedTime: left.ModifiedTime},
		CloudCandidate{RevisionID: right.ID, ModifiedTime: right.ModifiedTime},
	)
}

func (e *executor) captureCompetingRevisions(ctx context.Context, j Journal, revisions []drive.Revision) error {
	byID := make(map[string]drive.Revision, len(revisions))
	for _, revision := range revisions {
		if revision.ID == "" || byID[revision.ID].ID != "" {
			return blockedRecovery("history-incomplete")
		}
		byID[revision.ID] = revision
	}
	for _, id := range j.CompetingRevisionIDs {
		revision, ok := byID[id]
		if !ok {
			return blockedRecovery("history-incomplete")
		}
		if err := e.captureCompetingRevision(ctx, &j, revision); err != nil {
			return err
		}
	}
	if len(j.CandidateCaptures) != len(j.CompetingRevisionIDs) {
		return blockedRecovery("capture-pending")
	}
	sort.Slice(j.CandidateCaptures, func(i, k int) bool {
		return cloudCandidateLess(j.CandidateCaptures[i].Candidate, j.CandidateCaptures[k].Candidate)
	})
	first := j.CandidateCaptures[0].Candidate
	j.CloudRevisionID, j.CloudMD5, j.CloudSize = first.RevisionID, first.MD5, first.Size
	j.CloudStagePath, j.CloudStageIdentity, j.CloudSHA256 = first.StagePath, first.StageIdentity, first.SHA256
	j.Phase = "conflict-captured"
	if err := e.journal(j); err != nil {
		return err
	}
	return e.finishCapturedConflict(ctx, j)
}

func (e *executor) captureCompetingRevision(ctx context.Context, j *Journal, revision drive.Revision) error {
	if len(j.After) != 1 || revision.ID == "" || revision.ID == j.PreflightRevisionID || revision.ID == j.After[0].HeadRevisionID || !validHash(revision.MD5, 16) || !revision.HasSize || revision.Size < 0 {
		return blockedRecovery("history-incomplete")
	}
	if revision.ModifiedTime != "" {
		if _, err := time.Parse(time.RFC3339Nano, revision.ModifiedTime); err != nil {
			return blockedRecovery("history-incomplete")
		}
	}
	var capture *CandidateCapture
	for i := range j.CandidateCaptures {
		if j.CandidateCaptures[i].Candidate.RevisionID == revision.ID {
			capture = &j.CandidateCaptures[i]
			break
		}
	}
	if capture == nil {
		path := ".grove-cloud-conflict-" + nonce()
		if path == ".grove-cloud-conflict-" {
			return errors.New("cannot create cloud conflict stage identity")
		}
		entry := CandidateCapture{
			Candidate: CloudCandidate{RevisionID: revision.ID, ModifiedTime: revision.ModifiedTime, MD5: revision.MD5, Size: revision.Size, StagePath: path},
			Phase:     "pin-intent",
		}
		at := sort.Search(len(j.CandidateCaptures), func(i int) bool {
			return cloudCandidateLess(entry.Candidate, j.CandidateCaptures[i].Candidate)
		})
		j.CandidateCaptures = append(j.CandidateCaptures, CandidateCapture{})
		copy(j.CandidateCaptures[at+1:], j.CandidateCaptures[at:])
		j.CandidateCaptures[at] = entry
		capture = &j.CandidateCaptures[at]
		if err := e.journal(*j); err != nil {
			return err
		}
	} else if capture.Candidate.ModifiedTime != revision.ModifiedTime || capture.Candidate.MD5 != revision.MD5 || capture.Candidate.Size != revision.Size {
		return blockedRecovery("history-incomplete")
	}
	if capture.Phase == "captured" {
		return verifyPrivateStageContext(ctx, e.profileDir, capture.Candidate.StagePath, capture.Candidate.StageIdentity, capture.Candidate.SHA256, capture.Candidate.MD5, capture.Candidate.Size)
	}
	if capture.Phase == "capture-intent" {
		if err := removeStageIfPresent(e.profileDir, capture.Candidate.StagePath, capture.Candidate.StageIdentity); err != nil {
			return blockedRecovery("stage-unavailable")
		}
		capture.Candidate.StageIdentity = nil
		capture.Candidate.SHA256 = ""
		capture.Phase = "pinned"
		if err := e.journal(*j); err != nil {
			return err
		}
	}
	if capture.Phase == "pin-intent" || capture.Phase == "pinned" {
		if err := e.mutation.SetRevisionKeepForever(ctx, j.Operation.Entry.Remote.ID, revision.ID, true); err != nil {
			return &RecoveryError{Message: "cannot preserve concurrent remote revision; upload recovery is pending", Cause: err}
		}
		capture.Phase = "pinned"
		if err := e.journal(*j); err != nil {
			return err
		}
	}
	profileRoot, err := os.OpenRoot(e.profileDir)
	if err != nil {
		return errors.New("cannot open private conflict profile")
	}
	file, err := profileRoot.OpenFile(capture.Candidate.StagePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = profileRoot.Close()
		return errors.New("cannot create private cloud conflict stage")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		_ = profileRoot.Close()
		return errors.New("cannot identify cloud conflict stage")
	}
	identity, err := fileIdentity(info)
	if err != nil {
		_ = file.Close()
		_ = profileRoot.Close()
		return err
	}
	capture.Candidate.StageIdentity = &identity
	capture.Phase = "capture-intent"
	if err = e.journal(*j); err != nil {
		_ = file.Close()
		_ = profileRoot.Close()
		_ = removeStage(e.profileDir, capture.Candidate.StagePath, &identity)
		return err
	}
	sha, md5sum := sha256.New(), md5.New()
	count := &countWriter{writer: io.MultiWriter(file, sha, md5sum)}
	err = e.mutation.DownloadRevision(ctx, j.Operation.Entry.Remote.ID, revision.ID, count)
	syncErr := file.Sync()
	closeErr := file.Close()
	rootSyncErr := syncProfileRoot(profileRoot)
	rootCloseErr := profileRoot.Close()
	cloudSHA, cloudMD5 := hex.EncodeToString(sha.Sum(nil)), hex.EncodeToString(md5sum.Sum(nil))
	if err != nil || syncErr != nil || closeErr != nil || rootSyncErr != nil || rootCloseErr != nil || count.count != revision.Size || cloudMD5 != revision.MD5 {
		cause := errors.Join(err, syncErr, closeErr, rootSyncErr, rootCloseErr)
		if cause != nil {
			return &RecoveryError{Message: "cannot verify competing cloud revision; upload recovery is pending", Cause: cause}
		}
		return blockedRecovery("capture-pending")
	}
	capture.Candidate.SHA256 = cloudSHA
	capture.Phase = "captured"
	if err = e.journal(*j); err != nil {
		return err
	}
	return nil
}

func (e *executor) finishCapturedConflict(ctx context.Context, j Journal) error {
	if j.Phase != "conflict-captured" || len(j.After) != 1 || j.CloudRevisionID == "" {
		return errors.New("invalid captured conflict journal")
	}
	candidates := make([]CloudCandidate, 0, len(j.CandidateCaptures))
	if len(j.CandidateCaptures) == 0 {
		candidates = append(candidates, CloudCandidate{
			RevisionID: j.CloudRevisionID, MD5: j.CloudMD5, Size: j.CloudSize,
			SHA256: j.CloudSHA256, StagePath: j.CloudStagePath, StageIdentity: j.CloudStageIdentity,
		})
	} else {
		if len(j.CandidateCaptures) != len(j.CompetingRevisionIDs) {
			return blockedRecovery("capture-pending")
		}
		for _, capture := range j.CandidateCaptures {
			if capture.Phase != "captured" {
				return blockedRecovery("capture-pending")
			}
			candidate := capture.Candidate
			if err := verifyPrivateStageContext(ctx, e.profileDir, candidate.StagePath, candidate.StageIdentity, candidate.SHA256, candidate.MD5, candidate.Size); err != nil {
				return err
			}
			candidates = append(candidates, candidate)
		}
	}
	current, err := e.api.Get(ctx, j.Operation.Entry.Remote.ID)
	if err != nil {
		return err
	}
	if !recoveredStructure(j, current) || current.HeadRevisionID != j.After[0].HeadRevisionID || current.ModifiedTime != j.After[0].ModifiedTime || current.Size != j.After[0].Size || current.MD5 != j.After[0].MD5 {
		return blockedRecovery("remote-changed")
	}
	if err = ctx.Err(); err != nil {
		return err
	}

	sort.Slice(candidates, func(i, k int) bool { return cloudCandidateLess(candidates[i], candidates[k]) })
	first := candidates[0]
	conflict := Conflict{
		CandidateSetVersion: 1, CloudCandidates: candidates,
		Path: j.Operation.Entry.Path, Baseline: j.After[0],
		LocalSHA256: j.StageSHA256, LocalSize: j.StageSize,
		RemoteID: j.After[0].RemoteID, RemoteVersion: j.After[0].Version,
		RemoteMD5: j.After[0].MD5, RemoteSize: j.After[0].Size, RemoteRevisionID: j.After[0].HeadRevisionID,
		CloudRevisionID: first.RevisionID, CloudMD5: first.MD5, CloudSize: first.Size,
		LocalStagePath: j.StagePath, LocalStageIdentity: j.StageIdentity,
		CloudStagePath: first.StagePath, CloudStageIdentity: first.StageIdentity, CloudSHA256: first.SHA256,
	}
	conflict.ID = conflictID(conflict)
	next := *e.state
	if j.Operation.Conflict != nil {
		next.Conflicts = withoutConflict(next.Conflicts, j.Operation.Conflict.ID)
	}
	if findConflict(next.Conflicts, conflict.ID) == nil {
		next.Conflicts = append(next.Conflicts, conflict)
	}
	if err := applyBaseline(&next, j.Operation.Before, j.After); err != nil {
		return err
	}
	j.Phase = "committed"
	next.Transaction = &j
	if err := e.persist(next); err != nil {
		return err
	}
	return e.cleanupRecoveredUpload(ctx, j)
}

func (e *executor) resolveConflictOperation(ctx context.Context, operation Operation) error {
	conflict := *operation.Conflict
	saved := findConflict(e.state.Conflicts, conflict.ID)
	if saved == nil {
		return errors.New("pending conflict candidates are missing")
	}
	if saved.Deletion {
		if operation.ResolutionSide != SideCloud {
			return errors.New("deletion conflict must be resolved through its explicit trash plan")
		}
		next := *e.state
		if err := applyBaseline(&next, saved.DeletionSubtree, nil); err != nil {
			return err
		}
		next.Conflicts = withoutConflict(next.Conflicts, saved.ID)
		next.PopulationComplete = false
		return e.persist(next)
	}
	if operation.ResolutionSide == SideLocal {
		if len(operation.Before) != 1 {
			return errors.New("local resolution for an untracked same-path addition is not supported")
		}
		if operation.Before[0] == saved.Baseline && operation.Before[0].SHA256 == saved.LocalSHA256 && operation.Before[0].HeadRevisionID == saved.RemoteRevisionID {
			return e.forgetConflict(ctx, *saved)
		}
		selected := operation
		selected.Kind = OpUpload
		selected.Conflict = saved
		return e.upload(ctx, selected)
	}
	if operation.ResolutionSide != SideCloud {
		return errors.New("invalid conflict resolution side")
	}
	selectedConflict := *saved
	if selectedConflict.SelectedCandidate == 0 && len(conflictCandidates(selectedConflict)) == 1 {
		selectedConflict.SelectedSide = SideCloud
		selectedConflict.SelectedCandidate = 1
	}
	if err := e.installCloudCandidate(ctx, selectedConflict, operation.Entry.Remote); err != nil {
		return fmt.Errorf("install selected cloud candidate: %w", err)
	}
	var baseline *Completed
	for i := range e.state.Completed {
		if e.state.Completed[i].Path == saved.Path {
			baseline = &e.state.Completed[i]
			break
		}
	}
	if baseline == nil || baseline.Kind != "file" {
		return errors.New("cloud conflict resolution lost its remote baseline")
	}
	local, err := scanLocal(ctx, e.root, nil)
	if err != nil {
		return err
	}
	for _, candidate := range local {
		if candidate.Path == saved.Path && !matchesLocal(*baseline, candidate) {
			selected := Operation{Kind: OpUpload, Entry: operation.Entry, Local: candidate, Before: []Completed{*baseline}, Conflict: saved, ResolutionSide: SideLocal}
			if err := e.upload(ctx, selected); err != nil {
				return fmt.Errorf("upload selected cloud candidate: %w", err)
			}
			return nil
		}
	}
	return nil
}

func findConflict(conflicts []Conflict, id string) *Conflict {
	for i := range conflicts {
		if conflicts[i].ID == id {
			return &conflicts[i]
		}
	}
	return nil
}

func (e *executor) forgetConflict(ctx context.Context, conflict Conflict) error {
	next := *e.state
	filtered := next.Conflicts[:0]
	for _, current := range next.Conflicts {
		if current.ID != conflict.ID {
			filtered = append(filtered, current)
		}
	}
	next.Conflicts = filtered
	if err := e.persist(next); err != nil {
		return err
	}
	for _, stage := range []struct {
		path     string
		identity *FileIdentity
	}{{conflict.LocalStagePath, conflict.LocalStageIdentity}} {
		if stage.path != "" && stage.identity != nil {
			if err := removeStage(e.profileDir, stage.path, stage.identity); err != nil {
				return err
			}
		}
	}
	for _, candidate := range conflictCandidates(conflict) {
		if candidate.StagePath != "" && candidate.StageIdentity != nil && !conflictUsesStage(e.state.Conflicts, candidate.StagePath) {
			if err := removeVerifiedStageIfPresent(ctx, e.profileDir, candidate.StagePath, candidate.StageIdentity, candidate.SHA256, candidate.MD5, candidate.Size); err != nil {
				return fmt.Errorf("remove cloud candidate stage %q: %w", candidate.StagePath, err)
			}
		}
	}
	return nil
}

func (e *executor) installCloudCandidate(ctx context.Context, conflict Conflict, head drive.File) error {
	candidate, err := selectedCloudCandidate(conflict)
	if err != nil {
		return err
	}
	cloudSize := candidate.Size
	if err := verifyPrivateStage(e.profileDir, candidate.StagePath, candidate.StageIdentity, candidate.SHA256, candidate.MD5, cloudSize); err != nil {
		return err
	}
	current, err := e.inspectFile(ctx, conflict.Path, Completed{Kind: "file", Size: conflict.LocalSize, SHA256: conflict.LocalSHA256})
	if err != nil {
		cloudPresent, cloudErr := e.inspectFile(ctx, conflict.Path, Completed{Kind: "file", Size: cloudSize, SHA256: candidate.SHA256})
		if cloudErr != nil || !cloudPresent {
			return failedPrivately("local conflict candidate changed", err)
		}
	} else if !current {
		return errors.New("local conflict candidate is missing")
	}
	temp := filepath.Join(filepath.Dir(conflict.Path), ".grove-conflict-"+nonce())
	if err = rootPath(e.root, temp); err != nil {
		return err
	}
	profileRoot, err := os.OpenRoot(e.profileDir)
	if err != nil {
		return errors.New("cannot open cloud conflict stage")
	}
	source, err := profileRoot.Open(candidate.StagePath)
	if err != nil {
		_ = profileRoot.Close()
		return errors.New("cannot open cloud conflict stage")
	}
	target, err := e.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		_ = source.Close()
		_ = profileRoot.Close()
		return errors.New("cannot create conflict resolution temporary file")
	}
	sha := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(target, sha), source)
	syncErr := target.Sync()
	closeTargetErr := target.Close()
	closeSourceErr := source.Close()
	closeRootErr := profileRoot.Close()
	if copyErr != nil || syncErr != nil || closeTargetErr != nil || closeSourceErr != nil || closeRootErr != nil || hex.EncodeToString(sha.Sum(nil)) != candidate.SHA256 {
		_ = e.root.Remove(temp)
		return errors.New("cannot verify cloud conflict candidate")
	}
	if err = e.root.Rename(temp, conflict.Path); err != nil {
		_ = e.root.Remove(temp)
		return errors.New("cannot install cloud conflict candidate")
	}
	if err = e.parentSync(conflict.Path); err != nil {
		return err
	}
	baselineSHA, baselineSize := candidate.SHA256, cloudSize
	if candidate.RevisionID != conflict.RemoteRevisionID {
		baselineSHA, baselineSize = conflict.LocalSHA256, conflict.LocalSize
	}
	after := Completed{Path: conflict.Path, RemoteID: conflict.RemoteID, Version: conflict.RemoteVersion, HeadRevisionID: conflict.RemoteRevisionID, MD5: conflict.RemoteMD5, SHA256: baselineSHA, Kind: "file", Size: baselineSize}
	if head.HeadRevisionID == conflict.RemoteRevisionID {
		after.ModifiedTime = head.ModifiedTime
	}
	if conflict.Baseline.RemoteID != "" {
		if err = applyBaseline(e.state, []Completed{conflict.Baseline}, []Completed{after}); err != nil {
			return err
		}
	} else {
		if err = applyBaseline(e.state, nil, []Completed{after}); err != nil {
			return err
		}
	}
	if err = e.save(); err != nil {
		return err
	}
	return e.forgetConflict(ctx, conflict)
}

func removeStage(profileDir, path string, identity *FileIdentity) error {
	if identity == nil {
		return errors.New("upload stage identity is missing")
	}
	root, err := os.OpenRoot(profileDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err = removeOwnedStage(root, path, *identity); err != nil {
		return err
	}
	return syncProfileRoot(root)
}
