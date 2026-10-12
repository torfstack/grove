package syncengine

import (
	"encoding/json"
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

func TestLoadStateNormalizesAndSavesLegacyCloudCandidateSet(t *testing.T) {
	dir := t.TempDir()
	base := baselineFile("a", "remote-a", []byte("baseline"))
	conflict := Conflict{
		Path: "a", Baseline: base, LocalSHA256: hashText("local"), LocalSize: 5,
		RemoteID: base.RemoteID, RemoteVersion: "2", RemoteMD5: md5Text("head"), RemoteSize: 4,
		RemoteRevisionID: "head-revision", CloudRevisionID: "cloud-revision", CloudMD5: md5Text("cloud"), CloudSize: 5,
		LocalStagePath: ".grove-upload-local", LocalStageIdentity: &FileIdentity{Device: 1, Inode: 2},
		CloudStagePath: ".grove-cloud-conflict-cloud", CloudStageIdentity: &FileIdentity{Device: 1, Inode: 3}, CloudSHA256: hashText("cloud"),
	}
	conflict.ID = conflictID(conflict)
	legacy, err := json.Marshal(State{Version: 3, Completed: []Completed{base}, Conflicts: []Conflict{conflict}, PopulationComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "state.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	state, err := loadState(dir, Binding{})
	if err != nil {
		t.Fatal(err)
	}
	field := reflect.ValueOf(state.Conflicts[0]).FieldByName("CloudCandidates")
	if !field.IsValid() {
		t.Fatal("legacy candidate was not normalized: cloud candidates field is missing")
	}
	if field.Len() != 1 {
		t.Fatalf("legacy candidate count = %d, want 1", field.Len())
	}
	if state.Conflicts[0].ID != conflict.ID {
		t.Fatalf("legacy conflict ID changed: got %q, want %q", state.Conflicts[0].ID, conflict.ID)
	}
	if err = saveState(dir, state); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err = json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	conflicts := stored["conflicts"].([]any)
	storedConflict := conflicts[0].(map[string]any)
	if storedConflict["candidate_set_version"] != float64(1) || len(storedConflict["cloud_candidates"].([]any)) != 1 {
		t.Fatalf("legacy conflict was not written in candidate-set form: %v", storedConflict)
	}
}

func TestLoadStateRejectsMalformedCloudCandidateSets(t *testing.T) {
	dir := t.TempDir()
	base := baselineFile("a", "remote-a", []byte("baseline"))
	conflict := Conflict{
		Path: "a", Baseline: base, LocalSHA256: hashText("local"), LocalSize: 5,
		RemoteID: base.RemoteID, RemoteVersion: "2", RemoteMD5: md5Text("head"), RemoteSize: 4,
		RemoteRevisionID: "head-revision", CloudRevisionID: "cloud-revision", CloudMD5: md5Text("cloud"), CloudSize: 5,
		LocalStagePath: ".grove-upload-local", LocalStageIdentity: &FileIdentity{Device: 1, Inode: 2},
		CloudStagePath: ".grove-cloud-conflict-cloud", CloudStageIdentity: &FileIdentity{Device: 1, Inode: 3}, CloudSHA256: hashText("cloud"),
	}
	conflict.ID = conflictID(conflict)
	if err := saveState(dir, State{Version: 3, Completed: []Completed{base}, Conflicts: []Conflict{conflict}, PopulationComplete: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err = json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}

	mutations := map[string]func(map[string]any){
		"unknown version":        func(c map[string]any) { c["candidate_set_version"] = float64(2) },
		"missing stage identity": func(c map[string]any) { c["cloud_candidates"].([]any)[0].(map[string]any)["stage_identity"] = nil },
		"unsafe stage path":      func(c map[string]any) { c["cloud_candidates"].([]any)[0].(map[string]any)["stage_path"] = "../escape" },
		"invalid hash":           func(c map[string]any) { c["cloud_candidates"].([]any)[0].(map[string]any)["sha256"] = "bad" },
		"negative size":          func(c map[string]any) { c["cloud_candidates"].([]any)[0].(map[string]any)["size"] = float64(-1) },
		"invalid timestamp": func(c map[string]any) {
			c["cloud_candidates"].([]any)[0].(map[string]any)["modified_time"] = "yesterday"
		},
		"inconsistent legacy fields": func(c map[string]any) {
			c["cloud_candidates"].([]any)[0].(map[string]any)["revision_id"] = "different-revision"
		},
		"duplicate revision": func(c map[string]any) {
			first := c["cloud_candidates"].([]any)[0].(map[string]any)
			second, _ := json.Marshal(first)
			var copy map[string]any
			_ = json.Unmarshal(second, &copy)
			c["cloud_candidates"] = append(c["cloud_candidates"].([]any), copy)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			encoded, _ := json.Marshal(original)
			var fixture map[string]any
			_ = json.Unmarshal(encoded, &fixture)
			storedConflict := fixture["conflicts"].([]any)[0].(map[string]any)
			legacyCandidate := map[string]any{
				"revision_id": conflict.CloudRevisionID, "modified_time": "2026-10-12T12:00:00Z",
				"md5": conflict.CloudMD5, "size": float64(conflict.CloudSize), "sha256": conflict.CloudSHA256,
				"stage_path": conflict.CloudStagePath, "stage_identity": map[string]any{"device": float64(1), "inode": float64(3)},
			}
			storedConflict["candidate_set_version"] = float64(1)
			storedConflict["cloud_candidates"] = []any{legacyCandidate}
			mutate(storedConflict)
			encoded, _ = json.Marshal(fixture)
			if err := os.WriteFile(filepath.Join(dir, "state.json"), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadState(dir, Binding{}); err == nil {
				t.Fatal("malformed cloud candidate set was accepted")
			}
		})
	}
}

func TestLoadStateAcceptsVersionedCloudCandidateSet(t *testing.T) {
	dir := t.TempDir()
	base := baselineFile("a", "remote-a", []byte("baseline"))
	conflict := Conflict{
		Path: "a", Baseline: base, LocalSHA256: hashText("local"), LocalSize: 5,
		RemoteID: base.RemoteID, RemoteVersion: "2", RemoteMD5: md5Text("head"), RemoteSize: 4,
		RemoteRevisionID: "head-revision", CloudRevisionID: "cloud-revision", CloudMD5: md5Text("cloud"), CloudSize: 5,
		LocalStagePath: ".grove-upload-local", LocalStageIdentity: &FileIdentity{Device: 1, Inode: 2},
		CloudStagePath: ".grove-cloud-conflict-cloud", CloudStageIdentity: &FileIdentity{Device: 1, Inode: 3}, CloudSHA256: hashText("cloud"),
	}
	conflict.ID = conflictID(conflict)
	if err := saveState(dir, State{Version: 3, Completed: []Completed{base}, Conflicts: []Conflict{conflict}, PopulationComplete: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err = json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	storedConflict := stored["conflicts"].([]any)[0].(map[string]any)
	storedConflict["candidate_set_version"] = float64(1)
	storedConflict["cloud_candidates"] = []any{map[string]any{
		"revision_id": conflict.CloudRevisionID, "modified_time": "2026-10-12T12:00:00Z",
		"md5": conflict.CloudMD5, "size": float64(conflict.CloudSize), "sha256": conflict.CloudSHA256,
		"stage_path":     conflict.CloudStagePath,
		"stage_identity": map[string]any{"device": float64(1), "inode": float64(3)},
	}}
	data, err = json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadState(dir, Binding{})
	if err != nil {
		t.Fatalf("valid versioned cloud candidate set was rejected: %v", err)
	}
	field := reflect.ValueOf(loaded.Conflicts[0]).FieldByName("CloudCandidates")
	if !field.IsValid() {
		t.Fatal("versioned cloud candidate set was not loaded")
	}
	if field.Len() != 1 {
		t.Fatalf("candidate count = %d, want 1", field.Len())
	}
}
