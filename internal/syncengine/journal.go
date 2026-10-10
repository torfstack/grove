package syncengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

func (e *executor) persist(next State) error {
	previous := *e.state
	*e.state = next
	if err := e.save(); err != nil {
		*e.state = previous
		return err
	}
	return nil
}
func (e *executor) journal(j Journal) error {
	next := *e.state
	next.Transaction = &j
	return e.persist(next)
}
func (e *executor) clearJournal() error {
	next := *e.state
	next.Transaction = nil
	return e.persist(next)
}
func (e *executor) commitJournal(j Journal) error {
	next := *e.state
	if err := applyBaseline(&next, j.Operation.Before, j.After); err != nil {
		return err
	}
	j.Phase = "committed"
	next.Transaction = &j
	return e.persist(next)
}
func (e *executor) inspectFile(ctx context.Context, path string, expected Completed) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := rootPath(e.root, path); err != nil {
		return false, err
	}
	info, err := e.root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() != expected.Size {
		return true, errors.New("tracked file content or type changed")
	}
	f, err := e.root.Open(path)
	if err != nil {
		return true, errors.New("cannot open tracked file")
	}
	h := sha256.New()
	_, readErr := io.Copy(h, f)
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || hex.EncodeToString(h.Sum(nil)) != expected.SHA256 {
		return true, errors.New("tracked file content changed")
	}
	return true, nil
}
func (e *executor) requireFile(ctx context.Context, path string, expected Completed) error {
	exists, err := e.inspectFile(ctx, path, expected)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("tracked file is missing")
	}
	return nil
}
func (e *executor) removeVerified(ctx context.Context, path string, expected Completed) error {
	if path == "" {
		return nil
	}
	exists, err := e.inspectFile(ctx, path, expected)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if err = e.root.Remove(path); err != nil {
		return errors.New("cannot remove verified journal artifact")
	}
	return e.parentSync(path)
}
func (e *executor) currentRemote(ctx context.Context, entry Entry) error {
	current, err := e.api.Get(ctx, entry.Remote.ID)
	if err != nil {
		return err
	}
	if current.Trashed || !current.OwnedByMe || current.Name != entry.Remote.Name || len(current.Parents) != 1 || len(entry.Remote.Parents) != 1 || current.Parents[0] != entry.Remote.Parents[0] || current.Version != entry.Remote.Version || current.MD5 != entry.Remote.MD5 || current.Size != entry.Remote.Size {
		return errors.New("pending remote entry changed")
	}
	return nil
}
