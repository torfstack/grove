//go:build linux || darwin

package tray

import (
	"errors"
	"testing"

	"github.com/torfstack/grove/internal/privatefs"
)

func TestTrayLeaseIsProfileScopedAcrossHomeChanges(t *testing.T) {
	profile := optionsFixture(t).ProfileDir
	t.Setenv("HOME", t.TempDir())
	first, err := acquireTrayLease(profile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := first.Close(); err != nil {
			t.Error(err)
		}
	}()
	t.Setenv("HOME", t.TempDir())
	second, err := acquireTrayLease(profile)
	if second != nil {
		_ = second.Close()
	}
	if !errors.Is(err, privatefs.ErrLocked) {
		t.Fatalf("duplicate lease: %v", err)
	}
}
