package daemon

import (
	"context"
	"io"
	"testing"

	"github.com/torfstack/grove/internal/syncengine"
)

func TestUnsupportedLocalWatchBeforeStartup(t *testing.T) {
	err := runWithConfig(context.Background(), Config{WatchLocal: true}, Dependencies{
		InspectProfile: func(context.Context, string) (syncengine.ProfileSummary, error) {
			t.Fatal("profile inspected before rejecting local watching")
			return syncengine.ProfileSummary{}, nil
		},
		AcquireLease: func(string) (io.Closer, error) {
			t.Fatal("lease acquired before rejecting local watching")
			return nil, nil
		},
	}, false)
	if err == nil || err.Error() != "invalid daemon configuration: watch_local is not supported on this platform; set it to false or omit it" {
		t.Fatalf("unsupported local watching error = %v", err)
	}
}
