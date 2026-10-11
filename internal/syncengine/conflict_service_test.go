package syncengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	err = service.ResolveConflict(context.Background(), opts, conflict.ID, SideLocal)
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
	if err = service.ResolveConflict(context.Background(), opts, conflict.ID, SideCloud); err == nil || opened {
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
	if err = service.ResolveConflict(context.Background(), opts, conflict.ID, SideCloud); err != nil {
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
	api := &uploadAPI{file: remote, data: []byte("cloud candidate"), revisions: []drive.Revision{{ID: "rev-2", MD5: remote.MD5, Size: remote.Size, HasSize: true}}, pinned: map[string]bool{}}
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		return api, func() error { return nil }, nil
	}}
	if err = service.ResolveConflict(context.Background(), opts, conflict.ID, SideLocal); err != nil {
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
