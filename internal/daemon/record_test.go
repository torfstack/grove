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
