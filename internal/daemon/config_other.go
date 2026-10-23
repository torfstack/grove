//go:build !linux

package daemon

import "os"

func openConfig(string) (*os.File, error) {
	return nil, configError("daemon runtime requires Linux")
}
