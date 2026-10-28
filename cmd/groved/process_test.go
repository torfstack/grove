//go:build linux || darwin

package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/auth"
	"github.com/torfstack/grove/internal/daemon"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
	"golang.org/x/oauth2"
)

type processDrive struct {
	changesGate    chan struct{}
	changesStarted chan struct{}
	changesOnce    sync.Once
	mu             sync.Mutex
	data           string
	version        string
	gate           chan struct{}
	started        chan struct{}
	createStarted  chan struct{}
	once           sync.Once
}

func (d *processDrive) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	data, version, gate, started, createStarted := d.data, d.version, d.gate, d.started, d.createStarted
	changesGate, changesStarted := d.changesGate, d.changesStarted
	d.mu.Unlock()
	if r.Method == http.MethodGet && (strings.HasSuffix(r.URL.Path, "/changes") || strings.HasSuffix(r.URL.Path, "/changes/startPageToken")) {
		if changesGate != nil {
			d.changesOnce.Do(func() { close(changesStarted) })
			select {
			case <-changesGate:
			case <-r.Context().Done():
				return
			}
		}
		if strings.HasSuffix(r.URL.Path, "/startPageToken") {
			_ = json.NewEncoder(w).Encode(map[string]any{"startPageToken": version})
			return
		}
		entries := []any{}
		if r.URL.Query().Get("pageToken") != version {
			entries = append(entries, map[string]string{"changeType": "file"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"newStartPageToken": version, "changes": entries})
		return
	}
	if createStarted != nil && strings.HasSuffix(r.URL.Path, "/generateIds") {
		_ = json.NewEncoder(w).Encode(map[string]any{"ids": []string{"allocated"}})
		return
	}
	if createStarted != nil && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/files") {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			return
		}
		d.once.Do(func() { close(createStarted) })
		<-r.Context().Done()
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "unexpected mutation", 500)
		return
	}
	if r.URL.Query().Get("alt") == "media" {
		if gate != nil {
			d.once.Do(func() { close(started) })
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write([]byte(data))
		return
	}
	if strings.HasSuffix(r.URL.Path, "/root") {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "root", "name": "root", "mimeType": drive.FolderMIME, "ownedByMe": true})
		return
	}
	sum := md5.Sum([]byte(data))
	f := map[string]any{"id": "a", "name": "a", "mimeType": "text/plain", "parents": []string{"root"}, "size": strconv.Itoa(len(data)), "md5Checksum": hex.EncodeToString(sum[:]), "version": version, "headRevisionId": "rev-" + version, "modifiedTime": "2026-10-23T10:00:00Z", "ownedByMe": true, "capabilities": map[string]bool{"canDownload": true}}
	if strings.HasSuffix(r.URL.Path, "/files") {
		_ = json.NewEncoder(w).Encode(map[string]any{"files": []any{f}})
		return
	}
	_ = json.NewEncoder(w).Encode(f)
}

type processFixture struct {
	service syncengine.Service
	opts    syncengine.Options
	binding syncengine.Binding
	config  string
	url     string
	remote  *processDrive
}

func processService(url, registry string) syncengine.Service {
	return syncengine.Service{RegistryDir: registry, OpenAPI: func(ctx context.Context, token, access string) (drive.API, func() error, error) {
		session, err := auth.OpenSession(ctx, token, access)
		if err != nil {
			return nil, nil, err
		}
		return drive.NewClient(session.HTTPClient, drive.ClientOptions{BaseURL: url + "/drive/v3"}), session.Close, nil
	}}
}

func processConfig(t *testing.T, profile string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "daemon.json")
	if err := privatefs.WriteJSON(path, map[string]any{"version": 1, "profile_dir": profile, "mode": "download", "interval": "1s"}); err != nil {
		t.Fatal(err)
	}
	return path
}

