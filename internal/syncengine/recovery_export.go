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

type RecoveryExportOptions struct {
	Candidate ConflictSide
	Output    string
}
type RecoveryExportError struct {
	Published bool
	Cause     error
}

func (e *RecoveryExportError) Error() string {
	if e.Published {
		return "export failed; output may already exist"
	}
	return "recovery export failed"
}
func (e *RecoveryExportError) Unwrap() error { return e.Cause }

type recoveryExportIO struct {
	copy       func(io.Writer, io.Reader) (int64, error)
	syncFile   func(*os.File) error
	close      func(*os.File) error
	link       func(*os.Root, string, *os.Root, string) (bool, error)
	syncParent func(*os.Root) error
}

func defaultRecoveryExportIO() recoveryExportIO {
	return recoveryExportIO{
		copy:     func(w io.Writer, r io.Reader) (int64, error) { return io.CopyBuffer(w, r, make([]byte, 32*1024)) },
		syncFile: func(f *os.File) error { return f.Sync() }, close: func(f *os.File) error { return f.Close() },
		link: linkExportNoReplace, syncParent: syncProfileRoot,
	}
}

func (s Service) ExportRecovery(ctx context.Context, opts Options, export RecoveryExportOptions) (err error) {
	published := false
	defer func() {
		if err != nil {
			err = &RecoveryExportError{Published: published, Cause: err}
		}
	}()
	if err = ctx.Err(); err != nil {
		return err
	}
	if export.Candidate != SideLocal && export.Candidate != SideCloud {
		return errors.New("invalid export candidate")
	}
	canonical, state, lock, err := s.recoveryState(opts)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	if state.Transaction == nil {
		return errors.New("no pending transaction to export")
	}
	registry := s.RegistryDir
	if registry == "" {
		registry, err = registryPath()
		if err != nil {
			return err
		}
	}
	d, err := acquireExportDestination(registry, canonical.ProfileDir, canonical.LocalDir, export.Output)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.Close()) }()
	published, err = exportRecoveryStage(ctx, canonical.ProfileDir, *state.Transaction, export.Candidate, d, defaultRecoveryExportIO())
	return err
}

