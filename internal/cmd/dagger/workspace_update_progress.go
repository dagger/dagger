package daggercmd

import (
	"context"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	"github.com/spf13/cobra"
)

// Keep regeneration under the command that caused it, including its output
// and errors. Passthrough lets revealed progress replace the API plumbing.
func withWorkspaceUpdateProgress(ctx context.Context, cmd *cobra.Command, name string, run func(context.Context) error) (rerr error) {
	ctx, view := Tracer().Start(ctx, "updates", telemetry.Passthrough())
	defer telemetry.EndWithCause(view, &rerr)
	Frontend.SetPrimary(dagui.SpanID{SpanID: view.SpanContext().SpanID()})
	// The primary span's own title is omitted by the final progress view.
	// Keep the initiating operation as a visible child of that view.
	ctx, span := Tracer().Start(ctx, name, telemetry.Reveal())
	defer telemetry.EndWithCause(span, &rerr)
	ctx = telemetry.ContextWithGlobalLogsSpan(ctx)
	slog.SetDefault(slog.SpanLogger(ctx, InstrumentationLibrary))
	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	cmd.SetOut(stdio.Stdout)
	cmd.SetErr(stdio.Stderr)
	defer func() {
		cmd.SetOut(out)
		cmd.SetErr(errOut)
	}()
	return run(ctx)
}
