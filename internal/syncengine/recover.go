package syncengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

func (e *executor) recover(ctx context.Context, snapshot Snapshot) error {
	if e.state.Transaction != nil {
		if e.state.Transaction.Operation.Kind == OpMove {
			return e.recoverMove(ctx)
		}
		if e.state.Transaction.Operation.Kind == OpReplace {
			return e.recoverReplace(ctx)
		}
		return errors.New("unsupported pending operation")
	}
	p := e.state.Pending
	if p == nil {
		return nil
	}
	var current *Entry
	for _, entry := range snapshot.Entries {
		if entry.Remote.ID == p.Entry.Remote.ID {
			copy := entry
			current = &copy
			break
		}
	}
	expected := Completed{Path: p.Entry.Path, RemoteID: p.Entry.Remote.ID, Version: p.Entry.Remote.Version, MD5: p.Entry.Remote.MD5, Kind: entryKind(p.Entry), Size: p.Entry.Remote.Size}
	if current == nil || !same(*current, expected) {
		return errors.New("pending remote entry changed; initial sync cannot resume")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rootPath(e.root, p.Entry.Path); err != nil {
		return err
	}
	info, err := e.root.Lstat(p.Entry.Path)
	if err == nil {
		if entryKind(p.Entry) == "folder" {
			if !info.IsDir() {
				return errors.New("pending directory conflicts with local entry")
			}
			if err = e.parentSync(p.Entry.Path); err != nil {
				return err
			}
			return e.finish(p.Entry, "")
		}
		if p.Phase != "verified" || p.VerifiedSHA256 == "" || !info.Mode().IsRegular() || info.Size() != p.Entry.Remote.Size {
			return errors.New("pending final file is unverified or conflicting")
		}
		f, err := e.root.Open(p.Entry.Path)
		if err != nil {
			return errors.New("cannot inspect pending file")
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, f)
		closeErr := f.Close()
		if readErr != nil || closeErr != nil || hex.EncodeToString(hash.Sum(nil)) != p.VerifiedSHA256 {
			return errors.New("pending final file content mismatch")
		}
		if err = e.removeTemp(ctx, p.TempPath, p.TempIdentity, p.VerifiedSHA256, p.Entry.Remote.Size, e.state.Version == 1 && p.Phase != "intent"); err != nil {
			return err
		}
		if err = e.parentSync(p.Entry.Path); err != nil {
			return err
		}
		return e.finish(p.Entry, p.VerifiedSHA256)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot inspect pending destination")
	}
	if err = e.removeTemp(ctx, p.TempPath, p.TempIdentity, p.VerifiedSHA256, p.Entry.Remote.Size, e.state.Version == 1 && p.Phase != "intent"); err != nil {
		return err
	}
	e.state.Pending = nil
	return e.save()
}
func (e *executor) removeTemp(ctx context.Context, path string, identity *FileIdentity, hash string, size int64, legacy bool) error {
	if path == "" {
		return nil
	}
	if err := rootPath(e.root, path); err != nil {
		return err
	}
	info, err := e.root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("temporary file changed type or cannot be inspected")
	}
	if identity != nil {
		actual, err := fileIdentity(info)
		if err != nil || actual != *identity {
			return errors.New("temporary file ownership changed")
		}
	} else if !legacy {
		return errors.New("temporary file ownership is unconfirmed")
	}
	if hash != "" {
		return e.removeVerified(ctx, path, Completed{Kind: "file", Size: size, SHA256: hash})
	}
	if err := e.root.Remove(path); err != nil {
		return errors.New("cannot remove owned temporary file")
	}
	return e.parentSync(path)
}
