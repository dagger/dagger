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

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui/multiprefixw"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/muesli/termenv"
	"github.com/vito/go-interact/interact"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type frontendDots struct {
	profile     termenv.Profile
	out         TermOutput
	mu          sync.Mutex
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
		out:     out,

		db:       db,
		reporter: reporter,

		prefixW:     multiprefixw.New(out),
		pendingLogs: make(map[dagui.SpanID][]sdklog.Record),
	}
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
		e.prefixW.Prefix = dotsPrefix
		// Print dot or X based on span status - dots style
		switch span.Status().Code {
		case codes.Error:
			fmt.Fprint(e.prefixW, e.out.String("X").Foreground(termenv.ANSIRed))
		case codes.Ok, codes.Unset:
			fmt.Fprint(e.prefixW, e.out.String(".").Foreground(termenv.ANSIGreen))
		}

		// When context-switching from dots, don't print an overhang indicator
		e.prefixW.LineOverhang = ""
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

func dotLogsPrefix(r *renderer, profile termenv.Profile, span *dagui.Span) string {
	var spanName strings.Builder
	out := NewOutput(&spanName, termenv.WithProfile(profile))
	fmt.Fprintf(out, "%s ", CaretDownFilled)
	if span.Call() != nil {
		r.renderCall(out, span, span.Call(), "", false, 0, false, nil)
	} else {
		fmt.Fprintf(&spanName, "%s", out.String(span.Name).Bold())
	}
	return spanName.String() + "\n"
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

	// Set prefix
	r := newRenderer(e.db, 0, e.opts)
	prefix := dotLogsPrefix(r, e.profile, dbSpan)

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
		if body == "" {
			continue
		}

		// Only set prefix + track finisher when we're actually gonna print
		e.prefixW.Prefix = prefix
		fmt.Fprint(e.prefixW, body)

		// When context-switching, print an overhang so it's clear when the logs
		// haven't line-terminated
		e.prefixW.LineOverhang =
			e.out.String(multiprefixw.DefaultLineOverhang).
				Foreground(termenv.ANSIBrightBlack).String()
	}
}

// flushPendingLogsForSpan flushes any pending logs when a span becomes available
func (e *dotsLogsExporter) flushPendingLogsForSpan(spanID dagui.SpanID) {
	if records, exists := e.pendingLogs[spanID]; exists {
		e.flushLogsForSpan(spanID, records)
		delete(e.pendingLogs, spanID)
	}
}
