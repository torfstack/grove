package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/torfstack/grove/internal/daemon"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

type processWatcher struct{ registered chan string }

func (w *processWatcher) RegisterStatusNotifierItem(sender dbus.Sender, path string) *dbus.Error {
	if path != "/StatusNotifierItem" {
		return dbus.NewError("org.freedesktop.DBus.Error.InvalidArgs", nil)
	}
	w.registered <- string(sender)
	return nil
}
func privateBus(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1", "--nopidfile")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	r := bufio.NewReader(out)
	addr, err := r.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(addr)
}
func processConn(t *testing.T, addr string) *dbus.Conn {
	t.Helper()
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
func host(t *testing.T, conn *dbus.Conn) *processWatcher {
	t.Helper()
	w := &processWatcher{make(chan string, 8)}
	if err := conn.Export(w, "/StatusNotifierWatcher", "org.kde.StatusNotifierWatcher"); err != nil {
		t.Fatal(err)
	}
	reply, err := conn.RequestName("org.kde.StatusNotifierWatcher", dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("watcher: %v %v", reply, err)
	}
	return w
}
func registered(t *testing.T, w *processWatcher) string {
	t.Helper()
	select {
	case sender := <-w.registered:
		return sender
	case <-time.After(5 * time.Second):
		t.Fatal("actual tray did not register")
	}
	return ""
}
func buildTray(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "grove-tray")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, data)
	}
	return binary
}

type child struct {
	cmd  *exec.Cmd
	done chan struct{}
	err  error
	log  string
}

