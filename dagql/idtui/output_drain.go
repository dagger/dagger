package idtui

import (
	"errors"
	"fmt"
)

// OutputDrainError means local command output could not be fully delivered.
// The frontend may still have an exporter blocked in I/O. Callers must skip
// final rendering and DB reads instead of waiting on that exporter's lock.
type OutputDrainError struct{ Err error }

func (e *OutputDrainError) Error() string { return fmt.Sprintf("drain command output: %v", e.Err) }
func (e *OutputDrainError) Unwrap() error { return e.Err }

func isOutputDrainError(err error) bool {
	var drainErr *OutputDrainError
	return errors.As(err, &drainErr)
}

func outputDrainFailure(err error) error {
	if !isOutputDrainError(err) {
		return nil
	}
	// The CLI reports this before shutting down its diagnostic processor.
	// Avoid synchronous error printing when stderr itself may be blocked.
	return ExitError{OriginalCode: 1, Original: err}
}
