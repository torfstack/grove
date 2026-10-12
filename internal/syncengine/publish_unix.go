//go:build linux || darwin

package syncengine

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func exportPublicationSupported() bool { return true }

func publish(root *os.Root, temp, target string) error {
	if err := root.Link(temp, target); err != nil {
		return errors.New("cannot publish file without replacing existing content")
	}
	if err := root.Remove(temp); err != nil {
		return errors.New("cannot remove published temporary file")
	}
	return nil
}

func linkExportNoReplace(sourceRoot *os.Root, source string, targetRoot *os.Root, target string) (bool, error) {
	return linkExportNoReplaceWith(sourceRoot, source, targetRoot, target, unix.Linkat, func(f *os.File) error { return f.Close() })
}

func linkExportNoReplaceWith(sourceRoot *os.Root, source string, targetRoot *os.Root, target string, linkat func(int, string, int, string, int) error, closeFile func(*os.File) error) (bool, error) {
	sourceDir, err := sourceRoot.Open(".")
	if err != nil {
		return false, err
	}
	targetDir, err := targetRoot.Open(".")
	if err != nil {
		return false, errors.Join(err, closeFile(sourceDir))
	}
	linkErr := linkat(int(sourceDir.Fd()), source, int(targetDir.Fd()), target, 0)
	published := linkErr == nil
	return published, errors.Join(linkErr, closeFile(sourceDir), closeFile(targetDir))
}
