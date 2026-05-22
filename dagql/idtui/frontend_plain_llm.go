package idtui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dagger.io/dagger"
	"github.com/charmbracelet/huh"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/util/cleanups"
	telemetry "github.com/dagger/otel-go"
	"github.com/muesli/termenv"
	"github.com/vito/go-interact/interact"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	plainLLMStatusInterval = 10 * time.Second
	plainLLMLongStep       = 30 * time.Second
	plainLLMMaxLabelLen    = 96
	plainLLMMaxErrorLen    = 240
)

type frontendPlainLLM struct {
	profile termenv.Profile
	out     TermOutput

	mu             sync.Mutex
	db             *dagui.DB
	opts           dagui.FrontendOpts
	reporter       *frontendPretty
	telemetryError atomic.Pointer[error]

	started      time.Time
	lastStatus   time.Time
	lastEmit     time.Time
	announced    map[dagui.SpanID]struct{}
	ticker       *time.Ticker
	done         chan struct{}
	doneOnce     sync.Once
	shutdownOnce sync.Once
}

// NewPlain creates a concise, streaming progress frontend intended for
// non-interactive and LLM-driven runs.
func NewPlain(output io.Writer) Frontend {
	profile := ColorProfile()
	if output == nil {
		output = os.Stderr
	}
	out := NewOutput(output, termenv.WithProfile(profile))

	db := dagui.NewDB()
	reporter := NewWithDB(output, db)
	reporter.reportOnly = true

	return &frontendPlainLLM{
		profile:   profile,
		out:       out,
		db:        db,
		reporter:  reporter,
		announced: make(map[dagui.SpanID]struct{}),
		done:      make(chan struct{}),
	}
}

func (fe *frontendPlainLLM) Run(ctx context.Context, opts dagui.FrontendOpts, run func(context.Context) (cleanups.CleanupF, error)) error {
	if opts.TooFastThreshold == 0 {
		opts.TooFastThreshold = 100 * time.Millisecond
	}

	fe.mu.Lock()
	fe.opts = opts
	fe.reporter.FrontendOpts = opts
	fe.started = time.Now()
	fe.lastStatus = fe.started
	if !opts.Silent {
		fe.emitLineLocked("started")
		fe.startTickerLocked(ctx)
	}
	fe.mu.Unlock()

	cleanup, runErr := run(ctx)
	if cleanup != nil {
		runErr = errors.Join(runErr, cleanup())
	}

	fe.stopTicker()

	fe.mu.Lock()
	reportErr := normalizeFrontendExit(runErr, fe.db)
	if !opts.Silent {
		fe.renderCompletedLocked(true)
		fe.renderSummaryLocked(reportErr)
		if reportErr != nil {
			fmt.Fprintln(fe.out)
			if renderErr := fe.renderFinalReportLocked(reportErr); renderErr != nil {
				runErr = errors.Join(runErr, renderErr)
			} else {
				runErr = reportErr
			}
		} else if fe.renderFinalTestsLocked() {
			fmt.Fprintln(fe.out)
		}
	}

	if opts.Silent || reportErr == nil {
		fe.renderFinalMessagesLocked(fe.reporter.msgPreFinalRender.String())
		if writeErr := renderPrimaryOutput(fe.out, fe.db); writeErr != nil {
			runErr = errors.Join(runErr, writeErr)
		}
	}
	fe.mu.Unlock()

	fe.db.WriteDot(opts.DotOutputFilePath, opts.DotFocusField, opts.DotShowInternal)
	return normalizeFrontendExit(runErr, fe.db)
}

