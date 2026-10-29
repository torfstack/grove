//go:build linux || darwin

package tray

import (
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/torfstack/grove/internal/privatefs"
)

func readPrivateLaunchAgent(path string, uid uint32) ([]byte, error) {
	if !filepath.IsAbs(path) || privatefs.OutsideGit(path) != nil || privatefs.NoSymlinks(path) != nil {
		return nil, os.ErrPermission
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, os.ErrPermission
	}
	file := os.NewFile(uintptr(fd), "launch-agent")
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&^0600 != 0 {
		_ = file.Close()
		return nil, os.ErrPermission
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uid {
		_ = file.Close()
		return nil, os.ErrPermission
	}
	data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	closeErr := file.Close()
	if err != nil || closeErr != nil || len(data) > 64*1024 {
		return nil, os.ErrPermission
	}
	return data, nil
}
