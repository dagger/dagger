package idtui

import "fmt"

// ExitError is an error that indicates a command should exit with a specific
// status code, without printing an error message, assuming a human readable
// message has been printed already.
//
// It is basically a shortcut for `os.Exit` while giving the TUI a chance to
// exit gracefully and flush output.
type ExitError struct {
	Code int

	// An optional originating error, for any code paths that go looking for it,
	// e.g. telemetry.EndWithCause which looks for and applies error origins.
	Original error
}

var Fail = ExitError{Code: 1}

func (e ExitError) Error() string {
	// Not actually printed anywhere.
	return fmt.Sprintf("exit code %d", e.Code)
}

func (e ExitError) Unwrap() error {
	return e.Original
}
