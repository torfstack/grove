package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/torfstack/grove/internal/privatefs"
)

func (s Service) SetConflictPolicy(ctx context.Context, opts Options, policy ConflictPolicy) error {
	if err := validateConflictPolicy(policy); err != nil {
		return err
	}
	canonical, state, lock, err := s.lockedState(opts)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err = ctx.Err(); err != nil {
		return err
	}
	state.ConflictPolicy = policy
	return saveState(canonical.ProfileDir, state)
}

func (s Service) ListConflicts(_ context.Context, opts Options) ([]Conflict, error) {
	_, state, lock, err := s.lockedState(opts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	return append([]Conflict(nil), state.Conflicts...), nil
}

func (s Service) ResolveConflict(ctx context.Context, opts Options, id string, side ConflictSide) error {
	if id == "" || (side != SideLocal && side != SideCloud) {
		return errors.New("invalid conflict resolution")
	}
	canonical, state, lock, err := s.lockedState(opts)
	if err != nil {
		return err
	}
	conflict := findConflict(state.Conflicts, id)
	if conflict == nil {
		_ = lock.Close()
		return errors.New("pending conflict not found")
	}
	if conflict.Baseline.RemoteID == "" && side == SideLocal {
		_ = lock.Close()
		return errors.New("local resolution is not supported for an untracked same-path addition")
	}
	if err = verifyPrivateStage(canonical.ProfileDir, conflict.LocalStagePath, conflict.LocalStageIdentity, conflict.LocalSHA256, "", conflict.LocalSize); err != nil {
		_ = lock.Close()
		return err
	}
	cloudSize := conflict.CloudSize
	if conflict.CloudRevisionID == "" {
		cloudSize = conflict.RemoteSize
	}
	if err = verifyPrivateStage(canonical.ProfileDir, conflict.CloudStagePath, conflict.CloudStageIdentity, conflict.CloudSHA256, conflict.CloudMD5, cloudSize); err != nil {
		_ = lock.Close()
		return err
	}
	root, err := os.OpenRoot(canonical.LocalDir)
	if err != nil {
		_ = lock.Close()
		return errors.New("cannot open sync destination")
	}
	local, scanErr := scanLocal(ctx, root, nil)
	closeRootErr := root.Close()
	if scanErr != nil || closeRootErr != nil || !matchesLocalConflict(local, *conflict) {
		_ = lock.Close()
		return errors.New("local conflict candidate changed")
	}
	open := s.OpenAPI
	if open == nil {
		open = defaultOpenAPI
	}
	api, closeAPI, err := open(ctx, canonical.TokenFile, "read-only")
	if err != nil {
		_ = lock.Close()
		return err
	}
	snapshot, scanErr := Scan(ctx, api, canonical.RemoteRoot)
	closeAPIErr := closeAPI()
	if scanErr != nil || closeAPIErr != nil || !matchesRemoteConflict(snapshot, *conflict) {
		_ = lock.Close()
		return errors.New("remote conflict candidate changed")
	}
	conflict.SelectedSide = side
	state.Conflicts = replaceConflict(state.Conflicts, *conflict)
	if err = saveState(canonical.ProfileDir, state); err != nil {
		_ = lock.Close()
		return err
	}
	if err = lock.Close(); err != nil {
		return errors.New("cannot release sync profile lock")
	}
	canonical.TwoWay = true
	_, err = s.Run(ctx, canonical)
	return err
}

func (s Service) lockedState(opts Options) (Options, State, *privatefs.Lock, error) {
	canonical, err := canonicalOptions(opts)
	if err != nil {
		return Options{}, State{}, nil, err
	}
	lock, err := privatefs.Acquire(filepath.Join(canonical.ProfileDir, "profile.lock"))
	if err != nil {
		return Options{}, State{}, nil, err
	}
	state, err := loadState(canonical.ProfileDir, Binding{RemoteRoot: canonical.RemoteRoot, LocalDir: canonical.LocalDir, TokenFile: canonical.TokenFile})
	if err != nil {
		_ = lock.Close()
		return Options{}, State{}, nil, err
	}
	if !state.PopulationComplete {
		_ = lock.Close()
		return Options{}, State{}, nil, errors.New("profile is not fully populated")
	}
	return canonical, state, lock, nil
}

func matchesLocalConflict(entries []LocalEntry, conflict Conflict) bool {
	for _, entry := range entries {
		if entry.Path == conflict.Path {
			return entry.Kind == "file" && entry.SHA256 == conflict.LocalSHA256 && entry.Size == conflict.LocalSize
		}
	}
	return false
}

func matchesRemoteConflict(snapshot Snapshot, conflict Conflict) bool {
	for _, entry := range snapshot.Entries {
		if entry.Path == conflict.Path {
			remote := entry.Remote
			return remote.ID == conflict.RemoteID && remote.MD5 == conflict.RemoteMD5 && remote.Size == conflict.RemoteSize && (conflict.RemoteRevisionID == "" || remote.HeadRevisionID == conflict.RemoteRevisionID)
		}
	}
	return false
}

func replaceConflict(conflicts []Conflict, selected Conflict) []Conflict {
	result := append([]Conflict(nil), conflicts...)
	for i := range result {
		if result[i].ID == selected.ID {
			result[i] = selected
			break
		}
	}
	return result
}
