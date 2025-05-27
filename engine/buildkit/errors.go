package buildkit

import (
	"go.opentelemetry.io/otel/trace"
)

// ExecError is an error that occurred while executing an `Op_Exec`.
type ExecError struct {
	original error
	Origin   trace.SpanID
	Cmd      []string
	ExitCode int
	Stdout   string
	Stderr   string
}

func (e *ExecError) Error() string {
	return e.original.Error()
}

func (e *ExecError) Unwrap() error {
	return e.original
}

func (e *ExecError) Extensions() map[string]any {
	return map[string]any{
		"_type":    "EXEC_ERROR",
		"_origin":  e.Origin.String(),
		"cmd":      e.Cmd,
		"exitCode": e.ExitCode,
		"stdout":   e.Stdout,
		"stderr":   e.Stderr,
	}
}
