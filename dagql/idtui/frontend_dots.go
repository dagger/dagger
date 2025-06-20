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
	"time"

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

type frontendDots struct {
	profile     termenv.Profile
	output      TermOutput
	mu          sync.Mutex
	db          *dagui.DB
	opts        dagui.FrontendOpts
	reporter    *frontendPretty
	prefixW     *multiprefixw.Writer
	pendingLogs map[dagui.SpanID][]sdklog.Record
	finishers   map[dagui.SpanID]*dagui.Span
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

	prefixW := multiprefixw.New(out)
	prefixW.LineOverhang = ""

	return &frontendDots{
		profile: profile,
		output:  out,

		db:       db,
		reporter: reporter,

		prefixW:     prefixW,
		pendingLogs: make(map[dagui.SpanID][]sdklog.Record),
		finishers:   make(map[dagui.SpanID]*dagui.Span),
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

func (e *dotsSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Export to DB first like other frontends
	if err := e.db.ExportSpans(ctx, spans); err != nil {
		return err
	}

	// Group spans by their top-level parent
	topLevelGroups := make(map[*dagui.Span][]sdktrace.ReadOnlySpan)
	finishedTopLevel := make(map[dagui.SpanID]bool)
	rowsView := e.db.RowsView(dagui.FrontendOpts{
		ZoomedSpan: e.db.PrimarySpan,
		// We're only looking for the topmost spans, so show completed spans but
		// don't expand them.
		Verbosity: dagui.ShowCompletedVerbosity,
	})
	for _, toplevel := range rowsView.Body {
		for _, span := range spans {
			id := dagui.SpanID{SpanID: span.SpanContext().SpanID()}
			dbSpan := e.db.Spans.Map[id]
			if dbSpan == nil {
				continue // Span not found in DB?
			}
			if dbSpan.HasParent(toplevel.Span) {
				topLevelGroups[toplevel.Span] = append(topLevelGroups[toplevel.Span], span)
			}
			if dbSpan == toplevel.Span && !dbSpan.IsRunningOrEffectsRunning() {
				finishedTopLevel[dbSpan.ID] = true
			}
		}
	}

	// This obnoxious amount of code simply prints "DONE" for anything that had a
	// prefix printed for it, either because it's a top-level span or a span that
	// we showed logs for previously
	e.flushFinishers()

	// Process each group
	for topLevel, groupSpans := range topLevelGroups {
		e.processSpanGroup(topLevel, groupSpans)
		e.flushFinishers()
	}

	return nil
}

func (fe *frontendDots) flushFinishers() {
	r := newRenderer(fe.db, 0, fe.opts)
	done := fe.output.String("DONE").Foreground(termenv.ANSIGreen)
	for id, span := range fe.finishers {
		if !span.IsDone() {
			// fmt.Fprintln(fe.prefixW, "NOT DONE:", span.Name)
			continue
		}
		logsPrefix := dotLogsPrefix(r, fe.profile, span)
		spansPrefix := dotSpansPrefix(r, fe.profile, span)
		duration := dagui.FormatDuration(span.Activity.Duration(time.Now()))
		switch fe.prefixW.Prefix {
		case logsPrefix:
			fmt.Fprintf(fe.prefixW, "\n%s [%s]\n", done, duration)
			delete(fe.finishers, id)
		case spansPrefix:
			fmt.Fprintf(fe.prefixW, " %s [%s]\n", done, duration)
			// default:
			// 	e.prefixW.Prefix = spansPrefix
			// 	fmt.Fprintf(e.prefixW, " %s [%s]\n", done, duration)
			delete(fe.finishers, id)
		default:
			// fmt.Fprintf(fe.prefixW, " %s: %q != (%q || %q) [%s]\n", done, fe.prefixW.Prefix, logsPrefix, spansPrefix, duration)

		}
	}
}

// processSpanGroup processes a group of spans that belong to the same top-level parent
func (e *dotsSpanExporter) processSpanGroup(toplevel *dagui.Span, spans []sdktrace.ReadOnlySpan) {
	r := newRenderer(e.db, 0, e.opts)
	dotsPrefix := dotSpansPrefix(r, e.profile, toplevel)
	for _, span := range spans {
		id := dagui.SpanID{SpanID: span.SpanContext().SpanID()}
		dbSpan := e.db.Spans.Map[id]
		if dbSpan == nil {
			continue
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
		e.finishers[toplevel.ID] = toplevel

		// Print dot or X based on span status - dots style
		switch span.Status().Code {
		case codes.Error:
			fmt.Fprint(e.prefixW, e.output.String("X").Foreground(termenv.ANSIRed))
		case codes.Ok, codes.Unset:
			fmt.Fprint(e.prefixW, e.output.String(".").Foreground(termenv.ANSIGreen))
		}
	}
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

func dotSpansPrefix(r *renderer, profile termenv.Profile, span *dagui.Span) string {
	var spanName strings.Builder
	out := NewOutput(&spanName, termenv.WithProfile(profile))
	fmt.Fprintf(out, "%s ", CaretRightFilled)
	if span.Call() != nil {
		r.renderCall(out, span, span.Call(), "", false, 0, false, nil)
	} else {
		fmt.Fprintf(&spanName, "%s", out.String(span.Name).Bold())
	}
	return spanName.String() + " "
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
		e.finishers[spanID] = dbSpan

		fmt.Fprint(e.prefixW, body)
	}
}

// flushPendingLogsForSpan flushes any pending logs when a span becomes available
func (e *dotsLogsExporter) flushPendingLogsForSpan(spanID dagui.SpanID) {
	if records, exists := e.pendingLogs[spanID]; exists {
		e.flushLogsForSpan(spanID, records)
		delete(e.pendingLogs, spanID)
	}
}
