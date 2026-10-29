//go:build darwin

package tray

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
)

func OpenService(ctx context.Context, opts Options) (Controller, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validInstance(opts.ServiceInstance) {
		return nil, os.ErrInvalid
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, os.ErrPermission
	}
	uid := uint32(os.Geteuid())
	domain := "gui/" + strconv.FormatUint(uint64(uid), 10)
	label := "com.torfstack.groved." + opts.ServiceInstance
	plistPath := filepath.Join(home, "Library", "Application Support", "Grove", "LaunchAgents", label+".plist")
	loginPlistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	return newLaunchdController(opts, systemLaunchctl{}, domain, plistPath, loginPlistPath, uid)
}
