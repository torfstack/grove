package syncengine

import "errors"

type RecoveryReason string
type RecoveryAction string
type RecoveryStageStatus string
type RecoveryOutcome string

type RecoveryStage struct{ Status RecoveryStageStatus }
type RecoveryReport struct {
	Pending         bool
	Operation       OperationKind
	Phase           string
	Reason          RecoveryReason
	Actions         []RecoveryAction
	Local, Cloud    RecoveryStage
	CompetitorCount int
	Outcome         RecoveryOutcome
}

type RecoveryBlockedError struct{ Reason RecoveryReason }

func (*RecoveryBlockedError) Error() string        { return "upload recovery remains blocked" }
func (*RecoveryBlockedError) Is(target error) bool { return target == errUploadPending }

type RecoveryError struct {
	Message string
	Cause   error
}

func (e *RecoveryError) Error() string { return e.Message }
func (e *RecoveryError) Unwrap() error { return e.Cause }
func safeRecoveryError(err error) error {
	if err == nil {
		return nil
	}
	var blocked *RecoveryBlockedError
	if errors.As(err, &blocked) {
		return blocked
	}
	return &RecoveryError{Message: "recovery operation failed", Cause: err}
}

func blockedRecovery(reason RecoveryReason) error { return &RecoveryBlockedError{Reason: reason} }
