package live

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/auth"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/fixture"
	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type config struct{ Token, Runs string }

func outsideGit(path string) bool {
	for current := path; ; current = filepath.Dir(current) {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return false
		}
		if filepath.Dir(current) == current {
			return true
		}
	}
}
func contains(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}
func liveConfig(enabled, token, runs, defaultToken string) (config, bool, error) {
	if enabled != "1" {
		return config{}, false, nil
	}
	if !filepath.IsAbs(token) || !filepath.IsAbs(runs) {
		return config{}, true, errors.New("live tests require absolute dedicated token and runs paths")
	}
	token, err := privatefs.Canonical(token)
	if err != nil {
		return config{}, true, err
	}
	runs, err = privatefs.Canonical(runs)
	if err != nil {
		return config{}, true, err
	}
	defaultToken, err = privatefs.Canonical(defaultToken)
	if err != nil {
		return config{}, true, err
	}
	if token == defaultToken || !outsideGit(token) || !outsideGit(runs) || contains(runs, token) || contains(token, runs) {
		return config{}, true, errors.New("live paths must be outside Git, separate, and use a dedicated token")
	}
	return config{Token: token, Runs: runs}, true, nil
}

type mediaCounter struct {
	base  http.RoundTripper
	calls atomic.Int64
}

func (c *mediaCounter) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Query().Get("alt") == "media" {
		c.calls.Add(1)
	}
	return c.base.RoundTrip(r)
}
func testConfig(t *testing.T) config {
	t.Helper()
	if os.Getenv("GROVE_LIVE_TEST") != "1" {
		t.Skip("live Drive tests require explicit GROVE_LIVE_TEST=1")
	}
	defaultToken, err := auth.DefaultTokenPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := liveConfig("1", os.Getenv("GROVE_TEST_TOKEN_FILE"), os.Getenv("GROVE_TEST_RUNS_DIR"), defaultToken)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
func fixtureAPI(ctx context.Context, token, runDir, access string, work func(drive.API) error) (err error) {
	lock, err := privatefs.Acquire(runDir + ".lock")
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := lock.Close(); err == nil {
			err = closeErr
		}
	}()
	session, err := auth.OpenSession(ctx, token, access)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := session.Close(); err == nil {
			err = closeErr
		}
	}()
	return work(drive.NewClient(session.HTTPClient, drive.ClientOptions{PageSize: 2}))
}
func TestInitialSyncLive(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := os.MkdirAll(cfg.Runs, 0700); err != nil {
		t.Fatal("cannot create live runs directory")
	}
	base, err := os.MkdirTemp(cfg.Runs, "initial-sync-")
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
	result, err := service.Run(ctx, opts)
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
	if _, err = fixture.VerifyLocal(ctx, manifest, opts.LocalDir); err != nil {
		t.Fatal(err)
	}
	if err = fixtureAPI(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error { return fixture.Cleanup(ctx, api, runDir) }); err != nil {
		t.Fatal(err)
	}
	t.Logf("Live workflow verified; private run record: %s", filepath.Join(runDir, "run.json"))
}
