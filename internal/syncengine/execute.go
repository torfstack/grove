package syncengine

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"io"
	"os"
	"path/filepath"
)

type executor struct {
	root  *os.Root
	api   drive.API
	state *State
	save  func() error
}

func (e *executor) parentSync(path string) error {
	dir, err := e.root.Open(filepath.Dir(path))
	if err != nil {
		return errors.New("cannot open destination parent")
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil || closeErr != nil {
		return errors.New("cannot sync destination parent")
	}
	return nil
}
func (e *executor) finish(entry Entry, hash string) error {
	e.state.Completed = append(e.state.Completed, Completed{Path: entry.Path, RemoteID: entry.Remote.ID, Version: entry.Remote.Version, MD5: entry.Remote.MD5, SHA256: hash, Kind: entryKind(entry), Size: entry.Remote.Size})
	e.state.Pending = nil
	return e.save()
}
func (e *executor) recover(ctx context.Context, snapshot Snapshot) error {
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
func (e *executor) execute(ctx context.Context, op Operation) error {
	if op.Kind == "skip" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	entry := op.Entry
	if err := rootPath(e.root, entry.Path); err != nil {
		return err
	}
	if _, err := e.root.Lstat(entry.Path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("destination already exists or cannot be inspected")
	}
	p := &Pending{Entry: entry, Phase: "intent"}
	if op.Kind == "download" {
		id := nonce()
		if id == "" {
			return errors.New("cannot create transfer identity")
		}
		p.TempPath = filepath.Join(filepath.Dir(entry.Path), ".grove-download-"+id)
	}
	e.state.Pending = p
	if err := e.save(); err != nil {
		return err
	}
	if op.Kind == "mkdir" {
		if err := e.root.Mkdir(entry.Path, 0700); err != nil {
			return errors.New("cannot create destination directory")
		}
		if err := e.parentSync(entry.Path); err != nil {
			return err
		}
		return e.finish(entry, "")
	}
	if op.Kind != "download" {
		return errors.New("unknown sync operation")
	}
	f, err := e.root.OpenFile(p.TempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("cannot create exclusive download file")
	}
	defer func() { _ = f.Close() }()
	p.Phase = "downloading"
	if err = e.save(); err != nil {
		return err
	}
	md5hash := md5.New()
	sha := sha256.New()
	counter := &countWriter{writer: io.MultiWriter(f, md5hash, sha)}
	if err = e.api.Download(ctx, entry.Remote.ID, counter); err != nil {
		return err
	}
	if counter.count != entry.Remote.Size || hex.EncodeToString(md5hash.Sum(nil)) != entry.Remote.MD5 {
		return errors.New("download checksum or size mismatch")
	}
	fresh, err := e.api.Get(ctx, entry.Remote.ID)
	if err != nil {
		return err
	}
	if fresh.ID != entry.Remote.ID || fresh.Version != entry.Remote.Version || fresh.MD5 != entry.Remote.MD5 || fresh.Size != entry.Remote.Size || fresh.Trashed || !fresh.OwnedByMe {
		return errors.New("remote file changed during download")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return errors.New("cannot sync downloaded file")
	}
	if err = f.Close(); err != nil {
		return errors.New("cannot close downloaded file")
	}
	p.VerifiedSHA256 = hex.EncodeToString(sha.Sum(nil))
	p.Phase = "verified"
	if err = e.save(); err != nil {
		return err
	}
	if err = rootPath(e.root, entry.Path); err != nil {
		return err
	}
	if err = publish(e.root, p.TempPath, entry.Path); err != nil {
		return err
	}
	if err = e.parentSync(entry.Path); err != nil {
		return err
	}
	return e.finish(entry, p.VerifiedSHA256)
}

type countWriter struct {
	writer io.Writer
	count  int64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.count += int64(n)
	return n, err
}
