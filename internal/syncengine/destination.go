package syncengine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/privatefs"
	"os"
	"path/filepath"
	"sort"
)

type registration struct {
	Profile     string `json:"profile"`
	Destination string `json:"destination"`
}
type registryRecord struct {
	Version int            `json:"version"`
	Entries []registration `json:"entries"`
}

func registerDestination(dir, profile, destination string) (*privatefs.Lock, error) {
	var err error
	for _, path := range []*string{&dir, &profile, &destination} {
		*path, err = privatefs.Canonical(*path)
		if err != nil {
			return nil, err
		}
	}
	guard, err := privatefs.Acquire(filepath.Join(dir, "registry.lock"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = guard.Close() }()
	r := registryRecord{Version: 1}
	path := filepath.Join(dir, "registry.json")
	if _, err = os.Lstat(path); err == nil {
		if err = privatefs.ReadJSON(path, &r); err != nil {
			return nil, err
		}
		if r.Version != 1 {
			return nil, errors.New("unsupported destination registry")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("cannot inspect destination registry")
	}
	found := false
	for _, e := range r.Entries {
		if within(e.Destination, destination) || within(destination, e.Destination) {
			if e.Profile != profile || e.Destination != destination {
				return nil, errors.New("destination overlaps another registered profile")
			}
			found = true
		}
	}
	sum := sha256.Sum256([]byte(destination))
	lock, err := privatefs.Acquire(filepath.Join(dir, hex.EncodeToString(sum[:])+".lock"))
	if err != nil {
		return nil, err
	}
	if !found {
		r.Entries = append(r.Entries, registration{Profile: profile, Destination: destination})
		if err = privatefs.WriteJSON(path, r); err != nil {
			_ = lock.Close()
			return nil, err
		}
	}
	return lock, nil
}
func nonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
func rootPath(root *os.Root, path string) error {
	if path != "." && !safeRelative(path) {
		return errors.New("unsafe local path")
	}
	current := path
	for {
		info, err := root.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot inspect local path")
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return errors.New("local symlinks are unsupported")
		}
		if current == "." {
			return nil
		}
		current = filepath.Dir(current)
	}
}
func clearProbe(root *os.Root, state *State, save func() error) error {
	if state.ProbePath == "" {
		return nil
	}
	if err := rootPath(root, state.ProbePath); err != nil {
		return err
	}
	entries := append([]Entry(nil), state.ProbeEntries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path > entries[j].Path })
	for _, e := range entries {
		p := filepath.Join(state.ProbePath, e.Path)
		if err := rootPath(root, p); err != nil {
			return err
		}
		if err := root.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot remove owned validation probe")
		}
	}
	if err := root.Remove(state.ProbePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot remove owned validation probe directory")
	}
	state.ProbePath = ""
	state.ProbeEntries = nil
	return save()
}
func probeDestination(root *os.Root, snapshot Snapshot, state *State, save func() error) error {
	if err := clearProbe(root, state, save); err != nil {
		return err
	}
	id := nonce()
	if id == "" {
		return errors.New("cannot create probe identity")
	}
	state.ProbePath = ".grove-probe-" + id
	state.ProbeEntries = append([]Entry(nil), snapshot.Entries...)
	if err := save(); err != nil {
		return err
	}
	if err := root.Mkdir(state.ProbePath, 0700); err != nil {
		return errors.New("cannot create destination validation probe")
	}
	entries := append([]Entry(nil), snapshot.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var probeErr error
	for _, e := range entries {
		if !safeRelative(e.Path) {
			probeErr = errors.New("unsafe mapped path")
			break
		}
		p := filepath.Join(state.ProbePath, e.Path)
		if entryKind(e) == "folder" {
			probeErr = root.Mkdir(p, 0700)
		} else {
			var f *os.File
			f, probeErr = root.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if probeErr == nil {
				probeErr = f.Close()
			}
		}
		if probeErr != nil {
			probeErr = errors.New("destination has incompatible names or path limits")
			break
		}
	}
	if err := clearProbe(root, state, save); err != nil {
		return err
	}
	return probeErr
}
