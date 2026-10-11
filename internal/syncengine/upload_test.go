package syncengine

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageUploadCopiesAndFingerprintsLocalFile(t *testing.T) {
	localDir := t.TempDir()
	profileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(localDir, "file.txt"), []byte("local snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	dataWant := []byte("local snapshot")
	wantSHA := sha256.Sum256(dataWant)
	ex := executor{root: root, profileDir: profileDir}
	stage, err := ex.stageUpload(context.Background(), LocalEntry{Path: "file.txt", Kind: "file", SHA256: hex.EncodeToString(wantSHA[:]), Size: 14})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(profileDir, stage.Path))
	if err != nil || string(data) != "local snapshot" {
		t.Fatalf("stage bytes %q: %v", data, err)
	}
	sha := sha256.Sum256(data)
	md5sum := md5.Sum(data)
	if stage.Size != int64(len(data)) || stage.SHA256 != hex.EncodeToString(sha[:]) || stage.MD5 != hex.EncodeToString(md5sum[:]) || stage.Identity.Device == 0 || stage.Identity.Inode == 0 {
		t.Fatalf("stage fingerprint %+v", stage)
	}
	info, err := os.Stat(filepath.Join(profileDir, stage.Path))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("stage mode %v: %v", info.Mode(), err)
	}
	if err = os.WriteFile(filepath.Join(localDir, "file.txt"), []byte("newer local edit"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(profileDir, stage.Path))
	if err != nil || string(data) != "local snapshot" {
		t.Fatalf("staged snapshot changed with source: %q %v", data, err)
	}
}

func TestPrepareUploadPersistsStageOwnership(t *testing.T) {
	localDir := t.TempDir()
	profileDir := t.TempDir()
	data := []byte("local snapshot")
	if err := os.WriteFile(filepath.Join(localDir, "file.txt"), data, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	sha := sha256.Sum256(data)
	baseline := baselineFile("file.txt", "file-id", []byte("remote base"))
	baseline.HeadRevisionID = "revision-1"
	remote := file("file-id", "file.txt")
	remote.HeadRevisionID = "revision-1"
	state := State{Version: 3, Completed: []Completed{baseline}}
	ex := executor{root: root, profileDir: profileDir, state: &state, save: func() error { return saveState(profileDir, state) }}
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "file.txt", Remote: remote}, Local: LocalEntry{Path: "file.txt", Kind: "file", SHA256: hex.EncodeToString(sha[:]), Size: int64(len(data))}, Before: []Completed{baseline}}
	if err = ex.prepareUpload(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if state.Transaction == nil || state.Transaction.Phase != "staged" || state.Transaction.StageIdentity == nil {
		t.Fatalf("upload stage ownership was not journaled: %+v", state.Transaction)
	}
	loaded, err := loadState(profileDir, Binding{})
	if err != nil || loaded.Transaction == nil || loaded.Transaction.StagePath != state.Transaction.StagePath {
		t.Fatalf("durable upload journal did not validate: %v", err)
	}
}

func TestPrepareUploadSaveFailurePreservesUnownedStage(t *testing.T) {
	localDir := t.TempDir()
	profileDir := t.TempDir()
	data := []byte("local snapshot")
	if err := os.WriteFile(filepath.Join(localDir, "file.txt"), data, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	sha := sha256.Sum256(data)
	baseline := baselineFile("file.txt", "file-id", []byte("remote base"))
	baseline.HeadRevisionID = "revision-1"
	remote := file("file-id", "file.txt")
	remote.HeadRevisionID = "revision-1"
	state := State{Version: 3, Completed: []Completed{baseline}}
	ex := executor{root: root, profileDir: profileDir, state: &state, save: func() error { return os.ErrPermission }}
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "file.txt", Remote: remote}, Local: LocalEntry{Path: "file.txt", Kind: "file", SHA256: hex.EncodeToString(sha[:]), Size: int64(len(data))}, Before: []Completed{baseline}}
	if err = ex.prepareUpload(context.Background(), op); err == nil {
		t.Fatal("stage ownership save failure was ignored")
	}
	entries, err := os.ReadDir(profileDir)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), ".grove-upload-") {
		t.Fatalf("unowned stage was not preserved for inspection: %v %v", entries, err)
	}
}

func TestStageUploadRejectsBytesDifferentFromScan(t *testing.T) {
	localDir := t.TempDir()
	profileDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(localDir, "file.txt"), []byte("changed after scan"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	ex := executor{root: root, profileDir: profileDir}
	_, err = ex.stageUpload(context.Background(), LocalEntry{Path: "file.txt", Kind: "file", SHA256: strings.Repeat("0", 64), Size: 18})
	if err == nil {
		t.Fatal("stage accepted bytes that differ from the scan")
	}
	entries, err := os.ReadDir(profileDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed stage was not cleaned by verified identity: %v %v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(localDir, "file.txt"))
	if err != nil || string(data) != "changed after scan" {
		t.Fatalf("source candidate changed: %q %v", data, err)
	}
}

func TestRecoverUploadPreservesStageAndRequiresTwoWayResume(t *testing.T) {
	localDir := t.TempDir()
	profileDir := t.TempDir()
	data := []byte("local snapshot")
	if err := os.WriteFile(filepath.Join(localDir, "file.txt"), data, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(localDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	sha := sha256.Sum256(data)
	baseline := baselineFile("file.txt", "file-id", []byte("remote base"))
	baseline.HeadRevisionID = "revision-1"
	remote := file("file-id", "file.txt")
	remote.HeadRevisionID = "revision-1"
	state := State{Version: 3, Completed: []Completed{baseline}}
	ex := executor{root: root, profileDir: profileDir, state: &state, save: func() error { return saveState(profileDir, state) }}
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "file.txt", Remote: remote}, Local: LocalEntry{Path: "file.txt", Kind: "file", SHA256: hex.EncodeToString(sha[:]), Size: int64(len(data))}, Before: []Completed{baseline}}
	if err = ex.prepareUpload(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	err = ex.recover(context.Background(), Snapshot{})
	if !errors.Is(err, errUploadPending) {
		t.Fatalf("pending upload returned %v, want explicit two-way resume error", err)
	}
	stagePath := filepath.Join(profileDir, state.Transaction.StagePath)
	staged, readErr := os.ReadFile(stagePath)
	if readErr != nil || string(staged) != string(data) {
		t.Fatalf("pending upload stage was not preserved: %q %v", staged, readErr)
	}
	if state.Transaction == nil || state.Transaction.StageIdentity == nil {
		t.Fatal("pending upload intent was discarded")
	}
}
