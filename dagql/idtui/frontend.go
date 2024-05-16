package idtui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/telemetry/sdklog"
)

type FrontendOpts struct {
	// Debug tells the frontend to show everything and do one big final render.
	Debug bool

	// Silent tells the frontend to not display progress at all.
	Silent bool

	// Verbosity is the level of detail to show in the TUI.
	Verbosity int
}

type Frontend interface {
	// Run starts a frontend, and runs the target function.
	Run(ctx context.Context, opts FrontendOpts, f func(context.Context) error) error

	// SetPrimary tells the frontend which span should be treated like the focal
	// point of the command. Its output will be displayed at the end, and its
	// children will be promoted to the "top-level" of the TUI.
	SetPrimary(spanID trace.SpanID)
	Background(cmd tea.ExecCommand) error

	// Can consume otel spans and logs.
	sdktrace.SpanExporter
	sdklog.LogExporter

	// DumpID is exposed for troubleshooting.
	DumpID(*termenv.Output, *call.ID) error
}
