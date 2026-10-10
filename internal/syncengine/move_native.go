//go:build linux || darwin

package syncengine

import (
	"errors"
	"os"
	"path/filepath"
)

func moveNoReplace(root *os.Root, source, target string) error {
	for _, p := range []string{source, target} {
		if err := rootPath(root, p); err != nil {
			return err
		}
	}
	from, err := root.Open(filepath.Dir(source))
	if err != nil {
		return errors.New("cannot open move source parent")
	}
	defer func() { _ = from.Close() }()
	to, err := root.Open(filepath.Dir(target))
	if err != nil {
		return errors.New("cannot open move target parent")
	}
	defer func() { _ = to.Close() }()
	if err = nativeRename(int(from.Fd()), filepath.Base(source), int(to.Fd()), filepath.Base(target)); err != nil {
		return errors.New("cannot move without replacing existing content")
	}
	return nil
}
