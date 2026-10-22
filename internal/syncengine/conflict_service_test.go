package syncengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/torfstack/grove/internal/drive"
)

func TestConflictServicePersistsPolicyAndListsPendingConflict(t *testing.T) {
	opts, registry := options(t)
	if err := os.MkdirAll(opts.ProfileDir, 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	base := baselineFile("a", "remote-a", []byte("base"))
	conflict := Conflict{Path: "a", Baseline: base, LocalSHA256: hashText("local"), LocalSize: 5, RemoteID: base.RemoteID, RemoteVersion: "2", RemoteMD5: md5Text("cloud"), RemoteSize: 5}
	conflict.ID = conflictID(conflict)
	state := State{Version: 2, Binding: Binding{RemoteRoot: opts.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}, Completed: []Completed{base}, Conflicts: []Conflict{conflict}, PopulationComplete: true}
	if err := saveState(canonical.ProfileDir, state); err != nil {
		t.Fatal(err)
	}
	service := Service{RegistryDir: registry}
	if err = service.SetConflictPolicy(context.Background(), opts, ConflictKeepLocal); err != nil {
		t.Fatal(err)
	}
	conflicts, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(conflicts) != 1 || conflicts[0].ID != conflict.ID {
		t.Fatalf("pending conflict listing = %+v, err=%v", conflicts, err)
	}
	loaded, err := loadState(canonical.ProfileDir, state.Binding)
	if err != nil || loaded.ConflictPolicy != ConflictKeepLocal {
		t.Fatalf("policy was not persisted: %+v, err=%v", loaded, err)
	}
}

func TestResolveConflictRejectsLocalChoiceForUntrackedAddition(t *testing.T) {
	opts, registry := options(t)
	if err := os.MkdirAll(opts.ProfileDir, 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	conflict := Conflict{
		Path: "same.txt", LocalSHA256: hashText("local"), LocalSize: 5,
		RemoteID: "remote", RemoteVersion: "1", RemoteMD5: md5Text("cloud"), RemoteSize: 5,
		LocalStagePath: ".grove-upload-local", LocalStageIdentity: &FileIdentity{Device: 1, Inode: 2},
		CloudStagePath: ".grove-cloud-conflict-cloud", CloudStageIdentity: &FileIdentity{Device: 1, Inode: 3}, CloudSHA256: hashText("cloud"),
	}
	conflict.ID = conflictID(conflict)
	state := State{Version: 3, Binding: Binding{RemoteRoot: opts.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}, Conflicts: []Conflict{conflict}, PopulationComplete: true}
	if err = saveState(canonical.ProfileDir, state); err != nil {
		t.Fatal(err)
	}
	service := Service{RegistryDir: registry}
	err = service.ResolveConflict(context.Background(), opts, conflict.ID, SideLocal, 0)
	if err == nil || err.Error() != "local resolution is not supported for an untracked same-path addition" {
		t.Fatalf("unsupported local choice was not rejected before touching candidates: %v", err)
	}
}

func TestRemoteConflictCandidateIgnoresVersionOnlyChange(t *testing.T) {
	conflict := Conflict{Path: "a", RemoteID: "remote-a", RemoteVersion: "2", RemoteMD5: md5Text("cloud"), RemoteSize: 5, RemoteRevisionID: "rev-2"}
	remote := file("remote-a", "a")
	remote.Version = "3"
	remote.MD5, remote.Size, remote.HeadRevisionID = conflict.RemoteMD5, conflict.RemoteSize, conflict.RemoteRevisionID
	if !matchesRemoteConflict(Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}}, conflict) {
		t.Fatal("version-only metadata change invalidated an unchanged cloud candidate")
	}
	remote.HeadRevisionID = "rev-3"
	if matchesRemoteConflict(Snapshot{Entries: []Entry{{Path: "a", Remote: remote}}}, conflict) {
		t.Fatal("changed remote content revision was accepted")
	}
}

