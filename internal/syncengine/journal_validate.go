package syncengine

import (
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

func validateCompleted(entries []Completed) error {
	paths, ids := map[string]bool{}, map[string]bool{}
	for _, c := range entries {
		if !safeRelative(c.Path) || c.RemoteID == "" || paths[c.Path] || ids[c.RemoteID] || (c.Kind != "file" && c.Kind != "folder") {
			return errors.New("invalid journal membership")
		}
		if c.Kind == "file" && (c.Size < 0 || c.Version == "" || !validHash(c.MD5, 16) || !validHash(c.SHA256, 32)) {
			return errors.New("invalid journal fingerprint")
		}
		paths[c.Path] = true
		ids[c.RemoteID] = true
	}
	return nil
}
func validateJournal(j Journal, baseline []Completed) error {
	op := j.Operation
	if op.Kind != OpReplace && op.Kind != OpMove {
		return errors.New("invalid journal operation")
	}
	if !safeRelative(op.Entry.Path) || len(op.Before) == 0 {
		return errors.New("invalid journal target")
	}
	if err := validateCompleted(op.Before); err != nil {
		return err
	}
	if err := validateCompleted(j.After); err != nil {
		return err
	}
	if op.Kind == OpReplace {
		if len(op.Before) != 1 || op.Before[0].Kind != "file" || op.Before[0].Path != op.Entry.Path || op.Before[0].RemoteID != op.Entry.Remote.ID {
			return errors.New("invalid replacement identity")
		}
		if j.Phase != "intent" && j.Phase != "cancelled" && j.Phase != "verified" && j.Phase != "backed-up" && j.Phase != "committed" {
			return errors.New("invalid replacement phase")
		}
		for _, artifact := range []struct{ path, prefix string }{{j.TempPath, ".grove-download-"}, {j.BackupPath, ".grove-backup-"}} {
			if !safeRelative(artifact.path) || !strings.HasPrefix(filepath.Base(artifact.path), artifact.prefix) || filepath.Dir(artifact.path) != filepath.Dir(op.Entry.Path) || artifact.path == op.Entry.Path {
				return errors.New("invalid journal artifact")
			}
			for _, c := range baseline {
				if c.Path == artifact.path {
					return errors.New("artifact overlaps tracked content")
				}
			}
		}
		if j.TempPath == j.BackupPath {
			return errors.New("colliding journal paths")
		}
		if j.Phase == "intent" {
			if len(j.After) != 0 || j.VerifiedSHA256 != "" {
				return errors.New("unverified replacement has result")
			}
		} else if !validHash(j.VerifiedSHA256, 32) || len(j.After) != 1 || !reflect.DeepEqual(j.After[0], completedEntry(op.Entry, j.VerifiedSHA256)) {
			return errors.New("invalid verified replacement result")
		}
	} else {
		if j.Phase != "intent" && j.Phase != "committed" {
			return errors.New("invalid move phase")
		}
		if j.TempPath != "" || j.BackupPath != "" || j.VerifiedSHA256 != "" {
			return errors.New("invalid move artifacts")
		}
		source, err := moveSource(op)
		if err != nil {
			return err
		}
		if source.Path == op.Entry.Path || (source.Kind == "folder" && containsPath(source.Path, op.Entry.Path)) {
			return errors.New("invalid move paths")
		}
		for _, c := range op.Before {
			if !containsPath(source.Path, c.Path) {
				return errors.New("move contains unrelated baseline")
			}
		}
		expected := remapBaseline(op.Before, source.Path, op.Entry.Path)
		sort.Slice(expected, func(i, j int) bool { return expected[i].Path < expected[j].Path })
		if !reflect.DeepEqual(expected, j.After) {
			return errors.New("invalid move result")
		}
	}
	if j.Phase == "committed" {
		known := map[string]Completed{}
		for _, c := range baseline {
			known[c.RemoteID] = c
		}
		for _, c := range j.After {
			if !reflect.DeepEqual(known[c.RemoteID], c) {
				return errors.New("committed journal disagrees with baseline")
			}
		}
	} else {
		candidate := State{Completed: baseline}
		if err := applyBaseline(&candidate, op.Before, op.Before); err != nil {
			return err
		}
	}
	return nil
}
