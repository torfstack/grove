package live

import (
	"bytes"
	"context"
	"io"
	"net/http"
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

func TestTwoWayUploadAndConcurrentRevisionRecoveryLive(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if err := os.MkdirAll(cfg.Runs, 0700); err != nil {
		t.Fatal("cannot create live runs directory")
	}
	base, err := os.MkdirTemp(cfg.Runs, "upload-sync-")
	if err != nil {
		t.Fatal("cannot create private live run")
	}
	runDir := filepath.Join(base, "fixture")
	cleanup := false
	t.Cleanup(func() {
		if cleanup {
			if err := fixtureAPI(context.Background(), cfg.Token, runDir, "read-write", func(api drive.API) error { return fixture.Cleanup(context.Background(), api, runDir) }); err != nil {
				t.Errorf("cannot clean up upload fixture: %v", err)
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
		t.Fatal("cannot seed upload fixture")
	}
	rootID, targetID := "", ""
	for _, object := range run.Objects {
		if object.ParentID == "" {
			rootID = object.RemoteID
		}
		if object.LogicalID == "hello.txt" {
			targetID = object.RemoteID
		}
	}
	if rootID == "" || targetID == "" {
		t.Fatal("upload fixture is missing its root or target file")
	}
	time.Sleep(2 * time.Second)
	var injectRace atomic.Bool
	var raceInjected atomic.Bool
	service := syncengine.Service{RegistryDir: filepath.Join(base, "registry"), OpenAPI: func(ctx context.Context, token, access string) (drive.API, func() error, error) {
		session, err := auth.OpenSession(ctx, token, access)
		if err != nil {
			return nil, nil, err
		}
		client := *session.HTTPClient
		if injectRace.Load() && access == "read-write" {
			baseTransport := client.Transport
			if baseTransport == nil {
				baseTransport = http.DefaultTransport
			}
			client.Transport = competingEditTransport{base: baseTransport, client: drive.NewClient(&http.Client{Transport: baseTransport}, drive.ClientOptions{}), fileID: targetID, bytes: []byte("competing remote edit"), injected: &raceInjected}
		}
		return drive.NewClient(&client, drive.ClientOptions{PageSize: 2}), session.Close, nil
	}}
	opts := syncengine.Options{ProfileDir: filepath.Join(base, "profile"), RemoteRoot: rootID, LocalDir: filepath.Join(base, "local"), TokenFile: cfg.Token}
	if _, err = service.Run(ctx, opts); err != nil {
		t.Fatalf("cannot populate upload profile: %v", err)
	}
	opts.TwoWay = true
	localPath := filepath.Join(opts.LocalDir, "nested", "hello.txt")
	localBytes := []byte("first local edit")
	if err = os.WriteFile(localPath, localBytes, 0600); err != nil {
		t.Fatal("cannot edit local fixture file")
	}
	var result syncengine.Result
	if result, err = runWithRevisionRetry(ctx, service, opts); err != nil {
		t.Fatalf("ordinary local upload failed: %v", err)
	}
	if result.Updated != 1 {
		t.Fatalf("ordinary local edit was not planned as an upload: %+v", result)
	}
	var remote []byte
	if err = fixtureAPI(ctx, cfg.Token, runDir, "read-only", func(api drive.API) error {
		var data bytes.Buffer
		if downloadErr := api.Download(ctx, targetID, &data); downloadErr != nil {
			return downloadErr
		}
		remote = data.Bytes()
		return nil
	}); err != nil || !bytes.Equal(remote, localBytes) {
		t.Fatalf("ordinary local edit was not uploaded: bytes=%q err=%v", remote, err)
	}
	secondLocalBytes := []byte("second local edit")
	if err = os.WriteFile(localPath, secondLocalBytes, 0600); err != nil {
		t.Fatal("cannot make a later local edit")
	}
	injectRace.Store(true)
	if result, err = runWithRevisionRetry(ctx, service, opts); err != nil {
		t.Fatalf("two-way sync did not recover the concurrent revision: %v", err)
	}
	if result.Updated != 1 {
		t.Fatalf("later local edit was not planned as an upload: %+v", result)
	}
	if !raceInjected.Load() {
		t.Fatal("test did not inject a remote edit during Grove's upload")
	}
	conflicts, err := service.ListConflicts(ctx, opts)
	if err != nil || len(conflicts) != 1 {
		t.Fatalf("concurrent revision was not surfaced as one conflict: %+v err=%v", conflicts, err)
	}
	conflict := conflicts[0]
	if conflict.CloudRevisionID == "" || conflict.CloudRevisionID == conflict.RemoteRevisionID {
		t.Fatalf("conflict did not distinguish the competing revision: %+v", conflict)
	}
	cloudCandidate, err := os.ReadFile(filepath.Join(opts.ProfileDir, conflict.CloudStagePath))
	if err != nil || !bytes.Equal(cloudCandidate, []byte("competing remote edit")) {
		t.Fatalf("competing revision was not captured exactly: %q err=%v", cloudCandidate, err)
	}
	if err = service.ResolveConflict(ctx, opts, conflict.ID, syncengine.SideCloud, 0); err != nil {
		t.Fatalf("cannot resolve conflict with the competing cloud candidate: %v", err)
	}
	localResult, err := os.ReadFile(localPath)
	if err != nil || !bytes.Equal(localResult, []byte("competing remote edit")) {
		t.Fatalf("cloud candidate was not installed locally: %q err=%v", localResult, err)
	}
	if err = fixtureAPI(ctx, cfg.Token, runDir, "read-only", func(api drive.API) error {
		var data bytes.Buffer
		if downloadErr := api.Download(ctx, targetID, &data); downloadErr != nil {
			return downloadErr
		}
		remote = data.Bytes()
		return nil
	}); err != nil || !bytes.Equal(remote, []byte("competing remote edit")) {
		t.Fatalf("selected cloud candidate was not uploaded to Drive: %q err=%v", remote, err)
	}
	remaining, err := service.ListConflicts(ctx, opts)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("resolved conflict remains pending: %+v err=%v", remaining, err)
	}
	cleanup = true
}

func runWithRevisionRetry(ctx context.Context, service syncengine.Service, opts syncengine.Options) (syncengine.Result, error) {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		result, runErr := service.Run(ctx, opts)
		if runErr == nil {
			return result, nil
		}
		err = runErr
		if !strings.Contains(err.Error(), "revision history") && !strings.Contains(err.Error(), "pending upload requires explicit two-way resume") && !strings.Contains(err.Error(), "cannot release preserved preflight revision") {
			return syncengine.Result{}, err
		}
		select {
		case <-ctx.Done():
			return syncengine.Result{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return syncengine.Result{}, err
}

type competingEditTransport struct {
	base     http.RoundTripper
	client   drive.API
	fileID   string
	bytes    []byte
	injected *atomic.Bool
}

func (t competingEditTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPatch && request.URL.Query().Get("uploadType") == "resumable" && t.injected.CompareAndSwap(false, true) {
		mutation, ok := t.client.(drive.MutationAPI)
		if !ok {
			return nil, io.ErrUnexpectedEOF
		}
		if _, err := mutation.Update(request.Context(), t.fileID, drive.Update{ContentChanged: true}, bytes.NewReader(t.bytes)); err != nil {
			return nil, err
		}
	}
	return t.base.RoundTrip(request)
}
