package idtui

import (
	"context"
	"errors"
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
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type frontendLogs struct {
	profile termenv.Profile
	out     TermOutput
	mu      sync.Mutex
	db      *dagui.DB
	opts    dagui.FrontendOpts
	logs    streamingLogExporter
}

// NewLogs creates a new logs-style frontend that only prints logs from spans,
// as they arrive, prefixed by span name.
//
// This frontend does not support interactive features like shell or prompts.
func NewLogs(output io.Writer) Frontend {
	profile := ColorProfile()
	if output == nil {
		output = os.Stderr
	}
	out := NewOutput(output, termenv.WithProfile(profile))

	db := dagui.NewDB()

	fe := &frontendLogs{
		profile: profile,
		out:     out,
		db:      db,
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

func (fe *frontendLogs) SetClient(client *dagger.Client) {}

func (fe *frontendLogs) SetSidebarContent(SidebarSection) {}

func (fe *frontendLogs) Run(ctx context.Context, opts dagui.FrontendOpts, f func(context.Context) (cleanups.CleanupF, error)) error {
	fe.opts = opts
	cleanup, runErr := f(ctx)
	if cleanup != nil {
		runErr = errors.Join(runErr, cleanup())
	}
	// Replay the primary output log to stdout/stderr.
	if writeErr := renderPrimaryOutput(fe.out, fe.db); writeErr != nil {
		runErr = errors.Join(runErr, writeErr)
	}
	return runErr
}

func (fe *frontendLogs) Opts() *dagui.FrontendOpts {
	return &fe.opts
}

func (fe *frontendLogs) SetVerbosity(verbosity int) {
	fe.mu.Lock()
	fe.opts.Verbosity = verbosity
	fe.mu.Unlock()
}

func (fe *frontendLogs) SetPrimary(spanID dagui.SpanID) {
	fe.mu.Lock()
	fe.db.SetPrimarySpan(spanID)
	fe.opts.ZoomedSpan = spanID
	fe.opts.FocusedSpan = spanID
	fe.mu.Unlock()
}

func (fe *frontendLogs) Background(cmd tea.ExecCommand, raw bool) error {
	return fmt.Errorf("running shell without the TUI is not supported")
}

func (fe *frontendLogs) RevealAllSpans() {
	fe.mu.Lock()
	fe.opts.ZoomedSpan = dagui.SpanID{}
	fe.mu.Unlock()
}

func (fe *frontendLogs) SpanExporter() sdktrace.SpanExporter {
	return &logsSpanExporter{fe}
}

func (fe *frontendLogs) LogExporter() sdklog.Exporter {
	return &logsLogExporter{
		streamingLogExporter: &fe.logs,
		mu:                   &fe.mu,
	}
}

func (fe *frontendLogs) MetricExporter() sdkmetric.Exporter {
	return &logsMetricExporter{fe: fe}
}

func (fe *frontendLogs) SetCloudURL(ctx context.Context, url string, msg string, logged bool) {}

func (fe *frontendLogs) Shell(ctx context.Context, handler ShellHandler) {
	// Logs frontend doesn't support shell
}

func (fe *frontendLogs) HandlePrompt(ctx context.Context, _, prompt string, dest any) error {
	return interact.NewInteraction(prompt).Resolve(dest)
}

func (fe *frontendLogs) HandleForm(ctx context.Context, form *huh.Form) error {
	return form.RunWithContext(ctx)
}

// logsSpanExporter implements trace.SpanExporter for the logs frontend
type logsSpanExporter struct {
	*frontendLogs
}

func (e *logsSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
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
		e.logs.flushPendingLogsForSpan(id)
	}

	return nil
}

func (e *logsSpanExporter) Shutdown(ctx context.Context) error {
	return nil
}

func (e *logsSpanExporter) ForceFlush(ctx context.Context) error {
	return nil
}

// logsMetricExporter implements metric.Exporter for the logs frontend
type logsMetricExporter struct {
	fe *frontendLogs
}

func (e *logsMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	// Dots style doesn't show metrics
	return nil
}

func (e *logsMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (e *logsMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (e *logsMetricExporter) ForceFlush(ctx context.Context) error {
	return nil
}

func (e *logsMetricExporter) Shutdown(ctx context.Context) error {
	return nil
}

// logsLogExporter implements log.Exporter for the logs frontend
type logsLogExporter struct {
	*streamingLogExporter
	mu *sync.Mutex
}

func (e *logsLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.streamingLogExporter.Export(ctx, records)
}

func (e *logsLogExporter) ForceFlush(ctx context.Context) error {
	return nil
}

func (e *logsLogExporter) Shutdown(ctx context.Context) error {
	return nil
}
