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

func BuildDeletionResolutionPlan(remote Snapshot, local []LocalEntry, state State, id string) (Plan, error) {
	c := findConflict(state.Conflicts, id)
	if c == nil || !c.Deletion || c.SelectedSide != SideLocal {
		return Plan{}, errors.New("selected deletion conflict is missing")
	}
	for _, entry := range local {
		if containsPath(c.Path, entry.Path) {
			return Plan{}, errors.New("local deletion conflict path reappeared")
		}
	}
	if !matchesDeletionRemoteConflict(remote, *c) {
		return Plan{}, errors.New("remote deletion conflict candidate changed")
	}
	for _, entry := range remote.Entries {
		if entry.Remote.ID == c.RemoteID {
			return Plan{Operations: []Operation{{Kind: OpTrash, Entry: entry, Before: c.DeletionSubtree, RemoteSubtree: c.DeletionRemoteSubtree, Conflict: c, ResolutionSide: SideLocal}}}, nil
		}
	}
	return Plan{}, errors.New("remote deletion conflict identity is missing")
}

func BuildRestorationPlan(remote Snapshot, local []LocalEntry, state State) (Plan, error) {
	synthetic := append([]LocalEntry(nil), local...)
	for _, conflict := range state.Conflicts {
		filtered := make([]LocalEntry, 0, len(synthetic))
		for _, entry := range synthetic {
			if conflict.Deletion && containsPath(conflict.Path, entry.Path) {
				return Plan{}, errors.New("local deletion conflict path reappeared during restoration")
			}
			if !conflict.Deletion && entry.Path == conflict.Path {
				if entry.Kind != "file" || entry.SHA256 != conflict.LocalSHA256 || entry.Size != conflict.LocalSize {
					return Plan{}, errors.New("local conflict candidate changed during restoration")
				}
				continue
			}
			filtered = append(filtered, entry)
		}
		synthetic = filtered
		baselines := []Completed{conflict.Baseline}
		if conflict.Deletion {
			baselines = conflict.DeletionSubtree
		}
		for _, entry := range baselines {
			if entry.RemoteID != "" {
				synthetic = append(synthetic, LocalEntry{Path: entry.Path, Kind: entry.Kind, SHA256: entry.SHA256, Size: entry.Size})
			}
		}
	}
	plan, err := BuildPlan(remote, synthetic, state)
	if err != nil {
		return Plan{}, err
	}
	for _, conflict := range state.Conflicts {
		paths := []string{conflict.Path}
		for _, entry := range remote.Entries {
			if entry.Remote.ID == conflict.RemoteID {
				paths = append(paths, entry.Path)
				break
			}
		}
		baselines := []Completed{conflict.Baseline}
		if conflict.Deletion {
			baselines = conflict.DeletionSubtree
		}
		plan.Operations = withoutSubtreeOperations(plan.Operations, baselines, paths...)
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
		if exists && completed.Kind != entry.Kind {
			return Plan{}, errors.New("tracked local structure changed")
		}
		baselineLocal = append(baselineLocal, LocalEntry{Path: completed.Path, Kind: completed.Kind, SHA256: completed.SHA256, Size: completed.Size})
	}
	cloudPlan, err := BuildPlan(deletionPlanningSnapshot(remote, localByPath, state.Completed), baselineLocal, state)
	if err != nil {
		return Plan{}, err
	}
	remoteByID := make(map[string]Entry, len(remote.Entries))
	for _, entry := range remote.Entries {
		remoteByID[entry.Remote.ID] = entry
	}
	plan := Plan{Operations: append([]Operation(nil), cloudPlan.Operations...)}
	deletionRoots := missingDeletionRoots(localByPath, state.Completed)
	for _, root := range deletionRoots {
		subtree := baselineSubtree(state.Completed, root.Path)
		entry, exists := remoteByID[root.RemoteID]
		if !exists {
			return Plan{}, errors.New("remote removal is unsupported")
		}
		liveSubtree := remoteDeletionSubtree(remote.Entries, entry.Path)
		unchanged := remoteSubtreeMatches(remote.Entries, remoteByID, subtree, root, state)
		plan.Operations = withoutSubtreeOperations(plan.Operations, subtree, root.Path, entry.Path)
		if pending := deletionConflictForPath(state.Conflicts, root.Path); pending != nil {
			if sameDeletionRemoteSubtree(pending.DeletionRemoteSubtree, liveSubtree) {
				switch pending.SelectedSide {
				case SideLocal:
					plan.Operations = append(plan.Operations, Operation{Kind: OpTrash, Entry: entry, Before: subtree, RemoteSubtree: liveSubtree, Conflict: pending, ResolutionSide: SideLocal})
				case SideCloud:
					plan.Operations = append(plan.Operations, Operation{Kind: OpConflict, Entry: entry, Conflict: pending, ResolutionSide: SideCloud, Before: subtree})
				default:
					plan.Operations = append(plan.Operations, Operation{Kind: OpConflict, Entry: entry, Conflict: pending, Before: subtree})
				}
			} else {
				conflict := deletionConflict(root, entry, subtree, liveSubtree)
				plan.Operations = append(plan.Operations, Operation{Kind: OpConflict, Entry: entry, Conflict: &conflict, Before: subtree})
			}
			continue
		}
		if unchanged {
			plan.Operations = append(plan.Operations, Operation{Kind: OpTrash, Entry: entry, Before: subtree, RemoteSubtree: liveSubtree})
			continue
		}
		conflict := deletionConflict(root, entry, subtree, liveSubtree)
		plan.Operations = append(plan.Operations, Operation{Kind: OpConflict, Entry: entry, Conflict: &conflict, Before: subtree})
	}
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
		if _, exists := localByPath[completed.Path]; !exists {
			continue
		}
		if completed.Kind != "file" {
			continue
		}
		localEntry := localByPath[completed.Path]
		if pending, side := conflictResolutionForPath(state.Conflicts, completed.Path, state.ConflictPolicy); pending != nil {
			entry, exists := remoteByID[completed.RemoteID]
			if !exists || entry.Path != completed.Path {
				return Plan{}, errors.New("selected conflict overlaps a remote structural change")
			}
			plan.Operations = withoutOperationForRemote(plan.Operations, completed.RemoteID)
			plan.Operations = append(plan.Operations, Operation{Kind: OpConflict, Entry: entry, Local: localEntry, Conflict: pending, ResolutionSide: side, Before: []Completed{completed}})
			continue
		}
		if matchesLocal(completed, localEntry) {
			continue
		}
		entry, exists := remoteByID[completed.RemoteID]
		if !exists || entry.Path != completed.Path {
			return Plan{}, errors.New("local edit overlaps a remote structural change")
		}
		unchanged := sameUploadBaseline(entry, completed)
		plan.Operations = withoutOperationForRemote(plan.Operations, completed.RemoteID)
		if unchanged {
			plan.Operations = append(plan.Operations, Operation{Kind: OpUpload, Entry: entry, Local: localEntry, Before: []Completed{completed}})
			continue
		}
		conflict := Conflict{Path: completed.Path, Baseline: completed, LocalSHA256: localEntry.SHA256, LocalSize: localEntry.Size, RemoteID: entry.Remote.ID, RemoteVersion: entry.Remote.Version, RemoteMD5: entry.Remote.MD5, RemoteSize: entry.Remote.Size, RemoteRevisionID: entry.Remote.HeadRevisionID, CloudRevisionID: entry.Remote.HeadRevisionID, CloudMD5: entry.Remote.MD5, CloudSize: entry.Remote.Size}
		conflict.ID = conflictID(conflict)
		operation := Operation{Kind: OpConflict, Entry: entry, Local: localEntry, Conflict: &conflict, Before: []Completed{completed}}
		for _, pending := range state.Conflicts {
			if pending.ID == conflict.ID {
				operation.ResolutionSide = pending.SelectedSide
				break
			}
		}
		if operation.ResolutionSide == "" {
			switch state.ConflictPolicy {
			case "", ConflictAsk:
			case ConflictKeepLocal:
				operation.ResolutionSide = SideLocal
			case ConflictKeepCloud:
				operation.ResolutionSide = SideCloud
			default:
				return Plan{}, errors.New("invalid conflict policy")
			}
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
			conflict := Conflict{Path: entry.Path, LocalSHA256: entry.SHA256, LocalSize: entry.Size, RemoteID: remoteEntry.Remote.ID, RemoteVersion: remoteEntry.Remote.Version, RemoteMD5: remoteEntry.Remote.MD5, RemoteSize: remoteEntry.Remote.Size, RemoteRevisionID: remoteEntry.Remote.HeadRevisionID, CloudRevisionID: remoteEntry.Remote.HeadRevisionID, CloudMD5: remoteEntry.Remote.MD5, CloudSize: remoteEntry.Remote.Size}
			conflict.ID = conflictID(conflict)
			operation := Operation{Kind: OpConflict, Entry: remoteEntry, Local: entry, Conflict: &conflict}
			for _, pending := range state.Conflicts {
				if pending.ID == conflict.ID {
					if pending.SelectedSide == SideCloud {
						operation.ResolutionSide = SideCloud
					}
					break
				}
			}
			if operation.ResolutionSide == "" {
				switch state.ConflictPolicy {
				case "", ConflictAsk, ConflictKeepLocal:
				case ConflictKeepCloud:
					operation.ResolutionSide = SideCloud
				default:
					return Plan{}, errors.New("invalid conflict policy")
				}
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

func missingDeletionRoots(local map[string]LocalEntry, completed []Completed) []Completed {
	entries := append([]Completed(nil), completed...)
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i].Path) == len(entries[j].Path) {
			return entries[i].Path < entries[j].Path
		}
		return len(entries[i].Path) < len(entries[j].Path)
	})
	roots := make([]Completed, 0)
	for _, entry := range entries {
		if _, present := local[entry.Path]; present {
			continue
		}
		covered := false
		for _, root := range roots {
			if root.Kind == "folder" && containsPath(root.Path, entry.Path) {
				covered = true
				break
			}
		}
		if !covered {
			roots = append(roots, entry)
		}
	}
	return roots
}

