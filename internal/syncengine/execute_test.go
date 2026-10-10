package syncengine

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type downloadAPI struct {
	treeAPI
	data             []byte
	downloads        int
	corrupt, changed bool
	onDownload       func()
}

func downloader() *downloadAPI {
	data := []byte("expected bytes\n")
	sum := md5.Sum(data)
	f := file("file", "file.txt")
	f.Size = int64(len(data))
	f.MD5 = hex.EncodeToString(sum[:])
	a := tree()
	a.children = []drive.File{f}
	return &downloadAPI{treeAPI: a, data: data}
}
func (a *downloadAPI) Get(_ context.Context, id string) (drive.File, error) {
	if id == "root" {
		return a.root, nil
	}
	f := a.children[0]
	if a.changed && a.downloads > 0 {
		f.Version = "2"
	}
	return f, nil
}
func (a *downloadAPI) Download(ctx context.Context, _ string, w io.Writer) error {
	a.downloads++
	if a.onDownload != nil {
		a.onDownload()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	data := a.data
	if a.corrupt {
		data = []byte("corrupt")
	}
	_, err := w.Write(data)
	return err
}
func options(t *testing.T) (Options, string) {
	t.Helper()
	dir := t.TempDir()
	return Options{ProfileDir: filepath.Join(dir, "profile"), LocalDir: filepath.Join(dir, "local"), TokenFile: filepath.Join(dir, "token"), RemoteRoot: "root"}, filepath.Join(dir, "registry")
}
func testRun(ctx context.Context, opts Options, api drive.API, registry string) (Result, error) {
	return run(ctx, opts, registry, func(context.Context) (drive.API, func() error, error) { return api, func() error { return nil }, nil })
}
func TestDownloadVerified(t *testing.T) {
	opts, registry := options(t)
	api := downloader()
	result, err := testRun(context.Background(), opts, api, registry)
	if err != nil || result.Downloaded != 1 {
		t.Fatal("download failed", err)
	}
	data, err := os.ReadFile(filepath.Join(opts.LocalDir, "file.txt"))
	if err != nil || !bytes.Equal(data, api.data) {
		t.Fatal("wrong final bytes")
	}
}
func TestChecksumMismatch(t *testing.T) {
	opts, registry := options(t)
	api := downloader()
	api.corrupt = true
	if _, err := testRun(context.Background(), opts, api, registry); err == nil {
		t.Fatal("checksum failure accepted")
	}
	if _, err := os.Stat(filepath.Join(opts.LocalDir, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unverified file published")
	}
}
func TestRemoteChangedDuringDownload(t *testing.T) {
	opts, registry := options(t)
	api := downloader()
	api.changed = true
	if _, err := testRun(context.Background(), opts, api, registry); err == nil {
		t.Fatal("changed remote accepted")
	}
}
func TestCancellation(t *testing.T) {
	opts, registry := options(t)
	ctx, cancel := context.WithCancel(context.Background())
	api := downloader()
	api.onDownload = cancel
	if _, err := testRun(ctx, opts, api, registry); err == nil {
		t.Fatal("cancellation accepted")
	}
}
func TestNoWritesOnIncompletePreflight(t *testing.T) {
	opts, registry := options(t)
	api := downloader()
	api.incomplete = true
	if _, err := testRun(context.Background(), opts, api, registry); err == nil {
		t.Fatal("incomplete accepted")
	}
	if _, err := os.Stat(opts.LocalDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("destination mutated before scan completed")
	}
}
func TestNoReplaceRace(t *testing.T) {
	opts, registry := options(t)
	api := downloader()
	api.onDownload = func() {
		if err := os.WriteFile(filepath.Join(opts.LocalDir, "file.txt"), []byte("user data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := testRun(context.Background(), opts, api, registry); err == nil {
		t.Fatal("racing destination accepted")
	}
	data, err := os.ReadFile(filepath.Join(opts.LocalDir, "file.txt"))
	if err != nil || string(data) != "user data" {
		t.Fatal("user data overwritten")
	}
}
func TestSecondRunNoMedia(t *testing.T) {
	opts, registry := options(t)
	api := downloader()
	if _, err := testRun(context.Background(), opts, api, registry); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(opts.LocalDir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := testRun(context.Background(), opts, api, registry)
	if err != nil || api.downloads != 1 || result.Downloaded != 0 || result.Skipped != 1 {
		t.Fatal("second run transferred media", err)
	}
	after, err := os.Stat(filepath.Join(opts.LocalDir, "file.txt"))
	if err != nil || !after.ModTime().Equal(info.ModTime()) {
		t.Fatal("second run rewrote file")
	}
}
func TestRecoveryBoundaries(t *testing.T) {
	for failAt := 1; failAt <= 4; failAt++ {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			api := downloader()
			entry := Entry{Path: "file.txt", Remote: api.children[0]}
			state := State{Version: 1}
			saved := State{Version: 1}
			calls := 0
			save := func() error {
				calls++
				if calls == failAt {
					return os.ErrPermission
				}
				saved = cloneState(state)
				return nil
			}
			ex := executor{root: root, api: api, state: &state, save: save}
			if err := ex.execute(context.Background(), Operation{Kind: "download", Entry: entry}); err == nil {
				t.Fatal("fault ignored")
			}
			state = cloneState(saved)
			ex.state = &state
			ex.save = func() error { saved = cloneState(state); return nil }
			snapshot := Snapshot{Entries: []Entry{entry}}
			if err = ex.recover(context.Background(), snapshot); err != nil {
				t.Fatal("recovery failed", err)
			}
			local, err := scanLocal(context.Background(), root, nil)
			if err != nil {
				t.Fatal(err)
			}
			p, err := BuildPlan(snapshot, local, state)
			if err != nil {
				t.Fatal(err)
			}
			for _, op := range p.Operations {
				if err = ex.execute(context.Background(), op); err != nil {
					t.Fatal(err)
				}
			}
			data, err := root.ReadFile("file.txt")
			if err != nil || !bytes.Equal(data, api.data) {
				t.Fatal("recovery bytes wrong")
			}
		})
	}
}
func cloneState(s State) State {
	c := s
	c.Completed = append([]Completed(nil), s.Completed...)
	if s.Pending != nil {
		p := *s.Pending
		c.Pending = &p
	}
	return c
}
func TestDirectoryIntentRecovery(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	entry := Entry{Path: "folder", Remote: drive.File{ID: "folder", MIMEType: drive.FolderMIME}}
	s := State{Pending: &Pending{Entry: entry, Phase: "intent"}}
	if err = root.Mkdir("folder", 0700); err != nil {
		t.Fatal(err)
	}
	ex := executor{root: root, state: &s, save: func() error { return nil }}
	if err = ex.recover(context.Background(), Snapshot{Entries: []Entry{entry}}); err != nil || len(s.Completed) != 1 || s.Pending != nil {
		t.Fatal("directory recovery failed", err)
	}
}