func TestDeletionConflictExplicitResolution(t *testing.T) {
	for _, side := range []ConflictSide{SideCloud, SideLocal} {
		t.Run(string(side), func(t *testing.T) {
			service, opts, remote := httpService(t)
			if _, err := service.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			remote.put("new-child", "new-child", "folder", "new remote content")
			if err := os.RemoveAll(filepath.Join(opts.LocalDir, "old")); err != nil {
				t.Fatal(err)
			}
			opts.TwoWay = true
			if _, err := service.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			conflicts, err := service.ListConflicts(context.Background(), opts)
			if err != nil || len(conflicts) != 1 || !conflicts[0].Deletion || len(conflicts[0].DeletionRemoteSubtree) != 3 {
				t.Fatalf("missing durable subtree deletion conflict: %+v err=%v", conflicts, err)
			}
			detail, err := service.ConflictDetails(context.Background(), opts, conflicts[0].ID)
			if err != nil || len(detail.Candidates) != 2 || detail.Candidates[0].Description != "deletion" || detail.Candidates[1].Description != "remote tree" {
				t.Fatalf("wrong deletion conflict detail: %+v err=%v", detail, err)
			}
			if err = service.ResolveConflict(context.Background(), opts, conflicts[0].ID, side, 0); err != nil {
				t.Fatal(err)
			}
			remaining, err := service.ListConflicts(context.Background(), opts)
			if err != nil || len(remaining) != 0 {
				t.Fatalf("resolved deletion remained pending: %+v err=%v", remaining, err)
			}
			if side == SideCloud {
				for path, want := range map[string]string{"old/a": "old", "old/new-child": "new remote content"} {
					data, readErr := os.ReadFile(filepath.Join(opts.LocalDir, path))
					if readErr != nil || string(data) != want {
						t.Fatalf("remote subtree was not restored at %s: %q %v", path, data, readErr)
					}
				}
				if remote.files["folder"].Trashed || remote.mutations != 0 {
					t.Fatal("cloud resolution mutated Drive")
				}
			} else if !remote.files["folder"].ExplicitlyTrashed || !remote.files["a"].Trashed || !remote.files["new-child"].Trashed || remote.mutations != 1 {
				t.Fatalf("local resolution did not trash the validated subtree once: %+v", remote.files)
			}
		})
	}
}

func TestDeletionConflictResolutionRejectsChangedRemoteSubtree(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	remote.put("new-child", "new-child", "folder", "first remote content")
	if err := os.RemoveAll(filepath.Join(opts.LocalDir, "old")); err != nil {
		t.Fatal(err)
	}
	opts.TwoWay = true
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	conflicts, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("expected one deletion conflict: %+v %v", conflicts, err)
	}
	remote.put("new-child", "new-child", "folder", "changed remote content")
	if err = service.ResolveConflict(context.Background(), opts, conflicts[0].ID, SideCloud, 0); err == nil {
		t.Fatal("changed remote subtree was accepted for resolution")
	}
	remaining, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(remaining) != 1 || remaining[0].ID != conflicts[0].ID || remote.mutations != 0 || remote.files["folder"].Trashed {
		t.Fatalf("stale resolution changed conflict or remote data: conflicts=%+v remote=%+v err=%v", remaining, remote.files, err)
	}
}

func TestInterruptedCloudDeletionRestoreRequiresDownloadOnlyResume(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	remote.put("new-child", "new-child", "folder", "new remote content")
	if err := os.RemoveAll(filepath.Join(opts.LocalDir, "old")); err != nil {
		t.Fatal(err)
	}
	opts.TwoWay = true
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	conflicts, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("expected deletion conflict: %+v %v", conflicts, err)
	}
	remote.failDownloadID = "new-child"
	if err = service.ResolveConflict(context.Background(), opts, conflicts[0].ID, SideCloud, 0); err == nil {
		t.Fatal("injected partial restore failure was ignored")
	}
	if _, err = service.Run(context.Background(), opts); err == nil {
		t.Fatal("two-way sync accepted an incomplete cloud restoration")
	}
	remote.failDownloadID = ""
	opts.TwoWay = false
	if _, err = service.Run(context.Background(), opts); err != nil {
		t.Fatalf("download-only recovery did not finish restoration: %v", err)
	}
	for path, want := range map[string]string{"old/a": "old", "old/new-child": "new remote content"} {
		data, readErr := os.ReadFile(filepath.Join(opts.LocalDir, path))
		if readErr != nil || string(data) != want {
			t.Fatalf("incomplete restore at %s: %q %v", path, data, readErr)
		}
	}
	stateData, err := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state State
	if err = json.Unmarshal(stateData, &state); err != nil || !state.PopulationComplete {
		t.Fatalf("restored profile was not marked complete: %v %+v", err, state)
	}
}