func startChild(t *testing.T, cmd *exec.Cmd) *child {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "child.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	c := &child{cmd: cmd, done: make(chan struct{}), log: logPath}
	go func() { c.err = cmd.Wait(); _ = f.Close(); close(c.done) }()
	t.Cleanup(func() {
		select {
		case <-c.done:
			return
		default:
			_ = cmd.Process.Kill()
		}
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
			t.Error("child not joined")
		}
	})
	return c
}
func waitChild(t *testing.T, c *child) error {
	t.Helper()
	select {
	case <-c.done:
		return c.err
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit")
	}
	return nil
}
func startTray(t *testing.T, binary, addr, profile string) *child {
	t.Helper()
	cmd := exec.Command(binary, "--profile-dir", profile, "--name", "Process acceptance")
	cmd.Env = append(os.Environ(), "DBUS_SESSION_BUS_ADDRESS="+addr)
	return startChild(t, cmd)
}
func processFixture(t *testing.T) (profile, config, registry, gate string) {
	t.Helper()
	base := t.TempDir()
	profile = filepath.Join(base, "profile")
	local := filepath.Join(base, "local")
	if err := os.Mkdir(local, 0700); err != nil {
		t.Fatal(err)
	}
	binding := syncengine.Binding{RemoteRoot: "root", LocalDir: local, TokenFile: filepath.Join(base, "absent-token.json")}
	if err := privatefs.WriteJSON(filepath.Join(profile, "state.json"), syncengine.State{Version: 2, Binding: binding, PopulationComplete: true}); err != nil {
		t.Fatal(err)
	}
	config = filepath.Join(base, "daemon.json")
	if err := privatefs.WriteJSON(config, map[string]any{"version": 1, "profile_dir": profile, "mode": "download", "interval": "1s"}); err != nil {
		t.Fatal(err)
	}
	return profile, config, filepath.Join(base, "registry"), filepath.Join(base, "hold")
}
func trayProperty(t *testing.T, conn *dbus.Conn, sender, key string) dbus.Variant {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var value dbus.Variant
	if err := conn.Object(sender, "/StatusNotifierItem").CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.kde.StatusNotifierItem", key).Store(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
func refresh(t *testing.T, conn *dbus.Conn, sender string) {
	t.Helper()
	if err := conn.Object(sender, "/MenuBar").Call("com.canonical.dbusmenu.Event", 0, int32(100), "clicked", dbus.MakeVariant(int32(0)), uint32(0)).Err; err != nil {
		t.Fatal(err)
	}
}
func waitIcon(t *testing.T, conn *dbus.Conn, sender, icon string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if trayProperty(t, conn, sender, "IconName").Value() == icon {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("icon never became %s", icon)
}
func testRecord(t *testing.T, profile, phase string) {
	t.Helper()
	now := time.Now().UTC()
	next := now.Add(time.Hour)
	r := daemon.Record{Version: 1, SessionID: "synthetic", Mode: daemon.ModeDownload, Interval: time.Hour, UpdatedAt: now, Phase: phase, Outcome: "never", AttemptStartedAt: &now}
	if phase == "waiting" {
		r.AttemptFinishedAt = &now
		r.NextAttemptAt = &next
		r.Outcome = "completed"
	}
	if err := daemon.SaveRecord(profile, r); err != nil {
		t.Fatal(err)
	}
}
func TestTrayProcessLifecycle(t *testing.T) {
	binary := buildTray(t)
	addr := privateBus(t)
	conn := processConn(t, addr)
	w := host(t, conn)
	profile, _, _, _ := processFixture(t)
	before, err := os.ReadFile(filepath.Join(profile, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := startTray(t, binary, addr, profile)
	sender := registered(t, w)
	waitIcon(t, conn, sender, "grove-stopped")
	duplicate := startTray(t, binary, addr, profile)
	if waitChild(t, duplicate) == nil {
		t.Fatal("duplicate accepted")
	}
	lease, err := privatefs.AcquireLease(filepath.Join(profile, "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	testRecord(t, profile, "waiting")
	refresh(t, conn, sender)
	waitIcon(t, conn, sender, "grove-idle")
	testRecord(t, profile, "syncing")
	refresh(t, conn, sender)
	waitIcon(t, conn, sender, "grove-syncing")
	if _, err := conn.ReleaseName("org.kde.StatusNotifierWatcher"); err != nil {
		t.Fatal(err)
	}
	replacement := host(t, processConn(t, addr))
	if got := registered(t, replacement); got != sender {
		t.Fatal("registration identity changed")
	}
	if err := conn.Object(sender, "/MenuBar").Call("com.canonical.dbusmenu.Event", 0, int32(111), "clicked", dbus.MakeVariant(int32(0)), uint32(0)).Err; err != nil {
		t.Fatal(err)
	}
	if err := waitChild(t, c); err != nil {
		t.Fatal(err)
	}
	active, err := privatefs.ProbeLease(filepath.Join(profile, "daemon.lock"))
	if err != nil || !active {
		t.Fatalf("quit released daemon: %v %t", err, active)
	}
	after, err := os.ReadFile(filepath.Join(profile, "state.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("tray mutated profile state")
	}
	c = startTray(t, binary, addr, profile)
	registered(t, replacement)
	if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitChild(t, c); err != nil {
		t.Fatal(err)
	}
}

type emptyDrive struct{ gate string }

func (d emptyDrive) Get(ctx context.Context, id string) (drive.File, error) {
	for {
		if _, err := os.Stat(d.gate); errors.Is(err, os.ErrNotExist) {
			break
		}
		select {
		case <-ctx.Done():
			return drive.File{}, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return drive.File{ID: id, Name: "root", MIMEType: drive.FolderMIME, OwnedByMe: true}, nil
}
func (emptyDrive) List(context.Context, string, string) (drive.Page, error) { return drive.Page{}, nil }
func (emptyDrive) Download(context.Context, string, io.Writer) error {
	return errors.New("unexpected download")
}
func (emptyDrive) Create(context.Context, drive.Create, io.Reader) (drive.File, error) {
	return drive.File{}, errors.New("unexpected create")
}
func (emptyDrive) Trash(context.Context, string) error { return errors.New("unexpected trash") }
func TestTrayDaemonHelper(t *testing.T) {
	if os.Getenv("GROVE_TRAY_TEST_DAEMON") != "1" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service := syncengine.Service{RegistryDir: os.Getenv("GROVE_TRAY_TEST_REGISTRY"), OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		return emptyDrive{os.Getenv("GROVE_TRAY_TEST_GATE")}, func() error { return nil }, nil
	}}
	if err := daemon.Run(ctx, os.Getenv("GROVE_TRAY_TEST_CONFIG"), daemon.Dependencies{InspectProfile: service.InspectProfile, RunExisting: service.RunExisting}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
func TestTrayProcessDaemon(t *testing.T) {
	binary := buildTray(t)
	addr := privateBus(t)
	conn := processConn(t, addr)
	w := host(t, conn)
	profile, config, registry, gate := processFixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestTrayDaemonHelper$")
	cmd.Env = append(os.Environ(), "GROVE_TRAY_TEST_DAEMON=1", "GROVE_TRAY_TEST_CONFIG="+config, "GROVE_TRAY_TEST_REGISTRY="+registry, "GROVE_TRAY_TEST_GATE="+gate)
	d := startChild(t, cmd)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r, err := daemon.LoadRecord(profile)
		if err == nil && r != nil && r.Phase == "waiting" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c := startTray(t, binary, addr, profile)
	sender := registered(t, w)
	waitIcon(t, conn, sender, "grove-idle")
	if err := os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitIcon(t, conn, sender, "grove-syncing")
	if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitChild(t, c); err != nil {
		t.Fatal(err)
	}
	active, err := privatefs.ProbeLease(filepath.Join(profile, "daemon.lock"))
	if err != nil || !active {
		t.Fatal("tray quit stopped daemon")
	}
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := waitChild(t, d); err != nil {
		data, _ := os.ReadFile(d.log)
		t.Fatalf("daemon: %v %s", err, data)
	}
}
