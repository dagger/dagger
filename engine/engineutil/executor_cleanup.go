package engineutil

import (
	"errors"
	"fmt"
)

// ExecCleanupError distinguishes executor cleanup failure from a process exit.
// In particular, stopping a service may expect a signal exit but must still
// report a failure to remove the container or release its resources.
type ExecCleanupError struct{ Err error }

func (e *ExecCleanupError) Error() string { return fmt.Sprintf("executor cleanup: %v", e.Err) }
func (e *ExecCleanupError) Unwrap() error { return e.Err }

// ExecCleanupErrors extracts every cleanup failure, including joined failures.
// Process exit errors elsewhere in the tree are deliberately excluded.
func ExecCleanupErrors(err error) error {
	switch e := err.(type) {
	case *ExecCleanupError:
		return e
	case interface{ Unwrap() []error }:
		var cleanupErr error
		for _, child := range e.Unwrap() {
			cleanupErr = errors.Join(cleanupErr, ExecCleanupErrors(child))
		}
		return cleanupErr
	case interface{ Unwrap() error }:
		return ExecCleanupErrors(e.Unwrap())
	default:
		return nil
	}
}
