package syncengine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
)

func conflictID(c Conflict) string {
	identity := struct {
		Path          string `json:"path"`
		RemoteID      string `json:"remote_id"`
		BaselineSHA   string `json:"baseline_sha256"`
		LocalSHA      string `json:"local_sha256"`
		RemoteMD5     string `json:"remote_md5"`
		RemoteSize    int64  `json:"remote_size"`
		CloudRevision string `json:"cloud_revision_id,omitempty"`
	}{c.Path, c.RemoteID, c.Baseline.SHA256, c.LocalSHA256, c.RemoteMD5, c.RemoteSize, c.CloudRevisionID}
	data, _ := json.Marshal(identity)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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
		if requireStages && (!safeRelative(c.LocalStagePath) || !strings.HasPrefix(filepath.Base(c.LocalStagePath), ".grove-upload-") || c.LocalStageIdentity == nil || c.LocalStageIdentity.Inode == 0 || !safeRelative(c.CloudStagePath) || !strings.HasPrefix(filepath.Base(c.CloudStagePath), ".grove-cloud-conflict-") || c.CloudStageIdentity == nil || c.CloudStageIdentity.Inode == 0 || !validHash(c.CloudSHA256, 32)) {
			return errors.New("invalid pending conflict candidate stages")
		}
		if c.CloudRevisionID != "" && (!validHash(c.CloudMD5, 16) || c.CloudSize < 0) {
			return errors.New("invalid pending cloud revision fingerprint")
		}
		if c.SelectedSide != "" && c.SelectedSide != SideLocal && c.SelectedSide != SideCloud {
			return errors.New("invalid pending conflict selection")
		}
		seenIDs[c.ID] = true
		seenPaths[c.Path] = true
	}
	return nil
}
