package syncengine

import (
	"errors"
	"path/filepath"
	"strings"
)

func containsPath(parent, path string) bool {
	return path == parent || strings.HasPrefix(path, parent+string(filepath.Separator))
}
func matchesLocal(c Completed, l LocalEntry) bool {
	return c.Kind == l.Kind && (c.Kind == "folder" || (c.Size == l.Size && c.SHA256 == l.SHA256))
}
func validateLocal(local []LocalEntry, baseline []Completed) error {
	paths := map[string]Completed{}
	for _, c := range baseline {
		paths[c.Path] = c
	}
	found := map[string]bool{}
	for _, l := range local {
		c, ok := paths[l.Path]
		if !ok || found[l.Path] || !matchesLocal(c, l) {
			return errors.New("local content conflicts with recorded baseline")
		}
		found[l.Path] = true
	}
	if len(found) != len(paths) {
		return errors.New("tracked local content is missing")
	}
	return nil
}
func remapBaseline(before []Completed, source, target string) []Completed {
	after := append([]Completed(nil), before...)
	for i := range after {
		if containsPath(source, after[i].Path) {
			after[i].Path = target + strings.TrimPrefix(after[i].Path, source)
		}
	}
	return after
}
func completedEntry(entry Entry, hash string) Completed {
	return Completed{Path: entry.Path, RemoteID: entry.Remote.ID, Version: entry.Remote.Version, MD5: entry.Remote.MD5, SHA256: hash, Kind: entryKind(entry), Size: entry.Remote.Size}
}
