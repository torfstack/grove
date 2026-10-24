package daemon

import (
	"context"
	"errors"
	"time"

	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

type watchResult struct {
	result            syncengine.Result
	err               error
	started, finished time.Time
}

func recordAttempt(r *Record, result syncengine.Result, err error, cancellationCause error) {
	r.Counts = result
	switch {
	case err == nil:
		r.Outcome = "completed"
		r.Reason = ""
	case cancellationCause != nil && cancellationOnly(err, cancellationCause):
		r.Outcome = "cancelled"
		r.Reason = "cancelled"
	case errors.Is(err, privatefs.ErrLocked):
		r.Outcome = "busy"
		r.Reason = "resource_busy"
	default:
		r.Outcome = "failed"
		r.Reason = "sync_failed"
	}
}

func runWatching(ctx context.Context, cfg Config, deps Dependencies, binding syncengine.Binding, r *Record, save func(string) error, stop func() error) (err error) {
	clock, ok := deps.Clock.(TimerClock)
	if !ok {
		return failed("watch_clock_unavailable", nil)
	}
	open := deps.OpenWatcher
	if open == nil {
		open = openWatcher
	}
	life, cancel := context.WithCancelCause(ctx)
	defer cancel(context.Canceled)
	w, err := open(life, binding.LocalDir, deps.Clock)
	if err != nil {
		return failed("watch_setup_failed", err)
	}
	defer func() {
		if closeErr := w.Close(); closeErr != nil {
			err = errors.Join(err, failed("watch_cleanup_failed", closeErr))
		}
		if fault := w.Take().Err; fault != nil && !errors.Is(err, fault) {
			err = errors.Join(err, failed("watch_failed", fault))
		}
	}()
	if err := save("starting"); err != nil {
		return err
	}
	s := newSchedule(cfg.Interval)
	results := make(chan watchResult, 1)
	active := false
	finish := func(out watchResult) {
		stamp := out.finished.UTC()
		r.AttemptFinishedAt = &stamp
		recordAttempt(r, out.result, out.err, context.Cause(life))
		s.complete(out.started, out.finished, out.err == nil)
	}
	defer func() {
		cancel(context.Canceled)
		if active {
			out := <-results
			finish(out)
			if out.err != nil && !cancellationOnly(out.err, context.Cause(life)) && !errors.Is(out.err, privatefs.ErrLocked) {
				err = errors.Join(err, failed("sync_failed", out.err))
			}
		}
		if err != nil {
			err = errors.Join(err, stop())
		}
	}()
	take := func() error {
		b := w.Take()
		if b.Err != nil {
			return failed("watch_failed", b.Err)
		}
		s.hint(b.First, b.Last)
		return nil
	}
	var timer Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	var due time.Time
	for {
		if err := ctx.Err(); err != nil {
			cancel(context.Cause(ctx))
			if active {
				out := <-results
				active = false
				finish(out)
				if out.err != nil && !cancellationOnly(out.err, context.Cause(ctx)) && !errors.Is(out.err, privatefs.ErrLocked) {
					return errors.Join(stop(), failed("sync_failed", out.err))
				}
			}
			return stop()
		}
		if err := take(); err != nil {
			return err
		}
		if !active {
			now := clock.Now()
			next := s.next(now)
			if !next.After(now) {
				// Take and consume pending hints before starting; later hints belong to the follow-up.
				if err := take(); err != nil {
					return err
				}
				s.begin(now)
				r.AttemptStartedAt = utcStamp(now)
				r.AttemptFinishedAt = nil
				r.NextAttemptAt = nil
				r.Counts = syncengine.Result{}
				r.Outcome = "never"
				r.Reason = ""
				if err := save("syncing"); err != nil {
					return err
				}
				if ctx.Err() != nil {
					continue
				}
				active = true
				due = time.Time{}
				if timer != nil {
					timer.Stop()
					timer = nil
				}
				go func(started time.Time) {
					result, runErr := deps.RunExisting(life, cfg.ProfileDir, binding, cfg.Mode == ModeTwoWay)
					results <- watchResult{result, runErr, started, clock.Now()}
				}(now)
			} else if !next.Equal(due) {
				if timer != nil {
					timer.Stop()
				}
				due = next
				r.NextAttemptAt = utcStamp(next)
				if err := save("waiting"); err != nil {
					return err
				}
				timer = clock.NewTimer(next.Sub(now))
			}
		}
		var ticks <-chan time.Time
		if timer != nil {
			ticks = timer.C()
		}
		select {
		case <-ctx.Done():
		case <-w.Ready():
		case <-ticks:
			timer = nil
			due = time.Time{}
		case out := <-results:
			active = false
			finish(out)
			repairErr := w.Reconcile(life)
			if ctx.Err() != nil {
				var faults []error
				if out.err != nil && !cancellationOnly(out.err, context.Cause(ctx)) && !errors.Is(out.err, privatefs.ErrLocked) {
					faults = append(faults, failed("sync_failed", out.err))
				}
				if repairErr != nil && !cancellationOnly(repairErr, context.Cause(ctx)) {
					faults = append(faults, failed("watch_coverage_failed", repairErr))
				}
				faults = append(faults, stop())
				return errors.Join(faults...)
			}
			if repairErr != nil {
				return failed("watch_coverage_failed", repairErr)
			}
		}
	}
}
func utcStamp(stamp time.Time) *time.Time { utc := stamp.UTC(); return &utc }
