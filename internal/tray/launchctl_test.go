package tray

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchdPrintParsesSupportedJobs(t *testing.T) {
	for _, tc := range []struct {
		file, target, state string
	}{
		{"launchctl-print-macos13.txt", "gui/501/com.torfstack.groved.personal", "running"},
		{"launchctl-print-macos15.txt", "gui/502/com.torfstack.groved.personal", "not running"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			output, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			job, err := parseLaunchctlJob(output, "com.torfstack.groved.personal")
			if err != nil {
				t.Fatalf("parse launchctl output: %v", err)
			}
			if job.Target != tc.target || job.State != tc.state || job.Path != "/Users/test/Library/LaunchAgents/com.torfstack.groved.personal.plist" || job.Program != "/Applications/Grove.app/Contents/Helpers/groved" {
				t.Fatalf("parsed job: %+v", job)
			}
			if len(job.Arguments) != 3 || job.Arguments[1] != "--config" || job.Arguments[2] != "/Users/test/Library/Application Support/Grove Profiles/personal.json" {
				t.Fatalf("parsed arguments: %#v", job.Arguments)
			}
		})
	}
}

func TestLaunchdUnknownOrMismatchedJob(t *testing.T) {
	valid, err := os.ReadFile(filepath.Join("testdata", "launchctl-print-macos13.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for name, output := range map[string][]byte{
		"unknown layout":      []byte("service is running\n"),
		"wrong label":         valid,
		"unknown state":       []byte(strings.Replace(string(valid), "state = running", "state = maybe", 1)),
		"missing program":     []byte(strings.Replace(string(valid), "\tprogram = /Applications/Grove.app/Contents/Helpers/groved\n", "", 1)),
		"missing plist path":  []byte(strings.Replace(string(valid), "\tpath = /Users/test/Library/LaunchAgents/com.torfstack.groved.personal.plist\n", "", 1)),
		"malformed arguments": []byte(strings.Replace(string(valid), "\t}\n\n\tdomain", "\targuments are malformed\n\n\tdomain", 1)),
	} {
		t.Run(name, func(t *testing.T) {
			label := "com.torfstack.groved.personal"
			if name == "wrong label" {
				label = "com.torfstack.groved.other"
			}
			if _, err := parseLaunchctlJob(output, label); err == nil {
				t.Fatal("unrecognized launchctl output accepted")
			}
		})
	}
}

func TestLaunchdNotFoundRequiresKnownDiagnostic(t *testing.T) {
	output := []byte("Bad request.\nCould not find service \"com.torfstack.groved.personal\" in domain for user gui: 501\n")
	if !launchdServiceNotFound(output, "com.torfstack.groved.personal", 501) {
		t.Fatal("supported unloaded-job diagnostic not recognized")
	}
	for _, invalid := range [][]byte{
		[]byte("launchctl failed\n"),
		[]byte("Bad request.\nCould not find service \"other\" in domain for user gui: 501\n"),
		[]byte("Bad request.\nCould not find service \"com.torfstack.groved.personal\" in domain for user gui: 502\n"),
	} {
		if launchdServiceNotFound(invalid, "com.torfstack.groved.personal", 501) {
			t.Fatalf("unknown diagnostic classified as unloaded: %q", invalid)
		}
	}
}
