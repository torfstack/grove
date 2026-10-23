package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
)

func savedProfile(t *testing.T, complete bool) (Options, Binding, string) {
	t.Helper()
	opts, registry := options(t)
	canonical, err := canonicalOptions(opts)
	if err != nil {
		t.Fatal(err)
	}
	binding := Binding{RemoteRoot: canonical.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile}
	if err := saveState(canonical.ProfileDir, State{Version: 2, Binding: binding, PopulationComplete: complete}); err != nil {
		t.Fatal(err)
	}
	return canonical, binding, registry
}

func TestInspectExistingProfile(t *testing.T) {
	for _, shape := range []string{"complete", "incomplete", "download", "probe", "transaction"} {
		t.Run(shape, func(t *testing.T) {
			opts, binding, _ := savedProfile(t, shape != "incomplete")
			state, err := loadState(opts.ProfileDir, binding)
			if err != nil {
				t.Fatal(err)
			}
			switch shape {
			case "download":
				state.Pending = &Pending{Entry: Entry{Path: "a", Remote: file("a", "a")}, Phase: "intent", TempPath: ".grove-download-pending"}
			case "probe":
				state.ProbePath = ".grove-probe-pending"
			case "transaction":
				ex, _, _ := interruptedMetadataReplacement(t, false)
				state = *ex.state
				state.Binding = binding
			}
			if err := saveState(opts.ProfileDir, state); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			service := Service{OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
				t.Fatal("offline inspection opened credentials")
				return nil, nil, nil
			}}
			summary, err := service.InspectProfile(context.Background(), opts.ProfileDir)
			pending := shape == "download" || shape == "probe" || shape == "transaction"
			if err != nil || summary.Binding != binding || summary.PopulationComplete != state.PopulationComplete || summary.RecoveryPending != pending {
				t.Fatalf("wrong offline summary: %+v %v", summary, err)
			}
			after, err := os.ReadFile(filepath.Join(opts.ProfileDir, "state.json"))
			if err != nil || string(before) != string(after) {
				t.Fatal("inspection rewrote state")
			}
			lock, err := privatefs.Acquire(filepath.Join(opts.ProfileDir, "profile.lock"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.InspectProfile(context.Background(), opts.ProfileDir)
			_ = lock.Close()
			if !errors.Is(err, privatefs.ErrLocked) {
				t.Fatalf("busy profile not classified: %v", err)
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing")
		if _, err := (Service{}).InspectProfile(context.Background(), path); err == nil {
			t.Fatal("missing profile accepted")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("inspection created profile")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := (Service{}).InspectProfile(ctx, filepath.Join(t.TempDir(), "missing")); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	})
}

func TestInspectExistingProfileConflicts(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	remote.put("a", "a", "folder", "changed")
	if err := os.Remove(filepath.Join(opts.LocalDir, "old", "a")); err != nil {
		t.Fatal(err)
	}
	opts.TwoWay = true
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	service.OpenAPI = func(context.Context, string, string) (drive.API, func() error, error) {
		t.Fatal("inspection used Drive")
		return nil, nil, nil
	}
	summary, err := service.InspectProfile(context.Background(), opts.ProfileDir)
	if err != nil || summary.ConflictCount != 1 || summary.RecoveryPending {
		t.Fatalf("tombstone summary: %+v %v", summary, err)
	}
}

func TestRunExistingProfileLockAndBinding(t *testing.T) {
	opts, binding, registry := savedProfile(t, false)
	api := downloader()
	opened := 0
	service := Service{RegistryDir: registry, OpenAPI: func(_ context.Context, token, access string) (drive.API, func() error, error) {
		opened++
		if token != binding.TokenFile || access != "read-only" {
			t.Fatal("wrong session settings")
		}
		lock, err := privatefs.Acquire(filepath.Join(opts.ProfileDir, "profile.lock"))
		if lock != nil {
			_ = lock.Close()
		}
		if !errors.Is(err, privatefs.ErrLocked) {
			t.Fatalf("execution released profile lock: %v", err)
		}
		return api, func() error { return nil }, nil
	}}
	if result, err := service.RunExisting(context.Background(), opts.ProfileDir, binding, false); err != nil || result.Downloaded != 1 {
		t.Fatalf("existing download failed: %v", err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(opts.ProfileDir, alias); err != nil {
		t.Fatal(err)
	}
	if result, err := service.RunExisting(context.Background(), alias, binding, false); err != nil || result.Downloaded != 0 {
		t.Fatalf("alias rerun failed: %v", err)
	}
	before := opened
	state, err := loadState(opts.ProfileDir, binding)
	if err != nil {
		t.Fatal(err)
	}
	state.Binding.RemoteRoot = "changed-root"
	if err := saveState(opts.ProfileDir, state); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunExisting(context.Background(), opts.ProfileDir, binding, false); err == nil || opened != before {
		t.Fatal("rebound profile opened credentials")
	}
	if err := os.WriteFile(filepath.Join(opts.ProfileDir, "state.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunExisting(context.Background(), opts.ProfileDir, binding, false); err == nil || opened != before {
		t.Fatal("invalid profile opened credentials")
	}
	if err := os.Remove(filepath.Join(opts.ProfileDir, "state.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunExisting(context.Background(), opts.ProfileDir, binding, false); err == nil || opened != before {
		t.Fatal("missing state opened credentials")
	}
	if _, err := os.Stat(filepath.Join(opts.ProfileDir, "state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing state recreated")
	}
}

func TestRunExistingProfileModesAndRecovery(t *testing.T) {
	opts, binding, registry := savedProfile(t, false)
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		t.Fatal("incomplete two-way opened credentials")
		return nil, nil, nil
	}}
	if _, err := service.RunExisting(context.Background(), opts.ProfileDir, binding, true); err == nil {
		t.Fatal("incomplete two-way accepted")
	}
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	summary, err := service.InspectProfile(context.Background(), opts.ProfileDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(opts.LocalDir, "b")); err != nil {
		t.Fatal(err)
	}
	if result, err := service.RunExisting(context.Background(), opts.ProfileDir, summary.Binding, true); err != nil || result.Trashed != 1 || !remote.files["b"].Trashed {
		t.Fatalf("two-way forwarding failed: %v", err)
	}
}

func TestRunExistingProfileUncertainWrite(t *testing.T) {
	opts, binding, registry := savedProfile(t, true)
	if err := os.MkdirAll(opts.LocalDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opts.LocalDir, "a"), []byte("edit"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(opts.LocalDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	remote := file("a", "a")
	remote.Size = 3
	remote.MD5 = md5Text("old")
	baseline := baselineFile("a", "a", []byte("old"))
	baseline.ModifiedTime = remote.ModifiedTime
	baseline.HeadRevisionID = remote.HeadRevisionID
	state := State{Version: 3, Binding: binding, PopulationComplete: true, Completed: []Completed{baseline}}
	api := &uploadAPI{file: remote, pinned: map[string]bool{remote.HeadRevisionID: true}}
	ex := executor{root: root, profileDir: opts.ProfileDir, api: api, mutation: api, state: &state, save: func() error { return saveState(opts.ProfileDir, state) }, resumeUploads: true}
	op := Operation{Kind: OpUpload, Entry: Entry{Path: "a", Remote: remote}, Local: LocalEntry{Path: "a", Kind: "file", SHA256: hashText("edit"), Size: 4}, Before: []Completed{baseline}}
	if err := ex.prepareUpload(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	j := *state.Transaction
	j.Phase = "upload-intent"
	j.PinnedRevisionID = remote.HeadRevisionID
	if err := ex.journal(j); err != nil {
		t.Fatal(err)
	}
	service := Service{RegistryDir: registry, OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		return api, func() error { return nil }, nil
	}}
	for i := 0; i < 2; i++ {
		if _, err := service.RunExisting(context.Background(), opts.ProfileDir, binding, true); !errors.Is(err, errUploadPending) {
			t.Fatalf("uncertain upload not blocked: %v", err)
		}
		loaded, err := loadState(opts.ProfileDir, binding)
		if err != nil || loaded.Transaction == nil || loaded.Transaction.StagePath != j.StagePath || api.updates != 0 {
			t.Fatalf("uncertain intent changed: %v", err)
		}
	}
	if err := verifyPrivateStage(opts.ProfileDir, j.StagePath, j.StageIdentity, j.StageSHA256, j.StageMD5, j.StageSize); err != nil {
		t.Fatal(err)
	}
}

func TestRunExistingProfileTrashRecovery(t *testing.T) {
	service, opts, remote := httpService(t)
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	summary, err := service.InspectProfile(context.Background(), opts.ProfileDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(opts.LocalDir, "old")); err != nil {
		t.Fatal(err)
	}
	remote.failTrashVerificationID = "a"
	if _, err := service.RunExisting(context.Background(), opts.ProfileDir, summary.Binding, true); err == nil {
		t.Fatal("trash verification failure ignored")
	}
	if remote.mutations != 1 {
		t.Fatal("unexpected mutation count")
	}
	remote.failTrashVerificationID = ""
	result, err := service.RunExisting(context.Background(), opts.ProfileDir, summary.Binding, false)
	if err != nil || result.Trashed != 2 || remote.mutations != 1 {
		t.Fatalf("trash repeated or recovery lost: %+v %v", result, err)
	}
}
