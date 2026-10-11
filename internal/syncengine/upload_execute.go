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
	"time"

	"github.com/torfstack/grove/internal/drive"
)

func (e *executor) recordConflict(ctx context.Context, op Operation) error {
	if op.Conflict == nil {
		return errors.New("conflict operation has no conflict record")
	}
	conflict := *op.Conflict
	if conflict.Baseline.RemoteID != "" {
		if err := e.requireFile(ctx, conflict.Path, Completed{Kind: "file", Size: conflict.LocalSize, SHA256: conflict.LocalSHA256}); err != nil {
			return errors.New("local conflict candidate changed")
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
		return errors.New("cannot verify cloud conflict candidate")
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
			return errors.New("remote upload outcome is uncertain")
		}
	} else {
		j.Phase = "create-intent"
		if err := e.journal(j); err != nil {
			return err
		}
		created, createErr := e.mutation.CreateWithID(ctx, j.AllocatedID, input, nil, 0)
		if createErr != nil {
			return errors.New("remote folder creation outcome is uncertain")
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
					return errors.New("cannot preserve concurrent remote revision; upload recovery is pending")
				}
			}
			if !historyVerified || len(competitors) != 1 {
				return errors.New("concurrent remote revisions require manual recovery")
			}
			for _, revision := range revisions {
				if revision.ID == competitors[0] {
					if err = e.captureCompetingRevision(ctx, j, revision); err != nil {
						return err
					}
					return nil
				}
			}
			return errors.New("concurrent remote revision is missing; upload recovery is pending")
		}
	}
	if err := e.commitJournal(j); err != nil {
		return err
	}
	if op.Local.Kind == "file" {
		if err := removeStage(e.profileDir, j.StagePath, j.StageIdentity); err != nil {
			return err
		}
	}
	if op.Conflict != nil {
		if err := e.forgetConflict(ctx, *op.Conflict); err != nil {
			return err
		}
	}
	return e.clearJournal()
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
	boundaryTime, err := time.Parse(time.RFC3339Nano, boundary)
	if err != nil {
		return nil, false
	}
	var ids []string
	for _, revision := range revisions {
		if revision.ID == preflight || revision.ID == head {
			continue
		}
		modified, parseErr := time.Parse(time.RFC3339Nano, revision.ModifiedTime)
		if parseErr != nil {
			return ids, false
		}
		if !modified.Before(boundaryTime) {
			ids = append(ids, revision.ID)
		}
	}
	return ids, true
}

func (e *executor) captureCompetingRevision(ctx context.Context, j Journal, revision drive.Revision) error {
	if revision.ID == "" || revision.ID == j.PreflightRevisionID || revision.ID == j.After[0].HeadRevisionID || !validHash(revision.MD5, 16) || !revision.HasSize || revision.Size < 0 {
		return errors.New("competing remote revision metadata is incomplete; upload recovery is pending")
	}
	if err := e.mutation.SetRevisionKeepForever(ctx, j.Operation.Entry.Remote.ID, revision.ID, true); err != nil {
		return errors.New("cannot preserve concurrent remote revision; upload recovery is pending")
	}
	path := ".grove-cloud-conflict-" + nonce()
	if path == ".grove-cloud-conflict-" {
		return errors.New("cannot create cloud conflict stage identity")
	}
	profileRoot, err := os.OpenRoot(e.profileDir)
	if err != nil {
		return errors.New("cannot open private conflict profile")
	}
	file, err := profileRoot.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
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
	j.CloudStagePath, j.CloudStageIdentity = path, &identity
	j.CloudRevisionID, j.CloudMD5, j.CloudSize = revision.ID, revision.MD5, revision.Size
	j.Phase = "capture-intent"
	if err = e.journal(j); err != nil {
		_ = file.Close()
		_ = profileRoot.Close()
		_ = removeStage(e.profileDir, path, &identity)
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
		return errors.New("cannot verify competing cloud revision; upload recovery is pending")
	}
	j.CloudSHA256, j.CloudMD5, j.CloudSize = cloudSHA, cloudMD5, count.count
	j.Phase = "conflict-captured"
	if err = e.journal(j); err != nil {
		return err
	}
	return e.finishCapturedConflict(ctx, j)
}

func (e *executor) finishCapturedConflict(ctx context.Context, j Journal) error {
	if j.Phase != "conflict-captured" || len(j.After) != 1 || j.CloudRevisionID == "" {
		return errors.New("invalid captured conflict journal")
	}
	if err := verifyPrivateStage(e.profileDir, j.CloudStagePath, j.CloudStageIdentity, j.CloudSHA256, j.CloudMD5, j.CloudSize); err != nil {
		return err
	}
	conflict := Conflict{
		Path: j.Operation.Entry.Path, Baseline: j.After[0],
		LocalSHA256: j.StageSHA256, LocalSize: j.StageSize,
		RemoteID: j.After[0].RemoteID, RemoteVersion: j.After[0].Version,
		RemoteMD5: j.After[0].MD5, RemoteSize: j.After[0].Size, RemoteRevisionID: j.After[0].HeadRevisionID,
		CloudRevisionID: j.CloudRevisionID, CloudMD5: j.CloudMD5, CloudSize: j.CloudSize,
		LocalStagePath: j.StagePath, LocalStageIdentity: j.StageIdentity,
		CloudStagePath: j.CloudStagePath, CloudStageIdentity: j.CloudStageIdentity, CloudSHA256: j.CloudSHA256,
	}
	conflict.ID = conflictID(conflict)
	next := *e.state
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
	if err := e.installCloudCandidate(ctx, *saved, operation.Entry.Remote); err != nil {
		return err
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
			selected := Operation{Kind: OpUpload, Entry: operation.Entry, Local: candidate, Before: []Completed{*baseline}}
			return e.upload(ctx, selected)
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

func (e *executor) forgetConflict(_ context.Context, conflict Conflict) error {
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
	}{{conflict.LocalStagePath, conflict.LocalStageIdentity}, {conflict.CloudStagePath, conflict.CloudStageIdentity}} {
		if stage.path != "" && stage.identity != nil {
			if err := removeStage(e.profileDir, stage.path, stage.identity); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *executor) installCloudCandidate(ctx context.Context, conflict Conflict, head drive.File) error {
	cloudSize := conflict.CloudSize
	if conflict.CloudRevisionID == "" {
		cloudSize = conflict.RemoteSize
	}
	if err := verifyPrivateStage(e.profileDir, conflict.CloudStagePath, conflict.CloudStageIdentity, conflict.CloudSHA256, conflict.CloudMD5, cloudSize); err != nil {
		return err
	}
	current, err := e.inspectFile(ctx, conflict.Path, Completed{Kind: "file", Size: conflict.LocalSize, SHA256: conflict.LocalSHA256})
	if err != nil {
		cloudPresent, cloudErr := e.inspectFile(ctx, conflict.Path, Completed{Kind: "file", Size: cloudSize, SHA256: conflict.CloudSHA256})
		if cloudErr != nil || !cloudPresent {
			return errors.New("local conflict candidate changed")
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
	source, err := profileRoot.Open(conflict.CloudStagePath)
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
	if copyErr != nil || syncErr != nil || closeTargetErr != nil || closeSourceErr != nil || closeRootErr != nil || hex.EncodeToString(sha.Sum(nil)) != conflict.CloudSHA256 {
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
	baselineSHA, baselineSize := conflict.CloudSHA256, cloudSize
	if conflict.CloudRevisionID != "" && conflict.CloudRevisionID != conflict.RemoteRevisionID {
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
