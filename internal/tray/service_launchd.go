package tray

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/torfstack/grove/internal/privatefs"
)

type launchctlRunner interface {
	Print(context.Context, string) ([]byte, error)
	Bootout(context.Context, string) error
	Kickstart(context.Context, string) error
}

type launchdService struct {
	profileDir     string
	label          string
	domain         string
	target         string
	plistPath      string
	loginPlistPath string
	uid            uint32
	runner         launchctlRunner
}

type launchdObservation struct {
	loaded bool
	job    launchdJob
}

func newLaunchdController(opts Options, runner launchctlRunner, domain, plistPath, loginPlistPath string, uid uint32) (*launchdService, error) {
	if !validInstance(opts.ServiceInstance) || !filepath.IsAbs(opts.ProfileDir) || !filepath.IsAbs(plistPath) || !filepath.IsAbs(loginPlistPath) || filepath.Clean(plistPath) == filepath.Clean(loginPlistPath) || runner == nil {
		return nil, errors.New("invalid launchd service configuration")
	}
	wantDomain := "gui/" + strconv.FormatUint(uint64(uid), 10)
	if domain != wantDomain {
		return nil, errors.New("invalid launchd domain")
	}
	profileDir, err := privatefs.Canonical(opts.ProfileDir)
	if err != nil || privatefs.OutsideGit(profileDir) != nil {
		return nil, errors.New("invalid launchd profile")
	}
	label := "com.torfstack.groved." + opts.ServiceInstance
	if filepath.Base(plistPath) != label+".plist" || filepath.Base(loginPlistPath) != label+".plist" {
		return nil, errors.New("invalid launchd plist path")
	}
	return &launchdService{
		profileDir:     profileDir,
		label:          label,
		domain:         domain,
		target:         domain + "/" + label,
		plistPath:      plistPath,
		loginPlistPath: loginPlistPath,
		uid:            uid,
		runner:         runner,
	}, nil
}

func (s *launchdService) Observe(ctx context.Context) (ServiceState, error) {
	state, _, err := s.observe(ctx)
	return state, err
}

func (s *launchdService) observe(ctx context.Context) (ServiceState, launchdObservation, error) {
	state := ServiceState{Unit: s.label, Phase: "unavailable", Reason: "launchd status unavailable"}
	if err := ctx.Err(); err != nil {
		return state, launchdObservation{}, err
	}
	output, printErr := s.runner.Print(ctx, s.target)
	if printErr == nil {
		job, err := parseLaunchctlJob(output, s.label)
		jobPath := filepath.Clean(job.Path)
		if err != nil || job.Target != s.target || (jobPath != filepath.Clean(s.plistPath) && jobPath != filepath.Clean(s.loginPlistPath)) {
			return state, launchdObservation{}, errors.New("selected launchd job is unverified")
		}
		if err := validateLaunchInvocation(job.Program, job.Arguments, s.profileDir); err != nil {
			return state, launchdObservation{}, errors.New("selected launchd job does not match the profile")
		}
		plistData, err := readPrivateLaunchAgent(jobPath, s.uid)
		if err != nil || validateLaunchAgent(plistData, s.label, s.profileDir) != nil {
			return state, launchdObservation{}, errors.New("selected launchd plist is unverified")
		}
		plist, err := parseLaunchAgent(plistData)
		if err != nil || !slices.Equal(plist.ProgramArguments, job.Arguments) {
			return state, launchdObservation{}, errors.New("loaded launchd command does not match its plist")
		}
		active, err := privatefs.ProbeLease(filepath.Join(s.profileDir, "daemon.lock"))
		if err != nil {
			return state, launchdObservation{}, errors.New("profile ownership unavailable")
		}
		state = ServiceState{Unit: s.label, Phase: job.State}
		state.CanStart = !active && launchdStopped(job.State)
		state.CanStop = active && job.State == "running"
		if active && job.State != "running" {
			state.Reason = "profile owner does not match launchd state"
		}
		return state, launchdObservation{loaded: true, job: job}, nil
	}
	if !launchdServiceNotFound(output, s.label, s.uid) {
		return state, launchdObservation{}, errors.New("launchd status unavailable")
	}
	plist, err := readPrivateLaunchAgent(s.plistPath, s.uid)
	if err != nil || validateLaunchAgent(plist, s.label, s.profileDir) != nil {
		return state, launchdObservation{}, errors.New("selected launchd plist is unverified")
	}
	active, err := privatefs.ProbeLease(filepath.Join(s.profileDir, "daemon.lock"))
	if err != nil {
		return state, launchdObservation{}, errors.New("profile ownership unavailable")
	}
	state = ServiceState{Unit: s.label, Phase: "inactive"}
	if active {
		state.Phase = "unavailable"
		state.Reason = "profile already has an active owner"
		return state, launchdObservation{}, nil
	}
	state.Reason = "load agent manually before starting"
	return state, launchdObservation{}, nil
}

func launchdStopped(state string) bool {
	return state == "not running" || state == "exited"
}

func (s *launchdService) Request(ctx context.Context, action Action, current Sample) error {
	state, observation, err := s.observe(ctx)
	if err != nil {
		return err
	}
	switch action {
	case ActionStart:
		if !state.CanStart || !observation.loaded || current.Err != nil || current.Status.Owner != "inactive" || current.Status.ProfileState != "available" || current.Status.Profile == nil {
			return errors.New("service start is unavailable")
		}
		if err := s.runner.Kickstart(ctx, s.target); err != nil {
			return errors.New("launchd start failed")
		}
		return nil
	case ActionStop:
		if !state.CanStop || !observation.loaded {
			return errors.New("service stop is unavailable")
		}
		if err := s.runner.Bootout(ctx, s.target); err != nil {
			return errors.New("launchd stop failed")
		}
		return nil
	default:
		return fmt.Errorf("unsupported service action")
	}
}

func (*launchdService) Close() error { return nil }

var _ Controller = (*launchdService)(nil)