func TestInterruptedCloudRestoreRetainsUnrelatedDeletionConflict(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	remote.put("new-child", "new-child", "folder", "new remote content")
	remote.put("b", "b", "root", "changed remote content")
	if err := os.RemoveAll(filepath.Join(opts.LocalDir, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(opts.LocalDir, "b")); err != nil {
		t.Fatal(err)
	}
	opts.TwoWay = true
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	conflicts, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(conflicts) != 2 {
		t.Fatalf("expected two tombstones: %v %v", len(conflicts), err)
	}
	id := ""
	for _, c := range conflicts {
		if c.Path == "old" {
			id = c.ID
		}
	}
	remote.failDownloadID = "new-child"
	if err = service.ResolveConflict(context.Background(), opts, id, SideCloud, 0); err == nil {
		t.Fatal("injected restore failure ignored")
	}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{RemoteRoot: canonical.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}
	state, err := loadState(canonical.ProfileDir, binding)
	if err != nil || state.Pending == nil || len(state.Conflicts) != 1 || state.Conflicts[0].Path != "b" {
		t.Fatalf("restore journal lost unrelated tombstone: %v", err)
	}
	api, closeAPI, err := service.OpenAPI(context.Background(), opts.TokenFile, "read-only")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, scanErr := Scan(context.Background(), api, opts.RemoteRoot)
	if closeErr := closeAPI(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if scanErr != nil {
		t.Fatal(scanErr)
	}
	between := state
	between.Pending = nil
	var local []LocalEntry
	for _, c := range state.Completed {
		if c.Path != "b" {
			local = append(local, LocalEntry{Path: c.Path, Kind: c.Kind, SHA256: c.SHA256, Size: c.Size})
		}
	}
	plan, err := BuildRestorationPlan(snapshot, local, between)
	if err != nil {
		t.Fatalf("between-operation restoration plan failed: %v", err)
	}
	found := false
	for _, op := range plan.Operations {
		if op.Entry.Path == "b" || op.Kind == OpTrash {
			t.Fatal("restoration planned unrelated conflict")
		}
		found = found || op.Kind == OpDownload && op.Entry.Path == "old/new-child"
	}
	if !found {
		t.Fatal("between-operation restore lost remaining download")
	}
	remote.failDownloadID = ""
	opts.TwoWay = false
	if _, err = service.Run(context.Background(), opts); err != nil {
		t.Fatalf("restoration with unrelated tombstone failed: %v", err)
	}
	remaining, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(remaining) != 1 || remaining[0].Path != "b" || remote.files["b"].Trashed || remote.mutations != 0 {
		t.Fatalf("restoration resolved unrelated tombstone: %v %v", len(remaining), err)
	}
	if data, err := os.ReadFile(filepath.Join(opts.LocalDir, "old", "new-child")); err != nil || string(data) != "new remote content" {
		t.Fatalf("restored child missing: %v", err)
	}
}

func TestCloudRestorationDownloadMetadataVersion(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	remote.put("new-child", "new-child", "folder", "new remote content")
	f := remote.files["new-child"]
	f.Version = "9"
	remote.files["new-child"] = f
	remote.put("b", "b", "root", "changed remote content")
	if err := os.RemoveAll(filepath.Join(opts.LocalDir, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(opts.LocalDir, "b")); err != nil {
		t.Fatal(err)
	}
	opts.TwoWay = true
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	conflicts, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(conflicts) != 2 {
		t.Fatalf("expected two tombstones: %v", err)
	}
	id := ""
	for _, c := range conflicts {
		if c.Path == "old" {
			id = c.ID
		}
	}
	if id == "" {
		t.Fatal("missing subtree conflict")
	}
	service = advanceMetadataOnDownload(t, service, remote, "new-child")
	if err := service.ResolveConflict(context.Background(), opts, id, SideCloud, 0); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"old/a": "old", "old/new-child": "new remote content"} {
		data, err := os.ReadFile(filepath.Join(opts.LocalDir, path))
		if err != nil || string(data) != want {
			t.Fatalf("wrong restored bytes at %s: %v", path, err)
		}
	}
	state := testProfileState(t, opts)
	if !state.PopulationComplete || len(state.Conflicts) != 1 || state.Conflicts[0].Path != "b" || remote.files["new-child"].Version != "10" {
		t.Fatal("restoration lost unrelated tombstone or did not exercise advance")
	}
	downloads := remote.downloads
	result, err := service.Run(context.Background(), opts)
	if err != nil || result.Downloaded != 0 || remote.downloads != downloads || remote.mutations != 0 || remote.files["b"].Trashed {
		t.Fatalf("restoration repeat performed work: %v", err)
	}
}

func TestCloudDeletionResolutionRejectsUnrelatedLocalChangesBeforeSaving(t *testing.T) {
	for _, change := range []string{"edit", "add", "remove"} {
		t.Run(change, func(t *testing.T) {
			service, opts, remote := httpService(t)
			if _, err := service.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			remote.put("b", "b", "root", "changed remote content")
			if err := os.Remove(filepath.Join(opts.LocalDir, "b")); err != nil {
				t.Fatal(err)
			}
			opts.TwoWay = true
			if _, err := service.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			conflicts, err := service.ListConflicts(context.Background(), opts)
			if err != nil || len(conflicts) != 1 || !conflicts[0].Deletion {
				t.Fatalf("expected deletion tombstone: %+v %v", conflicts, err)
			}
			switch change {
			case "edit":
				if err = os.WriteFile(filepath.Join(opts.LocalDir, "old", "a"), []byte("unrelated edit"), 0600); err != nil {
					t.Fatal(err)
				}
			case "add":
				if err = os.WriteFile(filepath.Join(opts.LocalDir, "untracked"), []byte("new"), 0600); err != nil {
					t.Fatal(err)
				}
			case "remove":
				if err = os.Remove(filepath.Join(opts.LocalDir, "old", "a")); err != nil {
					t.Fatal(err)
				}
			}
			if err = service.ResolveConflict(context.Background(), opts, conflicts[0].ID, SideCloud, 0); err == nil {
				t.Fatal("cloud restoration accepted unrelated local changes")
			}
			canonical, err := canonicalOptions(opts)
			if err != nil {
				t.Fatal(err)
			}
			state, err := loadState(canonical.ProfileDir, Binding{RemoteRoot: canonical.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile})
			if err != nil || !state.PopulationComplete || len(state.Conflicts) != 1 || state.Conflicts[0].ID != conflicts[0].ID || state.Conflicts[0].SelectedSide != "" {
				t.Fatalf("rejected resolution changed durable profile: %+v %v", state, err)
			}
			if remote.mutations != 0 {
				t.Fatal("rejected resolution mutated Drive")
			}
		})
	}
}

func TestCloudDeletionResolutionRestoresThroughRemoteAncestorMove(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(opts.LocalDir, "old", "a")); err != nil {
		t.Fatal(err)
	}
	folder := remote.files["folder"]
	folder.Name = "new"
	remote.files["folder"] = folder
	opts.TwoWay = true
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	conflicts, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(conflicts) != 1 || !conflicts[0].Deletion {
		t.Fatalf("ancestor move lost deletion conflict: %v %v", len(conflicts), err)
	}
	if err = service.ResolveConflict(context.Background(), opts, conflicts[0].ID, SideCloud, 0); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(opts.LocalDir, "new", "a")); err != nil || string(data) != "old" {
		t.Fatalf("cloud file not restored through ancestor move: %v", err)
	}
	if _, err = os.Stat(filepath.Join(opts.LocalDir, "old")); !os.IsNotExist(err) {
		t.Fatal("old ancestor path remained")
	}
}

