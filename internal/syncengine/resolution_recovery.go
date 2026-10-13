package syncengine

import (
	"context"
	"errors"
	"reflect"
)

func pendingAllowsConflicts(p *Pending, conflicts []Conflict) bool {
	if p == nil {
		return true
	}
	for _, c := range conflicts {
		for _, path := range conflictPaths(c) {
			if c.RemoteID == p.Entry.Remote.ID || containsPath(path, p.Entry.Path) || containsPath(p.Entry.Path, path) {
				return false
			}
		}
	}
	return true
}

func journalAllowsConflicts(j *Journal, conflicts []Conflict) bool {
	if j == nil {
		return true
	}
	if j.Operation.Kind == OpMove || j.Operation.Kind == OpReplace {
		if j.Operation.Conflict != nil {
			return false
		}
		paths := []string{j.Operation.Entry.Path}
		for _, entry := range j.Operation.Before {
			paths = append(paths, entry.Path)
		}
		for _, entry := range j.After {
			paths = append(paths, entry.Path)
		}
		for _, conflict := range conflicts {
			for _, conflictPath := range conflictPaths(conflict) {
				for _, path := range paths {
					if containsPath(path, conflictPath) || containsPath(conflictPath, path) {
						return false
					}
				}
			}
			for _, entry := range j.Operation.Before {
				if entry.RemoteID == conflict.RemoteID {
					return false
				}
			}
		}
		return true
	}
	if j.Operation.Kind != OpUpload && j.Operation.Kind != OpCreate && j.Operation.Kind != OpTrash {
		return false
	}
	if j.Phase == "committed" {
		return true
	}
	found := j.Operation.Conflict == nil
	for _, c := range conflicts {
		overlaps := c.Path == j.Operation.Entry.Path || c.RemoteID == j.Operation.Entry.Remote.ID
		if j.Operation.Kind == OpTrash && len(j.Operation.Before) > 0 {
			root := j.Operation.Before[0].Path
			overlaps = overlaps || containsPath(root, c.Path) || containsPath(c.Path, root) || containsPath(j.Operation.Entry.Path, c.Path)
		}
		if overlaps {
			if j.Operation.Conflict == nil || !reflect.DeepEqual(*j.Operation.Conflict, c) {
				return false
			}
			found = true
		}
	}
	return found
}

func conflictPaths(c Conflict) []string {
	paths := []string{c.Path}
	if c.Deletion {
		for _, entry := range c.DeletionRemoteSubtree {
			if entry.RemoteID == c.RemoteID {
				paths = append(paths, entry.Path)
				break
			}
		}
	}
	return paths
}

func validateResolutionJournal(j Journal) error {
	c := j.Operation.Conflict
	if c == nil {
		return nil
	}
	validStage := c.LocalSHA256 == j.StageSHA256 && c.LocalSize == j.StageSize
	if c.SelectedSide == SideCloud && c.SelectedCandidate > 0 {
		if candidate, err := selectedCloudCandidate(*c); err == nil {
			validStage = candidate.SHA256 == j.StageSHA256 && candidate.Size == j.StageSize
		}
	}
	if j.Operation.Kind != OpUpload || j.Operation.ResolutionSide != SideLocal || len(j.Operation.Before) != 1 || c.Baseline != j.Operation.Before[0] || c.Path != j.Operation.Entry.Path || c.RemoteID != j.Operation.Entry.Remote.ID || !validStage {
		return errors.New("invalid resolution upload journal")
	}
	return validateConflicts([]Conflict{*c}, j.Operation.Before, true)
}

func withoutConflict(conflicts []Conflict, id string) []Conflict {
	filtered := make([]Conflict, 0, len(conflicts))
	for _, c := range conflicts {
		if c.ID != id {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func (e *executor) cleanupResolvedConflict(ctx context.Context, c Conflict) error {
	if c.LocalStagePath != "" && !conflictUsesStage(e.state.Conflicts, c.LocalStagePath) {
		if err := removeVerifiedStageIfPresent(ctx, e.profileDir, c.LocalStagePath, c.LocalStageIdentity, c.LocalSHA256, "", c.LocalSize); err != nil {
			return err
		}
	}
	for _, candidate := range conflictCandidates(c) {
		if !conflictUsesStage(e.state.Conflicts, candidate.StagePath) {
			if err := removeVerifiedStageIfPresent(ctx, e.profileDir, candidate.StagePath, candidate.StageIdentity, candidate.SHA256, candidate.MD5, candidate.Size); err != nil {
				return err
			}
		}
	}
	return nil
}

func recoveryConflictBelongs(c Conflict, j Journal) bool {
	if j.Operation.Conflict != nil && c.ID == j.Operation.Conflict.ID {
		return true
	}
	return j.StagePath != "" && c.LocalStagePath == j.StagePath && c.Path == j.Operation.Entry.Path && c.RemoteID == j.Operation.Entry.Remote.ID
}
