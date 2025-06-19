// Package idtui provides terminal user interface frontends for Dagger operations.
// This file contains the dots-style frontend that provides minimal, colorized
// output with green dots for successful operations and red X's for failures.
package idtui

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/dagui"
	"go.opentelemetry.io/otel/codes"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ANSI color codes for output
const (
	colorGreen = "\033[32m"
	colorRed   = "\033[31m"
	colorReset = "\033[0m"
)

type frontendDots struct {
	output    io.Writer
	mu        sync.Mutex
	verbosity int
	primary   bool
	db        *dagui.DB
	opts      dagui.FrontendOpts
	reporter  *frontendPretty
}

// NewDots creates a new dots-style frontend that outputs green dots for
// successful spans and red X's for failed spans. This frontend is ideal for
// console/non-TTY environments where you want minimal, clear feedback about
// operation progress without verbose output.
//
// Output format:
//   - Green dots (.) for successful operations
//   - Red X's (X) for failed operations
//   - Newline printed at the end when shutting down
//
// This frontend does not support interactive features like shell or prompts.
func NewDots(output io.Writer) Frontend {
	if output == nil {
		output = os.Stderr
	}

	db := dagui.NewDB()
	reporter := NewWithDB(output, db)
	reporter.reportOnly = true

	return &frontendDots{
		output:   output,
		db:       db,
		reporter: reporter,
	}
}

func (fe *frontendDots) Run(ctx context.Context, opts dagui.FrontendOpts, f func(context.Context) error) error {
	fe.opts = opts
	err := f(ctx)
	fmt.Fprintln(fe.output)
	fmt.Fprintln(fe.output)
	fe.reporter.FrontendOpts = fe.opts
	if renderErr := fe.reporter.FinalRender(os.Stderr); renderErr != nil {
		return renderErr
	}
	return err
}

func (fe *frontendDots) Opts() *dagui.FrontendOpts {
	return &fe.opts
}

func (fe *frontendDots) SetVerbosity(verbosity int) {
	fe.mu.Lock()
	fe.opts.Verbosity = verbosity
	fe.reporter.SetVerbosity(verbosity)
	fe.mu.Unlock()
}

func (fe *frontendDots) SetPrimary(spanID dagui.SpanID) {
	fe.mu.Lock()
	fe.db.SetPrimarySpan(spanID)
	fe.opts.ZoomedSpan = spanID
	fe.opts.FocusedSpan = spanID
	fe.reporter.SetPrimary(spanID)
	fe.mu.Unlock()
}

func (fe *frontendDots) Background(cmd tea.ExecCommand, raw bool) error {
	return fmt.Errorf("not implemented")
}

func (fe *frontendDots) RevealAllSpans() {
	fe.mu.Lock()
	fe.opts.ZoomedSpan = dagui.SpanID{}
	fe.reporter.RevealAllSpans()
	fe.mu.Unlock()
}

func (fe *frontendDots) SpanExporter() sdktrace.SpanExporter {
	return &dotsSpanExporter{fe: fe}
}

func (fe *frontendDots) LogExporter() sdklog.Exporter {
	return fe.db.LogExporter()
}

func (fe *frontendDots) MetricExporter() sdkmetric.Exporter {
	return &dotsMetricExporter{fe: fe}
}

func (fe *frontendDots) ConnectedToEngine(ctx context.Context, name string, version string, clientID string) {
	fe.reporter.ConnectedToEngine(ctx, name, version, clientID)
}

func (fe *frontendDots) SetCloudURL(ctx context.Context, url string, msg string, logged bool) {
	fe.reporter.SetCloudURL(ctx, url, msg, logged)
}

func (fe *frontendDots) Shell(ctx context.Context, handler ShellHandler) {
	// Dots frontend doesn't support shell
}

func (fe *frontendDots) HandlePrompt(ctx context.Context, prompt string, dest any) error {
	return fmt.Errorf("prompts not supported in dots frontend")
}

// dotsSpanExporter implements trace.SpanExporter for the dots frontend
type dotsSpanExporter struct {
	fe *frontendDots
}

func (e *dotsSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.fe.mu.Lock()
	defer e.fe.mu.Unlock()

	// Export to DB first like other frontends
	if err := e.fe.db.ExportSpans(ctx, spans); err != nil {
		return err
	}

	for _, span := range spans {
		id := dagui.SpanID{SpanID: span.SpanContext().SpanID()}
		dbSpan := e.fe.db.Spans.Map[id]
		if dbSpan == nil {
			continue // Span not found in DB?
		}

		// Only print output when span ends
		if span.EndTime().IsZero() {
			continue // Span hasn't ended yet
		}

		var skip bool
		for p := range dbSpan.Parents {
			if p.Encapsulate || !e.fe.opts.ShouldShow(e.fe.db, p) {
				skip = true
				break
			}
		}

		if skip || (dbSpan.Encapsulated && !dbSpan.IsFailedOrCausedFailure()) {
			// Don't print encapsulated spans
			continue
		}

		// Print dot or X based on span status - dots style
		switch span.Status().Code {
		case codes.Error:
			// Red X for failures
			if e.fe.output != nil {
				fmt.Fprintf(e.fe.output, "%sX%s", colorRed, colorReset)
			}
		case codes.Ok, codes.Unset:
			// Green dot for success (treating unset as success)
			if e.fe.output != nil {
				fmt.Fprintf(e.fe.output, "%s.%s", colorGreen, colorReset)
			}
		}
	}

	return nil
}

func (e *dotsSpanExporter) Shutdown(ctx context.Context) error {
	return nil
}

func (e *dotsSpanExporter) ForceFlush(ctx context.Context) error {
	return nil
}

// dotsMetricExporter implements metric.Exporter for the dots frontend
type dotsMetricExporter struct {
	fe *frontendDots
}

func (e *dotsMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	// Dots style doesn't show metrics
	return nil
}

func (e *dotsMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (e *dotsMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (e *dotsMetricExporter) ForceFlush(ctx context.Context) error {
	return nil
}

func (e *dotsMetricExporter) Shutdown(ctx context.Context) error {
	return nil
}