func deletionPlanningSnapshot(remote Snapshot, local map[string]LocalEntry, completed []Completed) Snapshot {
	byID := make(map[string]Completed, len(completed))
	for _, base := range completed {
		if _, present := local[base.Path]; !present {
			byID[base.RemoteID] = base
		}
	}
	typeRoots := map[string]Completed{}
	for _, entry := range remote.Entries {
		if base, found := byID[entry.Remote.ID]; found && base.Kind != entryKind(entry) {
			typeRoots[entry.Path] = base
		}
	}
	out := Snapshot{}
	for _, entry := range remote.Entries {
		covered := false
		for root := range typeRoots {
			if entry.Path != root && containsPath(root, entry.Path) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		if base, found := typeRoots[entry.Path]; found {
			entry.Remote.MIMEType = "application/octet-stream"
			if base.Kind == "folder" {
				entry.Remote.MIMEType = drive.FolderMIME
			}
			entry.Remote.Version, entry.Remote.MD5, entry.Remote.Size = base.Version, base.MD5, base.Size
		}
		out.Entries = append(out.Entries, entry)
	}
	return out
}

func baselineSubtree(completed []Completed, root string) []Completed {
	entries := make([]Completed, 0)
	for _, entry := range completed {
		if containsPath(root, entry.Path) {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func remoteSubtreeMatches(remote []Entry, byID map[string]Entry, subtree []Completed, root Completed, state State) bool {
	if len(subtree) == 0 {
		return false
	}
	known := make(map[string]bool, len(subtree))
	for _, baseline := range subtree {
		entry, ok := byID[baseline.RemoteID]
		if !ok || !sameTrashBaseline(entry, baseline, state) {
			return false
		}
		known[baseline.RemoteID] = true
	}
	for _, entry := range remote {
		if containsPath(root.Path, entry.Path) && !known[entry.Remote.ID] {
			return false
		}
	}
	return true
}

func sameTrashBaseline(entry Entry, baseline Completed, state State) bool {
	if entry.Path != baseline.Path || entry.Remote.ID != baseline.RemoteID || entryKind(entry) != baseline.Kind || entry.Remote.Trashed || entry.Remote.ExplicitlyTrashed || !entry.Remote.OwnedByMe || entry.Remote.Name != filepath.Base(baseline.Path) || len(entry.Remote.Parents) != 1 {
		return false
	}
	parentPath := filepath.Dir(baseline.Path)
	if parentPath == "." {
		if state.Binding.RemoteRoot != "" && entry.Remote.Parents[0] != state.Binding.RemoteRoot {
			return false
		}
	} else {
		parentFound := false
		for _, candidate := range state.Completed {
			if candidate.Path == parentPath && candidate.Kind == "folder" {
				parentFound = true
				if entry.Remote.Parents[0] != candidate.RemoteID {
					return false
				}
				break
			}
		}
		if !parentFound {
			return false
		}
	}
	if baseline.Kind == "folder" {
		return baseline.Version != "" && baseline.ModifiedTime != "" && entry.Remote.Version == baseline.Version && entry.Remote.ModifiedTime == baseline.ModifiedTime
	}
	return sameUploadBaseline(entry, baseline)
}

func withoutSubtreeOperations(operations []Operation, subtree []Completed, paths ...string) []Operation {
	ids := make(map[string]bool, len(subtree))
	for _, entry := range subtree {
		ids[entry.RemoteID] = true
	}
	filtered := operations[:0]
	for _, operation := range operations {
		covered := ids[operation.Entry.Remote.ID]
		for _, before := range operation.Before {
			covered = covered || ids[before.RemoteID]
		}
		for _, path := range paths {
			covered = covered || containsPath(path, operation.Entry.Path)
		}
		if covered {
			continue
		}
		filtered = append(filtered, operation)
	}
	return filtered
}

func remoteDeletionSubtree(remote []Entry, root string) []Completed {
	entries := make([]Completed, 0)
	for _, entry := range remote {
		if containsPath(root, entry.Path) {
			entries = append(entries, completedEntry(entry, ""))
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func sameDeletionRemoteSubtree(expected, current []Completed) bool {
	if len(expected) == 0 || len(expected) != len(current) {
		return false
	}
	for i := range expected {
		left, right := expected[i], current[i]
		left.SHA256, right.SHA256 = "", ""
		if left != right {
			return false
		}
	}
	return true
}

func deletionConflictForPath(conflicts []Conflict, path string) *Conflict {
	for i := range conflicts {
		if conflicts[i].Deletion && conflicts[i].Path == path {
			return &conflicts[i]
		}
	}
	return nil
}

func deletionConflict(root Completed, entry Entry, subtree, liveSubtree []Completed) Conflict {
	conflict := Conflict{
		Deletion: true, DeletionSubtree: append([]Completed(nil), subtree...), DeletionRemoteSubtree: append([]Completed(nil), liveSubtree...),
		Path: root.Path, Baseline: root, RemoteID: entry.Remote.ID,
		RemoteVersion: entry.Remote.Version, RemoteModifiedTime: entry.Remote.ModifiedTime, RemoteMD5: entry.Remote.MD5,
		RemoteSize: entry.Remote.Size, RemoteRevisionID: entry.Remote.HeadRevisionID,
	}
	conflict.ID = conflictID(conflict)
	return conflict
}

func sameUploadBaseline(entry Entry, completed Completed) bool {
	return entry.Path == completed.Path && entry.Remote.ID == completed.RemoteID && entryKind(entry) == "file" && completed.Kind == "file" && completed.HeadRevisionID != "" && entry.Remote.HeadRevisionID == completed.HeadRevisionID && completed.ModifiedTime != "" && entry.Remote.ModifiedTime == completed.ModifiedTime && entry.Remote.MD5 == completed.MD5 && entry.Remote.Size == completed.Size
}

func conflictResolutionForPath(conflicts []Conflict, path string, policy ConflictPolicy) (*Conflict, ConflictSide) {
	for i := range conflicts {
		if conflicts[i].Path != path {
			continue
		}
		side := conflicts[i].SelectedSide
		multi := len(conflictCandidates(conflicts[i])) > 1
		if multi && side == "" {
			return &conflicts[i], ""
		}
		if side == "" {
			switch policy {
			case ConflictKeepLocal:
				side = SideLocal
			case ConflictKeepCloud:
				side = SideCloud
			}
		}
		if side != "" {
			pending := conflicts[i]
			pending.SelectedSide = side
			return &pending, side
		}
	}
	return nil, ""
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
