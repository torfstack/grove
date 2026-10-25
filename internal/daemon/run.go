package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

type failure struct {
	reason string
	cause  error
}

func (e *failure) Error() string            { return "daemon failed: " + e.reason }
func (e *failure) Unwrap() error            { return e.cause }
func failed(reason string, err error) error { return &failure{reason: reason, cause: err} }

func cancellationOnly(err, cancellationCause error) bool {
	if err == context.Canceled || err == context.DeadlineExceeded {
		return true
	}
	if multiple, ok := err.(interface{ Unwrap() []error }); ok {
		causes := multiple.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !cancellationOnly(cause, cancellationCause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return cancellationOnly(wrapped.Unwrap(), cancellationCause)
	}
	return cancellationCause != nil && errors.Is(err, cancellationCause)
}

func Run(ctx context.Context, configPath string, deps Dependencies) (err error) {
	if ctx.Err() != nil {
		return nil
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	service := syncengine.Service{}
	if deps.InspectProfile == nil {
		deps.InspectProfile = service.InspectProfile
	}
	if deps.RunExisting == nil {
		deps.RunExisting = service.RunExisting
	}
	if deps.CheckRemoteChanges == nil {
		deps.CheckRemoteChanges = service.CheckRemoteChanges
	}
	if deps.Clock == nil {
		deps.Clock = realClock{}
	}
	if deps.SaveRecord == nil {
		deps.SaveRecord = SaveRecord
	}
	if deps.AcquireLease == nil {
		deps.AcquireLease = func(path string) (io.Closer, error) { return privatefs.AcquireLease(path) }
	}
	info, err := os.Stat(cfg.ProfileDir)
	if err != nil || !info.IsDir() {
		return failed("profile_unavailable", err)
	}
	info, err = os.Lstat(filepath.Join(cfg.ProfileDir, "state.json"))
	if err != nil || !info.Mode().IsRegular() {
		return failed("profile_unavailable", err)
	}
	lease, err := deps.AcquireLease(filepath.Join(cfg.ProfileDir, "daemon.lock"))
	if err != nil {
		return failed("ownership_unavailable", err)
	}
	defer func() {
		if closeErr := lease.Close(); closeErr != nil {
			err = errors.Join(err, failed("lease_cleanup_failed", closeErr))
		}
	}()
	if ctx.Err() != nil {
		return nil
	}
	summary, err := deps.InspectProfile(ctx, cfg.ProfileDir)
	if err != nil {
		if ctx.Err() != nil && cancellationOnly(err, context.Cause(ctx)) {
			return nil
		}
		return failed("profile_unavailable", err)
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := ValidateProfileConfig(cfg, summary); err != nil {
		return err
	}
	session := make([]byte, 16)
	if _, err := rand.Read(session); err != nil {
		return failed("session_unavailable", err)
	}
	r := Record{RemotePollInterval: cfg.RemotePollInterval, WatchLocal: cfg.WatchLocal, Version: 1, SessionID: hex.EncodeToString(session), Mode: cfg.Mode, Interval: cfg.Interval, Outcome: "never"}
	if cfg.RemotePollInterval > 0 {
		r.RemoteOutcome = "never"
	}
	now := func() *time.Time { stamp := deps.Clock.Now().UTC(); return &stamp }
	save := func(phase string) error {
		r.Phase = phase
		r.UpdatedAt = *now()
		if err := deps.SaveRecord(cfg.ProfileDir, r); err != nil {
			return failed("status_write_failed", err)
		}
		return nil
	}
	stop := func() error {
		r.NextAttemptAt = nil
		if r.AttemptStartedAt != nil && r.AttemptFinishedAt == nil {
			r.AttemptFinishedAt = now()
			r.Outcome = "cancelled"
			r.Reason = "cancelled"
		}
		if err := save("stopping"); err != nil {
			return err
		}
		return save("stopped")
	}
	if cfg.WatchLocal || cfg.RemotePollInterval > 0 {
		return runWatching(ctx, cfg, deps, summary.Binding, &r, save, stop)
	}
	if err := save("starting"); err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return stop()
		}
		r.AttemptStartedAt = now()
		r.AttemptFinishedAt = nil
		r.NextAttemptAt = nil
		r.Counts = syncengine.Result{}
		r.Outcome = "never"
		r.Reason = ""
		if err := save("syncing"); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return stop()
		}
		result, runErr := deps.RunExisting(ctx, cfg.ProfileDir, summary.Binding, cfg.Mode == ModeTwoWay)
		r.AttemptFinishedAt = now()
		recordAttempt(&r, result, runErr, context.Cause(ctx))
		if ctx.Err() != nil {
			stopErr := stop()
			if runErr != nil && !cancellationOnly(runErr, context.Cause(ctx)) && !errors.Is(runErr, privatefs.ErrLocked) {
				return errors.Join(stopErr, failed("sync_failed", runErr))
			}
			return stopErr
		}
		next := r.AttemptFinishedAt.Add(cfg.Interval)
		r.NextAttemptAt = &next
		if err := save("waiting"); err != nil {
			return err
		}
		if err := deps.Clock.Wait(ctx, cfg.Interval); err != nil {
			if ctx.Err() != nil && cancellationOnly(err, context.Cause(ctx)) {
				return stop()
			}
			return failed("clock_wait_failed", err)
		}
	}
}
