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
		if err = e.removeTemp(p.TempPath); err != nil {
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
	if err = e.removeTemp(p.TempPath); err != nil {
		return err
	}
	e.state.Pending = nil
	return e.save()
}
func (e *executor) removeTemp(path string) error {
	if path == "" {
		return nil
	}
	if err := rootPath(e.root, path); err != nil {
		return err
	}
	if info, err := e.root.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("owned temporary path changed type")
	}
	if err := e.root.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot remove owned temporary file")
	}
	return nil
}
