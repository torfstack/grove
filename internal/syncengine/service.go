package syncengine

import (
	"context"
	"errors"
	"github.com/torfstack/grove/internal/auth"
	"github.com/torfstack/grove/internal/drive"
	"github.com/torfstack/grove/internal/privatefs"
	"os"
	"path/filepath"
)

type Service struct {
	RegistryDir string
	OpenAPI     func(context.Context, string) (drive.API, func() error, error)
}

func Run(ctx context.Context, opts Options) (Result, error) { return (Service{}).Run(ctx, opts) }
func (s Service) Run(ctx context.Context, opts Options) (Result, error) {
	registry := s.RegistryDir
	if registry == "" {
		var err error
		registry, err = registryPath()
		if err != nil {
			return Result{}, err
		}
	}
	open := s.OpenAPI
	if open == nil {
		open = func(ctx context.Context, token string) (drive.API, func() error, error) {
			session, err := auth.OpenSession(ctx, token, "read-only")
			if err != nil {
				return nil, nil, err
			}
			return drive.NewClient(session.HTTPClient, drive.ClientOptions{}), session.Close, nil
		}
	}
	return run(ctx, opts, registry, func(ctx context.Context) (drive.API, func() error, error) { return open(ctx, opts.TokenFile) })
}

func run(ctx context.Context, opts Options, registry string, open func(context.Context) (drive.API, func() error, error)) (result Result, err error) {
	opts, err = canonicalOptions(opts)
	if err != nil {
		return result, err
	}
	profileLock, err := privatefs.Acquire(filepath.Join(opts.ProfileDir, "profile.lock"))
	if err != nil {
		return result, err
	}
	defer closeResult(profileLock.Close, &err)
	lease, err := registerDestination(registry, opts.ProfileDir, opts.LocalDir)
	if err != nil {
		return result, err
	}
	defer closeResult(lease.Close, &err)
	binding := Binding{RemoteRoot: opts.RemoteRoot, LocalDir: opts.LocalDir, TokenFile: opts.TokenFile}
	state, err := loadState(opts.ProfileDir, binding)
	if err != nil {
		return result, err
	}
	_, statErr := os.Lstat(filepath.Join(opts.ProfileDir, "state.json"))
	isNew := errors.Is(statErr, os.ErrNotExist)
	if isNew {
		if err = checkInitialDestination(opts.LocalDir); err != nil {
			return result, err
		}
	}
	api, closeAPI, err := open(ctx)
	if err != nil {
		return result, err
	}
	defer closeResult(closeAPI, &err)
	snapshot, err := Scan(ctx, api, opts.RemoteRoot)
	if err != nil {
		return result, err
	}
	if err = os.Mkdir(opts.LocalDir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return result, errors.New("cannot create destination")
	}
	if err = lease.attachDirectory(opts.LocalDir); err != nil {
		return result, err
	}
	root, err := os.OpenRoot(opts.LocalDir)
	if err != nil {
		return result, errors.New("cannot open rooted destination")
	}
	defer closeResult(root.Close, &err)
	save := func() error { return saveState(opts.ProfileDir, state) }
	if isNew {
		if err = save(); err != nil {
			return result, err
		}
	}
	if err = clearProbe(root, &state, save); err != nil {
		return result, err
	}
	ex := executor{root: root, api: api, state: &state, save: save}
	if err = ex.recover(ctx, snapshot); err != nil {
		return result, err
	}
	local, err := scanLocal(ctx, root, nil)
	if err != nil {
		return result, err
	}
	plan, err := BuildPlan(snapshot, local, state)
	if err != nil {
		return result, err
	}
	work := false
	for _, op := range plan.Operations {
		if op.Kind != "skip" {
			work = true
			break
		}
	}
	if state.Version == 1 {
		state, err = migrateState(opts.ProfileDir, state)
		if err != nil {
			return result, err
		}
	}
	if work {
		needsMove := false
		for _, op := range plan.Operations {
			if op.Kind == OpMove || op.Kind == OpReplace {
				needsMove = true
			}
		}
		if needsMove {
			if err = probeMove(root, &state, save); err != nil {
				return result, err
			}
		}
		if err = probeDestination(root, probeSnapshot(snapshot, state.Completed), &state, save); err != nil {
			return result, err
		}
	}
	for _, op := range plan.Operations {
		if err = ex.execute(ctx, op); err != nil {
			return result, err
		}
		switch op.Kind {
		case "download":
			result.Downloaded++
		case OpReplace:
			result.Downloaded++
			result.Updated++
		case OpMove:
			result.Moved++
		case "mkdir":
			result.CreatedDirectories++
		case "skip":
			if entryKind(op.Entry) == "file" {
				result.Skipped++
			}
		}
	}
	if !state.PopulationComplete {
		state.PopulationComplete = true
		if err = save(); err != nil {
			return result, err
		}
	}
	return result, nil
}
func closeResult(close func() error, err *error) {
	if closeErr := close(); closeErr != nil && *err == nil {
		*err = errors.New("cannot close sync resource")
	}
}
func registryPath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base != "" && !filepath.IsAbs(base) {
		return "", errors.New("XDG_STATE_HOME must be absolute")
	}
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("cannot determine state directory")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "grove", "destinations"), nil
}
