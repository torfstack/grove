//go:build linux || darwin

package syncengine

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func publish(root *os.Root, temp, target string) error {
	if err := root.Link(temp, target); err != nil {
		return errors.New("cannot publish file without replacing existing content")
	}
	if err := root.Remove(temp); err != nil {
		return errors.New("cannot remove published temporary file")
	}
	return nil
}

func linkExportNoReplace(sourceRoot *os.Root, source string, targetRoot *os.Root, target string) (err error) {
	sourceDir, err := sourceRoot.Open(".")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, sourceDir.Close()) }()
	targetDir, err := targetRoot.Open(".")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, targetDir.Close()) }()
	return unix.Linkat(int(sourceDir.Fd()), source, int(targetDir.Fd()), target, 0)
}
