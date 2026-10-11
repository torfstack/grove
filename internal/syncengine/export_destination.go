package syncengine

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/torfstack/grove/internal/privatefs"
)

type exportDestination struct {
	root           *os.Root
	parent, target string
	identity       FileIdentity
	lease          destinationLease
}

func (d *exportDestination) Close() error {
	var err error
	if d.root != nil {
		err = d.root.Close()
		d.root = nil
	}
	return errors.Join(err, d.lease.Close())
}

func acquireExportDestination(registry, profile, localDir, output string) (_ *exportDestination, err error) {
	if output == "" {
		return nil, errors.New("export output is required")
	}
	absolute, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	if !privatefs.SafeName(filepath.Base(absolute)) {
		return nil, errors.New("invalid export output")
	}
	if _, err = os.Lstat(absolute); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("export output must not exist")
	}
	parent, err := privatefs.Canonical(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	registry, err = privatefs.Canonical(registry)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() {
		return nil, errors.New("export parent must already exist")
	}
	d := &exportDestination{parent: parent, target: filepath.Base(absolute)}
	success := false
	defer func() {
		if !success {
			err = errors.Join(err, d.Close())
		}
	}()
	var ancestors []string
	for current := filepath.Dir(parent); ; current = filepath.Dir(current) {
		ancestors = append(ancestors, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		lock, err := privatefs.AcquireDirectory(ancestors[i], true)
		if err != nil {
			return nil, err
		}
		d.lease.locks = append(d.lease.locks, lock)
	}
	lock, err := privatefs.AcquireDirectory(parent, false)
	if err != nil {
		return nil, err
	}
	d.lease.locks = append(d.lease.locks, lock)
	guard, err := privatefs.Acquire(filepath.Join(registry, "registry.lock"))
	if err != nil {
		return nil, err
	}
	d.lease.locks = append(d.lease.locks, guard)
	record := registryRecord{Version: 1}
	path := filepath.Join(registry, "registry.json")
	if _, err = os.Lstat(path); err == nil {
		if err = privatefs.ReadJSON(path, &record); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if record.Version != 1 {
		return nil, errors.New("unsupported destination registry")
	}
	excluded := []string{profile, localDir, registry}
	for _, entry := range record.Entries {
		if !filepath.IsAbs(entry.Profile) || !filepath.IsAbs(entry.Destination) {
			return nil, errors.New("invalid destination registry")
		}
		excluded = append(excluded, entry.Profile, entry.Destination)
	}
	target := filepath.Join(parent, d.target)
	for _, path := range excluded {
		canonical, err := privatefs.Canonical(path)
		if err != nil {
			return nil, err
		}
		if physicalWithin(canonical, target) {
			return nil, errors.New("export overlaps private state or a sync destination")
		}
	}
	d.root, err = os.OpenRoot(parent)
	if err != nil {
		return nil, err
	}
	rootInfo, err := d.root.Stat(".")
	if err != nil {
		return nil, err
	}
	d.identity, err = fileIdentity(rootInfo)
	if err != nil {
		return nil, err
	}
	if err = d.checkParent(); err != nil {
		return nil, err
	}
	if _, err = d.root.Lstat(d.target); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("export output must not exist")
	}
	success = true
	return d, nil
}

func (d *exportDestination) checkParent() error {
	if err := privatefs.NoSymlinks(d.parent); err != nil {
		return err
	}
	info, err := os.Stat(d.parent)
	if err != nil {
		return errors.New("export parent changed")
	}
	id, err := fileIdentity(info)
	if err != nil || id != d.identity || !info.IsDir() {
		return errors.New("export parent changed")
	}
	return nil
}
