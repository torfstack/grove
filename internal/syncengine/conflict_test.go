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

func TestLoadStateAcceptsCommittedUploadJournalWithCapturedConflict(t *testing.T) {
	dir := t.TempDir()
	current := baselineFile("a", "remote-a", []byte("uploaded local"))
	current.Version, current.ModifiedTime, current.HeadRevisionID = "3", "2026-01-03T00:00:00Z", "revision-3"
	localHash := current.SHA256
	cloudMD5 := md5Text("competing cloud")
	conflict := Conflict{
		Path: "a", Baseline: current, LocalSHA256: localHash, LocalSize: current.Size,
		RemoteID: current.RemoteID, RemoteVersion: current.Version, RemoteMD5: current.MD5, RemoteSize: current.Size, RemoteRevisionID: current.HeadRevisionID,
		CloudRevisionID: "revision-2", CloudMD5: cloudMD5, CloudSize: int64(len("competing cloud")),
		LocalStagePath: ".grove-upload-local", LocalStageIdentity: &FileIdentity{Device: 1, Inode: 2},
		CloudStagePath: ".grove-cloud-conflict-remote", CloudStageIdentity: &FileIdentity{Device: 1, Inode: 3}, CloudSHA256: hashText("competing cloud"),
	}
	conflict.ID = conflictID(conflict)
	before := baselineFile("a", "remote-a", []byte("old"))
	before.HeadRevisionID = "revision-1"
	remote := file("remote-a", "a")
	remote.HeadRevisionID = before.HeadRevisionID
	journal := Journal{
		Operation: Operation{Kind: OpUpload, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: localHash, Size: current.Size}, Before: []Completed{before}},
		Phase:     "committed", StagePath: conflict.LocalStagePath, StageIdentity: conflict.LocalStageIdentity,
		StageSHA256: localHash, StageMD5: md5Text("uploaded local"), StageSize: current.Size,
		PreflightRevisionID: before.HeadRevisionID, PinnedRevisionID: before.HeadRevisionID,
		CloudStagePath: conflict.CloudStagePath, CloudStageIdentity: conflict.CloudStageIdentity, CloudSHA256: conflict.CloudSHA256,
		CloudRevisionID: conflict.CloudRevisionID, CloudMD5: conflict.CloudMD5, CloudSize: conflict.CloudSize, After: []Completed{current},
	}
	state := State{Version: 3, Completed: []Completed{current}, Conflicts: []Conflict{conflict}, Transaction: &journal, PopulationComplete: true}
	if err := saveState(dir, state); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(dir, Binding{}); err != nil {
		t.Fatalf("committed upload and captured conflict did not survive restart: %v", err)
	}
}

func TestConflictIDIsStableForSameSnapshots(t *testing.T) {
	conflict := Conflict{Path: "a", RemoteID: "id", Baseline: Completed{SHA256: "baseline"}, LocalSHA256: "local", RemoteVersion: "2", RemoteMD5: "remote", RemoteSize: 3}
	if first, second := conflictID(conflict), conflictID(conflict); first == "" || first != second {
		t.Fatalf("conflict IDs differ for same snapshots: %q, %q", first, second)
	}
}
