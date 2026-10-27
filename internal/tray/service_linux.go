package tray

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/torfstack/grove/internal/daemon"
)

const managerName = "org.freedesktop.systemd1"
const managerInterface = managerName + ".Manager"
const managerPath dbus.ObjectPath = "/org/freedesktop/systemd1"

type serviceExec struct {
	Path                                       string
	Args                                       []string
	IgnoreFailure                              bool
	Start, StartMonotonic, Stop, StopMonotonic uint64
	PID                                        uint32
	Code, Status                               int32
}
type serviceJob struct {
	ID   uint32
	Path dbus.ObjectPath
}
type Service struct {
	conn                                      *dbus.Conn
	opts                                      Options
	unit                                      string
	ctx                                       context.Context
	cancel                                    context.CancelFunc
	wg                                        sync.WaitGroup
	mu                                        sync.Mutex
	queryMu                                   sync.Mutex
	requestMu                                 sync.Mutex
	owner, subscribed, pending, phase, reason string
	requesting, uncertain                     bool
	results                                   map[dbus.ObjectPath]string
	closeOnce                                 sync.Once
	closeErr                                  error
}

func OpenService(ctx context.Context, conn *dbus.Conn, opts Options) (*Service, error) {
	if !validInstance(opts.SystemdInstance) {
		_ = conn.Close()
		return nil, errors.New("invalid service instance")
	}
	life, cancel := context.WithCancel(ctx)
	s := &Service{conn: conn, opts: opts, unit: "groved@" + opts.SystemdInstance + ".service", ctx: life, cancel: cancel, results: map[dbus.ObjectPath]string{}}
	if err := conn.AddMatchSignal(dbus.WithMatchSender(managerName), dbus.WithMatchInterface(managerInterface), dbus.WithMatchMember("JobRemoved")); err != nil {
		cancel()
		_ = conn.Close()
		return nil, errors.New("service monitoring unavailable")
	}
	sigs := make(chan *dbus.Signal, 64)
	conn.Signal(sigs)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer conn.RemoveSignal(sigs)
		for {
			select {
			case <-life.Done():
				return
			case <-conn.Context().Done():
				return
			case sig := <-sigs:
				if sig == nil || sig.Name != managerInterface+".JobRemoved" || len(sig.Body) != 4 {
					continue
				}
				path, ok := sig.Body[1].(dbus.ObjectPath)
				unit, unitOK := sig.Body[2].(string)
				result, resultOK := sig.Body[3].(string)
				if !ok || !unitOK || !resultOK || unit != s.unit {
					continue
				}
				s.mu.Lock()
				if len(s.results) >= 32 {
					s.results = map[dbus.ObjectPath]string{}
				}
				s.results[path] = result
				s.mu.Unlock()
			}
		}
	}()
	return s, nil
}
func (s *Service) Close() error {
	s.closeOnce.Do(func() { s.cancel(); s.closeErr = s.conn.Close(); s.wg.Wait() })
	return s.closeErr
}
func (s *Service) unavailable(reason string) (ServiceState, error) {
	return ServiceState{Unit: s.unit, Phase: "unavailable", Reason: reason}, errors.New(reason)
}
func (s *Service) Observe(ctx context.Context) (ServiceState, error) {
	s.queryMu.Lock()
	defer s.queryMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var owner string
	if err := s.conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, managerName).Store(&owner); err != nil {
		return s.unavailable("user service manager unavailable")
	}
	s.mu.Lock()
	if s.owner != owner {
		if s.pending != "" || s.requesting {
			s.uncertain = true
			s.reason = "service action outcome unknown"
		}
		s.owner = owner
		s.pending = ""
		s.requesting = false
		s.results = map[dbus.ObjectPath]string{}
	}
	subscribed := s.subscribed == owner
	s.mu.Unlock()
	manager := s.conn.Object(owner, managerPath)
	if !subscribed {
		if err := manager.CallWithContext(ctx, managerInterface+".Subscribe", 0).Err; err != nil {
			return s.unavailable("service monitoring unavailable")
		}
		s.mu.Lock()
		s.subscribed = owner
		s.mu.Unlock()
	}
	var path dbus.ObjectPath
	if err := manager.CallWithContext(ctx, managerInterface+".LoadUnit", 0, s.unit).Store(&path); err != nil {
		return s.unavailable("selected service unavailable")
	}
	obj := s.conn.Object(owner, path)
	var unitProps, execProps map[string]dbus.Variant
	if obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, managerName+".Unit").Store(&unitProps) != nil || obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, managerName+".Service").Store(&execProps) != nil {
		return s.unavailable("service configuration unavailable")
	}
	if unitProps["Id"].Value() != s.unit {
		return s.unavailable("service unit does not match")
	}
	var commands []serviceExec
	if err := dbus.Store([]any{execProps["ExecStart"].Value()}, &commands); err != nil || len(commands) != 1 {
		return s.unavailable("service command unsupported")
	}
	cmd := commands[0]
	if filepath.Base(cmd.Path) != "groved" || !filepath.IsAbs(cmd.Path) || len(cmd.Args) < 2 || filepath.Base(cmd.Args[0]) != "groved" {
		return s.unavailable("service command unsupported")
	}
	configPath := ""
	switch {
	case len(cmd.Args) == 3 && cmd.Args[1] == "--config":
		configPath = cmd.Args[2]
	case len(cmd.Args) == 2 && strings.HasPrefix(cmd.Args[1], "--config="):
		configPath = strings.TrimPrefix(cmd.Args[1], "--config=")
	default:
		return s.unavailable("service command unsupported")
	}
	cfg, err := daemon.LoadConfig(configPath)
	if err != nil || cfg.ProfileDir != s.opts.ProfileDir {
		return s.unavailable("service profile does not match")
	}
	state, ok := unitProps["ActiveState"].Value().(string)
	if !ok {
		return s.unavailable("service state unavailable")
	}
	var job serviceJob
	if err := dbus.Store([]any{unitProps["Job"].Value()}, &job); err != nil {
		return s.unavailable("service job unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if result, ok := s.results[dbus.ObjectPath(s.pending)]; ok && s.pending != "" {
		if result != "done" {
			s.reason = "service action failed"
		}
		delete(s.results, dbus.ObjectPath(s.pending))
		s.pending = ""
		s.phase = ""
	}
	transitional := state != "active" && state != "inactive" && state != "failed"
	if s.pending != "" && job.ID == 0 && !transitional {
		s.pending = ""
		s.phase = ""
	}
	if s.uncertain && job.ID == 0 && !transitional {
		s.uncertain = false
	}
	view := ServiceState{Unit: s.unit, Phase: state, Reason: s.reason}
	if s.pending != "" || s.requesting {
		view.Phase = s.phase
		return view, nil
	}
	if transitional || job.ID != 0 || s.uncertain {
		return view, nil
	}
	view.CanStart = state == "inactive" || state == "failed"
	view.CanStop = state == "active"
	return view, nil
}
func (s *Service) Request(ctx context.Context, action Action, current Sample) error {
	if !s.requestMu.TryLock() {
		return errors.New("service action already pending")
	}
	defer s.requestMu.Unlock()
	if action != ActionStart && action != ActionStop {
		return errors.New("unsupported service action")
	}
	if current.Err != nil || current.Status.Owner == "unknown" {
		return errors.New("profile ownership unavailable")
	}
	if action == ActionStart && (current.Status.Owner != "inactive" || current.Status.ProfileState != "available") {
		return errors.New("profile already active or unavailable")
	}
	state, err := s.Observe(ctx)
	if err != nil {
		return err
	}
	if action == ActionStart && !state.CanStart || action == ActionStop && !state.CanStop {
		return errors.New("service action unavailable")
	}
	s.mu.Lock()
	owner := s.owner
	s.requesting = true
	s.reason = ""
	s.results = map[dbus.ObjectPath]string{}
	s.phase = "starting"
	if action == ActionStop {
		s.phase = "stopping"
	}
	s.mu.Unlock()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	method := "StartUnit"
	if action == ActionStop {
		method = "StopUnit"
	}
	var job dbus.ObjectPath
	err = s.conn.Object(owner, managerPath).CallWithContext(callCtx, managerInterface+"."+method, 0, s.unit, "fail").Store(&job)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requesting = false
	if err != nil {
		s.uncertain = true
		s.reason = "service action outcome unknown"
		s.phase = ""
		return errors.New("service action unavailable or outcome unknown")
	}
	s.pending = string(job)
	if result, ok := s.results[job]; ok {
		if result != "done" {
			s.reason = "service action failed"
		}
		s.pending = ""
		s.phase = ""
		delete(s.results, job)
	}
	return nil
}
