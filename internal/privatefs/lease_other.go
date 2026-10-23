//go:build !linux && !darwin

package privatefs

import "errors"

func AcquireLease(string) (*Lock, error) {
	return nil, errors.New("daemon leases are unsupported on this platform")
}

func ProbeLease(string) (bool, error) {
	return false, errors.New("daemon leases are unsupported on this platform")
}
