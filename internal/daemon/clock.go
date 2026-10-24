package daemon

import (
	"context"
	"time"
)

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type TimerClock interface {
	Clock
	NewTimer(time.Duration) Timer
}

type realTimer struct{ timer *time.Timer }

func (t realTimer) C() <-chan time.Time          { return t.timer.C }
func (t realTimer) Stop() bool                   { return t.timer.Stop() }
func (realClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }
