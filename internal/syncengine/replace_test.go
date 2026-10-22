package syncengine

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func replacement(t *testing.T) (*executor, Operation, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	old := []byte("old bytes")
	if err = root.WriteFile("file.txt", old, 0600); err != nil {
		t.Fatal(err)
	}
	c := baselineFile("file.txt", "file", old)
	sum := md5.Sum(old)
	c.MD5 = hex.EncodeToString(sum[:])
	api := downloader()
	api.children[0].Version = "2"
	s := &State{Version: 2, Completed: []Completed{c}, PopulationComplete: true}
	profile := filepath.Join(dir, "profile")
	ex := &executor{root: root, api: api, state: s, save: func() error { return saveState(profile, *s) }}
	if err = ex.save(); err != nil {
		t.Fatal(err)
	}
	return ex, Operation{Kind: OpReplace, Entry: Entry{Path: "file.txt", Remote: api.children[0]}, Before: []Completed{c}}, profile
}

func interruptedMetadataReplacement(t *testing.T, backedUp bool) (*executor, string, *downloadAPI) {
	t.Helper()
	ex, op, profile := replacement(t)
	api := ex.api.(*downloadAPI)
	op.Entry.Remote = completeDownloadFile(op.Entry.Remote)
	op.Entry.Remote.Version = "9"
	api.children[0] = op.Entry.Remote
	api.onDownload = func() { api.children[0].Version = "10" }
	save := ex.save
	ex.save = func() error {
		if err := save(); err != nil {
			return err
		}
		if ex.state.Transaction != nil && ex.state.Transaction.Phase == "verified" {
			return os.ErrPermission
		}
		return nil
	}
	if err := ex.execute(context.Background(), op); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("stage interruption missing: %v", err)
	}
	ex.save = save
	staged, err := loadState(profile, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	*ex.state = staged
	if backedUp {
		j := *ex.state.Transaction
		if err := ex.root.Rename("file.txt", j.BackupPath); err != nil {
			t.Fatal(err)
		}
		j.Phase = "backed-up"
		if err := ex.journal(j); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := loadState(profile, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	*ex.state = loaded
	api.children[0].Version = "11"
	return ex, profile, api
}

func TestReplaceRecoveryMetadataVersion(t *testing.T) {
	for _, backedUp := range []bool{false, true} {
		ex, profile, _ := interruptedMetadataReplacement(t, backedUp)
		if err := ex.recover(context.Background(), Snapshot{}); err != nil {
			t.Fatalf("metadata-only replacement recovery failed: %v", err)
		}
		data, err := ex.root.ReadFile("file.txt")
		if err != nil || string(data) != "expected bytes\n" {
			t.Fatalf("new bytes missing: %v", err)
		}
		loaded, err := loadState(profile, Binding{})
		if err != nil || loaded.Transaction != nil || loaded.Completed[0].Version != "9" {
			t.Fatalf("planned snapshot changed or journal retained: %v", err)
		}
	}
}

func TestReplaceRecoveryMetadataPreservesEvidence(t *testing.T) {
	for _, change := range []string{"revision", "time", "parent", "missing-evidence", "get-error", "local-edit", "stage", "backup"} {
		for _, backedUp := range []bool{false, true} {
			t.Run(change+map[bool]string{false: "-verified", true: "-backed-up"}[backedUp], func(t *testing.T) {
				ex, _, api := interruptedMetadataReplacement(t, backedUp)
				j := *ex.state.Transaction
				switch change {
				case "revision":
					api.children[0].HeadRevisionID = "other-revision"
				case "time":
					api.children[0].ModifiedTime = "2026-10-22T11:00:00Z"
				case "parent":
					api.children[0].Parents = []string{"other"}
				case "missing-evidence":
					api.children[0].HeadRevisionID = ""
				case "get-error":
					ex.api = postDownloadErrorAPI{api}
				case "local-edit":
					if err := ex.root.WriteFile("file.txt", []byte("local edit"), 0600); err != nil {
						t.Fatal(err)
					}
				case "stage":
					if err := ex.root.WriteFile(j.TempPath, []byte("changed stage"), 0600); err != nil {
						t.Fatal(err)
					}
				case "backup":
					if !backedUp {
						return
					}
					if err := ex.root.WriteFile(j.BackupPath, []byte("changed backup"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := ex.recoverReplace(context.Background()); err == nil {
					t.Fatal("changed evidence accepted")
				}
				if backedUp {
					if ex.state.Transaction == nil {
						t.Fatal("backup recovery evidence discarded")
					}
					if _, err := ex.root.Stat(j.BackupPath); err != nil {
						t.Fatal("backup destroyed")
					}
				} else {
					want := "old bytes"
					if change == "local-edit" {
						want = "local edit"
					}
					data, err := ex.root.ReadFile("file.txt")
					if err != nil || string(data) != want {
						t.Fatalf("old/local bytes lost: %v", err)
					}
				}
			})
		}
	}
}

func TestReplaceRecoveryMetadataSaveFailures(t *testing.T) {
	for _, stop := range []string{"verified", "backed-up", "committed", "retired"} {
		t.Run(stop, func(t *testing.T) {
			ex, op, profile := replacement(t)
			api := ex.api.(*downloadAPI)
			op.Entry.Remote = completeDownloadFile(op.Entry.Remote)
			op.Entry.Remote.Version = "9"
			api.children[0] = op.Entry.Remote
			api.onDownload = func() { api.children[0].Version = "10" }
			save := ex.save
			ex.save = func() error {
				j := ex.state.Transaction
				if j != nil && j.Phase == stop || j == nil && stop == "retired" {
					return os.ErrPermission
				}
				return save()
			}
			if err := ex.execute(context.Background(), op); !errors.Is(err, os.ErrPermission) {
				t.Fatalf("save fault not reached: %v", err)
			}
			loaded, err := loadState(profile, Binding{})
			if err != nil {
				t.Fatal(err)
			}
			*ex.state = loaded
			ex.save = save
			api.children[0].Version = "11"
			if err := ex.recover(context.Background(), Snapshot{}); err != nil {
				t.Fatal(err)
			}
			if ex.state.Completed[0].Version == "1" {
				op.Entry.Remote = api.children[0]
				api.onDownload = nil
				if err := ex.execute(context.Background(), op); err != nil {
					t.Fatal(err)
				}
			}
			data, err := ex.root.ReadFile("file.txt")
			if err != nil || string(data) != "expected bytes\n" || ex.state.Transaction != nil {
				t.Fatalf("save-fault recovery incomplete: %v", err)
			}
		})
	}
}

func TestDownloadMetadataDoesNotRelaxOtherOperations(t *testing.T) {
	api := downloader()
	entry := Entry{Path: "file.txt", Remote: api.children[0]}
	api.children[0].Version = "2"
	ex := executor{api: api}
	if err := ex.currentRemote(context.Background(), entry); !errors.Is(err, errPendingRemoteChanged) {
		t.Fatalf("strict precondition relaxed: %v", err)
	}
}

func TestReplaceMetadataUnownedTempPreserved(t *testing.T) {
	ex, _, _ := interruptedMetadataReplacement(t, false)
	j := *ex.state.Transaction
	j.Phase = "intent"
	j.After = nil
	j.VerifiedSHA256 = ""
	j.TempIdentity = nil
	if err := ex.journal(j); err != nil {
		t.Fatal(err)
	}
	if err := ex.recoverReplace(context.Background()); err == nil {
		t.Fatal("unowned temporary file accepted")
	}
	if data, err := ex.root.ReadFile(j.TempPath); err != nil || string(data) != "expected bytes\n" {
		t.Fatalf("unowned file removed: %v", err)
	}
}
func TestReplaceVerified(t *testing.T) {
	ex, op, _ := replacement(t)
	if err := ex.execute(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	data, err := ex.root.ReadFile("file.txt")
	if err != nil || string(data) != "expected bytes\n" {
		t.Fatal("wrong new content", err)
	}
	if ex.state.Transaction != nil || len(ex.state.Completed) != 1 || ex.state.Completed[0].Version != "2" {
		t.Fatal("baseline not committed")
	}
	entries, err := os.ReadDir(ex.root.Name())
	if err != nil || len(entries) != 2 {
		t.Fatal("artifacts retained", err)
	}
}
func TestReplacePreservesLocalEdit(t *testing.T) {
	ex, op, _ := replacement(t)
	if err := ex.root.WriteFile("file.txt", []byte("local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ex.execute(context.Background(), op); err == nil {
		t.Fatal("edit replaced")
	}
	data, _ := ex.root.ReadFile("file.txt")
	if string(data) != "local edit" {
		t.Fatal("edit lost")
	}
}
func TestReplaceRecoveryMatrix(t *testing.T) {
	for fail := 1; fail <= 5; fail++ {
		t.Run(string(rune('0'+fail)), func(t *testing.T) {
			ex, op, profile := replacement(t)
			save := ex.save
			calls := 0
			ex.save = func() error {
				calls++
				if calls == fail {
					return os.ErrPermission
				}
				return save()
			}
			if err := ex.execute(context.Background(), op); err == nil {
				t.Fatal("fault ignored")
			}
			s, err := loadState(profile, Binding{})
			if err != nil {
				t.Fatal(err)
			}
			*ex.state = s
			ex.save = save
			if err = ex.recover(context.Background(), Snapshot{Entries: []Entry{op.Entry}}); err != nil {
				t.Fatal(err)
			}
			if ex.state.Completed[0].Version == "1" {
				if err = ex.execute(context.Background(), op); err != nil {
					t.Fatal(err)
				}
			}
			data, _ := ex.root.ReadFile("file.txt")
			if string(data) != "expected bytes\n" || ex.state.Transaction != nil {
				t.Fatal("recovery incomplete")
			}
		})
	}
}
func TestReplaceRecoveryPreservesUnexpectedContent(t *testing.T) {
	ex, op, profile := replacement(t)
	save := ex.save
	ex.save = func() error {
		if ex.state.Transaction != nil && ex.state.Transaction.Phase == "backed-up" {
			return os.ErrPermission
		}
		return save()
	}
	if err := ex.execute(context.Background(), op); err == nil {
		t.Fatal("fault ignored")
	}
	s, err := loadState(profile, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	*ex.state = s
	ex.save = save
	if err = ex.root.WriteFile("file.txt", []byte("user content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = ex.recover(context.Background(), Snapshot{Entries: []Entry{op.Entry}}); err == nil {
		t.Fatal("unexpected content adopted")
	}
	data, _ := ex.root.ReadFile("file.txt")
	if string(data) != "user content" {
		t.Fatal("user content lost")
	}
	backup, _ := ex.root.ReadFile(s.Transaction.BackupPath)
	if string(backup) != "old bytes" {
		t.Fatal("old backup lost")
	}
}
func TestReplaceBadTransferPreservesOld(t *testing.T) {
	for _, mode := range []string{"checksum", "version", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ex, op, _ := replacement(t)
			api := ex.api.(*downloadAPI)
			ctx := context.Background()
			switch mode {
			case "checksum":
				api.corrupt = true
			case "version":
				api.changed = true
				op.Entry.Remote.Version = "1"
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				api.onDownload = cancel
			}
			if err := ex.execute(ctx, op); err == nil {
				t.Fatal("bad transfer accepted")
			}
			data, _ := ex.root.ReadFile("file.txt")
			if string(data) != "old bytes" {
				t.Fatal("old bytes lost")
			}
		})
	}
}
