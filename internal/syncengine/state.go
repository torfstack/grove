package syncengine

import (
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/privatefs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	if (s.Version != 1 && s.Version != 2) || s.Binding != binding {
		return State{}, errors.New("unsupported state or changed profile binding")
	}
	seen := map[string]bool{}
	ids := map[string]bool{}
	for _, c := range s.Completed {
		if !safeRelative(c.Path) || seen[c.Path] || ids[c.RemoteID] || (c.Kind != "file" && c.Kind != "folder") || c.RemoteID == "" {
			return State{}, errors.New("invalid completed state")
		}
		if c.Kind == "file" && (c.Size < 0 || c.Version == "" || !validHash(c.MD5, 16) || !validHash(c.SHA256, 32)) {
			return State{}, errors.New("invalid completed fingerprint")
		}
		seen[c.Path] = true
		ids[c.RemoteID] = true
	}
	if s.Pending != nil {
		p := s.Pending
		if p.Phase != "intent" && p.Phase != "downloading" && p.Phase != "verified" {
			return State{}, errors.New("invalid pending phase")
		}
		if p.Phase == "verified" && !validHash(p.VerifiedSHA256, 32) {
			return State{}, errors.New("invalid pending hash")
		}
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
	if s.Transaction != nil {
		if s.Version != 2 || s.Pending != nil {
			return State{}, errors.New("invalid journal version")
		}
		if err := validateJournal(*s.Transaction); err != nil {
			return State{}, err
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

func validHash(value string, size int) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == size && strings.ToLower(value) == value
}
func migrateState(profile string, state State) (State, error) {
	if state.Version == 2 {
		return state, nil
	}
	if state.Version != 1 || state.Pending != nil || state.ProbePath != "" {
		return state, errors.New("legacy recovery required before migration")
	}
	next := state
	next.Version = 2
	if err := saveState(profile, next); err != nil {
		return state, err
	}
	return next, nil
}
func applyBaseline(state *State, before, after []Completed) error {
	entries := map[string]Completed{}
	for _, c := range state.Completed {
		if _, ok := entries[c.RemoteID]; ok {
			return errors.New("duplicate baseline identity")
		}
		entries[c.RemoteID] = c
	}
	for _, c := range before {
		if !reflect.DeepEqual(entries[c.RemoteID], c) {
			return errors.New("stale baseline")
		}
		delete(entries, c.RemoteID)
	}
	for _, c := range after {
		if _, ok := entries[c.RemoteID]; ok {
			return errors.New("duplicate updated identity")
		}
		entries[c.RemoteID] = c
	}
	paths := map[string]bool{}
	next := make([]Completed, 0, len(entries))
	for _, c := range entries {
		if paths[c.Path] {
			return errors.New("duplicate updated path")
		}
		paths[c.Path] = true
		next = append(next, c)
	}
	sort.Slice(next, func(i, j int) bool { return next[i].Path < next[j].Path })
	state.Completed = next
	return nil
}
func validateJournal(j Journal) error {
	if j.Operation.Kind != OpReplace && j.Operation.Kind != OpMove {
		return errors.New("invalid journal operation")
	}
	if j.Phase != "intent" && j.Phase != "verified" && j.Phase != "backed-up" && j.Phase != "committed" {
		return errors.New("invalid journal phase")
	}
	if !safeRelative(j.Operation.Entry.Path) || len(j.Operation.Before) == 0 {
		return errors.New("invalid journal baseline")
	}
	for _, p := range []string{j.TempPath, j.BackupPath} {
		if p != "" && (!safeRelative(p) || p == j.Operation.Entry.Path || filepath.Dir(p) != filepath.Dir(j.Operation.Entry.Path)) {
			return errors.New("invalid journal artifact")
		}
	}
	if j.TempPath != "" && j.TempPath == j.BackupPath {
		return errors.New("colliding journal paths")
	}
	return nil
}