func exportRecoveryStage(ctx context.Context, profile string, j Journal, candidate ConflictSide, d *exportDestination, ops recoveryExportIO) (published bool, err error) {
	if j.Operation.Kind != OpUpload && j.Operation.Kind != OpCreate {
		return false, errors.New("unsupported export transaction")
	}
	if !exportPublicationSupported() {
		return false, errors.New("safe export publication is supported on Linux and macOS only")
	}
	path, identity, shaExpected, md5Expected, size := j.StagePath, j.StageIdentity, j.StageSHA256, j.StageMD5, j.StageSize
	if candidate == SideCloud {
		if j.Phase != "conflict-captured" && j.Phase != "committed" {
			return false, errors.New("cloud capture is incomplete")
		}
		path, identity, shaExpected, md5Expected, size = j.CloudStagePath, j.CloudStageIdentity, j.CloudSHA256, j.CloudMD5, j.CloudSize
	}
	if path == "" || identity == nil || !safeRelative(path) || !validHash(shaExpected, 32) || !validHash(md5Expected, 16) || size < 0 {
		return false, errors.New("candidate is not verified")
	}
	sourceRoot, err := os.OpenRoot(profile)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, sourceRoot.Close()) }()
	if err = checkExportSource(sourceRoot, path, *identity, size); err != nil {
		return false, err
	}
	source, err := sourceRoot.Open(path)
	if err != nil {
		return false, err
	}
	sourceOpen := true
	defer func() {
		if sourceOpen {
			err = errors.Join(err, ops.close(source))
		}
	}()
	if err = checkExportDescriptor(source, *identity, size); err != nil {
		return false, err
	}
	tempDir := ".grove-export-" + nonce()
	if tempDir == ".grove-export-" {
		return false, errors.New("cannot create export identity")
	}
	if err = d.root.Mkdir(tempDir, 0700); err != nil {
		return false, err
	}
	dirInfo, err := d.root.Lstat(tempDir)
	if err != nil || !dirInfo.IsDir() {
		return false, errors.New("cannot identify export temporary directory")
	}
	dirIdentity, err := fileIdentity(dirInfo)
	if err != nil {
		return false, err
	}
	tempDirPresent := true
	defer func() {
		if tempDirPresent {
			err = errors.Join(err, removeOwnedExportDir(d.root, tempDir, dirIdentity))
		}
	}()
	tempRoot, err := d.root.OpenRoot(tempDir)
	if err != nil {
		return false, err
	}
	tempRootOpen := true
	var target *os.File
	targetOpen, tempPresent := false, false
	var tempIdentity FileIdentity
	defer func() {
		if targetOpen {
			err = errors.Join(err, ops.close(target))
		}
		if tempPresent {
			err = errors.Join(err, removeOwnedStage(tempRoot, "stage", tempIdentity))
		}
		if tempRootOpen {
			err = errors.Join(err, tempRoot.Close())
		}
	}()
	target, err = tempRoot.OpenFile("stage", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false, err
	}
	targetOpen, tempPresent = true, true
	info, err := target.Stat()
	if err != nil {
		return false, err
	}
	tempIdentity, err = fileIdentity(info)
	if err != nil {
		return false, err
	}
	sha, md5sum := sha256.New(), md5.New()
	count, err := ops.copy(io.MultiWriter(target, sha, md5sum), &contextReader{ctx: ctx, reader: source})
	if err != nil {
		return false, err
	}
	if count != size || hex.EncodeToString(sha.Sum(nil)) != shaExpected || hex.EncodeToString(md5sum.Sum(nil)) != md5Expected {
		return false, errors.New("candidate content changed")
	}
	if err = checkExportDescriptor(source, *identity, size); err != nil {
		return false, err
	}
	if err = checkExportSource(sourceRoot, path, *identity, size); err != nil {
		return false, err
	}
	if err = ops.close(source); err != nil {
		sourceOpen = false
		return false, err
	}
	sourceOpen = false
	if err = ops.syncFile(target); err != nil {
		return false, err
	}
	if err = ops.close(target); err != nil {
		targetOpen = false
		return false, err
	}
	targetOpen = false
	if err = checkExportSource(tempRoot, "stage", tempIdentity, size); err != nil {
		return false, err
	}
	if err = d.checkParent(); err != nil {
		return false, err
	}
	if err = ctx.Err(); err != nil {
		return false, err
	}
	linked, linkErr := ops.link(tempRoot, "stage", d.root, d.target)
	if linked {
		published = true
	}
	if linkErr != nil {
		return published, linkErr
	}
	if !linked {
		return false, errors.New("export publication did not occur")
	}
	if err = removeOwnedStage(tempRoot, "stage", tempIdentity); err != nil {
		return true, err
	}
	tempPresent = false
	if err = tempRoot.Close(); err != nil {
		tempRootOpen = false
		return true, err
	}
	tempRootOpen = false
	if err = removeOwnedExportDir(d.root, tempDir, dirIdentity); err != nil {
		return true, err
	}
	tempDirPresent = false
	if err = ops.syncParent(d.root); err != nil {
		return true, err
	}
	return true, nil
}

func removeOwnedExportDir(root *os.Root, path string, identity FileIdentity) error {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	actual, err := fileIdentity(info)
	if err != nil || !info.IsDir() || actual != identity {
		return errors.New("export temporary directory ownership changed")
	}
	return root.Remove(path)
}

func checkExportSource(root *os.Root, path string, identity FileIdentity, size int64) error {
	if err := rootPath(root, path); err != nil {
		return err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return errors.New("candidate is unavailable")
	}
	id, err := fileIdentity(info)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size || id != identity {
		return errors.New("candidate ownership changed")
	}
	return nil
}
func checkExportDescriptor(f *os.File, identity FileIdentity, size int64) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	id, err := fileIdentity(info)
	if err != nil || !info.Mode().IsRegular() || info.Size() != size || id != identity {
		return errors.New("candidate ownership changed")
	}
	return nil
}
