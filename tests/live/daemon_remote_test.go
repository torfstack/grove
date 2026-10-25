//go:build linux

package live

import "testing"

func TestDaemonRemoteChangesLive(t *testing.T) { testDaemonVariant(t, false, false, true) }
