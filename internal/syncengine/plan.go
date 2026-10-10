package syncengine

import (
	"errors"
	"github.com/torfstack/grove/internal/drive"
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
	entries := append([]Entry(nil), remote.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	byPath := map[string]Entry{}
	for _, e := range entries {
		if _, ok := byPath[e.Path]; ok {
			return Plan{}, errors.New("duplicate planned path")
		}
		byPath[e.Path] = e
	}
	completed := map[string]Completed{}
	for _, c := range state.Completed {
		e, ok := byPath[c.Path]
		if !ok || !same(e, c) {
			return Plan{}, errors.New("remote changes after initial download are unsupported")
		}
		completed[c.Path] = c
	}
	locals := map[string]LocalEntry{}
	for _, l := range local {
		if _, ok := byPath[l.Path]; !ok {
			return Plan{}, errors.New("unexpected local entry")
		}
		locals[l.Path] = l
	}
	for _, c := range state.Completed {
		l, ok := locals[c.Path]
		if !ok || l.Kind != c.Kind || (c.Kind == "file" && (l.Size != c.Size || l.SHA256 != c.SHA256)) {
			return Plan{}, errors.New("local content changed since download")
		}
	}
	p := Plan{}
	for _, e := range entries {
		_, done := completed[e.Path]
		if done {
			p.Operations = append(p.Operations, Operation{Kind: "skip", Entry: e})
			continue
		}
		if state.PopulationComplete {
			return Plan{}, errors.New("remote additions after initial population are unsupported")
		}
		if _, exists := locals[e.Path]; exists {
			return Plan{}, errors.New("unowned local destination entry")
		}
		kind := OpDownload
		if entryKind(e) == "folder" {
			kind = "mkdir"
		}
		p.Operations = append(p.Operations, Operation{Kind: kind, Entry: e})
	}
	return p, nil
}
