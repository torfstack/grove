package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
)

type ProfileSummary struct {
	Binding            Binding
	PopulationComplete bool
	ConflictCount      int
	RecoveryPending    bool
}

func existingProfile(ctx context.Context, dir string) (Options, State, *privatefs.Lock, error) {
	if err := ctx.Err(); err != nil {
		return Options{}, State{}, nil, err
	}
	if dir == "" {
		return Options{}, State{}, nil, errors.New("profile directory is required")
	}
	profile, err := privatefs.Canonical(dir)
	if err != nil {
		return Options{}, State{}, nil, err
	}
	if err := privatefs.OutsideGit(profile); err != nil {
		return Options{}, State{}, nil, err
	}
	info, err := os.Lstat(profile)
	if err != nil || !info.IsDir() {
		return Options{}, State{}, nil, errors.New("existing profile directory is required")
	}
	info, err = os.Lstat(filepath.Join(profile, "state.json"))
	if err != nil || !info.Mode().IsRegular() {
		return Options{}, State{}, nil, errors.New("existing profile state is required")
	}
	lock, err := privatefs.AcquireLease(filepath.Join(profile, "profile.lock"))
	if err != nil {
		return Options{}, State{}, nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = lock.Close()
		}
	}()
	var state State
	if err := privatefs.ReadJSON(filepath.Join(profile, "state.json"), &state); err != nil {
		return Options{}, State{}, nil, err
	}
	opts, err := canonicalOptions(Options{ProfileDir: profile, RemoteRoot: state.Binding.RemoteRoot, LocalDir: state.Binding.LocalDir, TokenFile: state.Binding.TokenFile})
	if err != nil {
		return Options{}, State{}, nil, err
	}
	binding := Binding{RemoteRoot: opts.RemoteRoot, LocalDir: opts.LocalDir, TokenFile: opts.TokenFile}
	state, err = validateState(state, binding)
	if err != nil {
		return Options{}, State{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return Options{}, State{}, nil, err
	}
	success = true
	return opts, state, lock, nil
}

func (s Service) InspectProfile(ctx context.Context, profileDir string) (summary ProfileSummary, err error) {
	_, state, lock, err := existingProfile(ctx, profileDir)
	if err != nil {
		return summary, err
	}
	defer closeResult(lock.Close, &err)
	return ProfileSummary{Binding: state.Binding, PopulationComplete: state.PopulationComplete, ConflictCount: len(state.Conflicts), RecoveryPending: state.Pending != nil || state.Transaction != nil || state.ProbePath != ""}, nil
}

func (s Service) RunExisting(ctx context.Context, profileDir string, expected Binding, twoWay bool) (result Result, err error) {
	opts, state, lock, err := existingProfile(ctx, profileDir)
	if err != nil {
		return result, err
	}
	defer closeResult(lock.Close, &err)
	if state.Binding != expected {
		return result, errors.New("profile binding changed")
	}
	if twoWay && !state.PopulationComplete {
		return result, errors.New("two-way sync requires a populated profile")
	}
	opts.TwoWay = twoWay
	registry := s.RegistryDir
	if registry == "" {
		registry, err = registryPath()
		if err != nil {
			return result, err
		}
	}
	open := s.OpenAPI
	if open == nil {
		open = defaultOpenAPI
	}
	access := "read-only"
	if twoWay {
		access = "read-write"
	}
	return runLocked(ctx, opts, registry, state, false, func(ctx context.Context) (drive.API, func() error, error) { return open(ctx, opts.TokenFile, access) })
}
