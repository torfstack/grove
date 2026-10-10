//go:build !linux && !darwin

package syncengine

import (
	"errors"
	"os"
)

func moveNoReplace(*os.Root, string, string) error {
	return errors.New("safe moves are supported on Linux and macOS only")
}
