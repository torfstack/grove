package syncengine

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"os"
	"path/filepath"
	"testing"
)

type moveAPI struct {
	treeAPI
	entry drive.File
}

func (a moveAPI) Get(context.Context, string) (drive.File, error) { return a.entry, nil }
func moving(t *testing.T) (*executor, Operation, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	for _, p := range []string{"old", "old/empty", "old/nested"} {
		if err = root.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = root.WriteFile("old/nested/a", []byte("bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	before := []Completed{{Path: "old", RemoteID: "folder", Kind: "folder"}, {Path: "old/empty", RemoteID: "empty", Kind: "folder"}, {Path: "old/nested", RemoteID: "nested", Kind: "folder"}, baselineFile("old/nested/a", "a", []byte("bytes"))}
	entry := folderEntry("folder", "new")
	entry.Remote.Name = "new"
	entry.Remote.Parents = []string{"root"}
	entry.Remote.OwnedByMe = true
	state := &State{Version: 2, Completed: before, PopulationComplete: true}
	profile := filepath.Join(t.TempDir(), "profile")
	ex := &executor{root: root, api: moveAPI{entry: entry.Remote}, state: state, save: func() error { return saveState(profile, *state) }}
	if err = ex.save(); err != nil {
		t.Fatal(err)
	}
	return ex, Operation{Kind: OpMove, Entry: entry, Before: before}, profile
}
func TestMoveTrackedSubtree(t *testing.T) {
	ex, op, _ := moving(t)
	if err := ex.execute(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	b, err := ex.root.ReadFile("new/nested/a")
	if err != nil || string(b) != "bytes" {
		t.Fatal("subtree lost", err)
	}
	if _, err = ex.root.Stat("old"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("old path retained")
	}
	if len(ex.state.Completed) != 4 || ex.state.Completed[0].Path != "new" || ex.state.Transaction != nil {
		t.Fatal("baseline not remapped")
	}
}
func TestMoveSourceChanged(t *testing.T) {
	for _, mode := range []string{"edited", "missing", "unexpected"} {
		t.Run(mode, func(t *testing.T) {
			ex, op, _ := moving(t)
			var err error
			switch mode {
			case "edited":
				err = ex.root.WriteFile("old/nested/a", []byte("edited"), 0600)
			case "missing":
				err = ex.root.Remove("old/nested/a")
			case "unexpected":
				err = ex.root.WriteFile("old/user", []byte("user"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = ex.execute(context.Background(), op); err == nil {
				t.Fatal("changed subtree moved")
			}
			if _, err = ex.root.Stat("old"); err != nil {
				t.Fatal("source lost")
			}
		})
	}
}
func TestMoveDestinationAppeared(t *testing.T) {
	ex, op, _ := moving(t)
	if err := ex.root.Mkdir("new", 0700); err != nil {
		t.Fatal(err)
	}
	if err := ex.execute(context.Background(), op); err == nil {
		t.Fatal("occupied target replaced")
	}
}
func TestMoveRecoveryMatrix(t *testing.T) {
	for fail := 1; fail <= 3; fail++ {
		t.Run(string(rune('0'+fail)), func(t *testing.T) {
			ex, op, profile := moving(t)
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
			if ex.state.Completed[0].Path == "old" {
				if err = ex.execute(context.Background(), op); err != nil {
					t.Fatal(err)
				}
			}
			b, err := ex.root.ReadFile("new/nested/a")
			if err != nil || string(b) != "bytes" || ex.state.Transaction != nil {
				t.Fatal("recovery failed", err)
			}
		})
	}
}
func TestMoveRecoveryUnexpected(t *testing.T) {
	ex, op, profile := moving(t)
	save := ex.save
	ex.save = func() error {
		if ex.state.Transaction != nil && ex.state.Transaction.Phase == "committed" {
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
	if err = ex.root.WriteFile("new/nested/a", []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = ex.recover(context.Background(), Snapshot{}); err == nil {
		t.Fatal("changed subtree adopted")
	}
	b, _ := ex.root.ReadFile("new/nested/a")
	if string(b) != "user" {
		t.Fatal("user data lost")
	}
}
