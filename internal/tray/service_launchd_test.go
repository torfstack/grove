package tray

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/torfstack/grove/internal/daemon"
	"github.com/torfstack/grove/internal/privatefs"
)

type fakeLaunchctl struct {
	output       []byte
	printErr     error
	bootstrapErr error
	bootoutErr   error
	kickstartErr error
	calls        []string
}

func (f *fakeLaunchctl) Print(_ context.Context, target string) ([]byte, error) {
	f.calls = append(f.calls, "print:"+target)
	return append([]byte(nil), f.output...), f.printErr
}
func (f *fakeLaunchctl) Bootstrap(_ context.Context, domain, path string) error {
	f.calls = append(f.calls, "bootstrap:"+domain+":"+path)
	return f.bootstrapErr
}
func (f *fakeLaunchctl) Bootout(_ context.Context, domain, path string) error {
	f.calls = append(f.calls, "bootout:"+domain+":"+path)
	return f.bootoutErr
}
func (f *fakeLaunchctl) Kickstart(_ context.Context, target string) error {
	f.calls = append(f.calls, "kickstart:"+target)
	return f.kickstartErr
}

func launchdServiceFixture(t *testing.T, state string, loaded bool) (*launchdService, *fakeLaunchctl, string, string) {
	t.Helper()
	root := t.TempDir()
	profile, config := launchdConfigFixture(t, root, "profile")
	label := "com.torfstack.groved.personal"
	program := "/Applications/Grove.app/Contents/Helpers/groved"
	plistPath := filepath.Join(root, label+".plist")
	if err := os.WriteFile(plistPath, launchdPlistFixture(label, program, config, true, false, 0), 0600); err != nil {
		t.Fatal(err)
	}
	uid := uint32(os.Geteuid())
	domain := "gui/" + strconv.FormatUint(uint64(uid), 10)
	runner := &fakeLaunchctl{output: launchdPrintOutput(domain+"/"+label, label, state, plistPath, program, config)}
	if !loaded {
		runner.output = []byte(fmt.Sprintf("Bad request.\nCould not find service %q in domain for user gui: %d\n", label, uid))
		runner.printErr = errors.New("exit status 113")
	}
	service, err := newLaunchdController(Options{ProfileDir: profile, ServiceInstance: "personal"}, runner, domain, plistPath, uid)
	if err != nil {
		t.Fatal(err)
	}
	return service, runner, profile, plistPath
}

func launchdPrintOutput(target, label, state, plistPath, program, config string) []byte {
	return []byte(fmt.Sprintf("%s = {\n\tpath = %s\n\tstate = %s\n\tprogram = %s\n\targuments = {\n\t\t%s\n\t\t--config\n\t\t%s\n\t}\n}\n", target, plistPath, state, program, program, config))
}

func inactiveLaunchdSample() Sample {
	return Sample{Status: daemon.StatusReport{Owner: "inactive", ProfileState: "available", Profile: &daemon.ProfileView{PopulationComplete: true}}}
}

func TestLaunchdUnloadedAgentCanStart(t *testing.T) {
	service, runner, _, plistPath := launchdServiceFixture(t, "not running", false)
	state, err := service.Observe(context.Background())
	if err != nil || !state.CanStart || state.CanStop {
		t.Fatalf("unloaded agent state = %+v, %v", state, err)
	}
	if err := service.Request(context.Background(), ActionStart, inactiveLaunchdSample()); err != nil {
		t.Fatal(err)
	}
	want := "bootstrap:" + service.domain + ":" + plistPath
	if runner.calls[len(runner.calls)-1] != want {
		t.Fatalf("last launchctl call = %q, want %q", runner.calls[len(runner.calls)-1], want)
	}
}

func TestLaunchdLoadedAgentUsesOnlyVerifiedControls(t *testing.T) {
	service, runner, profile, plistPath := launchdServiceFixture(t, "not running", true)
	state, err := service.Observe(context.Background())
	if err != nil || !state.CanStart || state.CanStop {
		t.Fatalf("inactive loaded agent state = %+v, %v", state, err)
	}
	if err := service.Request(context.Background(), ActionStart, inactiveLaunchdSample()); err != nil {
		t.Fatal(err)
	}
	if runner.calls[len(runner.calls)-1] != "kickstart:"+service.domain+"/com.torfstack.groved.personal" {
		t.Fatalf("kickstart call = %#v", runner.calls)
	}

	lease, err := privatefs.AcquireLease(filepath.Join(profile, "daemon.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lease.Close(); err != nil {
			t.Errorf("close profile lease: %v", err)
		}
	}()
	runner.output = launchdPrintOutput(service.domain+"/com.torfstack.groved.personal", "com.torfstack.groved.personal", "running", plistPath, "/Applications/Grove.app/Contents/Helpers/groved", filepath.Join(filepath.Dir(plistPath), "config with spaces.json"))
	state, err = service.Observe(context.Background())
	if err != nil || !state.CanStop || state.CanStart {
		t.Fatalf("active loaded agent state = %+v, %v", state, err)
	}
	if err := service.Request(context.Background(), ActionStop, Sample{Status: daemon.StatusReport{Owner: "active"}}); err != nil {
		t.Fatal(err)
	}
	if runner.calls[len(runner.calls)-1] != "bootout:"+service.domain+":"+plistPath {
		t.Fatalf("bootout call = %#v", runner.calls)
	}
}