func TestCloudRestoreWithSiblingTombstoneUnderMovedAncestor(t *testing.T) {
	for _, secondSide := range []ConflictSide{SideCloud, SideLocal} {
		t.Run(string(secondSide), func(t *testing.T) {
			service, opts, remote := httpService(t)
			remote.put("sibling", "sibling", "folder", "sibling content")
			if _, err := service.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"a", "sibling"} {
				if err := os.Remove(filepath.Join(opts.LocalDir, "old", name)); err != nil {
					t.Fatal(err)
				}
			}
			folder := remote.files["folder"]
			folder.Name = "new"
			remote.files["folder"] = folder
			opts.TwoWay = true
			if _, err := service.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			conflicts, err := service.ListConflicts(context.Background(), opts)
			if err != nil || len(conflicts) != 2 {
				t.Fatalf("expected sibling conflicts: %v %v", len(conflicts), err)
			}
			firstID, secondID := "", ""
			for _, c := range conflicts {
				if c.Path == "old/a" {
					firstID = c.ID
				} else {
					secondID = c.ID
				}
			}
			if err = service.ResolveConflict(context.Background(), opts, firstID, SideCloud, 0); err == nil {
				t.Fatal("unresolved sibling should defer ancestor restoration")
			}
			remaining, err := service.ListConflicts(context.Background(), opts)
			if err != nil || len(remaining) != 1 || remaining[0].ID != secondID {
				t.Fatalf("partial restore blocked remaining conflict inspection: %v", err)
			}
			if err = service.ResolveConflict(context.Background(), opts, secondID, secondSide, 0); err != nil {
				t.Fatalf("remaining sibling could not resolve: %v", err)
			}
			if data, err := os.ReadFile(filepath.Join(opts.LocalDir, "new", "a")); err != nil || string(data) != "old" {
				t.Fatalf("first sibling not restored: %v", err)
			}
			if secondSide == SideCloud {
				if data, err := os.ReadFile(filepath.Join(opts.LocalDir, "new", "sibling")); err != nil || string(data) != "sibling content" {
					t.Fatalf("second sibling not restored: %v", err)
				}
			} else if !remote.files["sibling"].ExplicitlyTrashed || remote.mutations != 1 {
				t.Fatal("selected sibling was not trashed exactly once")
			}
		})
	}
}

