package syncengine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func policyValue(t *testing.T, state State) string {
	t.Helper()
	field := reflect.ValueOf(state).FieldByName("ConflictPolicy")
	if !field.IsValid() {
		t.Fatal("profile state has no conflict policy")
	}
	return field.String()
}

func TestLoadStateDefaultsConflictPolicyToAsk(t *testing.T) {
	dir := t.TempDir()
	if err := saveState(dir, State{Version: 2}); err != nil {
		t.Fatal(err)
	}
	state, err := loadState(dir, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	if got := policyValue(t, state); got != "ask" {
		t.Fatalf("policy = %q, want ask", got)
	}
}

func TestLoadStateRejectsUnknownConflictPolicy(t *testing.T) {
	dir := t.TempDir()
	state := `{"version":2,"binding":{},"completed":[],"conflict_policy":"last-write-wins"}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(state), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(dir, Binding{}); err == nil {
		t.Fatal("unknown conflict policy accepted")
	}
}

func TestLoadStateValidatesPendingConflicts(t *testing.T) {
	base := baselineFile("file.txt", "remote-id", []byte("baseline"))
	conflict := Conflict{
		Path: "file.txt", Baseline: base, LocalSHA256: baselineFile("", "", []byte("local")).SHA256, LocalSize: 5,
		RemoteID: "remote-id", RemoteVersion: "2", RemoteMD5: base.MD5, RemoteSize: base.Size,
	}
	conflict.ID = conflictID(conflict)
	validProfile := t.TempDir()
	if err := saveState(validProfile, State{Version: 2, Completed: []Completed{base}, Conflicts: []Conflict{conflict}}); err != nil {
		t.Fatalf("valid conflict record could not be saved: %v", err)
	}
	if _, err := loadState(validProfile, Binding{}); err != nil {
		t.Fatalf("valid conflict record was rejected: %v", err)
	}
	bad := []func(*Conflict){
		func(c *Conflict) { c.Path = "../escape" },
		func(c *Conflict) { c.LocalSHA256 = "bad" },
		func(c *Conflict) { c.RemoteID = "untracked-id" },
		func(c *Conflict) { c.SelectedSide = "winner-takes-all" },
	}
	for i, mutate := range bad {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			c := conflict
			mutate(&c)
			c.ID = conflictID(c)
			profile := t.TempDir()
			if err := saveState(profile, State{Version: 2, Completed: []Completed{base}, Conflicts: []Conflict{c}}); err != nil {
				t.Fatal(err)
			}
			if _, err := loadState(profile, Binding{}); err == nil {
				t.Fatal("invalid pending conflict accepted")
			}
		})
	}
}

func TestConflictIDIsStableForSameSnapshots(t *testing.T) {
	conflict := Conflict{Path: "a", RemoteID: "id", Baseline: Completed{SHA256: "baseline"}, LocalSHA256: "local", RemoteVersion: "2", RemoteMD5: "remote", RemoteSize: 3}
	if first, second := conflictID(conflict), conflictID(conflict); first == "" || first != second {
		t.Fatalf("conflict IDs differ for same snapshots: %q, %q", first, second)
	}
}
