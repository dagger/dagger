// Package idtui provides terminal user interface frontends for Dagger operations.
// This file contains the dots-style frontend that provides minimal, colorized
// output with green dots for successful operations and red X's for failures.
package idtui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"dagger.io/dagger/telemetry"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/core/multiprefixw"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
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
	profile     termenv.Profile
	output      TermOutput
	mu          sync.Mutex
	verbosity   int
	primary     bool
	db          *dagui.DB
	opts        dagui.FrontendOpts
	reporter    *frontendPretty
	prefixW     *multiprefixw.Writer
	pendingLogs map[dagui.SpanID][]sdklog.Record
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

	return &frontendDots{
		profile: profile,
		output:  out,

		db:       db,
		reporter: reporter,

		prefixW:     multiprefixw.New(out),
		pendingLogs: make(map[dagui.SpanID][]sdklog.Record),
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
	return &dotsSpanExporter{fe}
}

func (fe *frontendDots) LogExporter() sdklog.Exporter {
	return &dotsLogsExporter{fe}
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
	*frontendDots
}

var dotsPrefix = termenv.String(CaretRightFilled).Foreground(termenv.ANSIBrightBlack).String() + " "

func (e *dotsSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Export to DB first like other frontends
	if err := e.db.ExportSpans(ctx, spans); err != nil {
		return err
	}

	for _, span := range spans {
		id := dagui.SpanID{SpanID: span.SpanContext().SpanID()}
		dbSpan := e.db.Spans.Map[id]
		if dbSpan == nil {
			continue // Span not found in DB?
		}

		// Check if there are pending logs for this span and flush them
		if logExporter, ok := e.LogExporter().(*dotsLogsExporter); ok {
			logExporter.flushPendingLogsForSpan(id)
		}

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
		e.prefixW.SetPrefix(dotsPrefix)

		// Print dot or X based on span status - dots style
		switch span.Status().Code {
		case codes.Error:
			fmt.Fprint(e.prefixW, termenv.String("X").Foreground(termenv.ANSIRed))
		case codes.Ok, codes.Unset:
			fmt.Fprint(e.prefixW, termenv.String(".").Foreground(termenv.ANSIGreen))
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

// dotsLogsExporter implements log.Exporter for the dots frontend
type dotsLogsExporter struct {
	*frontendDots
}

func (e *dotsLogsExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Export to DB first like other frontends
	if err := e.db.LogExporter().Export(ctx, records); err != nil {
		return err
	}

	if len(records) == 0 {
		return nil
	}

	// Group records by span and either flush immediately if span exists, or store for later
	spanGroups := make(map[dagui.SpanID][]sdklog.Record)
	for _, record := range records {
		spanID := dagui.SpanID{SpanID: record.SpanID()}
		spanGroups[spanID] = append(spanGroups[spanID], record)
	}

	for spanID, records := range spanGroups {
		// Check if span exists in DB
		dbSpan := e.db.Spans.Map[spanID]
		if dbSpan != nil && dbSpan.Name != "" {
			// Span exists, flush immediately
			e.flushLogsForSpan(spanID, records)
		} else {
			// Span doesn't exist yet, store for later
			e.pendingLogs[spanID] = append(e.pendingLogs[spanID], records...)
		}
	}

	return nil
}

func (e *dotsLogsExporter) ForceFlush(ctx context.Context) error {
	return nil
}

func (e *dotsLogsExporter) Shutdown(ctx context.Context) error {
	return nil
}

// flushLogsForSpan writes logs for a specific span with proper prefix
func (e *dotsLogsExporter) flushLogsForSpan(spanID dagui.SpanID, records []sdklog.Record) {
	// Get span info from DB
	dbSpan := e.db.Spans.Map[spanID]
	if dbSpan == nil {
		return
	}

	// Check if we should show this span
	var skip bool
	for p := range dbSpan.Parents {
		if p.Encapsulate || !e.opts.ShouldShow(e.db, p) {
			skip = true
			break
		}
	}
	if dbSpan.ID == e.db.PrimarySpan {
		// don't print primary span logs; they'll be printed at the end
		skip = true
	}

	if skip || (dbSpan.Encapsulated && !dbSpan.IsFailedOrCausedFailure()) {
		return // Skip logs for encapsulated spans
	}

	// Initialize writer if needed

	r := newRenderer(e.db, 0, e.opts)

	// Set prefix
	var spanName strings.Builder
	out := NewOutput(&spanName, termenv.WithProfile(e.profile))
	fmt.Fprintf(out, "%s ", CaretDownFilled)
	if dbSpan.Call() != nil {
		r.renderCall(out, dbSpan, dbSpan.Call(), "", false, 0, false, nil)
		fmt.Fprintln(out)
	} else {
		fmt.Fprintf(&spanName, "%s\n", termenv.String(dbSpan.Name).Bold())
	}
	e.prefixW.SetPrefix(spanName.String())

	// Write all logs for this span, filtering out verbose logs
	for _, record := range records {
		// Check if this log is marked as verbose
		isVerbose := false
		record.WalkAttributes(func(kv log.KeyValue) bool {
			if kv.Key == telemetry.LogsVerboseAttr && kv.Value.AsBool() {
				isVerbose = true
				return false // stop walking
			}
			return true // continue walking
		})

		// Skip verbose logs in the dots frontend
		if isVerbose {
			continue
		}

		body := record.Body().AsString()
		if body != "" {
			fmt.Fprint(e.prefixW, body)
		}
	}
}

// flushPendingLogsForSpan flushes any pending logs when a span becomes available
func (e *dotsLogsExporter) flushPendingLogsForSpan(spanID dagui.SpanID) {
	if records, exists := e.pendingLogs[spanID]; exists {
		e.flushLogsForSpan(spanID, records)
		delete(e.pendingLogs, spanID)
	}
}