func TestResolveConflictRejectsChangedLocalCandidate(t *testing.T) {
	opts, registry := options(t)
	for _, dir := range []string{opts.ProfileDir, opts.LocalDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(opts.LocalDir, "a"), []byte("changed after listing"), 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	localPath, localIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-upload-local", []byte("listed local"))
	cloudPath, cloudIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-cloud-conflict-cloud", []byte("cloud version"))
	base := baselineFile("a", "remote-a", []byte("base"))
	conflict := Conflict{Path: "a", Baseline: base, LocalSHA256: hashText("listed local"), LocalSize: int64(len("listed local")), RemoteID: base.RemoteID, RemoteVersion: "2", RemoteMD5: md5Text("cloud version"), RemoteSize: int64(len("cloud version")), RemoteRevisionID: "rev-2", LocalStagePath: localPath, LocalStageIdentity: &localIdentity, CloudStagePath: cloudPath, CloudStageIdentity: &cloudIdentity, CloudSHA256: hashText("cloud version")}
	conflict.ID = conflictID(conflict)
	state := State{Version: 3, Binding: Binding{RemoteRoot: opts.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}, Completed: []Completed{base}, Conflicts: []Conflict{conflict}, PopulationComplete: true}
	if err = saveState(canonical.ProfileDir, state); err != nil {
		t.Fatal(err)
	}
	opened := false
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		opened = true
		return nil, nil, nil
	}}
	if err = service.ResolveConflict(context.Background(), opts, conflict.ID, SideCloud, 0); err == nil || opened {
		t.Fatalf("stale local candidate accepted: opened=%v err=%v", opened, err)
	}
}

