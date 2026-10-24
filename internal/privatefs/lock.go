package privatefs

import "errors"

var ErrLocked = errors.New("resource is locked by another Grove command")