func TestLaunchdUnverifiedOwnerCannotStop(t *testing.T) {
	for _, tc := range []struct {
		name   string
		loaded bool
		state  string
	}{
		{name: "unloaded", loaded: false},
		{name: "loaded but not running", loaded: true, state: "not running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, runner, profile, _ := launchdServiceFixture(t, tc.state, tc.loaded)
			lease, err := privatefs.AcquireLease(filepath.Join(profile, "daemon.lock"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := lease.Close(); err != nil {
					t.Errorf("close profile lease: %v", err)
				}
			}()
			state, err := service.Observe(context.Background())
			if state.CanStop {
				t.Fatalf("unverified stop state = %+v, %v", state, err)
			}
			before := len(runner.calls)
			if err := service.Request(context.Background(), ActionStop, Sample{Status: daemon.StatusReport{Owner: "active"}}); err == nil {
				t.Fatal("unverified Stop accepted")
			}
			if len(runner.calls) != before+1 {
				t.Fatalf("unverified Stop reached launchctl: %#v", runner.calls[before:])
			}
		})
	}
}

func TestLaunchdUnknownOrMismatchedJobDoesNotControl(t *testing.T) {
	for name, alter := range map[string]func(t *testing.T, runner *fakeLaunchctl, plistPath string){
		"unknown output": func(_ *testing.T, runner *fakeLaunchctl, _ string) {
			runner.output = []byte("unsupported launchctl output")
		},
		"wrong domain": func(_ *testing.T, runner *fakeLaunchctl, _ string) {
			runner.output = []byte(strings.Replace(string(runner.output), "gui/"+strconv.Itoa(os.Geteuid()), "gui/99999", 1))
		},
		"wrong plist path": func(_ *testing.T, runner *fakeLaunchctl, _ string) {
			runner.output = []byte(strings.Replace(string(runner.output), "path = ", "path = /tmp/other.plist\n\tother path = ", 1))
		},
		"wrong executable": func(_ *testing.T, runner *fakeLaunchctl, _ string) {
			runner.output = []byte(strings.Replace(string(runner.output), "/Applications/Grove.app/Contents/Helpers/groved", "/bin/sh", 2))
		},
		"altered arguments": func(_ *testing.T, runner *fakeLaunchctl, _ string) {
			runner.output = []byte(strings.Replace(string(runner.output), "--config", "--config=other", 1))
		},
		"mismatched profile": func(t *testing.T, runner *fakeLaunchctl, plistPath string) {
			_, otherConfig := launchdConfigFixture(t, t.TempDir(), "other-profile")
			configPath := filepath.Join(filepath.Dir(plistPath), "config with spaces.json")
			runner.output = []byte(strings.Replace(string(runner.output), configPath, otherConfig, 1))
		},
		"changed loaded plist": func(t *testing.T, _ *fakeLaunchctl, plistPath string) {
			if err := os.WriteFile(plistPath, launchdPlistFixture("com.torfstack.groved.personal", "/bin/sh", "../../other.json", true, false, 0), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"command failure": func(_ *testing.T, runner *fakeLaunchctl, _ string) {
			runner.output = []byte("permission denied")
			runner.printErr = errors.New("exit status 1")
		},
		"malformed plist": func(t *testing.T, runner *fakeLaunchctl, plistPath string) {
			runner.output = []byte(fmt.Sprintf("Bad request.\nCould not find service %q in domain for user gui: %d\n", "com.torfstack.groved.personal", os.Geteuid()))
			runner.printErr = errors.New("exit status 113")
			if err := os.WriteFile(plistPath, []byte("not a plist"), 0600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			service, runner, _, plistPath := launchdServiceFixture(t, "running", true)
			alter(t, runner, plistPath)
			state, err := service.Observe(context.Background())
			if err == nil || state.CanStart || state.CanStop {
				t.Fatalf("unverified job enabled controls: %+v %v", state, err)
			}
			before := len(runner.calls)
			if err := service.Request(context.Background(), ActionStart, inactiveLaunchdSample()); err == nil {
				t.Fatal("unverified launchd job accepted Start")
			}
			if len(runner.calls) != before+1 {
				t.Fatalf("unverified Start reached a control command: %#v", runner.calls[before:])
			}
		})
	}
}

func TestLaunchdPrivatePlistRequiresPrivateRegularCurrentUserFile(t *testing.T) {
	service, _, _, plistPath := launchdServiceFixture(t, "not running", false)
	if _, err := readPrivateLaunchAgent(plistPath, service.uid); err != nil {
		t.Fatalf("private plist rejected: %v", err)
	}
	if err := os.Chmod(plistPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateLaunchAgent(plistPath, service.uid); err == nil {
		t.Fatal("group-readable plist accepted")
	}
	if err := os.Chmod(plistPath, 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(plistPath), "alias.plist")
	if err := os.Symlink(plistPath, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrivateLaunchAgent(alias, service.uid); err == nil {
		t.Fatal("symlink plist accepted")
	}
}
