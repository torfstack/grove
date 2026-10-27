package tray

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/torfstack/grove/internal/daemon"
	"github.com/torfstack/grove/internal/privatefs"
)

type fakeExec struct {
	Path                             string
	Args                             []string
	Ignore                           bool
	Start, StartMono, Stop, StopMono uint64
	PID                              uint32
	Code, Status                     int32
}
type fakeJob struct {
	ID   uint32
	Path dbus.ObjectPath
}
type fakeManager struct {
	mu                  sync.Mutex
	conn                *dbus.Conn
	unit, state, config string
	commands            []fakeExec
	job                 fakeJob
	requests            chan string
	early, denied       bool
	gate                <-chan struct{}
}

const fakeUnitPath dbus.ObjectPath = "/org/freedesktop/systemd1/unit/groved_40personal_2eservice"

func (m *fakeManager) LoadUnit(unit string) (dbus.ObjectPath, *dbus.Error) {
	if unit != "groved@personal.service" {
		return "", invalid("wrong unit")
	}
	return fakeUnitPath, nil
}
func (m *fakeManager) Subscribe() *dbus.Error { return nil }
func (m *fakeManager) StartUnit(unit, mode string) (dbus.ObjectPath, *dbus.Error) {
	return m.request(unit, mode, "start")
}
func (m *fakeManager) StopUnit(unit, mode string) (dbus.ObjectPath, *dbus.Error) {
	return m.request(unit, mode, "stop")
}
func (m *fakeManager) request(unit, mode, action string) (dbus.ObjectPath, *dbus.Error) {
	m.mu.Lock()
	gate, early, denied := m.gate, m.early, m.denied
	m.mu.Unlock()
	m.requests <- action + ":" + unit + ":" + mode
	if denied {
		return "", dbus.NewError("org.freedesktop.DBus.Error.AccessDenied", []any{"private denial"})
	}
	job := dbus.ObjectPath("/org/freedesktop/systemd1/job/7")
	m.mu.Lock()
	m.job = fakeJob{7, job}
	m.mu.Unlock()
	if early {
		m.finish(action, "done")
	}
	if gate != nil {
		<-gate
	}
	return job, nil
}
func (m *fakeManager) finish(action, result string) {
	m.mu.Lock()
	m.job = fakeJob{0, "/"}
	if result == "done" {
		if action == "start" {
			m.state = "active"
		} else {
			m.state = "inactive"
		}
	}
	m.mu.Unlock()
	_ = m.conn.Emit("/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager.JobRemoved", uint32(7), dbus.ObjectPath("/org/freedesktop/systemd1/job/7"), "groved@personal.service", result)
}

type managerProperties struct{ m *fakeManager }

