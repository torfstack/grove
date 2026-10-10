package syncengine

import (
	"errors"
	"github.com/torfstack/grove/internal/privatefs"
	"os"
	"path/filepath"
	"strings"
)

func safeRelative(path string) bool {
	if !filepath.IsLocal(path) || path == "." || filepath.Clean(path) != path {
		return false
	}
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if !privatefs.SafeName(part) {
			return false
		}
	}
	return true
}
func loadState(profile string, binding Binding) (State, error) {
	path := filepath.Join(profile, "state.json")
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: 1, Binding: binding}, nil
	}
	var s State
	if err = privatefs.ReadJSON(path, &s); err != nil {
		return State{}, err
	}
	if s.Version != 1 || s.Binding != binding {
		return State{}, errors.New("unsupported state or changed profile binding")
	}
	seen := map[string]bool{}
	for _, c := range s.Completed {
		if !safeRelative(c.Path) || seen[c.Path] || (c.Kind != "file" && c.Kind != "folder") || c.RemoteID == "" {
			return State{}, errors.New("invalid completed state")
		}
		seen[c.Path] = true
	}
	if s.Pending != nil {
		p := s.Pending
		if !safeRelative(p.Entry.Path) || p.Entry.Remote.ID == "" {
			return State{}, errors.New("invalid pending state")
		}
		if p.TempPath != "" && (!safeRelative(p.TempPath) || filepath.Dir(p.TempPath) != filepath.Dir(p.Entry.Path) || !strings.HasPrefix(filepath.Base(p.TempPath), ".grove-download-")) {
			return State{}, errors.New("invalid temporary state")
		}
	}
	if s.ProbePath != "" && (!privatefs.SafeName(s.ProbePath) || !strings.HasPrefix(s.ProbePath, ".grove-probe-")) {
		return State{}, errors.New("invalid probe state")
	}
	for _, e := range s.ProbeEntries {
		if !safeRelative(e.Path) {
			return State{}, errors.New("invalid probe entry")
		}
	}
	return s, nil
}
func saveState(profile string, state State) error {
	return privatefs.WriteJSON(filepath.Join(profile, "state.json"), state)
}
func within(parent, path string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}
func canonicalOptions(opts Options) (Options, error) {
	if opts.RemoteRoot == "" || opts.ProfileDir == "" || opts.LocalDir == "" || opts.TokenFile == "" {
		return Options{}, errors.New("profile, remote root, local directory, and token file are required")
	}
	if s, err := os.Lstat(opts.LocalDir); err == nil && s.Mode()&os.ModeSymlink != 0 {
		return Options{}, errors.New("destination cannot be a symlink")
	}
	var err error
	for _, p := range []*string{&opts.ProfileDir, &opts.LocalDir, &opts.TokenFile} {
		*p, err = privatefs.Canonical(*p)
		if err != nil {
			return Options{}, err
		}
	}
	if err := privatefs.OutsideGit(opts.ProfileDir); err != nil {
		return Options{}, err
	}
	if within(opts.LocalDir, opts.ProfileDir) || within(opts.ProfileDir, opts.LocalDir) || within(opts.LocalDir, opts.TokenFile) || within(opts.ProfileDir, opts.TokenFile) {
		return Options{}, errors.New("profile, token, and destination must be separate")
	}
	return opts, nil
}
func checkInitialDestination(path string) error {
	s, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
		return errors.New("destination must be a directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return errors.New("cannot inspect destination")
	}
	if len(entries) != 0 {
		return errors.New("new destination must be empty")
	}
	return nil
}
