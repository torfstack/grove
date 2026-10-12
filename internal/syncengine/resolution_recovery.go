package syncengine

import (
	"context"
	"errors"
	"reflect"
)

func uploadAllowsConflicts(j *Journal, conflicts []Conflict) bool {
	if j == nil {
		return true
	}
	if j.Operation.Kind != OpUpload && j.Operation.Kind != OpCreate {
		return false
	}
	if j.Phase == "committed" {
		return true
	}
	found := j.Operation.Conflict == nil
	for _, c := range conflicts {
		if c.Path == j.Operation.Entry.Path || c.RemoteID == j.Operation.Entry.Remote.ID {
			if j.Operation.Conflict == nil || !reflect.DeepEqual(*j.Operation.Conflict, c) {
				return false
			}
			found = true
		}
	}
	return found
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
	if !conflictUsesStage(e.state.Conflicts, c.LocalStagePath) {
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
