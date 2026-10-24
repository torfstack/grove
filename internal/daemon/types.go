package daemon

import (
	"context"
	"io"
	"time"

	"github.com/torfstack/grove/internal/syncengine"
)

type Clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

type Record struct {
	WatchLocal        bool              `json:"watch_local"`
	Version           int               `json:"version"`
	SessionID         string            `json:"session_id"`
	Mode              Mode              `json:"mode"`
	Interval          time.Duration     `json:"interval"`
	Phase             string            `json:"phase"`
	Outcome           string            `json:"outcome"`
	UpdatedAt         time.Time         `json:"updated_at"`
	AttemptStartedAt  *time.Time        `json:"attempt_started_at,omitempty"`
	AttemptFinishedAt *time.Time        `json:"attempt_finished_at,omitempty"`
	NextAttemptAt     *time.Time        `json:"next_attempt_at,omitempty"`
	Counts            syncengine.Result `json:"counts"`
	Reason            string            `json:"reason,omitempty"`
}

type Dependencies struct {
	InspectProfile func(context.Context, string) (syncengine.ProfileSummary, error)
	RunExisting    func(context.Context, string, syncengine.Binding, bool) (syncengine.Result, error)
	Clock          Clock
	AcquireLease   func(string) (io.Closer, error)
	SaveRecord     func(string, Record) error
}
