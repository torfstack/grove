//go:build linux || darwin

package syncengine

import (
	"errors"
	"os"
)

func publish(root *os.Root, temp, target string) error {
	if err := root.Link(temp, target); err != nil {
		return errors.New("cannot publish file without replacing existing content")
	}
	if err := root.Remove(temp); err != nil {
		return errors.New("cannot remove published temporary file")
	}
	return nil
}
