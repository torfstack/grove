//go:build !linux && !darwin

package syncengine

import (
	"errors"
	"os"
)

func publish(*os.Root, string, string) error {
	return errors.New("safe publication is supported on Linux and macOS only")
}
