//go:build linux || darwin

package live

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestDaemonSystemdDownloadLive(t *testing.T) {
	if os.Getenv("GROVE_SYSTEMD_LIVE_TEST") != "1" {
		t.Skip("authenticated systemd acceptance requires GROVE_SYSTEMD_LIVE_TEST=1")
	}
	testDaemonVariantMode(t, false, false, false, true)
}

func TestMakeAcceptanceUnitDirTracksCreatedParents(t *testing.T) {
	unitDir := filepath.Join(t.TempDir(), "config", "systemd", "user")
	created := []string{}
	if err := makeAcceptanceUnitDir(unitDir, &created); err != nil {
		t.Fatal("cannot create acceptance unit directory")
	}
	if len(created) != 3 {
		t.Fatalf("tracked %d directories, want 3", len(created))
	}
	for _, path := range created {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("tracked unit directory is unavailable: %s", path)
		}
	}
	if err := makeAcceptanceUnitDir(unitDir, &created); err != nil {
		t.Fatal("existing unit directory was rejected")
	}
	if len(created) != 3 {
		t.Fatalf("existing directories were marked as created: %d", len(created))
	}
}

func testDaemonLive(t *testing.T, upload bool) { testDaemonVariant(t, upload, false, false) }

func testDaemonVariant(t *testing.T, upload, watching, remote bool) {
	testDaemonVariantMode(t, upload, watching, remote, false)
}

