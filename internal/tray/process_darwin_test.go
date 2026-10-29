//go:build darwin

package tray

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/daemon"
	"github.com/torfstack/grove/internal/privatefs"
)

func TestDarwinTrayCoordinatorClosesOnlyItsPresentation(t *testing.T) {
	bus := newTestTray()
	opts := optionsFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, opts, Dependencies{
			Inspect:     func(context.Context, string) (daemon.StatusReport, error) { return healthy().Status, nil },
			Clock:       &manualClock{timers: make(chan *manualTimer, 4)},
			OpenBus:     func(context.Context, Options) (TrayBus, error) { return bus, nil },
			OpenService: func(context.Context, Options) (Controller, error) { return nil, nil },
		})
	}()
	_ = nextView(t, bus)
	bus.actions <- ActionQuit
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("tray quit did not close coordinator")
	}
	if !bus.closed.Load() {
		t.Fatal("tray presentation was not closed")
	}
}

func TestDarwinDisposableLaunchAgentStartStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, err := privatefs.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, config := launchdConfigFixture(t, root, "profile")
	program := filepath.Join(root, "groved")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nexec /bin/sleep 120\n"), 0700); err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Geteuid())
	instance := fmt.Sprintf("acceptance-%d", os.Getpid())
	label := "com.torfstack.groved." + instance
	plistPath := filepath.Join(root, label+".plist")
	if _, err := os.Lstat(plistPath); err == nil || !os.IsNotExist(err) {
		t.Fatalf("fixture plist already exists or cannot be checked: %v", err)
	}
	if err := os.WriteFile(plistPath, launchdPlistFixture(label, program, config, true, false, 0), 0600); err != nil {
		t.Fatal(err)
	}
	defer cleanupDisposableAgent(t, uid, label, plistPath)
	domain := "gui/" + strconv.FormatUint(uint64(uid), 10)
	service, err := newLaunchdController(Options{ProfileDir: profile, ServiceInstance: instance}, systemLaunchctl{}, domain, plistPath, filepath.Join(root, "login", label+".plist"), uid)
	if err != nil {
		t.Fatal(err)
	}
	current := Sample{Status: daemon.StatusReport{Owner: "inactive", ProfileState: "available", Profile: &daemon.ProfileView{}}}
	if err := service.Request(ctx, ActionStart, current); err != nil {
		t.Fatalf("bootstrap disposable agent: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state, err := service.Observe(ctx)
		if err == nil && state.Phase == "running" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	state, err := service.Observe(ctx)
	if err != nil || state.Phase != "running" {
		t.Fatalf("agent did not reach running state: %+v %v", state, err)
	}
	lease, err := privatefs.AcquireLease(filepath.Join(profile, "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Request(ctx, ActionStop, Sample{}); err != nil {
		_ = lease.Close()
		t.Fatalf("bootout disposable agent: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		state, observeErr := service.Observe(ctx)
		if observeErr == nil && state.CanStart {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("agent remained loaded after Stop: %+v", state)
}

func TestDarwinDisposableLaunchAgentFatalExitStaysStopped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, err := privatefs.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, config := launchdConfigFixture(t, root, "profile")
	marker := filepath.Join(root, "launch-count")
	program := filepath.Join(root, "groved")
	script := "#!/bin/sh\nprintf x >> '" + marker + "'\nexit 37\n"
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Geteuid())
	instance := fmt.Sprintf("fatal-%d", os.Getpid())
	label := "com.torfstack.groved." + instance
	plistPath := filepath.Join(root, label+".plist")
	if _, err := os.Lstat(plistPath); err == nil || !os.IsNotExist(err) {
		t.Fatalf("fixture plist already exists or cannot be checked: %v", err)
	}
	if err := os.WriteFile(plistPath, launchdPlistFixture(label, program, config, true, false, 0), 0600); err != nil {
		t.Fatal(err)
	}
	defer cleanupDisposableAgent(t, uid, label, plistPath)
	domain := "gui/" + strconv.FormatUint(uint64(uid), 10)
	service, err := newLaunchdController(Options{ProfileDir: profile, ServiceInstance: instance}, systemLaunchctl{}, domain, plistPath, filepath.Join(root, "login", label+".plist"), uid)
	if err != nil {
		t.Fatal(err)
	}
	current := Sample{Status: daemon.StatusReport{Owner: "inactive", ProfileState: "available", Profile: &daemon.ProfileView{}}}
	if err := service.Request(ctx, ActionStart, current); err != nil {
		t.Fatalf("bootstrap fatal-exit fixture: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(marker)
		state, observeErr := service.Observe(ctx)
		if observeErr == nil && state.CanStart && len(data) == 1 {
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				t.Fatal("timed out observing fatal exit")
			}
			data, err := os.ReadFile(marker)
			state, observeErr := service.Observe(ctx)
			if err != nil || len(data) != 1 || observeErr != nil || !state.CanStart {
				t.Fatalf("fatal agent restarted or became unavailable: starts=%d state=%+v err=%v observe=%v", len(data), state, err, observeErr)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	data, _ := os.ReadFile(marker)
	t.Fatalf("fatal agent did not remain stopped: starts=%d", len(data))
}

func cleanupDisposableAgent(t *testing.T, uid uint32, label, plistPath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runner := systemLaunchctl{}
	domain := "gui/" + strconv.FormatUint(uint64(uid), 10)
	target := domain + "/" + label
	output, err := runner.Print(ctx, target)
	if err == nil {
		if err := runner.Bootout(ctx, target); err != nil {
			t.Errorf("boot out fixture: %v", err)
			return
		}
	} else if !launchdServiceNotFound(output, label, uid) {
		t.Errorf("cannot verify fixture agent cleanup")
		return
	}
	output, err = runner.Print(ctx, target)
	if err == nil || !launchdServiceNotFound(output, label, uid) {
		t.Errorf("fixture agent still loaded or status unavailable")
		return
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		t.Errorf("remove fixture plist: %v", err)
	}
}
