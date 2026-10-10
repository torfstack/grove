package live

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/auth"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/fixture"
	"github.com/torfstack/grove/internal/syncengine"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIncrementalSyncLive(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := os.MkdirAll(cfg.Runs, 0700); err != nil {
		t.Fatal("cannot create live runs directory")
	}
	base, err := os.MkdirTemp(cfg.Runs, "incremental-sync-")
	if err != nil {
		t.Fatal("cannot create private live run")
	}
	runDir := filepath.Join(base, "fixture")
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Failed run retained at %s", filepath.Join(runDir, "run.json"))
		}
	})
	manifest, err := fixture.LoadManifest("../../testdata/fixtures/baseline/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var run fixture.Run
	err = fixtureAPI(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error {
		var err error
		run, err = fixture.Seed(ctx, api, manifest, runDir)
		if err != nil {
			return err
		}
		_, err = fixture.Inspect(ctx, api, runDir)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rootID := ""
	for _, o := range run.Objects {
		if o.ParentID == "" {
			rootID = o.RemoteID
		}
	}
	opts := syncengine.Options{ProfileDir: filepath.Join(base, "profile"), RemoteRoot: rootID, LocalDir: filepath.Join(base, "local"), TokenFile: cfg.Token}
	counter := &mediaCounter{}
	service := syncengine.Service{RegistryDir: filepath.Join(base, "registry"), OpenAPI: func(ctx context.Context, token string) (drive.API, func() error, error) {
		session, err := auth.OpenSession(ctx, token, "read-only")
		if err != nil {
			return nil, nil, err
		}
		counter.base = session.HTTPClient.Transport
		client := *session.HTTPClient
		client.Transport = counter
		return drive.NewClient(&client, drive.ClientOptions{PageSize: 2}), session.Close, nil
	}}
	if _, err = service.Run(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if _, err = fixture.VerifyLocal(ctx, manifest, opts.LocalDir); err != nil {
		t.Fatal(err)
	}
	target, err := fixture.LoadManifest("../../testdata/fixtures/baseline/incremental.json")
	if err != nil {
		t.Fatal(err)
	}
	err = fixtureAPI(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error {
		mutation, ok := api.(drive.MutationAPI)
		if !ok {
			return errors.New("fixture mutation unavailable")
		}
		return fixture.ApplyChanges(ctx, mutation, runDir, target)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = fixtureAPI(ctx, cfg.Token, runDir, "read-only", func(api drive.API) error { _, err := fixture.Inspect(ctx, api, runDir); return err })
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Downloaded != 2 || result.Updated != 1 || result.Moved != 3 {
		t.Fatalf("unexpected incremental counts: %+v", result)
	}
	if _, err = fixture.VerifyLocal(ctx, target, opts.LocalDir); err != nil {
		t.Fatal(err)
	}
	before := counter.calls.Load()
	times := map[string]time.Time{}
	if err = filepath.WalkDir(opts.LocalDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err == nil {
			times[path] = info.ModTime()
		}
		return err
	}); err != nil {
		t.Fatal("cannot inspect downloaded tree")
	}
	result, err = service.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Downloaded != 0 || result.CreatedDirectories != 0 || counter.calls.Load() != before {
		t.Fatal("second run performed unnecessary transfers")
	}
	for path, want := range times {
		info, err := os.Stat(path)
		if err != nil || !info.ModTime().Equal(want) {
			t.Fatal("second run modified downloaded tree")
		}
	}
	if _, err = fixture.VerifyLocal(ctx, target, opts.LocalDir); err != nil {
		t.Fatal(err)
	}
	if err = fixtureAPI(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error { return fixture.Cleanup(ctx, api, runDir) }); err != nil {
		t.Fatal(err)
	}
	t.Logf("Live workflow verified; private run record: %s", filepath.Join(runDir, "run.json"))
}
