package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/torfstack/grove/internal/drive"
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

func (s Service) ConflictDetails(ctx context.Context, opts Options, id string) (ConflictDetail, error) {
	conflicts, err := s.ListConflicts(ctx, opts)
	if err != nil {
		return ConflictDetail{}, err
	}
	for _, conflict := range conflicts {
		if conflict.ID != id {
			continue
		}
		detail := ConflictDetail{ID: conflict.ID, Path: conflict.Path, Candidates: []ConflictCandidateView{{Ordinal: 0, Side: SideLocal, Size: conflict.LocalSize, Selected: conflict.SelectedSide == SideLocal}}}
		for i, candidate := range conflictCandidates(conflict) {
			detail.Candidates = append(detail.Candidates, ConflictCandidateView{Ordinal: i + 1, Side: SideCloud, ModifiedTime: candidate.ModifiedTime, Size: candidate.Size, Selected: conflict.SelectedSide == SideCloud && conflict.SelectedCandidate == i+1})
		}
		return detail, nil
	}
	return ConflictDetail{}, errors.New("pending conflict not found")
}

func (s Service) ResolveConflict(ctx context.Context, opts Options, id string, side ConflictSide, candidateOrdinal int) error {
	if id == "" || (side != SideLocal && side != SideCloud) || candidateOrdinal < 0 || (side == SideLocal && candidateOrdinal != 0) {
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
	candidates := conflictCandidates(*conflict)
	if side == SideCloud && (candidateOrdinal == 0 && len(candidates) > 1 || candidateOrdinal > len(candidates)) {
		_ = lock.Close()
		return errors.New("a valid cloud candidate ordinal is required")
	}
	if side == SideCloud && candidateOrdinal == 0 {
		candidateOrdinal = 1
	}
	if err = verifyPrivateStage(canonical.ProfileDir, conflict.LocalStagePath, conflict.LocalStageIdentity, conflict.LocalSHA256, "", conflict.LocalSize); err != nil {
		_ = lock.Close()
		return err
	}
	for _, candidate := range candidates {
		if err = verifyPrivateStage(canonical.ProfileDir, candidate.StagePath, candidate.StageIdentity, candidate.SHA256, candidate.MD5, candidate.Size); err != nil {
			_ = lock.Close()
			return err
		}
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
	if scanErr != nil || !matchesRemoteConflict(snapshot, *conflict) {
		_ = lock.Close()
		return errors.New("remote conflict candidate changed")
	}
	if side == SideCloud && len(candidates) > 1 {
		mutationAPI, ok := api.(drive.SyncMutationAPI)
		if !ok {
			_ = lock.Close()
			return errors.New("cloud candidate history cannot be verified")
		}
		revisions, revisionErr := mutationAPI.ListRevisions(ctx, conflict.RemoteID)
		if revisionErr != nil {
			_ = lock.Close()
			return errors.New("cloud candidate history cannot be verified")
		}
		selected := candidates[candidateOrdinal-1]
		found := false
		for _, revision := range revisions {
			if revision.ID == selected.RevisionID && revision.MD5 == selected.MD5 && revision.Size == selected.Size && revision.ModifiedTime == selected.ModifiedTime {
				found = true
				break
			}
		}
		if !found {
			_ = lock.Close()
			return errors.New("selected cloud candidate changed")
		}
	}
	closeAPIErr := closeAPI()
	if closeAPIErr != nil {
		_ = lock.Close()
		return errors.New("cannot close remote candidate check")
	}
	conflict.SelectedSide = side
	conflict.SelectedCandidate = candidateOrdinal
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
			return remote.ID == conflict.RemoteID && remote.MIMEType != drive.FolderMIME && !remote.Trashed && remote.OwnedByMe && remote.MD5 == conflict.RemoteMD5 && remote.Size == conflict.RemoteSize && (conflict.RemoteRevisionID == "" || remote.HeadRevisionID == conflict.RemoteRevisionID)
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
