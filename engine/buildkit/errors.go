package buildkit

import (
	"context"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// ExecError is an error that occurred while executing an `Op_Exec`.
type ExecError struct {
	original error
	Origin   trace.SpanContext
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
	ext := map[string]any{
		"_type":    "EXEC_ERROR",
		"cmd":      e.Cmd,
		"exitCode": e.ExitCode,
		"stdout":   e.Stdout,
		"stderr":   e.Stderr,
	}
	ctx := trace.ContextWithSpanContext(context.Background(), e.Origin)
	telemetry.Propagator.Inject(ctx, telemetry.AnyMapCarrier(ext))
	return ext
}
