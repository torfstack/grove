//go:build darwin

package daemon

import (
	"os"
	"syscall"
)

func openConfig(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, configError("cannot open private regular file")
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&^0600 != 0 {
		_ = f.Close()
		return nil, configError("private regular file required")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		_ = f.Close()
		return nil, configError("current-user ownership required")
	}
	return f, nil
}
