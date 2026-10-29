//go:build !linux && !darwin

package tray

import "os"

func readPrivateLaunchAgent(string, uint32) ([]byte, error) { return nil, os.ErrPermission }
