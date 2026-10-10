package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

func SafeName(name string) bool {
	return name != "" && name != "." && name != ".." && utf8.ValidString(name) && !strings.ContainsAny(name, "/\\\x00")
}
func NoSymlinks(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return errors.New("cannot resolve filesystem path")
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(absolute, current), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		s, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return errors.New("cannot inspect filesystem path")
		}
		if s.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink paths are unsupported")
		}
	}
	return nil
}

func OutsideGit(path string) error {
	path, err := Canonical(path)
	if err != nil {
		return err
	}
	for {
		if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
			return errors.New("private runtime data must be outside Git")
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}
