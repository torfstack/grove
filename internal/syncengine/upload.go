package syncengine

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type uploadStage struct {
	Path     string
	Identity FileIdentity
	SHA256   string
	MD5      string
	Size     int64
}

func (e *executor) prepareUpload(ctx context.Context, op Operation) error {
	if e.state == nil || e.state.Version != 3 || e.state.Transaction != nil || (op.Kind != OpUpload && op.Kind != OpCreate) {
		return errors.New("upload requires an idle version 3 profile")
	}
	journal := Journal{Operation: op, Phase: "staged"}
	if op.Kind == OpUpload {
		journal.PreflightRevisionID = op.Entry.Remote.HeadRevisionID
	}
	if op.Kind == OpCreate {
		journal.AllocatedID = op.Entry.Remote.ID
		journal.OperationMarker = op.OperationMarker
	}
	if op.Local.Kind == "file" {
		stage, err := e.stageUpload(ctx, op.Local)
		if err != nil {
			return err
		}
		journal.StagePath = stage.Path
		journal.StageIdentity = &stage.Identity
		journal.StageSHA256 = stage.SHA256
		journal.StageMD5 = stage.MD5
		journal.StageSize = stage.Size
	}
	return e.journal(journal)
}

func (e *executor) stageUpload(ctx context.Context, entry LocalEntry) (_ uploadStage, retErr error) {
	if entry.Kind != "file" || !safeRelative(entry.Path) || entry.Size < 0 || !validHash(entry.SHA256, 32) || e.profileDir == "" {
		return uploadStage{}, errors.New("invalid local upload stage")
	}
	if err := ctx.Err(); err != nil {
		return uploadStage{}, err
	}
	if err := rootPath(e.root, entry.Path); err != nil {
		return uploadStage{}, err
	}
	sourceInfo, err := e.root.Lstat(entry.Path)
	if err != nil || !sourceInfo.Mode().IsRegular() || sourceInfo.Size() != entry.Size {
		return uploadStage{}, errors.New("local upload source changed")
	}
	sourceIdentity, err := fileIdentity(sourceInfo)
	if err != nil {
		return uploadStage{}, err
	}
	source, err := e.root.Open(entry.Path)
	if err != nil {
		return uploadStage{}, errors.New("cannot open local upload source")
	}
	openedInfo, err := source.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() != entry.Size {
		_ = source.Close()
		return uploadStage{}, errors.New("local upload source changed")
	}
	openedIdentity, err := fileIdentity(openedInfo)
	if err != nil || openedIdentity != sourceIdentity {
		_ = source.Close()
		return uploadStage{}, errors.New("local upload source changed")
	}
	defer func() {
		if closeErr := source.Close(); retErr == nil && closeErr != nil {
			retErr = errors.New("cannot close local upload source")
		}
	}()
	profileRoot, err := os.OpenRoot(e.profileDir)
	if err != nil {
		return uploadStage{}, errors.New("cannot open private upload profile")
	}
	defer func() {
		if closeErr := profileRoot.Close(); retErr == nil && closeErr != nil {
			retErr = errors.New("cannot close private upload profile")
		}
	}()
	name := ".grove-upload-" + nonce()
	if len(name) == len(".grove-upload-") {
		return uploadStage{}, errors.New("cannot create upload stage identity")
	}
	stageFile, err := profileRoot.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return uploadStage{}, errors.New("cannot create exclusive upload stage")
	}
	info, err := stageFile.Stat()
	if err != nil {
		_ = stageFile.Close()
		return uploadStage{}, errors.New("cannot identify upload stage")
	}
	identity, err := fileIdentity(info)
	if err != nil {
		_ = stageFile.Close()
		return uploadStage{}, err
	}
	sha, md5sum := sha256.New(), md5.New()
	count, copyErr := io.Copy(io.MultiWriter(stageFile, sha, md5sum), &contextReader{ctx: ctx, reader: source})
	syncErr := stageFile.Sync()
	closeErr := stageFile.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || count != entry.Size || hex.EncodeToString(sha.Sum(nil)) != entry.SHA256 {
		if removeErr := removeOwnedStage(profileRoot, name, identity); removeErr != nil {
			return uploadStage{}, errors.Join(errors.New("local upload source changed while staging"), removeErr)
		}
		return uploadStage{}, errors.New("local upload source changed while staging")
	}
	if err = syncProfileRoot(profileRoot); err != nil {
		return uploadStage{}, err
	}
	afterInfo, err := e.root.Lstat(entry.Path)
	if err != nil || !afterInfo.Mode().IsRegular() || afterInfo.Size() != entry.Size {
		return uploadStage{}, errors.New("local upload source changed while staging")
	}
	afterIdentity, err := fileIdentity(afterInfo)
	if err != nil || afterIdentity != sourceIdentity {
		return uploadStage{}, errors.New("local upload source changed while staging")
	}
	if err = e.requireFile(ctx, entry.Path, Completed{Kind: "file", Size: entry.Size, SHA256: entry.SHA256}); err != nil {
		return uploadStage{}, errors.New("local upload source changed while staging")
	}
	return uploadStage{Path: name, Identity: identity, SHA256: entry.SHA256, MD5: hex.EncodeToString(md5sum.Sum(nil)), Size: count}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func removeOwnedStage(root *os.Root, path string, identity FileIdentity) error {
	info, err := root.Lstat(path)
	if err != nil {
		return errors.New("cannot inspect upload stage after failure")
	}
	actual, err := fileIdentity(info)
	if err != nil || actual != identity {
		return errors.New("upload stage ownership changed")
	}
	if err = root.Remove(path); err != nil {
		return errors.New("cannot remove owned upload stage")
	}
	return nil
}

func syncProfileRoot(root *os.Root) error {
	dir, err := root.Open(filepath.Clean("."))
	if err != nil {
		return errors.New("cannot open upload profile directory")
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil || closeErr != nil {
		return errors.New("cannot sync upload profile directory")
	}
	return nil
}
