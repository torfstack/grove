package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/torfstack/grove/internal/privatefs"
)

func recordError() error { return errors.New("daemon status unavailable") }

func validRecord(r Record) bool {
	if r.RemotePollInterval == 0 {
		if r.RemoteCheckedAt != nil || r.RemoteOutcome != "" {
			return false
		}
	} else {
		if r.RemotePollInterval < 10*time.Second {
			return false
		}
		switch r.RemoteOutcome {
		case "never":
			if r.RemoteCheckedAt != nil {
				return false
			}
		case "completed", "busy", "failed":
			if r.RemoteCheckedAt == nil || !utcTime(*r.RemoteCheckedAt) {
				return false
			}
		default:
			return false
		}
	}

	if r.Version != 1 || r.SessionID == "" || len(r.SessionID) > 128 || (r.Mode != ModeDownload && r.Mode != ModeTwoWay) || r.Interval < time.Second || !utcTime(r.UpdatedAt) {
		return false
	}
	for _, stamp := range []*time.Time{r.AttemptStartedAt, r.AttemptFinishedAt, r.NextAttemptAt} {
		if stamp != nil && !utcTime(*stamp) {
			return false
		}
	}
	for _, count := range []int{r.Counts.Downloaded, r.Counts.Uploaded, r.Counts.CreatedDirectories, r.Counts.Skipped, r.Counts.Updated, r.Counts.Moved, r.Counts.Trashed} {
		if count < 0 || r.Outcome == "never" && count != 0 {
			return false
		}
	}
	switch r.Outcome {
	case "never", "completed":
		if r.Reason != "" {
			return false
		}
	case "busy":
		if r.Reason != "resource_busy" {
			return false
		}
	case "failed":
		if r.Reason != "sync_failed" {
			return false
		}
	case "cancelled":
		if r.Reason != "cancelled" {
			return false
		}
	default:
		return false
	}
	if r.AttemptStartedAt == nil && (r.AttemptFinishedAt != nil || r.Outcome != "never") {
		return false
	}
	switch r.Phase {
	case "starting":
		return r.AttemptStartedAt == nil && r.NextAttemptAt == nil && r.Outcome == "never"
	case "syncing":
		return r.AttemptStartedAt != nil && r.AttemptFinishedAt == nil && r.NextAttemptAt == nil && r.Outcome == "never"
	case "waiting":
		return r.AttemptStartedAt != nil && r.AttemptFinishedAt != nil && r.NextAttemptAt != nil && r.Outcome != "never"
	case "stopping", "stopped":
		return r.NextAttemptAt == nil && (r.AttemptStartedAt == nil || r.AttemptFinishedAt != nil)
	default:
		return false
	}
}

func utcTime(t time.Time) bool { _, offset := t.Zone(); return !t.IsZero() && offset == 0 }

func SaveRecord(profileDir string, r Record) error {
	if !validRecord(r) {
		return recordError()
	}
	if err := privatefs.WriteExistingJSON(filepath.Join(profileDir, "daemon-status.json"), r); err != nil {
		return recordError()
	}
	return nil
}

func LoadRecord(profileDir string) (*Record, error) {
	path := filepath.Join(profileDir, "daemon-status.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64*1024 {
		return nil, recordError()
	}
	var r Record
	if err := privatefs.ReadJSON(path, &r); err != nil || !validRecord(r) {
		return nil, recordError()
	}
	return &r, nil
}