func TestResolveConflictKeepsCloudCandidate(t *testing.T) {
	opts, registry := options(t)
	for _, dir := range []string{opts.ProfileDir, opts.LocalDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(opts.LocalDir, "a"), []byte("local candidate"), 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	localPath, localIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-upload-local", []byte("local candidate"))
	cloudPath, cloudIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-cloud-conflict-cloud", []byte("cloud candidate"))
	base := baselineFile("a", "remote-a", []byte("base"))
	remote := file("remote-a", "a")
	remote.Version, remote.HeadRevisionID, remote.ModifiedTime = "2", "rev-2", "2026-01-02T00:00:00Z"
	remote.Size, remote.MD5 = int64(len("cloud candidate")), md5Text("cloud candidate")
	conflict := Conflict{Path: "a", Baseline: base, LocalSHA256: hashText("local candidate"), LocalSize: int64(len("local candidate")), RemoteID: remote.ID, RemoteVersion: remote.Version, RemoteMD5: remote.MD5, RemoteSize: remote.Size, RemoteRevisionID: remote.HeadRevisionID, LocalStagePath: localPath, LocalStageIdentity: &localIdentity, CloudStagePath: cloudPath, CloudStageIdentity: &cloudIdentity, CloudSHA256: hashText("cloud candidate")}
	conflict.ID = conflictID(conflict)
	state := State{Version: 3, Binding: Binding{RemoteRoot: opts.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}, Completed: []Completed{base}, Conflicts: []Conflict{conflict}, PopulationComplete: true}
	if err = saveState(canonical.ProfileDir, state); err != nil {
		t.Fatal(err)
	}
	api := &uploadAPI{file: remote, data: []byte("cloud candidate"), revisions: []drive.Revision{{ID: "rev-2", MD5: remote.MD5, ModifiedTime: remote.ModifiedTime, Size: remote.Size, HasSize: true}}, pinned: map[string]bool{}}
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		return api, func() error { return nil }, nil
	}}
	if err = service.ResolveConflict(context.Background(), opts, conflict.ID, SideCloud, 0); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(opts.LocalDir, "a"))
	if err != nil || string(contents) != "cloud candidate" {
		t.Fatalf("cloud candidate was not installed: %q, %v", contents, err)
	}
	remaining, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("resolved conflict remains pending: %+v, %v", remaining, err)
	}
	updated, err := loadState(canonical.ProfileDir, state.Binding)
	if err != nil || len(updated.Completed) != 1 || updated.Completed[0].SHA256 != hashText("cloud candidate") || updated.Completed[0].HeadRevisionID != remote.HeadRevisionID || updated.Completed[0].ModifiedTime != remote.ModifiedTime {
		t.Fatalf("cloud resolution did not update baseline: %+v, %v", updated, err)
	}
	if err = os.WriteFile(filepath.Join(opts.LocalDir, "a"), []byte("later local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	opts.TwoWay = true
	if _, err = service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if api.updates != 1 || string(api.data) != "later local edit" {
		t.Fatal("a later local edit did not upload after cloud resolution")
	}
}

func TestResolveConflictUploadsLocalCandidate(t *testing.T) {
	opts, registry := options(t)
	for _, dir := range []string{opts.ProfileDir, opts.LocalDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(opts.LocalDir, "a"), []byte("local candidate"), 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	localPath, localIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-upload-local", []byte("local candidate"))
	cloudPath, cloudIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-cloud-conflict-cloud", []byte("cloud candidate"))
	base := baselineFile("a", "remote-a", []byte("base"))
	base.ModifiedTime, base.HeadRevisionID = "2026-01-01T00:00:00Z", "rev-1"
	remote := file("remote-a", "a")
	remote.Version, remote.HeadRevisionID, remote.ModifiedTime = "2", "rev-2", "2026-01-02T00:00:00Z"
	remote.Size, remote.MD5 = int64(len("cloud candidate")), md5Text("cloud candidate")
	conflict := Conflict{Path: "a", Baseline: base, LocalSHA256: hashText("local candidate"), LocalSize: int64(len("local candidate")), RemoteID: remote.ID, RemoteVersion: remote.Version, RemoteMD5: remote.MD5, RemoteSize: remote.Size, RemoteRevisionID: remote.HeadRevisionID, LocalStagePath: localPath, LocalStageIdentity: &localIdentity, CloudStagePath: cloudPath, CloudStageIdentity: &cloudIdentity, CloudSHA256: hashText("cloud candidate")}
	conflict.ID = conflictID(conflict)
	state := State{Version: 3, Binding: Binding{RemoteRoot: opts.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}, Completed: []Completed{base}, Conflicts: []Conflict{conflict}, PopulationComplete: true}
	if err = saveState(canonical.ProfileDir, state); err != nil {
		t.Fatal(err)
	}
	api := &uploadAPI{file: remote, data: []byte("cloud candidate"), revisions: []drive.Revision{{ID: "rev-2", MD5: remote.MD5, ModifiedTime: remote.ModifiedTime, Size: remote.Size, HasSize: true}}, pinned: map[string]bool{}}
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		return api, func() error { return nil }, nil
	}}
	if err = service.ResolveConflict(context.Background(), opts, conflict.ID, SideLocal, 0); err != nil {
		t.Fatal(err)
	}
	if string(api.data) != "local candidate" || api.file.HeadRevisionID != "rev-3" {
		t.Fatalf("local candidate was not uploaded: %q, %+v", api.data, api.file)
	}
	remaining, err := service.ListConflicts(context.Background(), opts)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("resolved conflict remains pending: %+v, %v", remaining, err)
	}
}

