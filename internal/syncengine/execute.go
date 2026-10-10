package syncengine

import (
	"context"

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
func (e *executor) execute(ctx context.Context, op Operation) error {
	if op.Kind == OpRecord {
		if len(op.Before) != 1 {
			return errors.New("invalid metadata update baseline")
		}
		if err := e.requireFile(ctx, op.Entry.Path, op.Before[0]); err != nil {
			return err
		}
		next := *e.state
		if err := applyBaseline(&next, op.Before, []Completed{completedEntry(op.Entry, op.Before[0].SHA256)}); err != nil {
			return err
		}
		return e.persist(next)
	}
	if op.Kind == OpMove {
		return e.move(ctx, op)
	}
	if op.Kind == OpReplace {
		return e.replace(ctx, op)
	}
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
	p.Phase = "downloading"
	if err := e.save(); err != nil {
		return err
	}
	hash, err := e.stageDownload(ctx, entry, p.TempPath)
	if err != nil {
		return err
	}
	p.VerifiedSHA256 = hash
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
