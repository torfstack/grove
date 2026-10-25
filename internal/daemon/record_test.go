package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonRecordRoundTrip(t *testing.T) {
	profile := t.TempDir()
	r := Record{Version: 1, SessionID: "private-session", Mode: ModeDownload, Interval: 5 * time.Minute, Phase: "starting", Outcome: "never", UpdatedAt: time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)}
	if err := SaveRecord(profile, r); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRecord(profile)
	if err != nil || loaded.SessionID != r.SessionID || loaded.Interval != 5*time.Minute {
		t.Fatalf("record roundtrip: %+v %v", loaded, err)
	}
	info, err := os.Stat(filepath.Join(profile, "daemon-status.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("record not private")
	}
	for _, change := range []func(*Record){func(r *Record) { r.Version = 2 }, func(r *Record) { r.Phase = "private-phase" }, func(r *Record) { r.Reason = "private-reason" }, func(r *Record) { r.UpdatedAt = time.Time{} }, func(r *Record) { r.Counts.Uploaded = -1 }, func(r *Record) { r.Outcome = "completed" }} {
		bad := r
		change(&bad)
		if err := SaveRecord(profile, bad); err == nil {
			t.Fatal("invalid record written")
		}
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if err := SaveRecord(missing, r); err == nil {
		t.Fatal("record writer created profile")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing profile recreated")
	}
	if r, err := LoadRecord(missing); err != nil || r != nil {
		t.Fatalf("absent record not normal: %+v %v", r, err)
	}
}

func TestDaemonMalformedRecord(t *testing.T) {
	profile := t.TempDir()
	if err := os.WriteFile(filepath.Join(profile, "daemon-status.json"), []byte(`{"version":1,"private":"private-content"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRecord(profile); err == nil || err.Error() != "daemon status unavailable" {
		t.Fatalf("malformed record accepted or echoed: %v", err)
	}
}

func TestDaemonUnfinishedRecordHasNoCounts(t *testing.T) {
	r := Record{Version: 1, SessionID: "session", Mode: ModeDownload, Interval: time.Minute, Phase: "starting", Outcome: "never", UpdatedAt: time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)}
	r.Counts.Downloaded = 1
	if err := SaveRecord(t.TempDir(), r); err == nil {
		t.Fatal("unstarted attempt claimed completed operations")
	}
}

func TestWatchLocalHistoricalStatus(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		profile := t.TempDir()
		r := Record{Version: 1, SessionID: "session", Mode: ModeDownload, Interval: time.Minute, Phase: "starting", Outcome: "never", UpdatedAt: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), WatchLocal: enabled}
		if err := SaveRecord(profile, r); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadRecord(profile)
		if err != nil || loaded.WatchLocal != enabled {
			t.Fatalf("roundtrip: %+v %v", loaded, err)
		}
	}
}

func TestRemotePollingRecordValidation(t *testing.T) {
	stamp := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	base := Record{Version: 1, SessionID: "session", Mode: ModeDownload, Interval: time.Minute, Phase: "starting", Outcome: "never", UpdatedAt: stamp}
	for _, tc := range []struct {
		name     string
		interval time.Duration
		checked  *time.Time
		outcome  string
		valid    bool
	}{
		{"disabled", 0, nil, "", true}, {"never", 10 * time.Second, nil, "never", true},
		{"completed", 30 * time.Second, &stamp, "completed", true}, {"busy", 30 * time.Second, &stamp, "busy", true}, {"failed", 30 * time.Second, &stamp, "failed", true},
		{"short", time.Second, nil, "never", false}, {"disabled-time", 0, &stamp, "", false}, {"disabled-outcome", 0, nil, "never", false},
		{"missing-time", 30 * time.Second, nil, "completed", false}, {"never-time", 30 * time.Second, &stamp, "never", false}, {"unknown", 30 * time.Second, &stamp, "private-reason", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			r.RemotePollInterval = tc.interval
			r.RemoteCheckedAt = tc.checked
			r.RemoteOutcome = tc.outcome
			if validRecord(r) != tc.valid {
				t.Fatalf("valid=%t record=%+v", tc.valid, r)
			}
		})
	}
}