func TestConflictDetailsExposeSafeCandidateOrdinals(t *testing.T) {
	opts, registry := options(t)
	for _, dir := range []string{opts.ProfileDir, opts.LocalDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	localPath, localIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-upload-local", []byte("local"))
	cloudPath1, cloudIdentity1 := createConflictTestStage(t, canonical.ProfileDir, ".grove-cloud-conflict-one", []byte("first"))
	cloudPath2, cloudIdentity2 := createConflictTestStage(t, canonical.ProfileDir, ".grove-cloud-conflict-two", []byte("second"))
	base := baselineFile("a", "remote-a", []byte("base"))
	conflict := Conflict{Path: "a", Baseline: base, LocalSHA256: hashText("local"), LocalSize: 5, RemoteID: base.RemoteID, RemoteVersion: "2", RemoteRevisionID: "rev-2", RemoteMD5: md5Text("second"), RemoteSize: 6, LocalStagePath: localPath, LocalStageIdentity: &localIdentity, CandidateSetVersion: 1,
		CloudCandidates: []CloudCandidate{{RevisionID: "rev-1", ModifiedTime: "2026-01-01T00:00:00Z", MD5: md5Text("first"), Size: 5, SHA256: hashText("first"), StagePath: cloudPath1, StageIdentity: &cloudIdentity1}, {RevisionID: "rev-2", ModifiedTime: "2026-01-02T00:00:00Z", MD5: md5Text("second"), Size: 6, SHA256: hashText("second"), StagePath: cloudPath2, StageIdentity: &cloudIdentity2}}}
	conflict.ID = conflictID(conflict)
	state := State{Version: 3, Binding: Binding{RemoteRoot: opts.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}, Completed: []Completed{base}, Conflicts: []Conflict{conflict}, PopulationComplete: true}
	if err = saveState(canonical.ProfileDir, state); err != nil {
		t.Fatal(err)
	}
	service := Service{RegistryDir: registry}
	detail, err := service.ConflictDetails(context.Background(), opts, conflict.ID)
	if err != nil || detail.ID != conflict.ID || detail.Path != "a" || len(detail.Candidates) != 3 {
		t.Fatalf("details=%+v err=%v", detail, err)
	}
	if detail.Candidates[0].Ordinal != 0 || detail.Candidates[0].Side != SideLocal || detail.Candidates[0].Size != 5 || detail.Candidates[1].Ordinal != 1 || detail.Candidates[1].ModifiedTime != "2026-01-01T00:00:00Z" || detail.Candidates[2].Ordinal != 2 {
		t.Fatalf("unexpected safe candidate details: %+v", detail.Candidates)
	}
}

func TestResolveConflictRejectsMissingMultiCandidateOrdinal(t *testing.T) {
	opts, registry := options(t)
	if err := os.MkdirAll(opts.ProfileDir, 0700); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	localPath, localIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-upload-local", []byte("local"))
	cloudPath1, cloudIdentity1 := createConflictTestStage(t, canonical.ProfileDir, ".grove-cloud-conflict-one", []byte("first"))
	cloudPath2, cloudIdentity2 := createConflictTestStage(t, canonical.ProfileDir, ".grove-cloud-conflict-two", []byte("second"))
	base := baselineFile("a", "remote-a", []byte("base"))
	conflict := Conflict{Path: "a", Baseline: base, LocalSHA256: hashText("local"), LocalSize: 5, RemoteID: base.RemoteID, RemoteVersion: "2", RemoteMD5: md5Text("second"), RemoteSize: 6, LocalStagePath: localPath, LocalStageIdentity: &localIdentity, CandidateSetVersion: 1, CloudCandidates: []CloudCandidate{{RevisionID: "rev-1", MD5: md5Text("first"), Size: 5, SHA256: hashText("first"), StagePath: cloudPath1, StageIdentity: &cloudIdentity1}, {RevisionID: "rev-2", MD5: md5Text("second"), Size: 6, SHA256: hashText("second"), StagePath: cloudPath2, StageIdentity: &cloudIdentity2}}}
	conflict.ID = conflictID(conflict)
	if err = saveState(canonical.ProfileDir, State{Version: 3, Binding: Binding{RemoteRoot: opts.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}, Completed: []Completed{base}, Conflicts: []Conflict{conflict}, PopulationComplete: true}); err != nil {
		t.Fatal(err)
	}
	opened := false
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		opened = true
		return nil, nil, nil
	}}
	err = service.ResolveConflict(context.Background(), opts, conflict.ID, SideCloud, 0)
	if err == nil || opened {
		t.Fatalf("missing ordinal was accepted: opened=%v err=%v", opened, err)
	}
}

