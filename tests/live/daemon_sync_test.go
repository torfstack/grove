//go:build linux

package live

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/daemon"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/fixture"
	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

func TestDaemonPeriodicDownloadLive(t *testing.T) { testDaemonLive(t, false) }
func TestDaemonPeriodicUploadLive(t *testing.T)   { testDaemonLive(t, true) }

func testDaemonLive(t *testing.T, upload bool) { testDaemonVariant(t, upload, false) }

func testDaemonVariant(t *testing.T, upload, watching bool) {
	cfg := testConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	t.Cleanup(cancel)
	if err := os.MkdirAll(cfg.Runs, 0700); err != nil {
		t.Fatal("cannot create private live runs")
	}
	base, err := os.MkdirTemp(cfg.Runs, "daemon-")
	if err != nil {
		t.Fatal("cannot create private daemon run")
	}
	runDir := filepath.Join(base, "fixture")
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Failed run retained at %s", filepath.Join(runDir, "run.json"))
		}
	})
	daemonBin, groveBin := filepath.Join(base, "groved"), filepath.Join(base, "grove")
	for _, build := range []struct{ out, pkg string }{{daemonBin, "./cmd/groved"}, {groveBin, "./cmd/grove"}} {
		cmd := exec.CommandContext(ctx, "go", "build", "-o", build.out, build.pkg)
		cmd.Dir = "../.."
		if err := cmd.Run(); err != nil {
			t.Fatal("cannot build live command")
		}
	}
	manifest, err := fixture.LoadManifest("../../testdata/fixtures/baseline/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var run fixture.Run
	if err := fixtureAPI(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error {
		var err error
		run, err = fixture.Seed(ctx, api, manifest, runDir)
		return err
	}); err != nil {
		t.Fatal("cannot seed daemon fixture")
	}
	cleaned := false
	t.Cleanup(func() {
		if cleaned {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if err := fixtureAPI(cleanupCtx, cfg.Token, runDir, "read-write", func(api drive.API) error { return fixture.Cleanup(cleanupCtx, api, runDir) }); err != nil {
			t.Error("owned daemon fixture cleanup failed; private run retained")
		}
	})
	var rootID, targetID string
	for _, o := range run.Objects {
		if o.ParentID == "" {
			rootID = o.RemoteID
		}
		if o.LogicalID == "hello.txt" {
			targetID = o.RemoteID
		}
	}
	if rootID == "" || targetID == "" {
		t.Fatal("fixture identities missing")
	}
	opts := syncengine.Options{ProfileDir: filepath.Join(base, "profile"), LocalDir: filepath.Join(base, "local"), RemoteRoot: rootID, TokenFile: cfg.Token}
	stateHome := filepath.Join(base, "state")
	service := syncengine.Service{RegistryDir: filepath.Join(stateHome, "grove", "destinations")}
	if _, err := service.Run(ctx, opts); err != nil {
		t.Fatal("cannot populate daemon profile")
	}
	if _, err := fixture.VerifyLocal(ctx, manifest, opts.LocalDir); err != nil {
		t.Fatal("initial independent verification failed")
	}
	mode := "download"
	if upload {
		mode = "two-way"
	}
	interval := "2s"
	if watching {
		interval = "10m"
	}
	config := filepath.Join(base, "daemon.json")
	if err := privatefs.WriteJSON(config, map[string]any{"version": 1, "profile_dir": opts.ProfileDir, "mode": mode, "interval": interval, "watch_local": watching}); err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(filepath.Join(base, "process.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("cannot create private process log")
	}
	cmd := exec.CommandContext(ctx, daemonBin, "--config", config)
	cmd.Env = append(os.Environ(), "XDG_STATE_HOME="+stateHome)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal("cannot launch daemon")
	}
	done := make(chan struct{})
	var processErr error
	go func() { processErr = cmd.Wait(); _ = log.Close(); close(done) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			select {
			case <-done:
			default:
				t.Error("cannot signal daemon")
			}
		}
		select {
		case <-done:
			if processErr != nil {
				t.Error("daemon exited unsuccessfully; private process log retained")
			}
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("daemon stop timed out")
		}
	}
	t.Cleanup(stop)
	wait := func(condition func() bool) {
		t.Helper()
		deadline := time.NewTimer(90 * time.Second)
		defer deadline.Stop()
		timer := time.NewTicker(100 * time.Millisecond)
		defer timer.Stop()
		for {
			if condition() {
				return
			}
			select {
			case <-deadline.C:
				t.Fatal("daemon acceptance exceeded 90s observation window")
			case <-ctx.Done():
				t.Fatal("daemon acceptance timed out; private records retained")
			case <-done:
				t.Fatal("daemon exited before acceptance; private records retained")
			case <-timer.C:
			}
		}
	}
	record := func() *daemon.Record {
		r, e := daemon.LoadRecord(opts.ProfileDir)
		if e != nil {
			t.Fatal("cannot read daemon observation")
		}
		return r
	}
	wait(func() bool { r := record(); return r != nil && r.Phase == "waiting" && r.Outcome == "completed" })
	queryStatus := func(owner string) {
		t.Helper()
		output, err := exec.CommandContext(ctx, groveBin, "status", "--profile-dir", opts.ProfileDir, "--json").Output()
		var report daemon.StatusReport
		if err != nil || json.Unmarshal(output, &report) != nil || report.Owner != owner || report.LastRecorded == nil || report.LastRecorded.WatchLocal != watching {
			t.Fatal("offline command status disagrees with process ownership")
		}
	}
	queryStatus("active")
	expected := manifest
	if upload {
		payload := []byte("daemon owned local edit\n")
		if err := os.WriteFile(filepath.Join(opts.LocalDir, "nested", "hello.txt"), payload, 0600); err != nil {
			t.Fatal("cannot edit owned local file")
		}
		wait(func() bool {
			r := record()
			return r.Phase == "waiting" && r.Outcome == "completed" && r.Counts.Updated > 0
		})
		if err := daemonFixtureSession(ctx, cfg.Token, runDir, "read-only", func(api drive.API) error {
			var content bytes.Buffer
			if err := api.Download(ctx, targetID, &content); err != nil {
				return err
			}
			if !bytes.Equal(content.Bytes(), payload) {
				return errors.New("uploaded bytes differ")
			}
			return nil
		}); err != nil {
			t.Fatal("independent uploaded-content verification failed")
		}
	} else {
		expected, err = fixture.LoadManifest("../../testdata/fixtures/baseline/incremental.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := daemonFixtureSession(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error {
			wait(func() bool { r := record(); return r.Phase == "waiting" && r.Outcome == "busy" })
			mutation, ok := api.(drive.MutationAPI)
			if !ok {
				return errors.New("fixture mutation unavailable")
			}
			return fixture.ApplyChanges(ctx, mutation, runDir, expected)
		}); err != nil {
			t.Fatal("owned fixture mutation failed; outcome retained without retry")
		}
		wait(func() bool { _, err := fixture.VerifyLocal(ctx, expected, opts.LocalDir); return err == nil })
	}
	settledAfter := time.Now()
	if watching {
		if err := os.Chmod(filepath.Join(opts.LocalDir, "nested", "hello.txt"), 0600); err != nil {
			t.Fatal("cannot request owned metadata reconciliation")
		}
	}
	wait(func() bool {
		r := record()
		c := r.Counts
		return r.Phase == "waiting" && r.Outcome == "completed" && r.AttemptStartedAt != nil && r.AttemptStartedAt.After(settledAfter) && c.Downloaded == 0 && c.Uploaded == 0 && c.Updated == 0 && c.CreatedDirectories == 0 && c.Moved == 0 && c.Trashed == 0
	})
	times := map[string]time.Time{}
	hashes := map[string][32]byte{}
	if err := filepath.WalkDir(opts.LocalDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err == nil {
			times[path] = info.ModTime()
			if info.Mode().IsRegular() {
				data, readErr := os.ReadFile(path)
				if readErr != nil {
					return readErr
				}
				hashes[path] = sha256.Sum256(data)
			}
		}
		return err
	}); err != nil {
		t.Fatal("cannot snapshot local mtimes")
	}
	after := time.Now()
	if watching {
		if err := os.Chmod(filepath.Join(opts.LocalDir, "nested", "hello.txt"), 0600); err != nil {
			t.Fatal("cannot request owned no-op reconciliation")
		}
	}
	wait(func() bool {
		r := record()
		c := r.Counts
		return r.Phase == "waiting" && r.Outcome == "completed" && r.AttemptStartedAt != nil && r.AttemptStartedAt.After(after) && c.Downloaded == 0 && c.Uploaded == 0 && c.Updated == 0 && c.CreatedDirectories == 0 && c.Moved == 0 && c.Trashed == 0
	})
	for path, want := range times {
		info, err := os.Stat(path)
		if err != nil || !info.ModTime().Equal(want) {
			t.Fatal("no-op cycle changed local mtimes")
		}
	}
	for path, want := range hashes {
		data, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(data) != want {
			t.Fatal("no-op cycle changed local bytes")
		}
	}
	if upload {
		data, err := os.ReadFile(filepath.Join(opts.LocalDir, "nested", "hello.txt"))
		if err != nil || !bytes.Equal(data, []byte("daemon owned local edit\n")) {
			t.Fatal("no-op cycle changed uploaded local bytes")
		}
	} else if _, err := fixture.VerifyLocal(ctx, expected, opts.LocalDir); err != nil {
		t.Fatal("no-op independent manifest verification failed")
	}
	stop()
	if t.Failed() {
		return
	}
	queryStatus("inactive")
	if err := fixtureAPI(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error { return fixture.Cleanup(ctx, api, runDir) }); err != nil {
		t.Fatal("owned daemon fixture cleanup failed")
	}
	cleaned = true
	t.Log("Actual daemon scheduling, independent content, no-op cycle, offline status, shutdown and owned cleanup verified")
}

func daemonFixtureSession(ctx context.Context, token, runDir, access string, work func(drive.API) error) error {
	retryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		entered := false
		err := fixtureAPI(ctx, token, runDir, access, func(api drive.API) error { entered = true; return work(api) })
		if entered || !errors.Is(err, privatefs.ErrLocked) {
			return err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return retryCtx.Err()
		case <-timer.C:
		}
	}
}
