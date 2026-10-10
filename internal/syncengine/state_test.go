package syncengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func baselineFile(path, id string, data []byte) Completed {
	sum := sha256.Sum256(data)
	return Completed{Path: path, RemoteID: id, Version: "1", Kind: "file", Size: int64(len(data)), MD5: "d41d8cd98f00b204e9800998ecf8427e", SHA256: hex.EncodeToString(sum[:])}
}
func TestStateRejectsMalformedBaseline(t *testing.T) {
	good := baselineFile("a", "a", nil)
	for _, change := range []func(*State){
		func(s *State) { s.Completed = append(s.Completed, s.Completed[0]) },
		func(s *State) { c := s.Completed[0]; c.Path = "b"; s.Completed = append(s.Completed, c) },
		func(s *State) { s.Completed[0].SHA256 = "bad" },
		func(s *State) { s.Completed[0].MD5 = "bad" },
		func(s *State) { s.Version = 99 },
		func(s *State) {
			s.Pending = &Pending{Entry: Entry{Path: "b", Remote: file("b", "b")}, Phase: "unknown"}
		},
		func(s *State) {
			s.Pending = &Pending{Entry: Entry{Path: "b", Remote: file("b", "b")}, Phase: "intent", TempPath: "../escape"}
		},
	} {
		dir := t.TempDir()
		s := State{Version: 1, Completed: []Completed{good}}
		change(&s)
		if err := saveState(dir, s); err != nil {
			t.Fatal(err)
		}
		if _, err := loadState(dir, Binding{}); err == nil {
			t.Fatal("malformed state accepted")
		}
	}
}
func TestStateV1Migration(t *testing.T) {
	dir := t.TempDir()
	s := State{Version: 1, Completed: []Completed{baselineFile("a", "a", nil)}, PopulationComplete: true}
	if err := saveState(dir, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(dir, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := migrateState(dir, loaded)
	if err != nil || upgraded.Version != 2 {
		t.Fatal("migration failed", err)
	}
	again, err := loadState(dir, Binding{})
	if err != nil || len(again.Completed) != 1 || again.Version != 2 {
		t.Fatal("migration lost baseline", err)
	}
	pending := s
	pending.Pending = &Pending{Entry: Entry{Path: "b", Remote: file("b", "b")}, Phase: "intent"}
	if _, err = migrateState(dir, pending); err == nil {
		t.Fatal("pending legacy work migrated")
	}
}
func TestBaselineUpdatesByIdentity(t *testing.T) {
	old := baselineFile("a", "id", nil)
	next := old
	next.Path = "b"
	s := State{Completed: []Completed{old}}
	if err := applyBaseline(&s, []Completed{old}, []Completed{next}); err != nil {
		t.Fatal(err)
	}
	if len(s.Completed) != 1 || s.Completed[0].Path != "b" {
		t.Fatal("duplicate baseline", s)
	}
	if err := applyBaseline(&s, []Completed{old}, nil); err == nil {
		t.Fatal("stale baseline accepted")
	}
}
func TestV1PendingRecoveryBeforeMigration(t *testing.T) {
	profile := t.TempDir()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	api := downloader()
	entry := Entry{Path: "file.txt", Remote: api.children[0]}
	sum := sha256.Sum256(api.data)
	state := State{Version: 1, Pending: &Pending{Entry: entry, TempPath: ".grove-download-legacy", Phase: "verified", VerifiedSHA256: hex.EncodeToString(sum[:])}}
	if err = root.WriteFile(state.Pending.TempPath, api.data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = root.Link(state.Pending.TempPath, entry.Path); err != nil {
		t.Fatal(err)
	}
	if err = saveState(profile, state); err != nil {
		t.Fatal(err)
	}
	ex := executor{root: root, api: api, state: &state, save: func() error { return os.ErrPermission }}
	snapshot := Snapshot{Entries: []Entry{entry}}
	if err = ex.recover(context.Background(), snapshot); err == nil {
		t.Fatal("recovery save fault ignored")
	}
	state, err = loadState(profile, Binding{})
	if err != nil || state.Version != 1 || state.Pending == nil {
		t.Fatal("failed recovery migrated or lost intent", err)
	}
	if _, err = migrateState(profile, state); err == nil {
		t.Fatal("pending recovery migrated")
	}
	data, err := root.ReadFile(entry.Path)
	if err != nil || string(data) != string(api.data) {
		t.Fatal("published legacy file lost", err)
	}
	ex.save = func() error { return saveState(profile, state) }
	if err = ex.recover(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	state, err = migrateState(profile, state)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(profile, Binding{})
	if err != nil || loaded.Version != 2 || loaded.Pending != nil || len(loaded.Completed) != 1 || loaded.Completed[0].SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("recovery/migration lost baseline", err)
	}
}
func TestV1ProbeFailurePreservesState(t *testing.T) {
	for _, failure := range []string{"content", "save"} {
		t.Run(failure, func(t *testing.T) {
			profile := t.TempDir()
			root, err := os.OpenRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			state := State{Version: 1, ProbePath: ".grove-probe-owned", ProbeEntries: []Entry{{Path: "file"}}}
			probe := filepath.Join(state.ProbePath, "file")
			if err = root.Mkdir(state.ProbePath, 0700); err != nil {
				t.Fatal(err)
			}
			content := []byte(nil)
			if failure == "content" {
				content = []byte("local data")
			}
			if err = root.WriteFile(probe, content, 0600); err != nil {
				t.Fatal(err)
			}
			if err = saveState(profile, state); err != nil {
				t.Fatal(err)
			}
			save := func() error { return os.ErrPermission }
			if failure == "content" {
				save = func() error { return saveState(profile, state) }
			}
			if err = clearProbe(root, &state, save); err == nil {
				t.Fatal("probe failure ignored")
			}
			state, err = loadState(profile, Binding{})
			if err != nil || state.Version != 1 || state.ProbePath == "" {
				t.Fatal("failed cleanup migrated or lost probe", err)
			}
			if _, err = migrateState(profile, state); err == nil {
				t.Fatal("probe recovery migrated")
			}
			if failure == "content" {
				data, readErr := root.ReadFile(probe)
				if readErr != nil || string(data) != "local data" {
					t.Fatal("unexpected local data removed", readErr)
				}
				if err = root.WriteFile(probe, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err = clearProbe(root, &state, func() error { return saveState(profile, state) }); err != nil {
				t.Fatal(err)
			}
			if _, err = migrateState(profile, state); err != nil {
				t.Fatal(err)
			}
			loaded, err := loadState(profile, Binding{})
			if err != nil || loaded.Version != 2 || loaded.ProbePath != "" {
				t.Fatal("probe migration incomplete", err)
			}
			entries, err := os.ReadDir(root.Name())
			if err != nil || len(entries) != 0 {
				t.Fatal("probe cleanup incomplete", err)
			}
		})
	}
}

func TestStateRejectsMalformedJournal(t *testing.T) {
	old := baselineFile("file.txt", "file", []byte("old"))
	api := downloader()
	op := Operation{Kind: OpReplace, Entry: Entry{Path: "file.txt", Remote: api.children[0]}, Before: []Completed{old}}
	for _, change := range []func(*Journal){
		func(j *Journal) { j.TempPath = "user-file" },
		func(j *Journal) { j.BackupPath = j.TempPath },
		func(j *Journal) { j.Operation.Before[0].SHA256 = "bad" },
		func(j *Journal) { j.Operation.Before[0].RemoteID = "other" },
		func(j *Journal) { j.Phase = "verified" },
		func(j *Journal) {
			j.After = []Completed{baselineFile("elsewhere", "other", nil)}
			j.Phase = "committed"
		},
	} {
		dir := t.TempDir()
		copyOp := op
		copyOp.Before = append([]Completed(nil), op.Before...)
		j := Journal{Operation: copyOp, Phase: "intent", TempPath: ".grove-download-id", BackupPath: ".grove-backup-id"}
		change(&j)
		state := State{Version: 2, Completed: []Completed{old}, Transaction: &j}
		if err := saveState(dir, state); err != nil {
			t.Fatal(err)
		}
		if _, err := loadState(dir, Binding{}); err == nil {
			t.Fatal("malformed journal accepted")
		}
	}
}