func newProcessFixture(t *testing.T) *processFixture {
	t.Helper()
	remote := &processDrive{data: "base", version: "1"}
	server := httptest.NewServer(http.HandlerFunc(remote.serve))
	t.Cleanup(server.Close)
	credentials := t.TempDir()
	client := filepath.Join(credentials, "client.json")
	token := filepath.Join(credentials, "token.json")
	if err := os.WriteFile(client, []byte(`{"installed":{"client_id":"test-client","client_secret":"synthetic-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	scope, err := auth.Scope("read-write")
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveRecord(token, auth.Record{Version: 1, ClientID: "test-client", ClientSecretPath: client, Scopes: []string{scope}, Token: &oauth2.Token{AccessToken: "synthetic-access", RefreshToken: "synthetic-refresh", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}}); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	opts := syncengine.Options{ProfileDir: filepath.Join(base, "profile"), LocalDir: filepath.Join(base, "local"), RemoteRoot: "root", TokenFile: token}
	service := processService(server.URL, filepath.Join(t.TempDir(), "registry"))
	if _, err := service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	summary, err := service.InspectProfile(context.Background(), opts.ProfileDir)
	if err != nil {
		t.Fatal(err)
	}
	return &processFixture{service: service, opts: opts, binding: summary.Binding, config: processConfig(t, opts.ProfileDir), url: server.URL, remote: remote}
}

func TestGrovedProcessHelper(t *testing.T) {
	if os.Getenv("GROVE_DAEMON_TEST_HELPER") != "1" {
		return
	}
	ctx, stop := processContext(context.Background())
	service := processService(os.Getenv("GROVE_DAEMON_TEST_DRIVE"), os.Getenv("GROVE_DAEMON_TEST_REGISTRY"))
	cmd := newCommand(func(ctx context.Context, path string) error {
		return daemon.Run(ctx, path, daemon.Dependencies{InspectProfile: service.InspectProfile, RunExisting: service.RunExisting, CheckRemoteChanges: service.CheckRemoteChanges})
	})
	cmd.SetArgs([]string{"--config", os.Getenv("GROVE_DAEMON_TEST_CONFIG")})
	err := cmd.ExecuteContext(ctx)
	stop()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "groved: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}

type daemonChild struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	log  string
}

func startDaemonChild(t *testing.T, f *processFixture, config string) *daemonChild {
	t.Helper()
	path := filepath.Join(t.TempDir(), "output.log")
	log, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestGrovedProcessHelper$")
	cmd.Env = append(os.Environ(), "GROVE_DAEMON_TEST_HELPER=1", "GROVE_DAEMON_TEST_CONFIG="+config, "GROVE_DAEMON_TEST_DRIVE="+f.url, "GROVE_DAEMON_TEST_REGISTRY="+f.service.RegistryDir)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	child := &daemonChild{cmd: cmd, done: make(chan struct{}), log: path}
	go func() { child.err = cmd.Wait(); _ = log.Close(); close(child.done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-child.done:
		case <-time.After(5 * time.Second):
			t.Error("daemon helper did not exit")
		}
	})
	return child
}

func waitProcess(t *testing.T, child *daemonChild) error {
	t.Helper()
	select {
	case <-child.done:
		return child.err
	case <-time.After(10 * time.Second):
		t.Fatal("daemon process did not exit")
		return nil
	}
}

func waitReady(t *testing.T, child *daemonChild, condition func() bool) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-timer.C:
			t.Fatal("daemon process condition timed out")
		case <-child.done:
			data, _ := os.ReadFile(child.log)
			t.Fatalf("daemon exited early: %v %s", child.err, data)
		case <-ticker.C:
		}
	}
}

func waiting(profile string) bool {
	r, err := daemon.LoadRecord(profile)
	return err == nil && r != nil && r.Phase == "waiting"
}

func TestGrovedProcessOwnershipAndSignals(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM, os.Kill} {
		t.Run(sig.String(), func(t *testing.T) {
			f := newProcessFixture(t)
			child := startDaemonChild(t, f, f.config)
			waitReady(t, child, func() bool { return waiting(f.opts.ProfileDir) })
			alias := filepath.Join(t.TempDir(), "profile-alias")
			if err := os.Symlink(f.opts.ProfileDir, alias); err != nil {
				t.Fatal(err)
			}
			second := startDaemonChild(t, f, processConfig(t, alias))
			if err := waitProcess(t, second); err == nil {
				t.Fatal("second daemon acquired profile")
			}
			f.remote.mu.Lock()
			f.remote.data = "new bytes"
			f.remote.version = "2"
			f.remote.gate = make(chan struct{})
			f.remote.started = make(chan struct{})
			started := f.remote.started
			f.remote.mu.Unlock()
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("transfer did not begin")
			}
			if _, err := f.service.RunExisting(context.Background(), f.opts.ProfileDir, f.binding, false); !errors.Is(err, privatefs.ErrLocked) {
				t.Fatalf("manual sync overlapped daemon: %v", err)
			}
			if _, err := f.service.InspectRecovery(context.Background(), f.opts); !errors.Is(err, privatefs.ErrLocked) {
				t.Fatalf("manual recovery overlapped daemon: %v", err)
			}
			session, err := auth.OpenSession(context.Background(), f.opts.TokenFile, "read-only")
			if session != nil {
				_ = session.Close()
			}
			if !errors.Is(err, privatefs.ErrLocked) {
				t.Fatalf("token session overlapped daemon: %v", err)
			}
			if err := child.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			err = waitProcess(t, child)
			if sig != os.Kill && err != nil {
				data, _ := os.ReadFile(child.log)
				t.Fatalf("requested stop failed: %v %s", err, data)
			}
			if active, err := privatefs.ProbeLease(filepath.Join(f.opts.ProfileDir, "daemon.lock")); err != nil || active {
				t.Fatalf("dead process retained ownership: %t %v", active, err)
			}
			r, err := daemon.LoadRecord(f.opts.ProfileDir)
			if err != nil {
				t.Fatal(err)
			}
			if sig == os.Kill && r.Phase != "syncing" {
				t.Fatal("crash did not retain historical active phase")
			}
			f.remote.mu.Lock()
			close(f.remote.gate)
			f.remote.gate = nil
			f.remote.mu.Unlock()
			restarted := startDaemonChild(t, f, f.config)
			waitReady(t, restarted, func() bool {
				data, err := os.ReadFile(filepath.Join(f.opts.LocalDir, "a"))
				return err == nil && string(data) == "new bytes" && waiting(f.opts.ProfileDir)
			})
			if err := restarted.cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			if err := waitProcess(t, restarted); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGrovedProcessesShareToken(t *testing.T) {
	f := newProcessFixture(t)
	base := t.TempDir()
	opts := syncengine.Options{ProfileDir: filepath.Join(base, "profile"), LocalDir: filepath.Join(base, "local"), RemoteRoot: "root", TokenFile: f.opts.TokenFile}
	if _, err := f.service.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	first := startDaemonChild(t, f, f.config)
	waitReady(t, first, func() bool { return waiting(f.opts.ProfileDir) })
	f.remote.mu.Lock()
	f.remote.data = "changed"
	f.remote.version = "2"
	f.remote.gate = make(chan struct{})
	f.remote.started = make(chan struct{})
	started := f.remote.started
	f.remote.mu.Unlock()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("first daemon did not hold session")
	}
	second := startDaemonChild(t, f, processConfig(t, opts.ProfileDir))
	waitReady(t, second, func() bool {
		r, err := daemon.LoadRecord(opts.ProfileDir)
		return err == nil && r != nil && r.Outcome == "busy" && r.Phase == "waiting"
	})
	if err := second.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitProcess(t, second); err != nil {
		t.Fatal(err)
	}
	if err := first.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitProcess(t, first); err != nil {
		t.Fatal(err)
	}
}

func TestGrovedSignalDuringFolderCreation(t *testing.T) {
	f := newProcessFixture(t)
	started := make(chan struct{})
	f.remote.mu.Lock()
	f.remote.createStarted = started
	f.remote.mu.Unlock()
	if err := os.Mkdir(filepath.Join(f.opts.LocalDir, "new-folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := privatefs.WriteJSON(f.config, map[string]any{"version": 1, "profile_dir": f.opts.ProfileDir, "mode": "two-way", "interval": "1s"}); err != nil {
		t.Fatal(err)
	}
	child := startDaemonChild(t, f, f.config)
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("folder creation did not start")
	}
	if err := child.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitProcess(t, child); err != nil {
		t.Fatal("requested stop during folder creation failed")
	}
	var state syncengine.State
	if err := privatefs.ReadJSON(filepath.Join(f.opts.ProfileDir, "state.json"), &state); err != nil || state.Transaction == nil || state.Transaction.Phase != "create-intent" {
		t.Fatal("cancelled folder creation discarded uncertain intent")
	}
	r, err := daemon.LoadRecord(f.opts.ProfileDir)
	if err != nil || r.Outcome != "cancelled" || r.Phase != "stopped" {
		t.Fatal("folder cancellation was not recorded as a clean stop")
	}
}
