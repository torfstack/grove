//go:build !linux

package daemon

import "context"

func openWatcher(context.Context, string, Clock) (Watcher, error) {
	return nil, failed("watch_unavailable", nil)
}
