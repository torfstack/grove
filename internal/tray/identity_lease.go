package tray

import (
	"errors"
	"path/filepath"

	"github.com/torfstack/grove/internal/privatefs"
)

func acquireTrayLease(profile string) (*privatefs.Lock, error) {
	if !filepath.IsAbs(profile) {
		return nil, errors.New("invalid tray profile")
	}
	canonical, err := privatefs.Canonical(profile)
	if err != nil || privatefs.OutsideGit(canonical) != nil {
		return nil, errors.New("invalid tray profile")
	}
	return privatefs.AcquireLease(filepath.Join(canonical, "tray.lock"))
}
