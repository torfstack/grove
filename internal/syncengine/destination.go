package syncengine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"github.com/torfstack/grove/internal/drive"
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
	if err := checkProbe(root, state, state.ProbePath, "folder"); err != nil {
		return err
	}
	entries := append([]Entry(nil), state.ProbeEntries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path > entries[j].Path })
	for _, e := range entries {
		p := filepath.Join(state.ProbePath, e.Path)
		if err := rootPath(root, p); err != nil {
			return err
		}
		if err := checkProbe(root, state, p, entryKind(e)); err != nil {
			return err
		}
		if err := root.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot remove owned validation probe")
		}
	}
	if err := checkProbe(root, state, state.ProbePath, "folder"); err != nil {
		return err
	}
	if err := root.Remove(state.ProbePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("cannot remove owned validation probe directory")
	}
	state.ProbePath = ""
	state.ProbeEntries = nil
	state.ProbeIdentities = nil
	return save()
}
func probeDestination(root *os.Root, snapshot Snapshot, state *State, save func() error) error {
	return probeDestinationWithLink(root, snapshot, state, save, root.Link)
}

func probeDestinationWithLink(root *os.Root, snapshot Snapshot, state *State, save func() error, link func(string, string) error) error {
	if err := clearProbe(root, state, save); err != nil {
		return err
	}
	id := nonce()
	if id == "" {
		return errors.New("cannot create probe identity")
	}
	state.ProbePath = ".grove-probe-" + id
	state.ProbeEntries = append([]Entry(nil), snapshot.Entries...)
	source, target := ".grove-link-source-"+id, ".grove-link-target-"+id
	state.ProbeEntries = append(state.ProbeEntries, Entry{Path: source}, Entry{Path: target})
	if err := save(); err != nil {
		return err
	}
	if err := createProbe(root, state, state.ProbePath, "folder", save); err != nil {
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
		probeErr = createProbe(root, state, p, entryKind(e), save)
		if probeErr != nil {
			probeErr = errors.New("destination has incompatible names or path limits")
			break
		}
	}
	if probeErr == nil {
		sourcePath := filepath.Join(state.ProbePath, source)
		err := createProbe(root, state, sourcePath, "file", save)
		if err == nil {
			targetPath := filepath.Join(state.ProbePath, target)
			state.ProbeIdentities[targetPath] = state.ProbeIdentities[sourcePath]
			err = save()
			if err == nil {
				err = link(sourcePath, targetPath)
			}
		}
		if err != nil {
			probeErr = errors.New("destination filesystem does not support safe publication")
		}
	}
	if err := clearProbe(root, state, save); err != nil {
		return err
	}
	return probeErr
}

func probeMove(root *os.Root, state *State, save func() error) error {
	return probeMoveWith(root, state, save, func(source, target string) error { return moveNoReplace(root, source, target) })
}
func probeMoveWith(root *os.Root, state *State, save func() error, move func(string, string) error) error {
	id := nonce()
	if id == "" {
		return errors.New("cannot create move probe identity")
	}
	state.ProbePath = ".grove-probe-" + id
	state.ProbeEntries = []Entry{{Path: "source", Remote: drive.File{MIMEType: drive.FolderMIME}}, {Path: "target", Remote: drive.File{MIMEType: drive.FolderMIME}}}
	if err := save(); err != nil {
		return err
	}
	if err := createProbe(root, state, state.ProbePath, "folder", save); err != nil {
		return errors.New("cannot create move probe")
	}
	source, target := filepath.Join(state.ProbePath, "source"), filepath.Join(state.ProbePath, "target")
	err := createProbe(root, state, source, "folder", save)
	if err == nil {
		state.ProbeIdentities[target] = state.ProbeIdentities[source]
		err = save()
		if err == nil {
			err = move(source, target)
		}
	}
	if err == nil {
		delete(state.ProbeIdentities, source)
		err = save()
		if err == nil {
			err = createProbe(root, state, source, "folder", save)
		}
	}
	if err == nil && move(source, target) == nil {
		err = errors.New("move primitive replaced occupied target")
	}
	cleanupErr := clearProbe(root, state, save)
	if cleanupErr != nil {
		return cleanupErr
	}
	if err != nil {
		return errors.New("destination filesystem does not support safe moves")
	}
	return nil
}

func probeSnapshot(remote Snapshot, baseline []Completed) Snapshot {
	out := Snapshot{Entries: append([]Entry(nil), remote.Entries...)}
	paths := map[string]bool{}
	for _, e := range out.Entries {
		paths[e.Path] = true
	}
	for _, c := range baseline {
		if paths[c.Path] {
			continue
		}
		entry := Entry{Path: c.Path}
		if c.Kind == "folder" {
			entry.Remote.MIMEType = drive.FolderMIME
		}
		out.Entries = append(out.Entries, entry)
	}
	return out
}
