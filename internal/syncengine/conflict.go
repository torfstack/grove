package syncengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func conflictID(c Conflict) string {
	identity := struct {
		Path           string   `json:"path"`
		RemoteID       string   `json:"remote_id"`
		BaselineSHA    string   `json:"baseline_sha256"`
		LocalSHA       string   `json:"local_sha256"`
		RemoteMD5      string   `json:"remote_md5"`
		RemoteSize     int64    `json:"remote_size"`
		CloudRevision  string   `json:"cloud_revision_id,omitempty"`
		CloudRevisions []string `json:"cloud_revision_ids,omitempty"`
	}{
		Path: c.Path, RemoteID: c.RemoteID, BaselineSHA: c.Baseline.SHA256,
		LocalSHA: c.LocalSHA256, RemoteMD5: c.RemoteMD5, RemoteSize: c.RemoteSize,
		CloudRevision: c.CloudRevisionID,
	}
	if c.CandidateSetVersion == 1 && len(c.CloudCandidates) > 1 {
		identity.CloudRevisions = make([]string, len(c.CloudCandidates))
		for i, candidate := range c.CloudCandidates {
			identity.CloudRevisions[i] = candidate.RevisionID
		}
	}
	data, _ := json.Marshal(identity)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func normalizeConflict(c Conflict) Conflict {
	if c.CandidateSetVersion == 1 && len(c.CloudCandidates) > 1 {
		sort.Slice(c.CloudCandidates, func(i, j int) bool {
			return cloudCandidateLess(c.CloudCandidates[i], c.CloudCandidates[j])
		})
		return c
	}
	if c.CandidateSetVersion != 0 || len(c.CloudCandidates) != 0 || c.CloudRevisionID == "" || c.CloudStagePath == "" || c.CloudStageIdentity == nil {
		return c
	}
	c.CandidateSetVersion = 1
	c.CloudCandidates = []CloudCandidate{{
		RevisionID: c.CloudRevisionID, MD5: c.CloudMD5, Size: c.CloudSize,
		SHA256: c.CloudSHA256, StagePath: c.CloudStagePath, StageIdentity: c.CloudStageIdentity,
	}}
	return c
}

func normalizeConflicts(conflicts []Conflict) []Conflict {
	for i := range conflicts {
		conflicts[i] = normalizeConflict(conflicts[i])
	}
	return conflicts
}

func cloudCandidateLess(left, right CloudCandidate) bool {
	if left.ModifiedTime == "" || right.ModifiedTime == "" {
		if left.ModifiedTime != right.ModifiedTime {
			return left.ModifiedTime != ""
		}
		return left.RevisionID < right.RevisionID
	}
	leftTime, _ := time.Parse(time.RFC3339Nano, left.ModifiedTime)
	rightTime, _ := time.Parse(time.RFC3339Nano, right.ModifiedTime)
	if leftTime.Equal(rightTime) {
		return left.RevisionID < right.RevisionID
	}
	return leftTime.Before(rightTime)
}

func validateConflictPolicy(policy ConflictPolicy) error {
	switch policy {
	case ConflictAsk, ConflictKeepLocal, ConflictKeepCloud:
		return nil
	default:
		return errors.New("invalid conflict policy")
	}
}

func validateConflicts(conflicts []Conflict, completed []Completed, requireStages bool) error {
	byID := make(map[string]Completed, len(completed))
	for _, entry := range completed {
		byID[entry.RemoteID] = entry
	}
	seenIDs := make(map[string]bool, len(conflicts))
	seenPaths := make(map[string]bool, len(conflicts))
	for _, c := range conflicts {
		baseline, tracked := byID[c.RemoteID]
		if !safeRelative(c.Path) || c.ID == "" || c.ID != conflictID(c) || seenIDs[c.ID] || seenPaths[c.Path] {
			return errors.New("invalid pending conflict identity")
		}
		if c.Baseline.RemoteID != "" {
			if !tracked || c.Baseline != baseline || c.Path != baseline.Path || baseline.Kind != "file" {
				return errors.New("pending conflict does not match baseline")
			}
		} else if tracked {
			return errors.New("new-file conflict unexpectedly has a baseline")
		}
		if !validHash(c.LocalSHA256, 32) || c.LocalSize < 0 || c.RemoteID == "" || c.RemoteVersion == "" || !validHash(c.RemoteMD5, 16) || c.RemoteSize < 0 {
			return errors.New("invalid pending conflict fingerprint")
		}
		if requireStages && (!safeRelative(c.LocalStagePath) || !strings.HasPrefix(filepath.Base(c.LocalStagePath), ".grove-upload-") || c.LocalStageIdentity == nil || c.LocalStageIdentity.Inode == 0) {
			return errors.New("invalid pending conflict candidate stages")
		}
		if c.CandidateSetVersion == 0 {
			if len(c.CloudCandidates) != 0 {
				return errors.New("unversioned conflict has cloud candidate set")
			}
			if requireStages && (!safeRelative(c.CloudStagePath) || !strings.HasPrefix(filepath.Base(c.CloudStagePath), ".grove-cloud-conflict-") || c.CloudStageIdentity == nil || c.CloudStageIdentity.Inode == 0 || !validHash(c.CloudSHA256, 32)) {
				return errors.New("invalid pending conflict candidate stages")
			}
			if c.CloudRevisionID != "" && (!validHash(c.CloudMD5, 16) || c.CloudSize < 0) {
				return errors.New("invalid pending cloud revision fingerprint")
			}
		} else if c.CandidateSetVersion != 1 || len(c.CloudCandidates) == 0 {
			return errors.New("invalid cloud candidate set version")
		} else {
			seenRevisions := make(map[string]bool, len(c.CloudCandidates))
			seenStages := make(map[string]bool, len(c.CloudCandidates))
			for i, candidate := range c.CloudCandidates {
				if candidate.RevisionID == "" || seenRevisions[candidate.RevisionID] || !validHash(candidate.MD5, 16) || !validHash(candidate.SHA256, 32) || candidate.Size < 0 || !safeRelative(candidate.StagePath) || !strings.HasPrefix(filepath.Base(candidate.StagePath), ".grove-cloud-conflict-") || seenStages[candidate.StagePath] || candidate.StageIdentity == nil || candidate.StageIdentity.Inode == 0 {
					return errors.New("invalid cloud conflict candidate")
				}
				if candidate.ModifiedTime != "" {
					if _, err := time.Parse(time.RFC3339Nano, candidate.ModifiedTime); err != nil {
						return errors.New("invalid cloud candidate timestamp")
					}
				}
				if i > 0 && !cloudCandidateLess(c.CloudCandidates[i-1], candidate) {
					return errors.New("cloud conflict candidates are not in stable order")
				}
				seenRevisions[candidate.RevisionID] = true
				seenStages[candidate.StagePath] = true
			}
			first := c.CloudCandidates[0]
			if (c.CloudRevisionID != "" && c.CloudRevisionID != first.RevisionID) || (c.CloudMD5 != "" && c.CloudMD5 != first.MD5) || (c.CloudSize != 0 && c.CloudSize != first.Size) || (c.CloudSHA256 != "" && c.CloudSHA256 != first.SHA256) || (c.CloudStagePath != "" && c.CloudStagePath != first.StagePath) || (c.CloudStageIdentity != nil && *c.CloudStageIdentity != *first.StageIdentity) {
				return errors.New("legacy cloud candidate fields disagree with candidate set")
			}
			if requireStages {
				for _, candidate := range c.CloudCandidates {
					if !validHash(candidate.SHA256, 32) {
						return errors.New("invalid cloud candidate stage fingerprint")
					}
				}
			}
		}
		if c.SelectedSide != "" && c.SelectedSide != SideLocal && c.SelectedSide != SideCloud {
			return errors.New("invalid pending conflict selection")
		}
		seenIDs[c.ID] = true
		seenPaths[c.Path] = true
	}
	return nil
}
