package syncengine

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
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
func durableClone(t *testing.T, s State) State {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var c State
	if err = json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}
