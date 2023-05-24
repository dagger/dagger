package dagger

import (
	"errors"
	"fmt"

	"github.com/vektah/gqlparser/v2/gqlerror"
)

const (
	errorHelpBlurb = "Please visit https://dagger.io/help#go for troubleshooting guidance."
)

func withErrorHelp(err error) error {
	err = parseGraphQLError(err)
	return fmt.Errorf("%w\n%s", err, errorHelpBlurb)
}

func parseGraphQLError(err error) error {
	var gqlErr *gqlerror.Error

	if !errors.As(err, &gqlErr) {
		return err
	}

	ext := gqlErr.Extensions

	typ, ok := ext["_type"].(string)
	if !ok {
		return err
	}

	if typ == "EXEC_ERROR" {
		e := &ExecError{
			original: err,
		}
		if code, ok := ext["exitCode"].(float64); ok {
			e.ExitCode = int(code)
		}
		if args, ok := ext["cmd"].([]interface{}); ok {
			cmd := make([]string, len(args))
			for i, v := range args {
				cmd[i] = v.(string)
			}
			e.Cmd = cmd
		}
		if stdout, ok := ext["stdout"].(string); ok {
			e.Stdout = stdout
		}
		if stderr, ok := ext["stderr"].(string); ok {
			e.Stderr = stderr
		}
		return e
	}

	return err
}

// ExecError is an API error from an exec operation.
type ExecError struct {
	original error
	Cmd      []string
	ExitCode int
	Stdout   string
	Stderr   string
}

func (e *ExecError) Error() string {
	// As a default when just printing the error, include the stdout
	// and stderr for visibility
	return fmt.Sprintf("%s\nStdout:\n%s\nStderr:\n%s", e.Message(), e.Stdout, e.Stderr)
}

func (e *ExecError) Message() string {
	return e.original.Error()
}

func (e *ExecError) Unwrap() error {
	return e.original
}
