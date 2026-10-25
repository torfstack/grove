//go:build linux

package live

import "testing"

func TestDaemonLocalWatchUploadLive(t *testing.T) { testDaemonVariant(t, true, true, false) }
