//go:build !linux && !darwin

package syncengine

import (
	"errors"
	"os"
)

func exportPublicationSupported() bool { return false }

func publish(*os.Root, string, string) error {
	return errors.New("safe publication is supported on Linux and macOS only")
}

func linkExportNoReplace(*os.Root, string, *os.Root, string) (bool, error) {
	return false, errors.New("safe export publication is supported on Linux and macOS only")
}
