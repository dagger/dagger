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

	"dagger.io/dagger"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui/multiprefixw"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/muesli/termenv"
	"github.com/vito/go-interact/interact"
	"go.opentelemetry.io/otel/codes"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type frontendDots struct {
	profile  termenv.Profile
	out      TermOutput
	mu       sync.Mutex
	db       *dagui.DB
	opts     dagui.FrontendOpts
	reporter *frontendPretty
	logs     streamingLogExporter
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
	profile := ColorProfile()
	if output == nil {
		output = os.Stderr
	}
	out := NewOutput(output, termenv.WithProfile(profile))

	db := dagui.NewDB()
	reporter := NewWithDB(output, db)
	reporter.reportOnly = true

	fe := &frontendDots{
		profile:  profile,
		out:      out,
		db:       db,
		reporter: reporter,
	}
	fe.logs = streamingLogExporter{
		db:          db,
		opts:        &fe.opts,
		profile:     profile,
		out:         out,
		prefixW:     multiprefixw.New(out),
		pendingLogs: make(map[dagui.SpanID][]sdklog.Record),
	}
	return fe
}

func (fe *frontendDots) SetClient(client *dagger.Client) {}

func (fe *frontendDots) SetSidebarContent(SidebarSection) {}

func (fe *frontendDots) Run(ctx context.Context, opts dagui.FrontendOpts, f func(context.Context) (cleanups.CleanupF, error)) error {
	fe.opts = opts
	return fe.reporter.Run(ctx, opts, func(ctx context.Context) (cleanups.CleanupF, error) {
		cleanup, err := f(ctx)
		fmt.Fprintln(fe.out)
		fmt.Fprintln(fe.out)
		return cleanup, err
	})
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
	return fmt.Errorf("running shell without the TUI is not supported")
}

func (fe *frontendDots) RevealAllSpans() {
	fe.mu.Lock()
	fe.opts.ZoomedSpan = dagui.SpanID{}
	fe.reporter.RevealAllSpans()
	fe.mu.Unlock()
}

func (fe *frontendDots) SpanExporter() sdktrace.SpanExporter {
	return &dotsSpanExporter{fe}
}

func (fe *frontendDots) LogExporter() sdklog.Exporter {
	return &dotsLogsExporter{
		streamingLogExporter: &fe.logs,
		mu:                   &fe.mu,
	}
}

func (fe *frontendDots) MetricExporter() sdkmetric.Exporter {
	return &dotsMetricExporter{fe: fe}
}

func (fe *frontendDots) SetCloudURL(ctx context.Context, url string, msg string, logged bool) {
	fe.reporter.SetCloudURL(ctx, url, msg, logged)
}

func (fe *frontendDots) Shell(ctx context.Context, handler ShellHandler) {
	// Dots frontend doesn't support shell
}

func (fe *frontendDots) HandlePrompt(ctx context.Context, _, prompt string, dest any) error {
	return interact.NewInteraction(prompt).Resolve(dest)
}

func (fe *frontendDots) HandleForm(ctx context.Context, form *huh.Form) error {
	return form.RunWithContext(ctx)
}

// dotsSpanExporter implements trace.SpanExporter for the dots frontend
type dotsSpanExporter struct {
	*frontendDots
}

func (e *dotsSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Export to DB first like other frontends
	if err := e.db.ExportSpans(ctx, spans); err != nil {
		return err
	}

	dotsPrefix := e.out.String(CaretRightFilled).Foreground(termenv.ANSIBrightBlack).String() + " "

	for _, span := range spans {
		id := dagui.SpanID{SpanID: span.SpanContext().SpanID()}
		dbSpan := e.db.Spans.Map[id]
		if dbSpan == nil {
			continue // Span not found in DB?
		}

		// Check if there are pending logs for this span and flush them
		e.logs.flushPendingLogsForSpan(id)

		// Only print output when span ends
		if span.EndTime().IsZero() {
			continue // Span hasn't ended yet
		}

		var skip bool
		for p := range dbSpan.Parents {
			if p.Encapsulate || !e.opts.ShouldShow(e.db, p) {
				skip = true
				break
			}
		}

		if skip || (dbSpan.Encapsulated && !dbSpan.IsFailedOrCausedFailure()) {
			// Don't print encapsulated spans
			continue
		}

		// Give the symbols their own neutral prefix, so they get separated nicely from logs.
		e.logs.prefixW.Prefix = dotsPrefix
		// Print dot or X based on span status - dots style
		switch span.Status().Code {
		case codes.Error:
			fmt.Fprint(e.logs.prefixW, e.out.String("X").Foreground(termenv.ANSIRed))
		case codes.Ok, codes.Unset:
			fmt.Fprint(e.logs.prefixW, e.out.String(".").Foreground(termenv.ANSIGreen))
		}

		// When context-switching from dots, don't print an overhang indicator
		e.logs.prefixW.LineOverhang = ""
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

// dotsLogsExporter implements log.Exporter for the dots frontend
type dotsLogsExporter struct {
	*streamingLogExporter
	mu *sync.Mutex
}

func (e *dotsLogsExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.streamingLogExporter.Export(ctx, records)
}

func (e *dotsLogsExporter) ForceFlush(ctx context.Context) error {
	return nil
}

func (e *dotsLogsExporter) Shutdown(ctx context.Context) error {
	return nil
}
