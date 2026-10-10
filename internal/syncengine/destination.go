package syncengine

import (
	"crypto/rand"
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

type destinationLease struct {
	locks     []*privatefs.Lock
	directory bool
}

func (l *destinationLease) Close() error {
	var result error
	for i := len(l.locks) - 1; i >= 0; i-- {
		if err := l.locks[i].Close(); err != nil {
			result = err
		}
	}
	l.locks = nil
	return result
}
func (l *destinationLease) attachDirectory(path string) error {
	if l.directory {
		return nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("destination must be a directory, not a symlink")
	}
	lock, err := privatefs.AcquireDirectory(path, false)
	if err != nil {
		return err
	}
	l.locks = append(l.locks, lock)
	l.directory = true
	return nil
}
func physicalWithin(parent, path string) bool {
	if within(parent, path) {
		return true
	}
	info, err := os.Stat(parent)
	if err != nil {
		return false
	}
	for current := path; ; current = filepath.Dir(current) {
		other, err := os.Stat(current)
		if err == nil && os.SameFile(info, other) {
			return true
		}
		if filepath.Dir(current) == current {
			return false
		}
	}
}
func registerDestination(dir, profile, destination string) (lease *destinationLease, err error) {
	for _, p := range []*string{&dir, &profile, &destination} {
		*p, err = privatefs.Canonical(*p)
		if err != nil {
			return nil, err
		}
	}
	if within(destination, dir) {
		return nil, errors.New("destination registry must be outside the destination")
	}
	parent := filepath.Dir(destination)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return nil, errors.New("destination parent directory must already exist")
	}
	lease = &destinationLease{}
	success := false
	defer func() {
		if !success {
			_ = lease.Close()
		}
	}()
	var ancestors []string
	for current := parent; ; current = filepath.Dir(current) {
		ancestors = append(ancestors, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		lock, err := privatefs.AcquireDirectory(ancestors[i], true)
		if err != nil {
			return lease, err
		}
		lease.locks = append(lease.locks, lock)
	}
	sidecar := filepath.Join(parent, "."+filepath.Base(destination)+".grove-destination.lock")
	lock, err := privatefs.Acquire(sidecar)
	if err != nil {
		return lease, err
	}
	lease.locks = append(lease.locks, lock)
	if err = lease.attachDirectory(destination); err != nil {
		return lease, err
	}
	guard, err := privatefs.Acquire(filepath.Join(dir, "registry.lock"))
	if err != nil {
		return lease, err
	}
	defer func() { _ = guard.Close() }()
	r := registryRecord{Version: 1}
	path := filepath.Join(dir, "registry.json")
	if _, err = os.Lstat(path); err == nil {
		if err = privatefs.ReadJSON(path, &r); err != nil {
			return lease, err
		}
		if r.Version != 1 {
			return lease, errors.New("unsupported destination registry")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return lease, errors.New("cannot inspect destination registry")
	}
	found := false
	for _, entry := range r.Entries {
		if physicalWithin(entry.Destination, destination) || physicalWithin(destination, entry.Destination) {
			if entry.Profile != profile || entry.Destination != destination {
				return lease, errors.New("destination overlaps another registered profile")
			}
			found = true
		}
	}
	if !found {
		r.Entries = append(r.Entries, registration{Profile: profile, Destination: destination})
		if err = privatefs.WriteJSON(path, r); err != nil {
			return lease, err
		}
	}
	success = true
	return lease, nil
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
