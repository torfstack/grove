package privatefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func Canonical(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("cannot resolve path")
	}
	current := absolute
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", errors.New("cannot resolve path ancestors")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("cannot resolve path root")
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
func regular(path string) error {
	s, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !s.Mode().IsRegular() {
		return errors.New("state path must be a regular file")
	}
	return nil
}
func WriteJSON(path string, value any) error { return writeJSON(path, value, os.Rename) }
func writeJSON(path string, value any, rename func(string, string) error) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return errors.New("cannot encode private state")
	}
	if err := regular(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("cannot create private directory")
	}
	f, err := os.CreateTemp(dir, ".grove-state-*")
	if err != nil {
		return errors.New("cannot create private state")
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return errors.New("cannot write private state")
	}
	if err = f.Sync(); err != nil {
		return errors.New("cannot sync private state")
	}
	if err = f.Close(); err != nil {
		return errors.New("cannot close private state")
	}
	if err = regular(path); err != nil {
		return err
	}
	if err = rename(f.Name(), path); err != nil {
		return errors.New("cannot replace private state")
	}
	d, err := os.Open(dir)
	if err != nil {
		return errors.New("cannot open private directory")
	}
	defer func() { _ = d.Close() }()
	if err = d.Sync(); err != nil {
		return errors.New("cannot sync private directory")
	}
	return nil
}
func ReadJSON(path string, value any) error {
	if err := regular(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("cannot read private state")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return errors.New("invalid private state")
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return errors.New("invalid trailing private state")
	}
	return nil
}
