package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

func TestMacOSAgentRenderer(t *testing.T) {
	output := filepath.Join(t.TempDir(), "agent.plist")
	args := []string{"../../contrib/macos/render-agent.py", "daemon", "--instance", "personal", "--app", "/Applications/Grove & Co.app", "--config", "/Users/me/config & data.json", "--output", output}
	if data, err := exec.Command("python3", args...).CombinedOutput(); err != nil {
		t.Fatalf("render agent: %v: %s", err, data)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("Grove &amp; Co.app")) || !bytes.Contains(data, []byte("config &amp; data.json")) {
		t.Fatal("renderer did not escape XML paths")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		_, err = decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(output)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("plist permissions: %v %v", info, err)
	}
	if err := exec.Command("python3", args...).Run(); err == nil {
		t.Fatal("renderer overwrote an existing agent")
	}
	trayOutput := filepath.Join(t.TempDir(), "tray.plist")
	trayArgs := []string{"../../contrib/macos/render-agent.py", "tray", "--instance", "personal", "--app", "/Applications/Grove.app", "--profile", "/Users/me/profile & data", "--name", "Work & Home", "--output", trayOutput}
	if data, err := exec.Command("python3", trayArgs...).CombinedOutput(); err != nil {
		t.Fatalf("render tray agent: %v: %s", err, data)
	}
	data, err = os.ReadFile(trayOutput)
	if err != nil || !bytes.Contains(data, []byte("profile &amp; data")) || !bytes.Contains(data, []byte("Work &amp; Home")) || !bytes.Contains(data, []byte("com.torfstack.grove-tray.personal")) {
		t.Fatalf("tray agent values: %v", err)
	}
}