func testDaemonVariantMode(t *testing.T, upload, watching, remote, underSystemd bool) {
	cfg := testConfig(t)
	watching = watching || remote
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
	var cleanupSystemd func()
	var markConfigOwned func()
	unit := ""
	if underSystemd {
		unit, config, cleanupSystemd, markConfigOwned = installAcceptanceUnit(t, daemonBin, stateHome)
		t.Cleanup(cleanupSystemd)
	}
	settings := map[string]any{"version": 1, "profile_dir": opts.ProfileDir, "mode": mode, "interval": interval, "watch_local": watching}
	if remote {
		settings["remote_poll_interval"] = "10s"
	}
	if err := privatefs.WriteJSON(config, settings); err != nil {
		t.Fatal(err)
	}
	if underSystemd {
		markConfigOwned()
	}
	if underSystemd {
		if err := exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run(); err != nil {
			t.Fatal("cannot reload user service manager")
		}
		if err := exec.CommandContext(ctx, "systemctl", "--user", "start", unit).Run(); err != nil {
			t.Fatal("cannot start authenticated daemon user service")
		}
		if timeout := unitProperty(ctx, unit, "TimeoutStopUSec"); timeout != "15s" {
			t.Fatalf("user service stop timeout is %q, want 15s", timeout)
		}
	}
	log, err := os.OpenFile(filepath.Join(base, "process.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("cannot create private process log")
	}
	done := make(chan struct{})
	var processErr error
	var cmd *exec.Cmd
	if underSystemd {
		_ = log.Close()
	} else {
		cmd = exec.CommandContext(ctx, daemonBin, "--config", config)
		cmd.Env = append(os.Environ(), "XDG_STATE_HOME="+stateHome)
		cmd.Stdout, cmd.Stderr = log, log
		if err := cmd.Start(); err != nil {
			_ = log.Close()
			t.Fatal("cannot launch daemon")
		}
		go func() { processErr = cmd.Wait(); _ = log.Close(); close(done) }()
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if underSystemd {
			if err := exec.CommandContext(ctx, "systemctl", "--user", "stop", unit).Run(); err != nil {
				t.Error("cannot stop authenticated daemon user service")
			}
			if state := unitProperty(ctx, unit, "ActiveState"); state != "inactive" {
				t.Errorf("user service did not become inactive: %s", state)
			}
			return
		}
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
			if underSystemd && unitProperty(ctx, unit, "ActiveState") == "failed" {
				t.Fatal("systemd daemon entered failed state; inspect its journal")
			}
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
		if err != nil || json.Unmarshal(output, &report) != nil || report.Owner != owner || report.LastRecorded == nil || report.LastRecorded.WatchLocal != watching || (remote && report.LastRecorded.RemotePollInterval != "10s") || (!remote && report.LastRecorded.RemotePollInterval != "") {
			t.Fatal("offline command status disagrees with process ownership")
		}
	}
	queryStatus("active")
	if remote {
		var initialized time.Time
		wait(func() bool {
			r := record()
			if r == nil || r.RemoteOutcome != "completed" || r.RemoteCheckedAt == nil {
				return false
			}
			initialized = *r.RemoteCheckedAt
			return true
		})
		wait(func() bool {
			r := record()
			c := r.Counts
			return r.Phase == "waiting" && r.Outcome == "completed" && r.AttemptStartedAt != nil && r.AttemptStartedAt.After(initialized) && r.NextAttemptAt != nil && time.Until(*r.NextAttemptAt) > time.Minute && c.Downloaded == 0 && c.Uploaded == 0 && c.Updated == 0 && c.CreatedDirectories == 0 && c.Moved == 0 && c.Trashed == 0
		})
	}
	expected := manifest
	if upload {
		payload := []byte("daemon owned local edit\n")
		editedAt := time.Now()
		if err := os.WriteFile(filepath.Join(opts.LocalDir, "nested", "hello.txt"), payload, 0600); err != nil {
			t.Fatal("cannot edit owned local file")
		}
		writtenAt := time.Now()
		wait(func() bool {
			r := record()
			return r != nil && r.Phase == "waiting" && r.Outcome == "completed" && r.AttemptStartedAt != nil && r.AttemptStartedAt.After(writtenAt)
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
		if watching {
			t.Logf("Watch-triggered upload independently verified in %.2fs", time.Since(editedAt).Seconds())
		}
	} else {
		expected, err = fixture.LoadManifest("../../testdata/fixtures/baseline/incremental.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := daemonFixtureSession(ctx, cfg.Token, runDir, "read-write", func(api drive.API) error {
			if !remote {
				wait(func() bool { r := record(); return r.Phase == "waiting" && r.Outcome == "busy" })
			}
			mutation, ok := api.(drive.MutationAPI)
			if !ok {
				return errors.New("fixture mutation unavailable")
			}
			return fixture.ApplyChanges(ctx, mutation, runDir, expected)
		}); err != nil {
			t.Fatal("owned fixture mutation failed; outcome retained without retry")
		}
		changedAt := time.Now()
		wait(func() bool { _, err := fixture.VerifyLocal(ctx, expected, opts.LocalDir); return err == nil })
		if remote {
			t.Logf("Remote-triggered download independently verified in %.2fs", time.Since(changedAt).Seconds())
		}
	}
	settledAfter := time.Now()
	if watching {
		if err := os.Chmod(filepath.Join(opts.LocalDir, "empty.bin"), 0600); err != nil {
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
		if err := os.Chmod(filepath.Join(opts.LocalDir, "empty.bin"), 0600); err != nil {
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
	if underSystemd {
		if err := os.RemoveAll(base); err != nil {
			t.Fatal("cannot remove successful systemd acceptance run data")
		}
	}
	t.Log("Actual daemon scheduling, independent content, no-op cycle, offline status, shutdown and owned cleanup verified")
}

func installAcceptanceUnit(t *testing.T, daemonBin, stateHome string) (string, string, func(), func()) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("cannot determine user home")
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	paths, err := exec.Command("systemctl", "--user", "show", "-p", "UnitPath", "--value").Output()
	unitDirFound := false
	for _, path := range strings.Fields(string(paths)) {
		unitDirFound = unitDirFound || path == unitDir
	}
	if err != nil || !unitDirFound {
		t.Fatal("the running user manager does not search the expected unit directory")
	}
	configDir := filepath.Join(home, ".config", "grove", "daemons")
	configRoot := filepath.Dir(configDir)
	label := fmt.Sprintf("acceptance-%d-%d", os.Getpid(), time.Now().Unix())
	unit := "groved@" + label + ".service"
	config := filepath.Join(configDir, label+".json")
	template := filepath.Join(unitDir, "groved@.service")
	dropinDir := filepath.Join(unitDir, "groved@"+label+".service.d")
	dropin := filepath.Join(dropinDir, "override.conf")
	for _, path := range []string{config, dropinDir, dropin} {
		if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("refusing to replace an existing acceptance path: %s", path)
		}
	}
	templateCreated := false
	configCreated := false
	_, configDirErr := os.Lstat(configDir)
	configDirCreated := errors.Is(configDirErr, os.ErrNotExist)
	_, configRootErr := os.Lstat(configRoot)
	configRootCreated := errors.Is(configRootErr, os.ErrNotExist)
	dropinDirCreated := false
	createdUnitDirs := []string{}
	cleanupFiles := func() {
		for _, path := range []string{dropin} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("cannot remove acceptance file %s: %v", path, err)
			}
		}
		if configCreated {
			if err := os.Remove(config); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("cannot remove acceptance config %s: %v", config, err)
			}
		}
		if dropinDirCreated {
			if err := os.Remove(dropinDir); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("cannot remove acceptance drop-in directory: %v", err)
			}
		}
		if templateCreated {
			if err := os.Remove(template); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("cannot remove temporary user service template: %v", err)
			}
		}
		if configDirCreated {
			if err := os.Remove(configDir); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("cannot remove empty acceptance config directory: %v", err)
			}
		}
		if configRootCreated {
			if err := os.Remove(configRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("cannot remove empty acceptance Grove config directory: %v", err)
			}
		}
		for i := len(createdUnitDirs) - 1; i >= 0; i-- {
			if err := os.Remove(createdUnitDirs[i]); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("cannot remove empty acceptance unit directory %s: %v", createdUnitDirs[i], err)
			}
		}
	}
	defer func() {
		if t.Failed() {
			cleanupFiles()
		}
	}()
	if _, err := os.Lstat(template); errors.Is(err, os.ErrNotExist) {
		data, readErr := os.ReadFile("../../contrib/systemd/groved@.service")
		if readErr != nil {
			t.Fatal("cannot read Grove service template")
		}
		if err := makeAcceptanceUnitDir(unitDir, &createdUnitDirs); err != nil {
			t.Fatal("cannot create user unit directory")
		}
		file, err := os.OpenFile(template, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			t.Fatal("cannot install temporary Grove service template")
		}
		templateCreated = true
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			t.Fatal("cannot write temporary Grove service template")
		}
		if err := file.Close(); err != nil {
			t.Fatal("cannot close temporary Grove service template")
		}
	} else if err != nil {
		t.Fatal("cannot inspect user unit template")
	}
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal("cannot create private daemon config directory")
	}
	if err := os.Mkdir(dropinDir, 0755); err != nil {
		t.Fatal("cannot create isolated service drop-in")
	}
	dropinDirCreated = true
	quote := func(value string) string {
		value = strings.ReplaceAll(value, `\`, `\\`)
		value = strings.ReplaceAll(value, `"`, `\"`)
		value = strings.ReplaceAll(value, "%", "%%")
		value = strings.ReplaceAll(value, "$", "$$")
		return `"` + value + `"`
	}
	content := "[Service]\nTimeoutStopSec=15s\nExecStart=\nExecStart=" + quote(daemonBin) + " --config " + quote(config) + "\nEnvironment=" + quote("XDG_STATE_HOME="+stateHome) + "\n"
	if err := os.WriteFile(dropin, []byte(content), 0600); err != nil {
		t.Fatal("cannot write isolated service drop-in")
	}
	return unit, config, func() {
		if err := exec.Command("systemctl", "--user", "stop", unit).Run(); err != nil {
			t.Errorf("cannot stop acceptance service during cleanup: %v", err)
		}
		cleanupFiles()
		if err := exec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
			t.Errorf("cannot reload user service manager during cleanup: %v", err)
		}
	}, func() { configCreated = true }
}

func makeAcceptanceUnitDir(path string, created *[]string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() {
			return errors.New("acceptance unit path is not a directory")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if parent != path {
		if err := makeAcceptanceUnitDir(parent, created); err != nil {
			return err
		}
	}
	if err := os.Mkdir(path, 0755); err != nil {
		if errors.Is(err, os.ErrExist) {
			info, statErr := os.Lstat(path)
			if statErr == nil && info.IsDir() {
				return nil
			}
		}
		return err
	}
	*created = append(*created, path)
	return nil
}

func unitProperty(ctx context.Context, unit, property string) string {
	output, err := exec.CommandContext(ctx, "systemctl", "--user", "show", "-p", property, "--value", unit).Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
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
