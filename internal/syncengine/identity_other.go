//go:build !linux && !darwin

package syncengine

import (
	"errors"
	"os"
)

func fileIdentity(os.FileInfo) (FileIdentity, error) {
	return FileIdentity{}, errors.New("persistent file ownership is supported on Linux and macOS only")
}
