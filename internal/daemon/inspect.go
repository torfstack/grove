package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

type ProfileView struct {
	PopulationComplete bool `json:"population_complete"`
	ConflictCount      int  `json:"conflict_count"`
	RecoveryPending    bool `json:"recovery_pending"`
}

type OperationCounts struct {
	Downloaded         int `json:"downloaded"`
	Uploaded           int `json:"uploaded"`
	CreatedDirectories int `json:"created_directories"`
	Skipped            int `json:"skipped"`
	Updated            int `json:"updated"`
	Moved              int `json:"moved"`
	Trashed            int `json:"trashed"`
}

type Observation struct {
	RemotePollInterval string          `json:"remote_poll_interval"`
	RemoteCheckedAt    *time.Time      `json:"remote_checked_at"`
	RemoteOutcome      string          `json:"remote_outcome"`
	WatchLocal         bool            `json:"watch_local"`
	Mode               Mode            `json:"mode"`
	Interval           string          `json:"interval"`
	Phase              string          `json:"phase"`
	Outcome            string          `json:"outcome"`
	UpdatedAt          time.Time       `json:"updated_at"`
	AttemptStartedAt   *time.Time      `json:"attempt_started_at"`
	AttemptFinishedAt  *time.Time      `json:"attempt_finished_at"`
	NextAttemptAt      *time.Time      `json:"next_attempt_at"`
	Counts             OperationCounts `json:"counts"`
	Reason             string          `json:"reason"`
}

type StatusReport struct {
	Owner        string       `json:"owner"`
	LastRecorded *Observation `json:"last_recorded"`
	ProfileState string       `json:"profile_state"`
	Profile      *ProfileView `json:"profile"`
}

func Inspect(ctx context.Context, profileDir string, service syncengine.Service) (StatusReport, error) {
	r := StatusReport{Owner: "unknown", ProfileState: "unavailable"}
	if err := ctx.Err(); err != nil {
		return r, failed("status_unavailable", err)
	}
	if !filepath.IsAbs(profileDir) {
		return r, failed("profile_unavailable", nil)
	}
	profile, err := privatefs.Canonical(profileDir)
	if err != nil {
		return r, failed("profile_unavailable", err)
	}
	var faults []error
	active, err := privatefs.ProbeLease(filepath.Join(profile, "daemon.lock"))
	if err != nil {
		faults = append(faults, failed("ownership_unavailable", err))
	} else if active {
		r.Owner = "active"
	} else {
		r.Owner = "inactive"
	}
	record, err := LoadRecord(profile)
	if err != nil {
		faults = append(faults, failed("status_unavailable", err))
	} else if record != nil {
		c := record.Counts
		remoteInterval := ""
		if record.RemotePollInterval > 0 {
			remoteInterval = record.RemotePollInterval.String()
		}
		r.LastRecorded = &Observation{RemotePollInterval: remoteInterval, RemoteCheckedAt: record.RemoteCheckedAt, RemoteOutcome: record.RemoteOutcome, WatchLocal: record.WatchLocal, Mode: record.Mode, Interval: record.Interval.String(), Phase: record.Phase, Outcome: record.Outcome, UpdatedAt: record.UpdatedAt, AttemptStartedAt: record.AttemptStartedAt, AttemptFinishedAt: record.AttemptFinishedAt, NextAttemptAt: record.NextAttemptAt, Reason: record.Reason, Counts: OperationCounts{Downloaded: c.Downloaded, Uploaded: c.Uploaded, CreatedDirectories: c.CreatedDirectories, Skipped: c.Skipped, Updated: c.Updated, Moved: c.Moved, Trashed: c.Trashed}}
	}
	summary, err := service.InspectProfile(ctx, profile)
	if errors.Is(err, privatefs.ErrLocked) {
		r.ProfileState = "busy"
	} else if err != nil {
		faults = append(faults, failed("profile_unavailable", err))
	} else {
		r.ProfileState = "available"
		r.Profile = &ProfileView{PopulationComplete: summary.PopulationComplete, ConflictCount: summary.ConflictCount, RecoveryPending: summary.RecoveryPending}
	}
	return r, errors.Join(faults...)
}