func (fe *frontendPlainLLM) startTickerLocked(ctx context.Context) {
	fe.ticker = time.NewTicker(time.Second)
	go func() {
		for {
			select {
			case <-fe.ticker.C:
				fe.mu.Lock()
				fe.renderStatusLocked(false)
				fe.mu.Unlock()
			case <-fe.done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (fe *frontendPlainLLM) stopTicker() {
	fe.doneOnce.Do(func() {
		if fe.ticker != nil {
			fe.ticker.Stop()
		}
		close(fe.done)
	})
}

func (fe *frontendPlainLLM) Shutdown(ctx context.Context) error {
	fe.stopTicker()
	var err error
	fe.shutdownOnce.Do(func() {
		err = fe.db.Shutdown(ctx)
	})
	return err
}

func (fe *frontendPlainLLM) SetSidebarContent(SidebarSection) {}

func (fe *frontendPlainLLM) Shell(ctx context.Context, handler ShellHandler) {}

func (fe *frontendPlainLLM) SetCloudURL(ctx context.Context, url string, msg string, logged bool) {
	fe.reporter.SetCloudURL(ctx, url, msg, logged)
}

func (fe *frontendPlainLLM) SetClient(client *dagger.Client) {}

func (fe *frontendPlainLLM) HandlePrompt(ctx context.Context, _, prompt string, dest any) error {
	return interact.NewInteraction(prompt).Resolve(dest)
}

func (fe *frontendPlainLLM) HandleForm(ctx context.Context, form *huh.Form) error {
	return form.RunWithContext(ctx)
}

func (fe *frontendPlainLLM) Opts() *dagui.FrontendOpts {
	return &fe.opts
}

func (fe *frontendPlainLLM) SetVerbosity(n int) {
	fe.mu.Lock()
	fe.opts.Verbosity = n
	fe.reporter.SetVerbosity(n)
	fe.mu.Unlock()
}

func (fe *frontendPlainLLM) SetTelemetryError(err error) {
	fe.telemetryError.Store(&err)
	fe.reporter.SetTelemetryError(err)
}

func (fe *frontendPlainLLM) SetPrimary(spanID dagui.SpanID) {
	fe.mu.Lock()
	fe.db.SetPrimarySpan(spanID)
	fe.opts.ZoomedSpan = spanID
	fe.opts.FocusedSpan = spanID
	fe.reporter.SetPrimary(spanID)
	fe.mu.Unlock()
}

func (fe *frontendPlainLLM) RevealAllSpans() {
	fe.mu.Lock()
	fe.opts.ZoomedSpan = dagui.SpanID{}
	fe.reporter.RevealAllSpans()
	fe.mu.Unlock()
}

func (fe *frontendPlainLLM) Background(cmd ExecCommand, raw bool) error {
	return fmt.Errorf("running shell without the TUI is not supported")
}

func (fe *frontendPlainLLM) SpanExporter() sdktrace.SpanExporter {
	return plainLLMSpanExporter{fe}
}

type plainLLMSpanExporter struct {
	*frontendPlainLLM
}

func (fe plainLLMSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	if err := fe.db.ExportSpans(ctx, spans); err != nil {
		return err
	}

	for _, span := range spans {
		spanID := dagui.SpanID{SpanID: span.SpanContext().SpanID()}
		if logs := fe.db.DrainResolvedLogs(spanID); len(logs) > 0 {
			if err := fe.reporter.logs.Export(ctx, logs); err != nil {
				return err
			}
		}
	}

	if !fe.opts.Silent {
		fe.renderCompletedLocked(false)
	}
	return nil
}

func (fe *frontendPlainLLM) LogExporter() sdklog.Exporter {
	return plainLLMLogExporter{fe}
}

type plainLLMLogExporter struct {
	*frontendPlainLLM
}

func (fe plainLLMLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	if err := fe.db.LogExporter().Export(ctx, logs); err != nil {
		return err
	}
	return fe.reporter.logs.Export(ctx, logs)
}

func (fe *frontendPlainLLM) MetricExporter() sdkmetric.Exporter {
	return plainLLMMetricExporter{fe}
}

type plainLLMMetricExporter struct {
	*frontendPlainLLM
}

func (fe plainLLMMetricExporter) Export(ctx context.Context, resourceMetrics *metricdata.ResourceMetrics) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	return fe.db.MetricExporter().Export(ctx, resourceMetrics)
}

func (fe plainLLMMetricExporter) Temporality(ik sdkmetric.InstrumentKind) metricdata.Temporality {
	return fe.db.Temporality(ik)
}

func (fe plainLLMMetricExporter) Aggregation(ik sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return fe.db.Aggregation(ik)
}

func (fe plainLLMMetricExporter) ForceFlush(context.Context) error {
	return nil
}

func (fe *frontendPlainLLM) ForceFlush(context.Context) error {
	return nil
}

func (fe *frontendPlainLLM) renderCompletedLocked(final bool) {
	rowsView := fe.db.RowsView(fe.opts)
	for _, span := range fe.db.Spans.Order {
		if _, ok := fe.announced[span.ID]; ok {
			continue
		}
		if !span.Received || span.IsRunningOrEffectsRunning() || span.IsPending() {
			continue
		}
		tree, ok := rowsView.BySpan[span.ID]
		if !ok || !fe.shouldAnnounceSpanLocked(tree, final) {
			continue
		}
		fe.emitLineLocked(fe.spanCompletionLineLocked(span))
		fe.announced[span.ID] = struct{}{}
	}
}

func (fe *frontendPlainLLM) shouldAnnounceSpanLocked(tree *dagui.TraceTree, final bool) bool {
	span := tree.Span
	if span.ID == fe.db.PrimarySpan {
		return false
	}
	if span.CheckName != "" {
		return true
	}
	if span.IsFailedOrCausedFailure() || span.IsCanceled() {
		return fe.shouldShowFailureLocked(span)
	}
	if final {
		return false
	}
	return !span.IsCached() && span.Activity.Duration(time.Now()) >= plainLLMLongStep && fe.isProgressRootTree(tree)
}

func (fe *frontendPlainLLM) spanCompletionLineLocked(span *dagui.Span) string {
	duration := dagui.FormatDuration(span.Activity.Duration(time.Now()))
	label := quoteForProgress(fe.spanLabelLocked(span))

	if span.CheckName != "" {
		status := "FAIL"
		if span.CheckPassed && !span.IsFailedOrCausedFailure() && !span.IsCanceled() {
			status = "PASS"
		}
		line := fmt.Sprintf("check %s name=%s duration=%s", status, label, duration)
		if status == "FAIL" {
			line += fe.spanErrorSuffix(span)
		}
		return line
	}

	status := "DONE"
	switch {
	case span.IsCanceled():
		status = "CANCELED"
	case span.IsFailedOrCausedFailure():
		status = "FAIL"
	case span.IsCached():
		status = "CACHED"
	}

	line := fmt.Sprintf("step %s name=%s duration=%s", status, label, duration)
	if status == "FAIL" {
		line += fe.spanErrorSuffix(span)
	}
	return line
}

func (fe *frontendPlainLLM) spanErrorSuffix(span *dagui.Span) string {
	if span.Status.Description == "" {
		return ""
	}
	return " error=" + quoteForProgress(truncateProgress(collapseWhitespace(span.Status.Description), plainLLMMaxErrorLen))
}

func (fe *frontendPlainLLM) renderStatusLocked(force bool) {
	now := time.Now()
	if !force && now.Sub(fe.lastEmit) < plainLLMStatusInterval {
		return
	}

	stats := fe.progressStatsLocked(now)
	if !force && stats.running == 0 && stats.pending == 0 {
		return
	}

	parts := []string{
		"progress",
		fmt.Sprintf("running=%d", stats.running),
		fmt.Sprintf("pending=%d", stats.pending),
		fmt.Sprintf("done=%d", stats.done()),
		fmt.Sprintf("cached=%d", stats.cached),
		fmt.Sprintf("failed=%d", stats.failed),
	}
	if stats.checksPassed+stats.checksFailed > 0 {
		parts = append(parts,
			fmt.Sprintf("checks_passed=%d", stats.checksPassed),
			fmt.Sprintf("checks_failed=%d", stats.checksFailed),
		)
	}
	if len(stats.active) > 0 {
		parts = append(parts, "active="+quoteForProgress(strings.Join(stats.active, ", ")))
	} else if stats.total == 0 {
		parts = append(parts, "state=waiting-for-telemetry")
	}
	parts = append(parts, fe.resourceSummaryLocked()...)

	fe.emitLineLocked(strings.Join(parts, " "))
	fe.lastStatus = now
}

func (fe *frontendPlainLLM) renderSummaryLocked(reportErr error) {
	stats := fe.progressStatsLocked(time.Now())
	status := "OK"
	if reportErr != nil {
		status = "FAIL"
	}
	parts := []string{
		"result=" + status,
		fmt.Sprintf("done=%d", stats.done()),
		fmt.Sprintf("cached=%d", stats.cached),
		fmt.Sprintf("failed=%d", stats.failed),
	}
	if stats.checksPassed+stats.checksFailed > 0 {
		parts = append(parts,
			fmt.Sprintf("checks_passed=%d", stats.checksPassed),
			fmt.Sprintf("checks_failed=%d", stats.checksFailed),
		)
	}
	fe.emitLineLocked(strings.Join(parts, " "))
}

type plainProgressStats struct {
	total        int
	running      int
	pending      int
	success      int
	cached       int
	failed       int
	canceled     int
	checksPassed int
	checksFailed int
	active       []string
}

func (s plainProgressStats) done() int {
	return s.success + s.cached + s.failed + s.canceled
}

func (fe *frontendPlainLLM) progressStatsLocked(now time.Time) plainProgressStats {
	var stats plainProgressStats
	rowsView := fe.db.RowsView(fe.opts)
	walkTraceTrees(rowsView.Body, func(tree *dagui.TraceTree) {
		span := tree.Span
		if span.ID == fe.db.PrimarySpan {
			return
		}
		stats.total++
		switch {
		case span.IsRunningOrEffectsRunning():
			stats.running++
		case span.IsPending():
			stats.pending++
		case span.IsCanceled():
			stats.canceled++
		case span.IsFailedOrCausedFailure():
			stats.failed++
		case span.IsCached():
			stats.cached++
		default:
			stats.success++
		}

		if span.CheckName != "" && !span.IsRunningOrEffectsRunning() && !span.IsPending() {
			if span.CheckPassed && !span.IsFailedOrCausedFailure() && !span.IsCanceled() {
				stats.checksPassed++
			} else {
				stats.checksFailed++
			}
		}
	})
	fe.collectActiveRowsLocked(rowsView.Body, now, &stats.active)
	return stats
}

func (fe *frontendPlainLLM) shouldShowFailureLocked(span *dagui.Span) bool {
	return fe.isRenderedSpanLocked(span)
}

func (fe *frontendPlainLLM) isRenderedSpanLocked(span *dagui.Span) bool {
	view := fe.db.RowsView(fe.opts)
	_, ok := view.BySpan[span.ID]
	return ok
}

func (fe *frontendPlainLLM) collectActiveRowsLocked(trees []*dagui.TraceTree, now time.Time, active *[]string) {
	for _, tree := range trees {
		if len(*active) >= 3 {
			return
		}
		if !tree.IsRunningOrChildRunning {
			continue
		}
		if tree.Span.ID != fe.db.PrimarySpan && fe.isProgressRootTree(tree) {
			label := truncateProgress(fe.spanLabelLocked(tree.Span), plainLLMMaxLabelLen)
			if dur := tree.Span.Activity.Duration(now); dur > 0 {
				label += " " + dagui.FormatDuration(dur)
			}
			*active = append(*active, label)
			continue
		}
		fe.collectActiveRowsLocked(tree.Children, now, active)
	}
}

func (fe *frontendPlainLLM) isProgressRootTree(tree *dagui.TraceTree) bool {
	return tree.Parent == nil || tree.Parent.Span.ID == fe.db.PrimarySpan || !tree.Parent.IsRunningOrChildRunning
}

func walkTraceTrees(trees []*dagui.TraceTree, f func(*dagui.TraceTree)) {
	for _, tree := range trees {
		f(tree)
		walkTraceTrees(tree.Children, f)
	}
}

func (fe *frontendPlainLLM) renderFinalReportLocked(reportErr error) error {
	preFinalMessage := fe.reporter.msgPreFinalRender.String()
	fe.reporter.msgPreFinalRender.Reset()
	fe.reporter.err = reportErr

	renderErr := fe.reporter.FinalRender(fe.out)
	fe.renderFinalMessagesLocked(preFinalMessage)
	return renderErr
}

func (fe *frontendPlainLLM) renderFinalMessagesLocked(preFinalMessage string) {
	var telemetryErr error
	if p := fe.telemetryError.Load(); p != nil {
		telemetryErr = *p
	}
	handleTelemetryErrorOutput(fe.out, fe.out, telemetryErr)
	if preFinalMessage != "" {
		fe.emitLineLocked(preFinalMessage)
	}
}

func (fe *frontendPlainLLM) renderFinalTestsLocked() bool {
	view := fe.db.TestView()
	if !view.HasTests() {
		return false
	}
	tv := &TestView{
		Profile:         fe.profile,
		Logs:            fe.reporter.logs.Logs,
		SummaryLogLines: -1,
	}
	for _, line := range tv.renderTestSummaryLines(fe.out, view, 80, finalTestViewHeight(tv)) {
		fmt.Fprintln(fe.out, line)
	}
	return true
}

func (fe *frontendPlainLLM) spanLabelLocked(span *dagui.Span) string {
	switch {
	case span.CheckName != "":
		return truncateProgress(span.CheckName, plainLLMMaxLabelLen)
	case span.ServiceName != "":
		return truncateProgress("service "+span.ServiceName, plainLLMMaxLabelLen)
	case span.GeneratorName != "":
		return truncateProgress("generate "+span.GeneratorName, plainLLMMaxLabelLen)
	case span.LLMTool != "":
		return truncateProgress("tool "+span.LLMTool, plainLLMMaxLabelLen)
	case span.Message != "":
		return truncateProgress(collapseWhitespace(span.Message), plainLLMMaxLabelLen)
	case span.Name != "":
		return truncateProgress(collapseWhitespace(span.Name), plainLLMMaxLabelLen)
	default:
		return span.ID.String()
	}
}

func (fe *frontendPlainLLM) resourceSummaryLocked() []string {
	var diskRead, diskWrite, ioPressure, cpuSome, cpuFull, memPeak, filesyncWritten int64
	for _, metricsByName := range fe.db.MetricsByCall {
		diskRead += lastMetricValue(metricsByName, telemetry.IOStatDiskReadBytes)
		diskWrite += lastMetricValue(metricsByName, telemetry.IOStatDiskWriteBytes)
		ioPressure += lastMetricValue(metricsByName, telemetry.IOStatPressureSomeTotal)
		cpuSome += lastMetricValue(metricsByName, telemetry.CPUStatPressureSomeTotal)
		cpuFull += lastMetricValue(metricsByName, telemetry.CPUStatPressureFullTotal)
		memPeak = max(memPeak, lastMetricValue(metricsByName, telemetry.MemoryPeakBytes))
	}
	for _, metricsByName := range fe.db.MetricsBySpan {
		filesyncWritten += lastMetricValue(metricsByName, telemetry.FilesyncWrittenBytes)
	}

	var parts []string
	if memPeak > 0 {
		parts = append(parts, "mem_peak="+quoteForProgress(humanizeBytes(memPeak)))
	}
	if diskRead > 0 {
		parts = append(parts, "disk_read="+quoteForProgress(humanizeBytes(diskRead)))
	}
	if diskWrite > 0 {
		parts = append(parts, "disk_write="+quoteForProgress(humanizeBytes(diskWrite)))
	}
	if ioPressure > 0 {
		parts = append(parts, "io_pressure="+durationString(ioPressure))
	}
	if cpuSome > 0 {
		parts = append(parts, "cpu_pressure_some="+durationString(cpuSome))
	}
	if cpuFull > 0 {
		parts = append(parts, "cpu_pressure_full="+durationString(cpuFull))
	}
	if filesyncWritten > 0 {
		parts = append(parts, "filesync_written="+quoteForProgress(humanizeBytes(filesyncWritten)))
	}
	return parts
}

func (fe *frontendPlainLLM) emitLineLocked(line string) {
	now := time.Now()
	prefix := ""
	if !fe.started.IsZero() {
		prefix = "[+" + dagui.FormatDuration(now.Sub(fe.started)) + "] "
	}
	for _, part := range strings.Split(strings.TrimSuffix(line, "\n"), "\n") {
		fmt.Fprintln(fe.out, prefix+part)
	}
	fe.lastEmit = now
}

func lastMetricValue(metricsByName map[string][]metricdata.DataPoint[int64], name string) int64 {
	points := metricsByName[name]
	if len(points) == 0 {
		return 0
	}
	return points[len(points)-1].Value
}

func quoteForProgress(s string) string {
	return fmt.Sprintf("%q", s)
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateProgress(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
