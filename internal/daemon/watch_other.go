//go:build !linux

package daemon

import "context"

func localWatchSupported() bool { return false }

func openWatcher(context.Context, string, Clock) (Watcher, error) {
	return nil, failed("watch_unavailable", nil)
}