func TestResolveConflictUploadsSelectedMultiCandidate(t *testing.T) {
	opts, registry := options(t)
	for _, dir := range []string{opts.ProfileDir, opts.LocalDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(opts.LocalDir, "a"), []byte("local"), 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	localPath, localIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-upload-local", []byte("local"))
	oldPath, oldIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-cloud-conflict-old", []byte("old cloud"))
	headPath, headIdentity := createConflictTestStage(t, canonical.ProfileDir, ".grove-cloud-conflict-head", []byte("head cloud"))
	base := baselineFile("a", "remote-a", []byte("base"))
	remote := file("remote-a", "a")
	remote.Version, remote.HeadRevisionID, remote.ModifiedTime = "2", "rev-head", "2026-01-02T00:00:00Z"
	remote.Size, remote.MD5 = int64(len("head cloud")), md5Text("head cloud")
	conflict := Conflict{Path: "a", Baseline: base, LocalSHA256: hashText("local"), LocalSize: 5, RemoteID: remote.ID, RemoteVersion: remote.Version, RemoteRevisionID: remote.HeadRevisionID, RemoteMD5: remote.MD5, RemoteSize: remote.Size, LocalStagePath: localPath, LocalStageIdentity: &localIdentity, CandidateSetVersion: 1,
		CloudCandidates: []CloudCandidate{{RevisionID: "rev-old", ModifiedTime: "2026-01-01T00:00:00Z", MD5: md5Text("old cloud"), Size: 9, SHA256: hashText("old cloud"), StagePath: oldPath, StageIdentity: &oldIdentity}, {RevisionID: "rev-head", ModifiedTime: remote.ModifiedTime, MD5: remote.MD5, Size: remote.Size, SHA256: hashText("head cloud"), StagePath: headPath, StageIdentity: &headIdentity}}}
	conflict.ID = conflictID(conflict)
	state := State{Version: 3, Binding: Binding{RemoteRoot: opts.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}, Completed: []Completed{base}, Conflicts: []Conflict{conflict}, PopulationComplete: true}
	if err = saveState(canonical.ProfileDir, state); err != nil {
		t.Fatal(err)
	}
	api := &uploadAPI{file: remote, data: []byte("head cloud"), revisions: []drive.Revision{{ID: "rev-old", MD5: md5Text("old cloud"), ModifiedTime: "2026-01-01T00:00:00Z", Size: 9, HasSize: true}, {ID: "rev-head", MD5: remote.MD5, ModifiedTime: remote.ModifiedTime, Size: remote.Size, HasSize: true}}, pinned: map[string]bool{}}
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		return api, func() error { return nil }, nil
	}}
	if err = service.ResolveConflict(context.Background(), opts, conflict.ID, SideCloud, 1); err != nil {
		t.Fatal(err)
	}
	if got, readErr := os.ReadFile(filepath.Join(opts.LocalDir, "a")); readErr != nil || string(got) != "old cloud" {
		t.Fatalf("selected bytes not installed: %q err=%v", got, readErr)
	}
	if string(api.data) != "old cloud" || api.updates != 1 {
		t.Fatalf("selected bytes not uploaded as head: %q updates=%d", api.data, api.updates)
	}
	loaded, err := loadState(canonical.ProfileDir, state.Binding)
	if err != nil || len(loaded.Conflicts) != 0 {
		t.Fatalf("conflict not cleared after commit: %+v err=%v", loaded.Conflicts, err)
	}
	for _, stage := range []string{localPath, oldPath, headPath} {
		if _, err := os.Lstat(filepath.Join(canonical.ProfileDir, stage)); !os.IsNotExist(err) {
			t.Fatalf("resolved stage %q was retained: %v", stage, err)
		}
	}
}

func createConflictTestStage(t *testing.T, profile, name string, contents []byte) (string, FileIdentity) {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(profile, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(contents); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	info, err := file.Stat()
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	identity, err := fileIdentity(info)
	if err != nil {
		t.Fatal(err)
	}
	return name, identity
}

func hashText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func md5Text(value string) string { return md5Hex([]byte(value)) }
