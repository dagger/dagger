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
	msg := fmt.Sprintf("exit code %d", e.Code)
	if e.Original != nil {
		// Be sure to include the original error in the message so that we can still
		// parse out error origins.
		msg += "\n\n" + e.Original.Error()
	}
	return msg
}

func (e ExitError) Unwrap() error {
	return e.Original
}
