package syncengine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/torfstack/grove/internal/drive"
)

type cancellingMutation struct {
	*uploadAPI
	cancel context.CancelCauseFunc
	cause  error
	calls  int
}

func (a *cancellingMutation) List(ctx context.Context, q, token string) (drive.Page, error) {
	if a.file.ID == "" {
		return drive.Page{}, nil
	}
	return a.uploadAPI.List(ctx, q, token)
}
func (*cancellingMutation) GenerateIDs(context.Context, int) ([]string, error) {
	return []string{"allocated"}, nil
}
func (a *cancellingMutation) CreateWithID(context.Context, string, drive.Create, io.Reader, int64) (drive.File, error) {
	a.calls++
	a.cancel(a.cause)
	return drive.File{}, a.cause
}
func (a *cancellingMutation) UpdateContent(context.Context, string, drive.Update, io.Reader, int64) (drive.File, error) {
	a.calls++
	a.cancel(a.cause)
	return drive.File{}, a.cause
}

func TestRunExistingMutationCancellation(t *testing.T) {
	for _, scenario := range []string{"upload", "file-create", "folder-create", "upload-cleanup-failure"} {
		t.Run(scenario, func(t *testing.T) {
			kind := scenario
			cleanupFailure := scenario == "upload-cleanup-failure"
			if cleanupFailure {
				kind = "upload"
			}
			opts, binding, registry := savedProfile(t, true)
			if err := os.MkdirAll(opts.LocalDir, 0700); err != nil {
				t.Fatal(err)
			}
			state := State{Version: 3, Binding: binding, PopulationComplete: true}
			api := &uploadAPI{pinned: map[string]bool{}}
			if kind == "upload" {
				remote := file("a", "a")
				remote.Size = 3
				remote.MD5 = md5Text("old")
				api.file = remote
				api.data = []byte("old")
				api.revisions = []drive.Revision{{ID: remote.HeadRevisionID, MD5: remote.MD5, ModifiedTime: remote.ModifiedTime, Size: 3, HasSize: true, KeepForever: true}}
				api.pinned[remote.HeadRevisionID] = true
				base := baselineFile("a", "a", []byte("old"))
				base.ModifiedTime = remote.ModifiedTime
				base.HeadRevisionID = remote.HeadRevisionID
				state.Completed = []Completed{base}
			}
			if kind == "folder-create" {
				if err := os.Mkdir(filepath.Join(opts.LocalDir, "a"), 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(opts.LocalDir, "a"), []byte("edit"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := saveState(opts.ProfileDir, state); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			cause := errors.New("private-cancellation-detail")
			mutation := &cancellingMutation{uploadAPI: api, cancel: cancel, cause: cause}
			service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
				return mutation, func() error {
					if cleanupFailure {
						return errors.New("private-cleanup-detail")
					}
					return nil
				}, nil
			}}
			_, err := service.RunExisting(ctx, opts.ProfileDir, binding, true)
			if !errors.Is(err, cause) || strings.Contains(err.Error(), "private-") {
				t.Fatalf("mutation cancellation lost or exposed: %v", err)
			}
			if cleanupFailure && !strings.Contains(err.Error(), "cannot close sync resource") {
				t.Fatal("cleanup failure disappeared behind cancellation")
			}
			loaded, err := loadState(opts.ProfileDir, binding)
			want := "create-intent"
			if kind == "upload" {
				want = "upload-intent"
			}
			if err != nil || loaded.Transaction == nil || loaded.Transaction.Phase != want || mutation.calls != 1 {
				t.Fatal("uncertain intent was discarded or mutation repeated")
			}
			if kind != "folder-create" {
				j := loaded.Transaction
				if err := verifyPrivateStage(opts.ProfileDir, j.StagePath, j.StageIdentity, j.StageSHA256, j.StageMD5, j.StageSize); err != nil {
					t.Fatal("cancelled mutation lost its verified stage")
				}
			}
		})
	}
}

type cancellingCheck struct {
	context.Context
	cancel     context.CancelFunc
	checks, at int
}

func (c *cancellingCheck) Err() error {
	c.checks++
	if c.checks == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func TestUploadStagingCancellation(t *testing.T) {
	for _, at := range []int{2, 4} {
		t.Run(string(rune('0'+at)), func(t *testing.T) {
			local, profile := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(local, "a"), []byte("edit"), 0600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(local)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checked := &cancellingCheck{Context: ctx, cancel: cancel, at: at}
			ex := executor{root: root, profileDir: profile}
			_, err = ex.stageUpload(checked, LocalEntry{Path: "a", Kind: "file", Size: 4, SHA256: hashText("edit")})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("staging cancellation lost: %v (checks %d)", err, checked.checks)
			}
		})
	}
}

type cancelledReadAPI struct {
	*uploadAPI
	cause error
}

func (a cancelledReadAPI) Download(context.Context, string, io.Writer) error { return a.cause }
func (a cancelledReadAPI) Get(ctx context.Context, id string) (drive.File, error) {
	if id == "child" {
		return drive.File{}, a.cause
	}
	return a.uploadAPI.Get(ctx, id)
}

func TestConflictCancellation(t *testing.T) {
	for _, step := range []string{"local-check", "cloud-download"} {
		t.Run(step, func(t *testing.T) {
			local, profile := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(local, "a"), []byte("edit"), 0600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(local)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			state := State{Version: 3}
			remote := file("a", "a")
			remote.Size = 3
			remote.MD5 = md5Text("old")
			conflict := Conflict{ID: "conflict", Path: "a", RemoteID: "a", LocalSize: 4, LocalSHA256: hashText("edit"), RemoteSize: 3, RemoteMD5: remote.MD5}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if step == "local-check" {
				conflict.Baseline = baselineFile("a", "a", []byte("old"))
				cancel()
			}
			api := cancelledReadAPI{uploadAPI: &uploadAPI{file: remote}, cause: context.Canceled}
			ex := executor{root: root, profileDir: profile, api: api, state: &state, save: func() error { return nil }}
			err = ex.recordConflict(ctx, Operation{Kind: OpConflict, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", Size: 4, SHA256: hashText("edit")}, Conflict: &conflict})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("conflict cancellation lost: %v", err)
			}
			if len(state.Conflicts) != 0 {
				t.Fatal("partial conflict was committed")
			}
		})
	}
}

func TestTrashDescendantCancellation(t *testing.T) {
	remote := drive.File{ID: "folder", Name: "folder", MIMEType: drive.FolderMIME, Parents: []string{"root"}, OwnedByMe: true, Trashed: true, ExplicitlyTrashed: true, Version: "1"}
	entry := Entry{Path: "folder", Remote: remote}
	base := completedEntry(entry, "")
	child := baselineFile("folder/a", "child", []byte("old"))
	state := State{Version: 3, Completed: []Completed{base, child}, Transaction: &Journal{Operation: Operation{Kind: OpTrash, Entry: entry, Before: []Completed{base, child}, RemoteSubtree: []Completed{base, child}}, Phase: "trash-intent"}}
	api := cancelledReadAPI{uploadAPI: &uploadAPI{file: remote}, cause: context.Canceled}
	ex := executor{api: api, state: &state}
	if err := ex.recoverTrash(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("descendant cancellation lost: %v", err)
	}
	if state.Transaction == nil || len(state.Completed) != 2 {
		t.Fatal("unconfirmed trash discarded intent")
	}
}

func TestPrivateStageVerificationCancellation(t *testing.T) {
	local, profile := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(local, "a"), []byte("edit"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(local)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	ex := executor{root: root, profileDir: profile}
	stage, err := ex.stageUpload(context.Background(), LocalEntry{Path: "a", Kind: "file", Size: 4, SHA256: hashText("edit")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	checked := &cancellingCheck{Context: ctx, cancel: cancel, at: 2}
	err = verifyPrivateStageContext(checked, profile, stage.Path, &stage.Identity, stage.SHA256, stage.MD5, stage.Size)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("private-stage cancellation lost: %v", err)
	}
	if err := verifyPrivateStage(profile, stage.Path, &stage.Identity, stage.SHA256, stage.MD5, stage.Size); err != nil {
		t.Fatal("cancelled verification changed the stage")
	}
}
