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
	OpenAPI     func(context.Context, string, string) (drive.API, func() error, error)
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
		open = defaultOpenAPI
	}
	access := "read-only"
	if opts.TwoWay {
		access = "read-write"
	}
	return run(ctx, opts, registry, func(ctx context.Context) (drive.API, func() error, error) { return open(ctx, opts.TokenFile, access) })
}

func defaultOpenAPI(ctx context.Context, token, access string) (drive.API, func() error, error) {
	session, err := auth.OpenSession(ctx, token, access)
	if err != nil {
		return nil, nil, err
	}
	return drive.NewClient(session.HTTPClient, drive.ClientOptions{}), session.Close, nil
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
	binding := Binding{RemoteRoot: opts.RemoteRoot, LocalDir: opts.LocalDir, TokenFile: opts.TokenFile}
	state, err := loadState(opts.ProfileDir, binding)
	if err != nil {
		return result, err
	}
	_, statErr := os.Lstat(filepath.Join(opts.ProfileDir, "state.json"))
	return runLocked(ctx, opts, registry, state, errors.Is(statErr, os.ErrNotExist), open)
}

func runLocked(ctx context.Context, opts Options, registry string, state State, isNew bool, open func(context.Context) (drive.API, func() error, error)) (result Result, err error) {
	if err = ctx.Err(); err != nil {
		return result, err
	}
	lease, err := registerDestination(registry, opts.ProfileDir, opts.LocalDir)
	if err != nil {
		return result, err
	}
	defer closeResult(lease.Close, &err)
	if isNew {
		if opts.TwoWay {
			return result, errors.New("two-way sync requires a populated profile")
		}
		if err = checkInitialDestination(opts.LocalDir); err != nil {
			return result, err
		}
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	api, closeAPI, err := open(ctx)
	if err != nil {
		return result, err
	}
	defer closeResult(closeAPI, &err)
	if opts.TwoWay {
		if _, ok := api.(drive.SyncMutationAPI); !ok {
			return result, errors.New("two-way sync requires a mutation-capable Drive API")
		}
	}
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
	ex := executor{root: root, profileDir: opts.ProfileDir, remoteRoot: opts.RemoteRoot, api: api, state: &state, save: save}
	ex.resumeUploads = opts.TwoWay
	if mutation, ok := api.(drive.SyncMutationAPI); ok {
		ex.mutation = mutation
	}
	var recoveringTrash *Journal
	if state.Transaction != nil && state.Transaction.Operation.Kind == OpTrash {
		copy := *state.Transaction
		recoveringTrash = &copy
	}
	if err = ex.recover(ctx, snapshot); err != nil {
		return result, err
	}
	if recoveringTrash != nil && state.Transaction == nil {
		stillTracked := false
		for _, completed := range state.Completed {
			stillTracked = stillTracked || completed.RemoteID == recoveringTrash.Operation.Entry.Remote.ID
		}
		if !stillTracked {
			result.Trashed += len(recoveringTrash.Operation.RemoteSubtree)
		}
	}
	local, err := scanLocal(ctx, root, nil)
	if err != nil {
		return result, err
	}
	if opts.TwoWay {
		if !state.PopulationComplete && opts.deletionResolutionID == "" {
			return result, errors.New("two-way sync requires a populated profile")
		}
		if state.Version == 1 {
			state, err = migrateState(opts.ProfileDir, state)
			if err != nil {
				return result, err
			}
		}
		state, err = migrateUploadState(opts.ProfileDir, state)
		if err != nil {
			return result, err
		}
	}
	var plan Plan
	if opts.deletionResolutionID != "" {
		plan, err = BuildDeletionResolutionPlan(snapshot, local, state, opts.deletionResolutionID)
	} else if opts.TwoWay {
		plan, err = BuildTwoWayPlan(snapshot, local, state)
	} else if !state.PopulationComplete && len(state.Conflicts) > 0 {
		plan, err = BuildRestorationPlan(snapshot, local, state)
	} else {
		plan, err = BuildPlan(snapshot, local, state)
	}
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
	if !opts.TwoWay && state.Version == 1 {
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
	for index := 0; index < len(plan.Operations); index++ {
		op := plan.Operations[index]
		if err = ex.execute(ctx, op); err != nil {
			return result, err
		}
		cloudRestore := op.Kind == OpConflict && op.Conflict != nil && op.Conflict.Deletion && op.ResolutionSide == SideCloud
		partialLocalResolution := op.Kind == OpTrash && opts.deletionResolutionID != "" && !state.PopulationComplete
		if cloudRestore || partialLocalResolution {
			if partialLocalResolution {
				result.Trashed += len(op.RemoteSubtree)
			}
			snapshot, err = Scan(ctx, api, opts.RemoteRoot)
			if err != nil {
				return result, err
			}
			local, err = scanLocal(ctx, root, nil)
			if err != nil {
				return result, err
			}
			plan, err = BuildRestorationPlan(snapshot, local, state)
			if err != nil {
				return result, err
			}
			for _, restoreOp := range plan.Operations {
				if restoreOp.Kind == OpMove || restoreOp.Kind == OpReplace {
					if err = probeMove(root, &state, save); err != nil {
						return result, err
					}
					break
				}
			}
			if err = probeDestination(root, probeSnapshot(snapshot, state.Completed), &state, save); err != nil {
				return result, err
			}
			index = -1
			continue
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
		case OpUpload:
			result.Updated++
		case OpCreate:
			if op.Local.Kind == "folder" {
				result.CreatedDirectories++
			} else {
				result.Uploaded++
			}
		case OpTrash:
			result.Trashed += len(op.RemoteSubtree)
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
	if closeErr := close(); closeErr != nil {
		*err = errors.Join(*err, errors.New("cannot close sync resource"))
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
