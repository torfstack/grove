package daemon

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/torfstack/grove/internal/privatefs"
	"github.com/torfstack/grove/internal/syncengine"
)

type remoteSource struct {
	ctx       context.Context
	cancel    context.CancelFunc
	interval  time.Duration
	clock     TimerClock
	check     func(context.Context, string) (syncengine.RemoteChangesResult, error)
	box       *watchMailbox
	done      chan struct{}
	closeOnce sync.Once
}

func openRemoteSource(ctx context.Context, interval time.Duration, clock TimerClock, check func(context.Context, string) (syncengine.RemoteChangesResult, error)) ChangeSource {
	life, cancel := context.WithCancel(ctx)
	s := &remoteSource{ctx: life, cancel: cancel, interval: interval, clock: clock, check: check, box: newWatchMailbox(), done: make(chan struct{})}
	go s.run()
	return s
}
func (s *remoteSource) Ready() <-chan struct{} { return s.box.ready }
func (s *remoteSource) Take() WatchBatch       { return s.box.take() }
func (s *remoteSource) Close() error           { s.closeOnce.Do(func() { s.cancel(); <-s.done }); return nil }
func (s *remoteSource) run() {
	defer close(s.done)
	cursor := ""
	for {
		if s.ctx.Err() != nil {
			return
		}
		checkCtx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		result, err := s.check(checkCtx, cursor)
		cancel()
		fatal := errors.Is(err, syncengine.ErrRemoteCheckCleanup) || (s.ctx.Err() != nil && err != nil && !cancellationOnly(err, context.Cause(s.ctx)))
		if fatal {
			s.box.observeRemote(s.clock.Now(), "failed", result.Changed, failed("remote_check_failed", err))
			return
		}
		if s.ctx.Err() != nil {
			return
		}
		outcome := "completed"
		switch {
		case errors.Is(err, privatefs.ErrLocked):
			outcome = "busy"
		case err != nil:
			outcome = "failed"
		case result.Cursor == "" || len(result.Cursor) > 64*1024:
			outcome = "failed"
		default:
			cursor = result.Cursor
		}
		s.box.observeRemote(s.clock.Now(), outcome, result.Changed, nil)
		timer := s.clock.NewTimer(s.interval)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-timer.C():
			timer.Stop()
		}
	}
}
