package tray

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func launchdConfigFixture(t *testing.T, parent, name string) (string, string) {
	t.Helper()
	profile := filepath.Join(parent, name)
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(parent, "config with spaces.json")
	data, err := json.Marshal(map[string]any{"version": 1, "profile_dir": profile, "mode": "download"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	return profile, configPath
}

func launchdPlistFixture(label, program, config string, runAtLoad, keepAlive bool, exitTimeout int) []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>--config</string><string>%s</string></array>
<key>RunAtLoad</key><%t/>
<key>KeepAlive</key><%t/>
<key>ExitTimeOut</key><integer>%d</integer>
</dict></plist>`, label, program, config, runAtLoad, keepAlive, exitTimeout))
}

func TestLaunchdPlistValidatesPrivateDaemonInvocation(t *testing.T) {
	dir := t.TempDir()
	profile, configPath := launchdConfigFixture(t, dir, "profile with spaces")
	label := "com.torfstack.groved.personal"
	program := "/Applications/Grove.app/Contents/Helpers/groved"
	valid := launchdPlistFixture(label, program, configPath, true, false, 0)
	if err := validateLaunchAgent(valid, label, profile); err != nil {
		t.Fatalf("valid plist rejected: %v", err)
	}

	_, otherProfileConfig := launchdConfigFixture(t, dir, "other profile")
	for name, data := range map[string][]byte{
		"wrong label":     launchdPlistFixture("com.torfstack.groved.other", program, configPath, true, false, 0),
		"wrong profile":   launchdPlistFixture(label, program, otherProfileConfig, true, false, 0),
		"keep alive":      launchdPlistFixture(label, program, configPath, true, true, 0),
		"not run at load": launchdPlistFixture(label, program, configPath, false, false, 0),
		"forced timeout":  launchdPlistFixture(label, program, configPath, true, false, 5),
		"wrapper":         launchdPlistFixture(label, "/bin/sh", configPath, true, false, 0),
		"extra argument":  []byte(strings.Replace(string(valid), "</array>", "<string>extra</string></array>", 1)),
		"unknown key":     []byte(strings.Replace(string(valid), "</dict>", "<key>EnvironmentVariables</key><dict/></dict>", 1)),
		"malformed XML":   []byte(strings.Replace(string(valid), "</plist>", "", 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateLaunchAgent(data, label, profile); err == nil {
				t.Fatal("unsafe launch agent accepted")
			}
		})
	}
}

func TestLaunchdDaemonAgentTemplate(t *testing.T) {
	template, err := os.ReadFile("../../contrib/macos/launchagents/com.torfstack.groved.plist.in")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	profile, config := launchdConfigFixture(t, root, "profile with spaces")
	label := "com.torfstack.groved.personal"
	program := "/Applications/Grove App/Contents/Helpers/groved"
	rendered := strings.NewReplacer(
		"@GROVE_LABEL@", label,
		"@GROVED_PATH@", program,
		"@GROVE_CONFIG@", config,
	).Replace(string(template))
	if err := validateLaunchAgent([]byte(rendered), label, profile); err != nil {
		t.Fatalf("template rejected: %v", err)
	}
}
