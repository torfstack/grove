package tray

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/torfstack/grove/internal/daemon"
)

type testTray struct {
	views   chan View
	actions chan Action
	done    chan struct{}
	closed  atomic.Bool
	fail    error
}

func newTestTray() *testTray {
	return &testTray{views: make(chan View, 100), actions: make(chan Action, 100), done: make(chan struct{})}
}
func (b *testTray) Publish(v View) error   { b.views <- v; return b.fail }
func (b *testTray) Actions() <-chan Action { return b.actions }
func (b *testTray) Done() <-chan struct{}  { return b.done }
func (b *testTray) Close() error           { b.closed.Store(true); return nil }
func nextView(t *testing.T, b *testTray) View {
	t.Helper()
	select {
	case v := <-b.views:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("no tray publication")
	}
	return View{}
}

type manualClock struct{ timers chan *manualTimer }
type manualTimer struct{ ticks chan time.Time }

func (c *manualClock) Now() time.Time { return time.Unix(0, 0) }
func (c *manualClock) Wait(ctx context.Context, _ time.Duration) error {
	<-ctx.Done()
	return ctx.Err()
}
func (c *manualClock) NewTimer(d time.Duration) daemon.Timer {
	if d != 2*time.Second {
		panic("wrong refresh interval")
	}
	timer := &manualTimer{make(chan time.Time, 1)}
	c.timers <- timer
	return timer
}
func (t *manualTimer) C() <-chan time.Time { return t.ticks }
func (t *manualTimer) Stop() bool          { return true }
func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition timeout")
}
func runTestTray(t *testing.T, opts Options, inspect func(context.Context, string) (daemon.StatusReport, error), clock daemon.TimerClock) (*testTray, chan error, context.CancelFunc) {
	t.Helper()
	bus := newTestTray()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, opts, Dependencies{Inspect: inspect, Clock: clock, OpenBus: func(context.Context, Options) (TrayBus, error) { return bus, nil }})
	}()
	return bus, result, cancel
}
func TestTrayStartupAndPolling(t *testing.T) {
	var calls atomic.Int32
	gate := make(chan struct{})
	started := make(chan struct{})
	inspect := func(ctx context.Context, _ string) (daemon.StatusReport, error) {
		n := calls.Add(1)
		if n == 2 {
			close(started)
			select {
			case <-gate:
			case <-ctx.Done():
				return daemon.StatusReport{}, ctx.Err()
			}
		}
		return healthy().Status, nil
	}
	clock := &manualClock{make(chan *manualTimer, 20)}
	bus, result, cancel := runTestTray(t, optionsFixture(t), inspect, clock)
	nextView(t, bus)
	if calls.Load() != 1 {
		t.Fatal("startup inspection count")
	}
	timer := <-clock.timers
	timer.ticks <- time.Unix(0, 0)
	<-started
	for i := 0; i < 10; i++ {
		bus.actions <- ActionRefresh
	}
	waitUntil(t, func() bool { return len(bus.actions) == 0 })
	if calls.Load() != 2 {
		t.Fatal("overlapping inspection")
	}
	close(gate)
	waitUntil(t, func() bool { return calls.Load() == 3 })
	cancel()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if !bus.closed.Load() {
		t.Fatal("bus not closed")
	}
}
func TestTrayProfileUnavailable(t *testing.T) {
	var calls atomic.Int32
	inspect := func(context.Context, string) (daemon.StatusReport, error) {
		if calls.Add(1) == 1 {
			return healthy().Status, nil
		}
		return daemon.StatusReport{Owner: "unknown", ProfileState: "unavailable"}, errors.New("private-path")
	}
	bus, result, _ := runTestTray(t, optionsFixture(t), inspect, &manualClock{make(chan *manualTimer, 10)})
	nextView(t, bus)
	bus.actions <- ActionRefresh
	v := nextView(t, bus)
	if v.Status != "NeedsAttention" || !strings.Contains(v.TooltipText, "Status unavailable") || strings.Contains(v.TooltipText, "private-path") {
		t.Fatalf("unavailable: %+v", v)
	}
	bus.actions <- ActionQuit
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

type testController struct {
	mu       sync.Mutex
	state    ServiceState
	requests []Action
	closed   bool
}

func (s *testController) Observe(context.Context) (ServiceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state, nil
}
func (s *testController) Request(_ context.Context, a Action, current Sample) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current.Status.Owner != "inactive" {
		return errors.New("not freshly inactive")
	}
	s.requests = append(s.requests, a)
	return nil
}
func (s *testController) Close() error { s.mu.Lock(); defer s.mu.Unlock(); s.closed = true; return nil }
func TestTrayActions(t *testing.T) {
	opts := optionsFixture(t)
	opts.SystemdInstance = "personal"
	bus := newTestTray()
	ctrl := &testController{state: ServiceState{Unit: "groved@personal.service", Phase: "inactive", CanStart: true}}
	var calls atomic.Int32
	inspect := func(context.Context, string) (daemon.StatusReport, error) {
		calls.Add(1)
		s := healthy().Status
		s.Owner = "inactive"
		return s, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, opts, Dependencies{Inspect: inspect, Clock: &manualClock{make(chan *manualTimer, 20)}, OpenBus: func(context.Context, Options) (TrayBus, error) { return bus, nil }, OpenService: func(context.Context, Options) (Controller, error) { return ctrl, nil }})
	}()
	nextView(t, bus)
	ctrl.mu.Lock()
	if len(ctrl.requests) != 0 {
		t.Fatal("launch started service")
	}
	ctrl.mu.Unlock()
	bus.actions <- ActionStart
	waitUntil(t, func() bool { ctrl.mu.Lock(); defer ctrl.mu.Unlock(); return len(ctrl.requests) == 1 })
	if calls.Load() < 2 {
		t.Fatal("service used stale startup inspection")
	}
	bus.actions <- ActionQuit
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	ctrl.mu.Lock()
	defer ctrl.mu.Unlock()
	if !ctrl.closed || len(ctrl.requests) != 1 || ctrl.requests[0] != ActionStart {
		t.Fatalf("quit changed service: %+v", ctrl.requests)
	}
}
func TestTrayShutdown(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "signal", true: "bus loss"}[lost], func(t *testing.T) {
			var calls atomic.Int32
			started := make(chan struct{})
			finished := make(chan struct{})
			inspect := func(ctx context.Context, _ string) (daemon.StatusReport, error) {
				if calls.Add(1) == 1 {
					return healthy().Status, nil
				}
				close(started)
				<-ctx.Done()
				close(finished)
				return daemon.StatusReport{}, ctx.Err()
			}
			bus, result, cancel := runTestTray(t, optionsFixture(t), inspect, &manualClock{make(chan *manualTimer, 10)})
			nextView(t, bus)
			bus.actions <- ActionRefresh
			<-started
			if lost {
				close(bus.done)
			} else {
				cancel()
			}
			err := <-result
			if lost && err == nil {
				t.Fatal("lost bus success")
			}
			if !lost && err != nil {
				t.Fatal(err)
			}
			select {
			case <-finished:
			default:
				t.Fatal("inspection not joined")
			}
		})
	}
}
func TestTrayStartupBusyAndInvalid(t *testing.T) {
	opts := optionsFixture(t)
	bus := newTestTray()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, opts, Dependencies{Inspect: func(context.Context, string) (daemon.StatusReport, error) {
			return daemon.StatusReport{Owner: "active", ProfileState: "busy"}, nil
		}, OpenBus: func(context.Context, Options) (TrayBus, error) { return bus, nil }})
	}()
	if v := nextView(t, bus); v.Status != "NeedsAttention" {
		t.Fatal("busy startup advertised healthy")
	}
	bus.actions <- ActionQuit
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	opened := false
	err := Run(context.Background(), opts, Dependencies{Inspect: func(context.Context, string) (daemon.StatusReport, error) {
		return daemon.StatusReport{}, errors.New("secret")
	}, OpenBus: func(context.Context, Options) (TrayBus, error) { opened = true; return bus, nil }})
	if err == nil || opened || strings.Contains(err.Error(), "secret") {
		t.Fatalf("invalid startup: %v %t", err, opened)
	}
}

type canceledBus struct {
	*testTray
	cancel context.CancelFunc
	once   sync.Once
}

func (b *canceledBus) Publish(v View) error {
	b.once.Do(func() { b.cancel(); close(b.done) })
	return nil
}
func TestTrayCanceledBus(t *testing.T) {
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		b := &canceledBus{testTray: newTestTray(), cancel: cancel}
		err := Run(ctx, optionsFixture(t), Dependencies{Inspect: func(context.Context, string) (daemon.StatusReport, error) { return healthy().Status, nil }, OpenBus: func(context.Context, Options) (TrayBus, error) { return b, nil }})
		cancel()
		if err != nil {
			t.Fatalf("requested shutdown reported as failure: %v", err)
		}
	}
}
