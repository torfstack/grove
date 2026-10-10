//go:build !linux && !darwin

package privatefs

import "errors"

type Lock struct{}

func Acquire(string) (*Lock, error) {
	return nil, errors.New("locking is supported on Linux and macOS only")
}
func (*Lock) Close() error { return nil }

func AcquireDirectory(string, bool) (*Lock, error) {
	return nil, errors.New("directory leases are supported on Linux and macOS only")
}
