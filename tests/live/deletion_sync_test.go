package live

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/auth"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/fixture"
	"github.com/torfstack/grove/internal/syncengine"
)

func TestLocalDeletionMovesFixtureObjectsToTrashLive(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if err := os.MkdirAll(cfg.Runs, 0700); err != nil {
		t.Fatal("cannot create live runs directory")
	}
	base, err := os.MkdirTemp(cfg.Runs, "deletion-sync-")
	if err != nil {
		t.Fatal("cannot create private live run")
	}
	runDir := filepath.Join(base, "fixture")
	cleanup := false
	t.Cleanup(func() {
		if cleanup {
			if cleanupErr := fixtureAPI(context.Background(), cfg.Token, runDir, "read-write", func(api drive.API) error { return fixture.Cleanup(context.Background(), api, runDir) }); cleanupErr != nil {
				t.Errorf("cannot clean up deletion fixture: %v", cleanupErr)
			}
		} else {
			t.Logf("Failed run retained at %s", filepath.Join(runDir, "run.json"))
		}
	})
	manifest, err := fixture.LoadManifest("../../testdata/fixtures/baseline/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var run fixture.Run
	if err = fixtureAPI(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error {
		var seedErr error
		run, seedErr = fixture.Seed(ctx, api, manifest, runDir)
		if seedErr != nil {
			return seedErr
		}
		_, seedErr = fixture.Inspect(ctx, api, runDir)
		return seedErr
	}); err != nil {
		t.Fatalf("cannot seed deletion fixture: %v", err)
	}
	cleanup = true
	rootID, fileID, folderID := "", "", ""
	for _, object := range run.Objects {
		switch object.LogicalID {
		case "root":
			rootID = object.RemoteID
		case "empty.bin":
			fileID = object.RemoteID
		case "nested":
			folderID = object.RemoteID
		}
	}
	if rootID == "" || fileID == "" || folderID == "" {
		t.Fatal("fixture is missing deletion targets")
	}
	var trashRequests atomic.Int64
	service := syncengine.Service{RegistryDir: filepath.Join(base, "registry"), OpenAPI: func(ctx context.Context, token, access string) (drive.API, func() error, error) {
		session, openErr := auth.OpenSession(ctx, token, access)
		if openErr != nil {
			return nil, nil, openErr
		}
		return countedTrashAPI{SyncMutationAPI: drive.NewClient(session.HTTPClient, drive.ClientOptions{PageSize: 2}), requests: &trashRequests}, session.Close, nil
	}}
	opts := syncengine.Options{ProfileDir: filepath.Join(base, "profile"), RemoteRoot: rootID, LocalDir: filepath.Join(base, "local"), TokenFile: cfg.Token}
	if _, err = service.Run(ctx, opts); err != nil {
		t.Fatalf("cannot populate profile: %v", err)
	}
	opts.TwoWay = true
	if err = os.Remove(filepath.Join(opts.LocalDir, "empty.bin")); err != nil {
		t.Fatal("cannot remove fixture file locally")
	}
	if err = os.RemoveAll(filepath.Join(opts.LocalDir, "nested")); err != nil {
		t.Fatal("cannot remove fixture folder locally")
	}
	result, err := runWithTrashRecoveryRetry(ctx, service, opts)
	if err != nil {
		t.Fatalf("two-way deletion sync failed: %v", err)
	}
	if result.Trashed != 4 {
		t.Fatalf("unexpected trashed item count: %+v", result)
	}
	if trashRequests.Load() != 2 {
		t.Fatalf("trash recovery repeated a remote mutation: %d requests", trashRequests.Load())
	}
	if err = fixtureAPI(ctx, cfg.Token, runDir, "read-only", func(api drive.API) error {
		file, getErr := api.Get(ctx, fileID)
		if getErr != nil || !file.ExplicitlyTrashed {
			t.Fatalf("deleted file is not explicitly trashed: %v", getErr)
		}
		folder, getErr := api.Get(ctx, folderID)
		if getErr != nil || !folder.ExplicitlyTrashed {
			t.Fatalf("deleted folder is not explicitly trashed: %v", getErr)
		}
		for _, object := range run.Objects {
			if object.LogicalID != "hello.txt" && object.LogicalID != "deep" {
				continue
			}
			child, childErr := api.Get(ctx, object.RemoteID)
			if childErr != nil || !child.Trashed {
				t.Fatalf("folder descendant is not inherited as trashed: %s %v", object.LogicalID, childErr)
			}
		}
		return nil
	}); err != nil {
		t.Fatal("cannot verify Drive Trash state")
	}
	result, err = service.Run(ctx, opts)
	if err != nil || result.Trashed != 0 || result.Downloaded != 0 || result.Uploaded != 0 || result.Updated != 0 || result.Moved != 0 {
		t.Fatalf("unchanged deletion profile was not a no-op: %+v err=%v", result, err)
	}
	t.Log("Live deletion workflow verified with fixture-owned objects")
}

type countedTrashAPI struct {
	drive.SyncMutationAPI
	requests *atomic.Int64
}

func (a countedTrashAPI) Trash(ctx context.Context, id string) error {
	a.requests.Add(1)
	return a.SyncMutationAPI.Trash(ctx, id)
}

func runWithTrashRecoveryRetry(ctx context.Context, service syncengine.Service, opts syncengine.Options) (syncengine.Result, error) {
	var total syncengine.Result
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		result, runErr := service.Run(ctx, opts)
		total.Trashed += result.Trashed
		if runErr == nil {
			return total, nil
		}
		err = runErr
		if !strings.Contains(err.Error(), "trash") && !strings.Contains(err.Error(), "drive request failed") && !strings.Contains(err.Error(), "HTTP 5") {
			return total, err
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return total, err
}
