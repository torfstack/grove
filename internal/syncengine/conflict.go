package syncengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

func conflictID(c Conflict) string {
	identity := struct {
		Path           string      `json:"path"`
		RemoteID       string      `json:"remote_id"`
		BaselineSHA    string      `json:"baseline_sha256"`
		LocalSHA       string      `json:"local_sha256"`
		RemoteMD5      string      `json:"remote_md5"`
		RemoteSize     int64       `json:"remote_size"`
		CloudRevision  string      `json:"cloud_revision_id,omitempty"`
		CloudRevisions []string    `json:"cloud_revision_ids,omitempty"`
		Deletion       bool        `json:"deletion,omitempty"`
		Deleted        []Completed `json:"deleted,omitempty"`
		RemoteTree     []Completed `json:"remote_tree,omitempty"`
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
	if c.Deletion {
		identity.Deletion = true
		identity.Deleted = append([]Completed(nil), c.DeletionSubtree...)
		sort.Slice(identity.Deleted, func(i, j int) bool { return identity.Deleted[i].Path < identity.Deleted[j].Path })
		identity.RemoteTree = append([]Completed(nil), c.DeletionRemoteSubtree...)
		sort.Slice(identity.RemoteTree, func(i, j int) bool { return identity.RemoteTree[i].Path < identity.RemoteTree[j].Path })
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
	if c.CandidateSetVersion != 0 || len(c.CloudCandidates) != 0 || (c.CloudRevisionID == "" && c.RemoteRevisionID == "") || c.CloudStagePath == "" || c.CloudStageIdentity == nil {
		return c
	}
	revisionID, md5, size := c.CloudRevisionID, c.CloudMD5, c.CloudSize
	if revisionID == "" {
		revisionID = c.RemoteRevisionID
	}
	if md5 == "" {
		md5 = c.RemoteMD5
	}
	if size == 0 {
		size = c.RemoteSize
	}
	c.CandidateSetVersion = 1
	c.CloudCandidates = []CloudCandidate{{
		RevisionID: revisionID, MD5: md5, Size: size,
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

func conflictCandidates(c Conflict) []CloudCandidate {
	c = normalizeConflict(c)
	return append([]CloudCandidate(nil), c.CloudCandidates...)
}

func selectedCloudCandidate(c Conflict) (CloudCandidate, error) {
	candidates := conflictCandidates(c)
	ordinal := c.SelectedCandidate
	if ordinal == 0 && len(candidates) == 1 {
		ordinal = 1
	}
	if c.SelectedSide != SideCloud || ordinal < 1 || ordinal > len(candidates) {
		return CloudCandidate{}, errors.New("selected cloud candidate is missing")
	}
	return candidates[ordinal-1], nil
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
		if c.Deletion {
			if err := validateDeletionConflict(c, byID, requireStages); err != nil {
				return err
			}
			if c.SelectedSide != "" && c.SelectedSide != SideLocal && c.SelectedSide != SideCloud {
				return errors.New("invalid pending conflict selection")
			}
			if c.SelectedCandidate != 0 {
				return errors.New("deletion conflict cannot select a cloud revision ordinal")
			}
			seenIDs[c.ID] = true
			for _, entry := range c.DeletionSubtree {
				if seenPaths[entry.Path] {
					return errors.New("overlapping pending conflict paths")
				}
				seenPaths[entry.Path] = true
			}
			continue
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
		if c.SelectedCandidate < 0 || (c.SelectedSide == SideLocal && c.SelectedCandidate != 0) || (c.SelectedSide == SideCloud && len(conflictCandidates(c)) > 0 && c.SelectedCandidate > len(conflictCandidates(c))) {
			return fmt.Errorf("invalid selected conflict candidate ordinal %d", c.SelectedCandidate)
		}
		seenIDs[c.ID] = true
		seenPaths[c.Path] = true
	}
	return nil
}

func validateDeletionConflict(c Conflict, completed map[string]Completed, requireStages bool) error {
	if !requireStages || c.Baseline.RemoteID == "" || c.RemoteID != c.Baseline.RemoteID || c.Path != c.Baseline.Path || !reflect.DeepEqual(completed[c.RemoteID], c.Baseline) || len(c.DeletionSubtree) == 0 {
		return errors.New("invalid deletion conflict baseline")
	}
	if c.LocalSHA256 != "" || c.LocalSize != 0 || c.LocalStagePath != "" || c.LocalStageIdentity != nil || c.CloudStagePath != "" || c.CloudStageIdentity != nil || c.CloudSHA256 != "" || c.CloudRevisionID != "" || c.CloudMD5 != "" || c.CloudSize != 0 || c.CandidateSetVersion != 0 || len(c.CloudCandidates) != 0 || c.RemoteVersion == "" {
		return errors.New("deletion conflict contains content candidates")
	}
	seenIDs, seenPaths := map[string]bool{}, map[string]bool{}
	rootSeen := false
	for i, entry := range c.DeletionSubtree {
		if !safeRelative(entry.Path) || !containsPath(c.Path, entry.Path) || entry.RemoteID == "" || seenIDs[entry.RemoteID] || seenPaths[entry.Path] || !reflect.DeepEqual(completed[entry.RemoteID], entry) {
			return errors.New("invalid deletion conflict subtree")
		}
		if i > 0 && c.DeletionSubtree[i-1].Path >= entry.Path {
			return errors.New("deletion conflict subtree is not ordered")
		}
		if entry.Path == c.Path && reflect.DeepEqual(entry, c.Baseline) {
			rootSeen = true
		}
		seenIDs[entry.RemoteID], seenPaths[entry.Path] = true, true
	}
	if !rootSeen {
		return errors.New("deletion conflict subtree lacks its root")
	}
	remoteIDs, remotePaths := map[string]bool{}, map[string]bool{}
	remoteRoot := Completed{}
	for i, entry := range c.DeletionRemoteSubtree {
		if !safeRelative(entry.Path) || entry.RemoteID == "" || entry.Version == "" || remoteIDs[entry.RemoteID] || remotePaths[entry.Path] || (entry.Kind != "file" && entry.Kind != "folder") {
			return errors.New("invalid deletion conflict live subtree")
		}
		if i > 0 && c.DeletionRemoteSubtree[i-1].Path >= entry.Path {
			return errors.New("deletion conflict live subtree is not ordered")
		}
		if entry.Kind == "file" {
			if entry.ModifiedTime == "" || entry.HeadRevisionID == "" || !validHash(entry.MD5, 16) || entry.Size < 0 {
				return errors.New("invalid deletion conflict live file fingerprint")
			}
		} else if entry.ModifiedTime == "" || entry.MD5 != "" || entry.HeadRevisionID != "" || entry.Size != 0 {
			return errors.New("invalid deletion conflict live folder fingerprint")
		}
		if entry.RemoteID == c.RemoteID {
			remoteRoot = entry
		}
		remoteIDs[entry.RemoteID], remotePaths[entry.Path] = true, true
	}
	if remoteRoot.RemoteID == "" || remoteRoot.Version != c.RemoteVersion || remoteRoot.ModifiedTime != c.RemoteModifiedTime || remoteRoot.MD5 != c.RemoteMD5 || remoteRoot.Size != c.RemoteSize || remoteRoot.HeadRevisionID != c.RemoteRevisionID {
		return errors.New("deletion conflict live subtree lacks its remote root")
	}
	for _, entry := range c.DeletionRemoteSubtree {
		if !containsPath(remoteRoot.Path, entry.Path) {
			return errors.New("deletion conflict live subtree contains unrelated path")
		}
	}
	return nil
}
