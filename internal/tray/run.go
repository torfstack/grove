package tray

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/torfstack/grove/internal/daemon"
	"github.com/torfstack/grove/internal/syncengine"
)

type TrayBus interface {
	Publish(View) error
	Actions() <-chan Action
	Done() <-chan struct{}
	Close() error
}
type Controller interface {
	Observe(context.Context) (ServiceState, error)
	Request(context.Context, Action, Sample) error
	Close() error
}
type Dependencies struct {
	Inspect     func(context.Context, string) (daemon.StatusReport, error)
	Clock       daemon.TimerClock
	OpenBus     func(context.Context, Options) (TrayBus, error)
	OpenService func(context.Context, Options) (Controller, error)
}
type Failure struct {
	reason string
	cause  error
}

func (e *Failure) Error() string                  { return "tray failed: " + e.reason }
func (e *Failure) Unwrap() error                  { return e.cause }
func trayFailed(reason string, cause error) error { return &Failure{reason, cause} }
func Run(ctx context.Context, opts Options, deps Dependencies) (err error) {
	if ctx.Err() != nil {
		return nil
	}
	opts, err = NormalizeOptions(opts)
	if err != nil {
		return trayFailed("invalid profile or tray configuration", err)
	}
	if deps.Inspect == nil {
		deps.Inspect = func(ctx context.Context, path string) (daemon.StatusReport, error) {
			return daemon.Inspect(ctx, path, syncengine.Service{})
		}
	}
	if deps.Clock == nil {
		deps.Clock = trayClock{}
	}
	if deps.OpenBus == nil {
		deps.OpenBus = func(ctx context.Context, o Options) (TrayBus, error) { return OpenBus(ctx, o) }
	}
	if deps.OpenService == nil {
		deps.OpenService = func(ctx context.Context, o Options) (Controller, error) {
			return OpenService(ctx, o)
		}
	}
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	report, inspectErr := deps.Inspect(life, opts.ProfileDir)
	if ctx.Err() != nil {
		return nil
	}
	if inspectErr != nil {
		return trayFailed("profile unavailable", inspectErr)
	}
	bus, err := deps.OpenBus(life, opts)
	if err != nil {
		return trayFailed("session bus unavailable or tray already running", err)
	}
	var controller Controller
	var workers sync.WaitGroup
	var timer daemon.Timer
	defer func() {
		cancel()
		if timer != nil {
			timer.Stop()
		}
		workers.Wait()
		if controller != nil {
			if closeErr := controller.Close(); closeErr != nil {
				err = errors.Join(err, trayFailed("service cleanup failed", closeErr))
			}
		}
		if closeErr := bus.Close(); closeErr != nil {
			err = errors.Join(err, trayFailed("bus cleanup failed", closeErr))
		}
	}()
	service := ServiceState{}
	if opts.ServiceInstance != "" {
		controller, err = deps.OpenService(life, opts)
		if err != nil {
			controller = nil
			service = ServiceState{Unit: "groved@" + opts.ServiceInstance + ".service", Phase: "unavailable", Reason: "service monitoring unavailable"}
			err = nil
		} else {
			service, _ = controller.Observe(life)
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	memory := Memory{}
	current := Sample{Status: report}
	var view View
	controlActive := false
	controlError := ""
	publish := func() error {
		display := service
		if controlActive {
			display.CanStart = false
			display.CanStop = false
		}
		if controlError != "" {
			display.Reason = controlError
		}
		memory, view = Reduce(memory, current, display, opts)
		if err := bus.Publish(view); err != nil {
			return trayFailed("status publication failed", err)
		}
		return nil
	}
	if err := publish(); err != nil {
		return err
	}
	type observation struct {
		sample  Sample
		service ServiceState
	}
	requests := make(chan struct{}, 1)
	observed := make(chan observation, 1)
	controlDone := make(chan error, 1)
	requestRefresh := func() {
		select {
		case requests <- struct{}{}:
		default:
		}
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-life.Done():
				return
			case <-requests:
				report, err := deps.Inspect(life, opts.ProfileDir)
				result := observation{sample: Sample{Status: report, Err: err}}
				if controller != nil {
					result.service, _ = controller.Observe(life)
				}
				select {
				case observed <- result:
				case <-life.Done():
					return
				}
			}
		}
	}()
	resetTimer := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = deps.Clock.NewTimer(2 * time.Second)
	}
	resetTimer()
	pending := ActionNone
	enabled := func(action Action) bool {
		for _, row := range view.Rows {
			if row.Action == action && row.Enabled {
				return true
			}
		}
		return false
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-bus.Done():
			if ctx.Err() != nil {
				return nil
			}
			return trayFailed("session bus disconnected", nil)
		case <-timer.C():
			requestRefresh()
		case action := <-bus.Actions():
			switch action {
			case ActionQuit:
				return nil
			case ActionRefresh:
				requestRefresh()
			case ActionStart, ActionStop:
				if !controlActive && pending == ActionNone && controller != nil && enabled(action) {
					pending = action
					requestRefresh()
				}
			}
		case result := <-observed:
			if ctx.Err() != nil {
				return nil
			}
			current = result.sample
			if controller != nil {
				service = result.service
			}
			resetTimer()
			if err := publish(); err != nil {
				return err
			}
			if pending != ActionNone {
				action := pending
				pending = ActionNone
				if enabled(action) && !controlActive {
					controlActive = true
					controlError = ""
					service.CanStart = false
					service.CanStop = false
					service.Phase = "starting"
					if action == ActionStop {
						service.Phase = "stopping"
					}
					if err := publish(); err != nil {
						return err
					}
					snapshot := current
					workers.Add(1)
					go func() {
						defer workers.Done()
						err := controller.Request(life, action, snapshot)
						select {
						case controlDone <- err:
						case <-life.Done():
						}
					}()
				}
			}
		case controlErr := <-controlDone:
			if ctx.Err() != nil {
				return nil
			}
			controlActive = false
			if controlErr != nil {
				controlError = "service action failed or outcome unknown"
			}
			if err := publish(); err != nil {
				return err
			}
			requestRefresh()
		}
	}
}

type trayClock struct{}
type trayTimer struct{ timer *time.Timer }

func (trayClock) Now() time.Time { return time.Now() }
func (trayClock) Wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (trayClock) NewTimer(d time.Duration) daemon.Timer { return trayTimer{time.NewTimer(d)} }
func (t trayTimer) C() <-chan time.Time                 { return t.timer.C }
func (t trayTimer) Stop() bool                          { return t.timer.Stop() }
