package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func interruptedDownload(t *testing.T, phase string) (*executor, Entry, string, *downloadAPI) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	api := downloader()
	api.children[0] = completeDownloadFile(api.children[0])
	api.children[0].Version = "9"
	entry := Entry{Path: "file.txt", Remote: api.children[0]}
	api.onDownload = func() { api.children[0].Version = "10" }
	state := State{Version: 2}
	profile := filepath.Join(dir, "profile")
	ex := &executor{root: root, api: api, state: &state}
	save := func() error { return saveState(profile, *ex.state) }
	ex.save = func() error {
		p := ex.state.Pending
		if phase == "intent" && p != nil && p.Phase == "downloading" {
			return os.ErrPermission
		}
		if phase == "downloading" && p != nil && p.Phase == "verified" {
			return os.ErrPermission
		}
		if phase == "published" && p == nil {
			return os.ErrPermission
		}
		if err := save(); err != nil {
			return err
		}
		if phase == "verified" && p != nil && p.Phase == "verified" {
			return os.ErrPermission
		}
		return nil
	}
	if err := ex.execute(context.Background(), Operation{Kind: OpDownload, Entry: entry}); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("interruption not injected: %v", err)
	}
	loaded, err := loadState(profile, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	ex.state = &loaded
	ex.save = save
	api.children[0].Version = "10"
	return ex, entry, profile, api
}

func TestInitialDownloadRecoveryMetadataVersion(t *testing.T) {
	for _, phase := range []string{"intent", "downloading", "verified", "published"} {
		t.Run(phase, func(t *testing.T) {
			ex, entry, profile, api := interruptedDownload(t, phase)
			if ex.state.Pending.Entry.Remote.Version != "9" {
				t.Fatal("saved snapshot changed")
			}
			entry.Remote = api.children[0]
			snapshot := Snapshot{Entries: []Entry{entry}}
			before := api.downloads
			if err := ex.recover(context.Background(), snapshot); err != nil {
				t.Fatalf("metadata-only recovery rejected: %v", err)
			}
			if api.downloads != before {
				t.Fatal("recovery started an unplanned transfer")
			}
			if phase == "published" {
				if len(ex.state.Completed) != 1 || ex.state.Completed[0].Version != "9" {
					t.Fatal("verified snapshot not adopted")
				}
			} else {
				if ex.state.Pending != nil || len(ex.state.Completed) != 0 {
					t.Fatal("unpublished intent was not retired")
				}
				api.onDownload = nil
				if err := ex.execute(context.Background(), Operation{Kind: OpDownload, Entry: entry}); err != nil {
					t.Fatal(err)
				}
			}
			loaded, err := loadState(profile, Binding{})
			if err != nil || loaded.Pending != nil || len(loaded.Completed) != 1 {
				t.Fatalf("invalid completed state: %v", err)
			}
			data, err := ex.root.ReadFile("file.txt")
			if err != nil || string(data) != "expected bytes\n" {
				t.Fatalf("wrong recovered bytes: %v", err)
			}
		})
	}
}

func TestInitialDownloadRecoveryRejectsChangedEvidence(t *testing.T) {
	for _, change := range []string{"revision", "time", "content", "parent", "ancestor", "missing", "missing-evidence", "corrupt-final", "replaced-temp"} {
		t.Run(change, func(t *testing.T) {
			phase := "published"
			if change == "replaced-temp" {
				phase = "downloading"
			}
			ex, entry, _, api := interruptedDownload(t, phase)
			entry.Remote = api.children[0]
			snapshot := Snapshot{Entries: []Entry{entry}}
			switch change {
			case "revision":
				snapshot.Entries[0].Remote.HeadRevisionID = "other"
			case "time":
				snapshot.Entries[0].Remote.ModifiedTime = "2026-10-22T11:00:00Z"
			case "content":
				snapshot.Entries[0].Remote.MD5 = "00000000000000000000000000000000"
			case "parent":
				snapshot.Entries[0].Remote.Parents = []string{"other"}
			case "ancestor":
				snapshot.Entries[0].Path = "moved/file.txt"
			case "missing":
				snapshot.Entries = nil
			case "missing-evidence":
				snapshot.Entries[0].Remote.HeadRevisionID = ""
			case "corrupt-final":
				if err := ex.root.WriteFile("file.txt", []byte(strings.Repeat("x", len(api.data))), 0600); err != nil {
					t.Fatal(err)
				}
			case "replaced-temp":
				path := ex.state.Pending.TempPath
				if err := ex.root.Rename(path, path+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := ex.root.WriteFile(path, api.data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := ex.recover(context.Background(), snapshot); err == nil {
				t.Fatal("changed evidence accepted")
			}
			if ex.state.Pending == nil || len(ex.state.Completed) != 0 {
				t.Fatal("recovery evidence discarded")
			}
			if phase == "published" {
				if _, err := ex.root.Stat("file.txt"); err != nil {
					t.Fatal("final file removed")
				}
			} else if _, err := ex.root.Stat(ex.state.Pending.TempPath); err != nil {
				t.Fatal("unowned artifact removed")
			}
		})
	}
}

func TestInitialDownloadRecoveryLegacyEvidence(t *testing.T) {
	for _, advance := range []bool{false, true} {
		ex, _, _, _ := interruptedDownload(t, "published")
		ex.state.Pending.Entry.Remote.HeadRevisionID = ""
		ex.state.Pending.Entry.Remote.ModifiedTime = ""
		entry := ex.state.Pending.Entry
		if advance {
			entry.Remote.Version = "10"
		}
		if err := ex.recover(context.Background(), Snapshot{Entries: []Entry{entry}}); (err == nil) == advance {
			t.Fatalf("legacy fallback advance=%t: %v", advance, err)
		}
	}
}

func TestInitialDownloadRecoverySaveFailure(t *testing.T) {
	ex, entry, profile, api := interruptedDownload(t, "published")
	entry.Remote = api.children[0]
	entry.Remote.Version = "11"
	ex.save = func() error { return os.ErrPermission }
	if err := ex.recover(context.Background(), Snapshot{Entries: []Entry{entry}}); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("save failure lost: %v", err)
	}
	loaded, err := loadState(profile, Binding{})
	if err != nil || loaded.Pending == nil || loaded.Pending.Entry.Remote.Version != "9" {
		t.Fatalf("durable snapshot lost: %v", err)
	}
	ex.state = &loaded
	ex.save = func() error { return saveState(profile, *ex.state) }
	entry.Remote.Version = "12"
	if err := ex.recover(context.Background(), Snapshot{Entries: []Entry{entry}}); err != nil {
		t.Fatal(err)
	}
	if ex.state.Pending != nil || len(ex.state.Completed) != 1 || ex.state.Completed[0].Version != "9" {
		t.Fatal("repeated recovery changed original snapshot")
	}
}
