//go:build linux || darwin

package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

type Lock struct{ file *os.File }

func Acquire(path string) (*Lock, error) {
	path, err := Canonical(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, errors.New("cannot create lock directory")
	}
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errors.New("cannot open lock")
	}
	f := os.NewFile(uintptr(fd), path)
	s, err := f.Stat()
	if err != nil || !s.Mode().IsRegular() {
		_ = f.Close()
		return nil, errors.New("invalid lock file")
	}
	if err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("resource is locked by another Grove command")
	}
	return &Lock{file: f}, nil
}
func (l *Lock) Close() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	if err != nil {
		return errors.New("cannot release lock")
	}
	return nil
}