func (p *managerProperties) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	p.m.mu.Lock()
	defer p.m.mu.Unlock()
	switch iface {
	case "org.freedesktop.systemd1.Unit":
		return map[string]dbus.Variant{"Id": dbus.MakeVariant(p.m.unit), "ActiveState": dbus.MakeVariant(p.m.state), "Job": dbus.MakeVariant(p.m.job)}, nil
	case "org.freedesktop.systemd1.Service":
		commands := append([]fakeExec(nil), p.m.commands...)
		for i := range commands {
			commands[i].Args = append([]string(nil), commands[i].Args...)
		}
		return map[string]dbus.Variant{"ExecStart": dbus.MakeVariant(commands)}, nil
	default:
		return nil, invalid("unknown interface")
	}
}
func newFakeManager(t *testing.T) (*Service, *fakeManager, Sample) {
	t.Helper()
	addr := NewTestBus(t)
	c := testConn(t, addr)
	opts := optionsFixture(t)
	opts.SystemdInstance = "personal"
	config := filepath.Join(t.TempDir(), "private config.json")
	if err := privatefs.WriteJSON(config, map[string]any{"version": 1, "profile_dir": opts.ProfileDir, "mode": "download"}); err != nil {
		t.Fatal(err)
	}
	m := &fakeManager{conn: c, unit: "groved@personal.service", state: "inactive", config: config, commands: []fakeExec{{Path: "/usr/bin/groved", Args: []string{"/usr/bin/groved", "--config", config}}}, job: fakeJob{0, "/"}, requests: make(chan string, 10)}
	if err := c.Export(m, "/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager"); err != nil {
		t.Fatal(err)
	}
	if err := c.Export(&managerProperties{m}, fakeUnitPath, "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RequestName("org.freedesktop.systemd1", dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	s, err := OpenService(context.Background(), testConn(t, addr), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	sample := healthy()
	sample.Status.Owner = "inactive"
	return s, m, sample
}
func TestServiceMapping(t *testing.T) {
	s, m, _ := newFakeManager(t)
	state, err := s.Observe(testContext(t))
	if err != nil || !state.CanStart || state.CanStop {
		t.Fatalf("valid: %+v %v", state, err)
	}
	for _, change := range []func(){
		func() { m.mu.Lock(); m.commands[0].Args = []string{"/bin/sh", "-c", "groved"}; m.mu.Unlock() },
		func() { m.mu.Lock(); m.unit = "other.service"; m.mu.Unlock() },
		func() {
			m.mu.Lock()
			m.commands = []fakeExec{{Path: "/usr/bin/groved", Args: []string{"groved", "--config", m.config}}, {Path: "/usr/bin/groved"}}
			m.mu.Unlock()
		},
	} {
		change()
		state, err = s.Observe(testContext(t))
		if err == nil || state.CanStart || state.CanStop {
			t.Fatalf("unsafe mapping allowed: %+v %v", state, err)
		}
	}
}
func TestServiceAvailability(t *testing.T) {
	s, m, sample := newFakeManager(t)
	sample.Status.Owner = "active"
	if err := s.Request(testContext(t), ActionStart, sample); err == nil {
		t.Fatal("start over active owner")
	}
	select {
	case <-m.requests:
		t.Fatal("manager invoked over active owner")
	default:
	}
	m.mu.Lock()
	m.state = "active"
	m.mu.Unlock()
	state, err := s.Observe(testContext(t))
	if err != nil || !state.CanStop || state.CanStart {
		t.Fatalf("active: %+v %v", state, err)
	}
	if err := os.Chmod(m.config, 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.Request(testContext(t), ActionStop, healthy()); err == nil {
		t.Fatal("stale private-config validation used")
	}
}
func TestServiceJobs(t *testing.T) {
	s, m, sample := newFakeManager(t)
	if err := s.Request(testContext(t), ActionStart, sample); err != nil {
		t.Fatal(err)
	}
	if got := <-m.requests; got != "start:groved@personal.service:fail" {
		t.Fatal(got)
	}
	state, err := s.Observe(testContext(t))
	if err != nil || state.CanStart || state.CanStop || state.Phase != "starting" {
		t.Fatalf("pending: %+v %v", state, err)
	}
	if err := s.Request(testContext(t), ActionStart, sample); err == nil {
		t.Fatal("duplicate pending accepted")
	}
	m.finish("start", "failed")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, _ = s.Observe(testContext(t))
		if state.Reason == "service action failed" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if state.Reason != "service action failed" {
		t.Fatalf("lost failure: %+v", state)
	}
	m.mu.Lock()
	m.early = true
	m.mu.Unlock()
	if err := s.Request(testContext(t), ActionStart, sample); err != nil {
		t.Fatal(err)
	}
	state, err = s.Observe(testContext(t))
	if err != nil || !state.CanStop {
		t.Fatalf("early completion: %+v %v", state, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	active := m.state
	m.mu.Unlock()
	if active != "active" {
		t.Fatal("close stopped service")
	}
}
func TestServiceDeniedAndAbsent(t *testing.T) {
	s, m, sample := newFakeManager(t)
	m.mu.Lock()
	m.denied = true
	m.mu.Unlock()
	if err := s.Request(testContext(t), ActionStart, sample); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("denied: %v", err)
	}
	if _, err := m.conn.ReleaseName("org.freedesktop.systemd1"); err != nil {
		t.Fatal(err)
	}
	state, err := s.Observe(testContext(t))
	if err == nil || state.CanStart || state.CanStop {
		t.Fatalf("absent: %+v %v", state, err)
	}
}

func TestServiceMappingIndependentInputs(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *fakeManager)
	}{
		{"wrapper", func(_ *testing.T, m *fakeManager) {
			m.commands[0] = fakeExec{Path: "/bin/sh", Args: []string{"sh", "-c", "groved"}}
		}},
		{"extra flag", func(_ *testing.T, m *fakeManager) { m.commands[0].Args = append(m.commands[0].Args, "--version") }},
		{"other unit alias", func(_ *testing.T, m *fakeManager) { m.unit = "other.service" }},
		{"multiple commands", func(_ *testing.T, m *fakeManager) { m.commands = append(m.commands, m.commands[0]) }},
		{"mismatching profile", func(t *testing.T, m *fakeManager) {
			other := optionsFixture(t)
			if err := privatefs.WriteJSON(m.config, map[string]any{"version": 1, "profile_dir": other.ProfileDir, "mode": "download"}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, m, _ := newFakeManager(t)
			m.mu.Lock()
			tc.change(t, m)
			m.mu.Unlock()
			state, err := s.Observe(testContext(t))
			if err == nil || state.CanStart || state.CanStop {
				t.Fatalf("unsafe mapping: %+v %v", state, err)
			}
		})
	}
	t.Run("equals syntax", func(t *testing.T) {
		s, m, _ := newFakeManager(t)
		m.mu.Lock()
		m.commands[0].Args = []string{"groved", "--config=" + m.config}
		m.mu.Unlock()
		state, err := s.Observe(testContext(t))
		if err != nil || !state.CanStart {
			t.Fatalf("equals config: %+v %v", state, err)
		}
	})
}
func TestServiceLongStop(t *testing.T) {
	s, m, _ := newFakeManager(t)
	m.mu.Lock()
	m.state = "active"
	m.mu.Unlock()
	if err := s.Request(testContext(t), ActionStop, healthy()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		state, err := s.Observe(testContext(t))
		if err != nil || state.Phase != "stopping" || state.CanStop || state.CanStart {
			t.Fatalf("stopping: %+v %v", state, err)
		}
	}
	m.finish("stop", "done")
	state, err := s.Observe(testContext(t))
	if err != nil || state.Phase != "inactive" || !state.CanStart {
		t.Fatalf("stopped: %+v %v", state, err)
	}
}

func TestServiceDelayedFailureResult(t *testing.T) {
	s, m, sample := newFakeManager(t)
	if err := s.Request(testContext(t), ActionStart, sample); err != nil {
		t.Fatal(err)
	}
	<-m.requests
	m.mu.Lock()
	m.job = fakeJob{0, "/"}
	m.mu.Unlock()
	state, err := s.Observe(testContext(t))
	if err != nil || state.Reason != "service action outcome unknown" || state.CanStart || state.CanStop {
		t.Fatalf("missing job must stay explicitly unknown: %+v %v", state, err)
	}
	m.finish("start", "failed")
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err := s.Observe(testContext(t))
		if err != nil {
			t.Fatal(err)
		}
		if state.Reason == "service action failed" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("late failed job result lost after manager state settled")
}
func TestServiceStopUnavailableProfile(t *testing.T) {
	s, m, _ := newFakeManager(t)
	m.mu.Lock()
	m.state = "active"
	m.mu.Unlock()
	sample := Sample{Status: daemon.StatusReport{Owner: "unknown", ProfileState: "unavailable"}, Err: errors.New("private read failure")}
	if err := s.Request(testContext(t), ActionStop, sample); err != nil {
		t.Fatalf("cannot stop explicitly validated service: %v", err)
	}
	if got := <-m.requests; got != "stop:groved@personal.service:fail" {
		t.Fatalf("wrong stop target: %s", got)
	}
}
