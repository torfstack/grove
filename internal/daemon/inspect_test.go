//go:build linux

package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

func TestDaemonInspectOffline(t *testing.T) {
	_, profile, _ := daemonFixture(t, "download")
	service := syncengine.Service{OpenAPI: func(context.Context, string, string) (drive.API, func() error, error) {
		t.Fatal("status opened credentials/network")
		return nil, nil, nil
	}}
	before, err := os.ReadFile(filepath.Join(profile, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(context.Background(), profile, service)
	if err != nil || report.Owner != "inactive" || report.LastRecorded != nil || report.ProfileState != "available" || report.Profile == nil || !report.Profile.PopulationComplete {
		t.Fatalf("initial status: %+v %v", report, err)
	}
	stamp := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	r := Record{Version: 1, SessionID: "private-session-sentinel", WatchLocal: true, Mode: ModeDownload, Interval: time.Minute, Phase: "syncing", Outcome: "never", UpdatedAt: stamp, AttemptStartedAt: &stamp}
	if err := SaveRecord(profile, r); err != nil {
		t.Fatal(err)
	}
	report, err = Inspect(context.Background(), profile, service)
	if err != nil || report.Owner != "inactive" || report.LastRecorded.Phase != "syncing" || !report.LastRecorded.WatchLocal {
		t.Fatalf("stale history mistaken for active daemon: %+v %v", report, err)
	}
	lease, err := privatefs.AcquireLease(filepath.Join(profile, "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	report, err = Inspect(context.Background(), profile, service)
	if err != nil || report.Owner != "active" {
		t.Fatalf("active owner not observed: %+v %v", report, err)
	}
	_ = lease.Close()
	lock, err := privatefs.Acquire(filepath.Join(profile, "profile.lock"))
	if err != nil {
		t.Fatal(err)
	}
	report, err = Inspect(context.Background(), profile, service)
	_ = lock.Close()
	if err != nil || report.ProfileState != "busy" || report.Profile != nil {
		t.Fatalf("busy profile invented a summary: %+v %v", report, err)
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-session-sentinel", "token.json", "remote_root", "session_id"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("status leaked private data")
		}
	}
	after, err := os.ReadFile(filepath.Join(profile, "state.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal("status mutated sync state")
	}
}

func TestDaemonInspectFailures(t *testing.T) {
	for _, kind := range []string{"lease", "record", "profile", "missing-profile"} {
		t.Run(kind, func(t *testing.T) {
			_, profile, _ := daemonFixture(t, "download")
			switch kind {
			case "lease":
				if err := os.Mkdir(filepath.Join(profile, "daemon.lock"), 0700); err != nil {
					t.Fatal(err)
				}
			case "record":
				if err := os.WriteFile(filepath.Join(profile, "daemon-status.json"), []byte("private-invalid-data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "profile":
				if err := os.WriteFile(filepath.Join(profile, "state.json"), []byte("private-invalid-data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-profile":
				if err := os.RemoveAll(profile); err != nil {
					t.Fatal(err)
				}
			}
			report, err := Inspect(context.Background(), profile, syncengine.Service{})
			if err == nil || strings.Contains(err.Error(), "private-invalid-data") {
				t.Fatalf("inspection failure ignored or exposed: %+v %v", report, err)
			}
			if kind == "lease" && report.Owner != "unknown" {
				t.Fatal("lease failure guessed ownership")
			}
			if (kind == "profile" || kind == "missing-profile") && report.Profile != nil {
				t.Fatal("invalid profile invented counts")
			}
			if kind == "missing-profile" {
				if _, err := os.Stat(profile); !os.IsNotExist(err) {
					t.Fatal("inspection recreated profile")
				}
			}
		})
	}
}

func TestDaemonInspectConcurrentReaders(t *testing.T) {
	_, profile, _ := daemonFixture(t, "download")
	lease, err := privatefs.AcquireLease(filepath.Join(profile, "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	type answer struct {
		report StatusReport
		err    error
	}
	done := make(chan answer, 16)
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func() {
			<-start
			r, err := Inspect(context.Background(), profile, syncengine.Service{})
			done <- answer{r, err}
		}()
	}
	close(start)
	for i := 0; i < 16; i++ {
		result := <-done
		if result.err != nil || result.report.Owner != "inactive" {
			t.Fatalf("readers invented an owner: %+v %v", result.report, result.err)
		}
	}
}

func TestDaemonInspectRecordedCounts(t *testing.T) {
	_, profile, _ := daemonFixture(t, "download")
	stamp := time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)
	next := stamp.Add(time.Minute)
	r := Record{Version: 1, SessionID: "private-session", Mode: ModeTwoWay, Interval: time.Minute, Phase: "waiting", Outcome: "completed", UpdatedAt: stamp, AttemptStartedAt: &stamp, AttemptFinishedAt: &stamp, NextAttemptAt: &next, Counts: syncengine.Result{Downloaded: 1, Uploaded: 2, CreatedDirectories: 3, Skipped: 4, Updated: 5, Moved: 6, Trashed: 7}}
	if err := SaveRecord(profile, r); err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(context.Background(), profile, syncengine.Service{})
	if err != nil || report.LastRecorded.Counts != (OperationCounts{Downloaded: 1, Uploaded: 2, CreatedDirectories: 3, Skipped: 4, Updated: 5, Moved: 6, Trashed: 7}) {
		t.Fatalf("recorded operation counts changed: %+v %v", report, err)
	}
	data, err := json.Marshal(report)
	if err != nil || !strings.Contains(string(data), `"created_directories":3`) {
		t.Fatalf("public count format changed: %s %v", data, err)
	}
}

func TestRemotePollingStatus(t *testing.T) {
	_, profile, _ := daemonFixture(t, "download")
	stamp := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	r := Record{Version: 1, SessionID: "private-session", Mode: ModeDownload, Interval: time.Minute, Phase: "starting", Outcome: "never", UpdatedAt: stamp, RemotePollInterval: 30 * time.Second, RemoteCheckedAt: &stamp, RemoteOutcome: "completed"}
	if err := SaveRecord(profile, r); err != nil {
		t.Fatal(err)
	}
	report, err := Inspect(context.Background(), profile, syncengine.Service{})
	if err != nil || report.Owner != "inactive" || report.LastRecorded == nil || report.LastRecorded.RemotePollInterval != "30s" || report.LastRecorded.RemoteOutcome != "completed" || !report.LastRecorded.RemoteCheckedAt.Equal(stamp) {
		t.Fatalf("historical report: %+v %v", report, err)
	}
	data, err := json.Marshal(report)
	if err != nil || strings.Contains(string(data), "private-session") || strings.Contains(string(data), "cursor") {
		t.Fatal("private information in report")
	}
}
