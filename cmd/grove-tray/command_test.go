package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/torfstack/grove/internal/tray"
)

func TestTrayCommand(t *testing.T) {
	var got tray.Options
	calls := 0
	run := func(_ context.Context, o tray.Options) error { calls++; got = o; return nil }
	cmd := newCommand(run)
	cmd.SetArgs([]string{"--profile-dir", "/private/profile", "--name", "Personal", "--service-instance", "personal"})
	if err := cmd.Execute(); err != nil || got.ProfileDir != "/private/profile" || got.Name != "Personal" || got.ServiceInstance != "personal" || calls != 1 {
		t.Fatalf("routing: %+v %v", got, err)
	}
	for _, args := range [][]string{{}, {"--profile-dir", "relative"}, {"--profile-dir", "/profile", "extra"}, {"--profile-dir", "/profile", "--name", ""}, {"--profile-dir", "/profile", "--name", "bad\nname"}, {"--profile-dir", "/profile", "--service-instance", ""}, {"--profile-dir", "/profile", "--service-instance", "../other"}} {
		before := calls
		cmd = newCommand(run)
		cmd.SetArgs(args)
		if cmd.Execute() == nil || calls != before {
			t.Fatalf("invalid command reached run: %v", args)
		}
	}
	cmd = newCommand(run)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetArgs([]string{"--help"})
	before := calls
	if err := cmd.Execute(); err != nil || calls != before || !strings.Contains(buf.String(), "--profile-dir") {
		t.Fatalf("help: %v %s", err, buf.String())
	}
	cmd = newCommand(func(context.Context, tray.Options) error { return errors.New("private error") })
	cmd.SetArgs([]string{"--profile-dir", "/profile"})
	if err := cmd.Execute(); err == nil || strings.Contains(err.Error(), "private error") {
		t.Fatalf("raw error exposed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd = newCommand(run)
	cmd.SetArgs([]string{"--profile-dir", "/profile"})
	before = calls
	if err := cmd.ExecuteContext(ctx); err != nil || calls != before {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestMacOSBundleMetadata(t *testing.T) {
	info, err := os.ReadFile("../../contrib/macos/Grove-Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(info, []byte("<key>LSUIElement</key><true/>")) || !bytes.Contains(info, []byte("13.0")) {
		t.Fatal("bundle metadata must hide the Dock icon and declare macOS 13 minimum")
	}
	trayAgent, err := os.ReadFile("../../contrib/macos/launchagents/com.torfstack.grove-tray.plist.in")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(trayAgent, []byte("com.torfstack.grove-tray.")) || !bytes.Contains(trayAgent, []byte("@GROVE_APP@/Contents/MacOS/grove-tray")) || !bytes.Contains(trayAgent, []byte("@GROVE_PROFILE@")) {
		t.Fatal("companion LaunchAgent needs a distinct label and absolute app/profile arguments")
	}
	build, err := os.ReadFile("../../contrib/macos/build-app.sh")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(build, []byte("launchctl bootstrap")) || bytes.Contains(build, []byte("launchctl enable")) {
		t.Fatal("app build must not register or enable login services")
	}
}
