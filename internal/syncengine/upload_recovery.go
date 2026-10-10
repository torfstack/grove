package syncengine

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
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
	if journal.StagePath != "" {
		if err := verifyPrivateStage(e.profileDir, journal.StagePath, journal.StageIdentity, journal.StageSHA256, journal.StageMD5, journal.StageSize); err != nil {
			return err
		}
	}
	if journal.CloudStagePath != "" {
		if err := verifyPrivateStage(e.profileDir, journal.CloudStagePath, journal.CloudStageIdentity, journal.CloudSHA256, "", -1); err != nil {
			return err
		}
	}
	return errUploadPending
}

func verifyPrivateStage(profileDir, path string, identity *FileIdentity, shaExpected, md5Expected string, sizeExpected int64) error {
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
	sha, md5sum := sha256.New(), md5.New()
	_, readErr := io.Copy(io.MultiWriter(sha, md5sum), f)
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || (shaExpected != "" && hex.EncodeToString(sha.Sum(nil)) != shaExpected) || (md5Expected != "" && hex.EncodeToString(md5sum.Sum(nil)) != md5Expected) {
		return errors.New("private upload stage content changed")
	}
	return nil
}
