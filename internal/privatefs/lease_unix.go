//go:build linux || darwin

package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func openLease(path string, create bool) (*os.File, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
	if create {
		flags = syscall.O_CREAT | syscall.O_RDWR | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
	}
	fd, err := syscall.Open(filepath.Join(parent, filepath.Base(path)), flags, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("lease requires a regular file")
	}
	return f, nil
}

func AcquireLease(path string) (*Lock, error) {
	f, err := openLease(path, true)
	if err != nil {
		return nil, errors.New("cannot open daemon lease")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, errors.New("cannot acquire daemon lease")
	}
	return &Lock{file: f}, nil
}

func ProbeLease(path string) (bool, error) {
	f, err := openLease(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("cannot inspect daemon lease")
	}
	lockErr := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB)
	if err := f.Close(); err != nil {
		return false, errors.New("cannot close daemon lease probe")
	}
	if lockErr == nil {
		return false, nil
	}
	if errors.Is(lockErr, syscall.EWOULDBLOCK) || errors.Is(lockErr, syscall.EAGAIN) {
		return true, nil
	}
	return false, errors.New("cannot inspect daemon lease")
}
