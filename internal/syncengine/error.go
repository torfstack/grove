package syncengine

import "errors"

type privateFailure struct {
	message string
	cause   error
}

func (e privateFailure) Error() string { return e.message }
func (e privateFailure) Unwrap() error { return e.cause }
func failedPrivately(message string, causes ...error) error {
	cause := errors.Join(causes...)
	if cause == nil {
		return errors.New(message)
	}
	return privateFailure{message: message, cause: cause}
}
