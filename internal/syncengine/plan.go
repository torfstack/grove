package syncengine

import (
	"errors"
	"github.com/torfstack/grove/internal/drive"
	"path/filepath"
	"sort"
)

func entryKind(e Entry) string {
	if e.Remote.MIMEType == drive.FolderMIME {
		return "folder"
	}
	return "file"
}
func same(e Entry, c Completed) bool {
	return e.Path == c.Path && e.Remote.ID == c.RemoteID && entryKind(e) == c.Kind && (c.Kind == "folder" || (e.Remote.Version == c.Version && e.Remote.MD5 == c.MD5 && e.Remote.Size == c.Size))
}
func BuildPlan(remote Snapshot, local []LocalEntry, state State) (Plan, error) {
	if err := validateLocal(local, state.Completed); err != nil {
		return Plan{}, err
	}
	entries := append([]Entry(nil), remote.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	byID := map[string]Entry{}
	paths := map[string]bool{}
	for _, e := range entries {
		if !safeRelative(e.Path) || e.Remote.ID == "" || paths[e.Path] {
			return Plan{}, errors.New("invalid remote planned path")
		}
		if _, ok := byID[e.Remote.ID]; ok {
			return Plan{}, errors.New("duplicate remote identity")
		}
		byID[e.Remote.ID] = e
		paths[e.Path] = true
	}
	simulated := append([]Completed(nil), state.Completed...)
	original := map[string]string{}
	ids := map[string]bool{}
	for _, c := range simulated {
		e, ok := byID[c.RemoteID]
		if !ok {
			return Plan{}, errors.New("remote removal is unsupported")
		}
		if ids[c.RemoteID] || entryKind(e) != c.Kind {
			return Plan{}, errors.New("remote identity or type conflict")
		}
		ids[c.RemoteID] = true
		original[c.Path] = c.RemoteID
	}
	for _, e := range entries {
		if id, ok := original[e.Path]; ok && id != e.Remote.ID {
			return Plan{}, errors.New("occupied remote target or path reuse is unsupported")
		}
	}
	plan := Plan{}
	directories := map[string]bool{".": true}
	for _, c := range simulated {
		if c.Kind == "folder" {
			directories[c.Path] = true
		}
	}
	// Resolve structural dependencies by repeatedly selecting the first ready path.
	for {
		changed := false
		remaining := false
		indices := map[string]int{}
		for i, c := range simulated {
			indices[c.RemoteID] = i
		}
		for _, e := range entries {
			index, known := indices[e.Remote.ID]

			if !known {
				if entryKind(e) != "folder" || directories[e.Path] {
					continue
				}
				remaining = true
				if !directories[filepath.Dir(e.Path)] {
					continue
				}
				plan.Operations = append(plan.Operations, Operation{Kind: OpMkdir, Entry: e})
				directories[e.Path] = true
				simulated = append(simulated, completedEntry(e, ""))
				changed = true
				break
			}
			c := simulated[index]
			if c.Path == e.Path {
				continue
			}
			remaining = true
			if !directories[filepath.Dir(e.Path)] {
				continue
			}
			if c.Kind == "folder" && containsPath(c.Path, e.Path) {
				return Plan{}, errors.New("folder cannot move inside itself")
			}
			occupied := false
			for _, v := range simulated {
				if containsPath(e.Path, v.Path) {
					occupied = true
					break
				}
			}
			if occupied {
				return Plan{}, errors.New("move target is occupied")
			}
			before := []Completed{}
			for _, v := range simulated {
				if v.Path == c.Path || (c.Kind == "folder" && containsPath(c.Path, v.Path)) {
					before = append(before, v)
				}
			}
			plan.Operations = append(plan.Operations, Operation{Kind: OpMove, Entry: e, Before: before})
			simulated = remapBaseline(simulated, c.Path, e.Path)
			directories = map[string]bool{".": true}
			for _, v := range simulated {
				if v.Kind == "folder" {
					directories[v.Path] = true
				}
			}
			changed = true
			break
		}
		if !remaining {
			break
		}
		if !changed {
			return Plan{}, errors.New("unsupported cyclic move or missing parent")
		}
	}
	baseline := map[string]Completed{}
	for _, c := range simulated {
		baseline[c.RemoteID] = c
	}
	for _, e := range entries {
		c, exists := baseline[e.Remote.ID]
		if !exists {
			if !directories[filepath.Dir(e.Path)] {
				return Plan{}, errors.New("missing planned parent")
			}
			plan.Operations = append(plan.Operations, Operation{Kind: OpDownload, Entry: e})
			continue
		}
		if c.Kind == "folder" {
			continue
		}
		kind := OpSkip
		if e.Remote.Size != c.Size || e.Remote.MD5 != c.MD5 {
			kind = OpReplace
		} else if e.Remote.Version != c.Version {
			kind = OpRecord
		}
		plan.Operations = append(plan.Operations, Operation{Kind: kind, Entry: e, Before: []Completed{c}})
	}
	return plan, nil
}

func BuildTwoWayPlan(remote Snapshot, local []LocalEntry, state State) (Plan, error) {
	if state.ConflictPolicy != "" {
		if err := validateConflictPolicy(state.ConflictPolicy); err != nil {
			return Plan{}, err
		}
	}
	local = append([]LocalEntry(nil), local...)
	sort.Slice(local, func(i, j int) bool { return local[i].Path < local[j].Path })
	baselineLocal := make([]LocalEntry, 0, len(state.Completed))
	localByPath := make(map[string]LocalEntry, len(local))
	for _, entry := range local {
		if !safeRelative(entry.Path) {
			return Plan{}, errors.New("invalid local planned path")
		}
		if _, exists := localByPath[entry.Path]; exists {
			return Plan{}, errors.New("duplicate local path")
		}
		localByPath[entry.Path] = entry
	}
	for _, completed := range state.Completed {
		entry, exists := localByPath[completed.Path]
		if !exists || completed.Kind != entry.Kind {
			return Plan{}, errors.New("tracked local structure changed")
		}
		baselineLocal = append(baselineLocal, LocalEntry{Path: completed.Path, Kind: completed.Kind, SHA256: completed.SHA256, Size: completed.Size})
	}
	cloudPlan, err := BuildPlan(remote, baselineLocal, state)
	if err != nil {
		return Plan{}, err
	}
	remoteByID := make(map[string]Entry, len(remote.Entries))
	for _, entry := range remote.Entries {
		remoteByID[entry.Remote.ID] = entry
	}
	plan := Plan{Operations: append([]Operation(nil), cloudPlan.Operations...)}
	folders := map[string]bool{".": true}
	for _, entry := range remote.Entries {
		if entryKind(entry) == "folder" {
			folders[entry.Path] = true
		}
	}
	for _, entry := range local {
		if entry.Kind == "folder" {
			folders[entry.Path] = true
		}
	}
	for _, completed := range state.Completed {
		if completed.Kind != "file" {
			continue
		}
		localEntry := localByPath[completed.Path]
		if matchesLocal(completed, localEntry) {
			continue
		}
		entry, exists := remoteByID[completed.RemoteID]
		if !exists || entry.Path != completed.Path {
			return Plan{}, errors.New("local edit overlaps a remote structural change")
		}
		unchanged := same(entry, completed)
		plan.Operations = withoutOperationForRemote(plan.Operations, completed.RemoteID)
		if unchanged {
			plan.Operations = append(plan.Operations, Operation{Kind: OpUpload, Entry: entry, Local: localEntry, Before: []Completed{completed}})
			continue
		}
		conflict := Conflict{Path: completed.Path, Baseline: completed, LocalSHA256: localEntry.SHA256, LocalSize: localEntry.Size, RemoteID: entry.Remote.ID, RemoteVersion: entry.Remote.Version, RemoteMD5: entry.Remote.MD5, RemoteSize: entry.Remote.Size}
		conflict.ID = conflictID(conflict)
		operation := Operation{Kind: OpConflict, Entry: entry, Local: localEntry, Conflict: &conflict, Before: []Completed{completed}}
		switch state.ConflictPolicy {
		case "", ConflictAsk:
		case ConflictKeepLocal:
			operation.ResolutionSide = SideLocal
		case ConflictKeepCloud:
			operation.ResolutionSide = SideCloud
		default:
			return Plan{}, errors.New("invalid conflict policy")
		}
		plan.Operations = append(plan.Operations, operation)
	}
	for _, entry := range local {
		if hasCompletedPath(state.Completed, entry.Path) {
			continue
		}
		if remoteEntry, remoteExists := pathEntry(remote.Entries, entry.Path); remoteExists {
			if entry.Kind != "file" || entryKind(remoteEntry) != "file" {
				return Plan{}, errors.New("local and remote structural additions share a path")
			}
			plan.Operations = withoutOperationForRemote(plan.Operations, remoteEntry.Remote.ID)
			conflict := Conflict{Path: entry.Path, LocalSHA256: entry.SHA256, LocalSize: entry.Size, RemoteID: remoteEntry.Remote.ID, RemoteVersion: remoteEntry.Remote.Version, RemoteMD5: remoteEntry.Remote.MD5, RemoteSize: remoteEntry.Remote.Size}
			conflict.ID = conflictID(conflict)
			operation := Operation{Kind: OpConflict, Entry: remoteEntry, Local: entry, Conflict: &conflict}
			switch state.ConflictPolicy {
			case "", ConflictAsk:
			case ConflictKeepLocal:
				operation.ResolutionSide = SideLocal
			case ConflictKeepCloud:
				operation.ResolutionSide = SideCloud
			default:
				return Plan{}, errors.New("invalid conflict policy")
			}
			plan.Operations = append(plan.Operations, operation)
			continue
		}
		if entry.Kind != "file" && entry.Kind != "folder" {
			return Plan{}, errors.New("unsupported local entry")
		}
		if !folders[filepath.Dir(entry.Path)] {
			return Plan{}, errors.New("local addition has no known parent folder")
		}
		plan.Operations = append(plan.Operations, Operation{Kind: OpCreate, Local: entry})
	}
	return plan, nil
}

func withoutOperationForRemote(operations []Operation, id string) []Operation {
	filtered := operations[:0]
	for _, operation := range operations {
		if operation.Entry.Remote.ID != id {
			filtered = append(filtered, operation)
		}
	}
	return filtered
}

func hasCompletedPath(completed []Completed, path string) bool {
	for _, entry := range completed {
		if entry.Path == path {
			return true
		}
	}
	return false
}

func pathEntry(entries []Entry, path string) (Entry, bool) {
	for _, entry := range entries {
		if entry.Path == path {
			return entry, true
		}
	}
	return Entry{}, false
}
