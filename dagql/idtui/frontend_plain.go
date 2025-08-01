package idtui

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"dagger.io/dagger/telemetry"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
	"github.com/muesli/termenv"
	"github.com/pkg/browser"
	"github.com/vito/go-interact/interact"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const plainMaxLiteralLen = 256 // same value as cloud currently

type frontendPlain struct {
	dagui.FrontendOpts

	// db stores info about all the spans
	db   *dagui.DB
	data map[dagui.SpanID]*spanData

	// idx is an incrementing counter to assign human-readable names to spans
	idx uint

	// lastContext stores the chain of parent spans for the span that was last
	// rendered, from shallowest to deepest
	lastContext []dagui.SpanID
	// lastContextLock is the span in lastContext that is being held for the
	// contextHold duration (not always the last one rendered, since there are
	// a couple points we need to manually transfer the lock for best results)
	lastContextLock dagui.SpanID
	// lastContextTime is the time at which the lastContext was rendered at
	lastContextTime time.Time
	// lastContextStartTime is the time at which the lastContext first acquired the lock
	lastContextStartTime time.Time
	// lastContextDepth is a cached value to indicate the depth of the
	// lastContext (since it may be relatively expensive to compute)
	lastContextDepth int

	// contextHold is the amount of time that a span is allowed exclusive
	// access for - during this amount of time after a render, no context
	// switches are allowed
	contextHold time.Duration
	// contextHoldMax is the amount of time that a span is allowed exclusive
	// for - after this amount of time, a span's lock is evicted, even if it
	// has continued to renew the lock.
	contextHoldMax time.Duration

	// output is the target to render to
	output  *termenv.Output
	profile termenv.Profile

	// msgPreFinalRender contains messages to display on the final render
	msgPreFinalRender strings.Builder

	// ticker keeps a constant frame rate
	ticker *time.Ticker

	// done is closed during shutdown
	done     chan struct{}
	doneOnce sync.Once

	mu sync.Mutex
}

type spanData struct {
	// idx is the human-readable number for this span
	idx uint

	// the parent span ID, if the span has a parent
	parentID dagui.SpanID

	// ready indicates that the span is ready to be displayed - this allows to
	// start bufferings logs before we've actually exported the span itself
	ready bool
	// started indicates that the span has started and has been rendered for
	// the first time
	started bool
	// ended indicates that the span has copmleted and has been rendered for
	// the second time
	ended bool

	// logs is a list of log lines pending printing for this span
	logs        []logLine
	logsPending bool
}

type logLine struct {
	line cursorBuffer
	time time.Time
}

func NewPlain(w io.Writer) Frontend {
	db := dagui.NewDB()
	return &frontendPlain{
		db:   db,
		data: make(map[dagui.SpanID]*spanData),

		profile:        ColorProfile(),
		output:         NewOutput(w),
		contextHold:    1 * time.Second,
		contextHoldMax: 10 * time.Second,

		done:   make(chan struct{}),
		ticker: time.NewTicker(50 * time.Millisecond),
	}
}

func (fe *frontendPlain) Shell(ctx context.Context, handler ShellHandler) {
	fmt.Fprintln(fe.output.Writer(), "Shell not supported in plain mode")
}

func (fe *frontendPlain) SetCloudURL(ctx context.Context, url string, msg string, logged bool) {
	if fe.OpenWeb {
		if err := browser.OpenURL(url); err != nil {
			slog.Warn("failed to open URL", "url", url, "err", err)
		}
	}
	if fe.Silent {
		return
	}
	fe.addVirtualLog(trace.SpanFromContext(ctx), "cloud", "url", url)

	if cmdContext, ok := FromCmdContext(ctx); ok && cmdContext.printTraceLink {
		if logged {
			fe.msgPreFinalRender.WriteString(traceMessage(fe.profile, url, msg))
		} else if !skipLoggedOutTraceMsg() {
			fe.msgPreFinalRender.WriteString(fmt.Sprintf(loggedOutTraceMsg, url))
		}
	}
}

// addVirtualLog attaches a fake log row to a given span
func (fe *frontendPlain) addVirtualLog(span trace.Span, name string, fields ...string) {
	if !span.SpanContext().SpanID().IsValid() {
		return
	}

	fe.mu.Lock()
	defer fe.mu.Unlock()

	line := name
	for i := 0; i+1 < len(fields); i += 2 {
		line += " " + fe.output.String(fields[i]+"=").Faint().String() + fields[i+1]
	}

	spanID := dagui.SpanID{SpanID: span.SpanContext().SpanID()}
	spanDt, ok := fe.data[spanID]
	if !ok {
		spanDt = &spanData{}
		fe.data[spanID] = spanDt
	}
	spanDt.logs = append(spanDt.logs, logLine{newCursorBuffer([]byte(line)), time.Now()})
}

func (fe *frontendPlain) Run(ctx context.Context, opts dagui.FrontendOpts, run func(context.Context) error) error {
	if opts.TooFastThreshold == 0 {
		opts.TooFastThreshold = 100 * time.Millisecond
	}
	fe.FrontendOpts = opts

	if !fe.Silent {
		go func() {
		loop:
			for {
				select {
				case <-fe.ticker.C:
				case <-fe.done:
					break loop
				case <-ctx.Done():
					break loop
				}

				fe.render()
			}
			fe.render()
		}()
	}

	runErr := run(ctx)
	fe.finalRender()

	fe.db.WriteDot(opts.DotOutputFilePath, opts.DotFocusField, opts.DotShowInternal)

	return runErr
}

func (fe *frontendPlain) HandlePrompt(ctx context.Context, prompt string, dest any) error {
	switch x := dest.(type) {
	case *bool:
		return fe.handlePromptBool(ctx, prompt, x)
	default:
		return fmt.Errorf("unsupported prompt destination type: %T", dest)
	}
}

func (fe *frontendPlain) handlePromptBool(_ context.Context, prompt string, dest *bool) error {
	return interact.NewInteraction(prompt).Resolve(dest)
}

func (fe *frontendPlain) Opts() *dagui.FrontendOpts {
	return &fe.FrontendOpts
}

func (fe *frontendPlain) SetVerbosity(n int) {
	fe.mu.Lock()
	fe.Opts().Verbosity = n
	fe.mu.Unlock()
}

func (fe *frontendPlain) SetPrimary(spanID dagui.SpanID) {
	fe.mu.Lock()
	fe.db.SetPrimarySpan(spanID)
	fe.ZoomedSpan = spanID
	fe.FocusedSpan = spanID
	fe.mu.Unlock()
}

func (fe *frontendPlain) RevealAllSpans() {
	fe.mu.Lock()
	fe.FrontendOpts.ZoomedSpan = dagui.SpanID{}
	fe.mu.Unlock()
}

func (fe *frontendPlain) Background(cmd tea.ExecCommand, raw bool) error {
	return fmt.Errorf("not implemented")
}

func (fe *frontendPlain) Shutdown(ctx context.Context) error {
	fe.doneOnce.Do(func() {
		fe.ticker.Stop()
		close(fe.done)
	})
	return fe.db.Shutdown(ctx)
}

func (fe *frontendPlain) SpanExporter() sdktrace.SpanExporter {
	return plainSpanExporter{fe}
}

type plainSpanExporter struct {
	*frontendPlain
}

func (fe plainSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	if err := fe.db.ExportSpans(ctx, spans); err != nil {
		return err
	}

	if fe.Debug {
		spanIDs := make([]string, len(spans))
		for i, span := range spans {
			spanIDs[i] = span.SpanContext().SpanID().String()
		}
		slog.Debug("frontend exporting spans", "spans", len(spanIDs))
	}

	for _, span := range spans {
		spanID := dagui.SpanID{SpanID: span.SpanContext().SpanID()}

		spanDt, ok := fe.data[spanID]
		if !ok {
			spanDt = &spanData{}
			fe.data[spanID] = spanDt
		}

		// NOTE: assign parent ID unconditionally in case it was initialized at
		// a time that we didn't have it (i.e. from a log)
		spanDt.parentID = dagui.SpanID{SpanID: span.Parent().SpanID()}

		spanDt.ready = true
	}
	return nil
}

func (fe *frontendPlain) LogExporter() sdklog.Exporter {
	return plainLogExporter{fe}
}

type plainLogExporter struct {
	*frontendPlain
}

func (fe plainLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	err := fe.db.LogExporter().Export(ctx, logs)
	if err != nil {
		return err
	}
	for _, record := range logs {
		// Check if this log is marked as verbose
		isVerbose := false
		record.WalkAttributes(func(kv log.KeyValue) bool {
			if kv.Key == telemetry.LogsVerboseAttr && kv.Value.AsBool() {
				isVerbose = true
				return false // stop walking
			}
			return true // continue walking
		})

		// Skip verbose logs in the plain frontend
		if isVerbose {
			continue
		}

		spanID := dagui.SpanID{SpanID: record.SpanID()}
		spanDt, ok := fe.data[spanID]
		if !ok {
			spanDt = &spanData{}
			fe.data[spanID] = spanDt
		}

		body := record.Body().AsString()
		if body == "" {
			// NOTE: likely just indicates EOF (stdio.eof=true attr); either way we
			// want to avoid giving it its own line.
			continue
		}

		lines := strings.SplitAfter(body, "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}

			hasNewline := line[len(line)-1] == '\n'
			if hasNewline {
				line = line[:len(line)-1]
			}

			if spanDt.logsPending && len(spanDt.logs) > 0 {
				spanDt.logs[len(spanDt.logs)-1].line.Write([]byte(line))
				spanDt.logs[len(spanDt.logs)-1].time = record.Timestamp()
			} else {
				spanDt.logs = append(spanDt.logs, logLine{
					line: newCursorBuffer([]byte(line)),
					time: record.Timestamp(),
				})
			}

			spanDt.logsPending = !hasNewline
		}
	}
	return nil
}

func (fe *frontendPlain) ForceFlush(context.Context) error {
	return nil
}

func (fe *frontendPlain) MetricExporter() sdkmetric.Exporter {
	return PlainFrontendMetricExporter{fe}
}

type PlainFrontendMetricExporter struct {
	*frontendPlain
}

func (fe PlainFrontendMetricExporter) Export(ctx context.Context, resourceMetrics *metricdata.ResourceMetrics) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	return fe.db.MetricExporter().Export(ctx, resourceMetrics)
}

func (fe PlainFrontendMetricExporter) Temporality(ik sdkmetric.InstrumentKind) metricdata.Temporality {
	return fe.db.Temporality(ik)
}

func (fe PlainFrontendMetricExporter) Aggregation(ik sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return fe.db.Aggregation(ik)
}

func (fe PlainFrontendMetricExporter) ForceFlush(context.Context) error {
	return nil
}

func (fe *frontendPlain) render() {
	fe.mu.Lock()
	fe.renderProgress()
	fe.mu.Unlock()
}

func (fe *frontendPlain) finalRender() {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	stderr := fe.output.Writer()
	if !fe.Silent {
		// disable context holds, for this final render of *everything*
		fe.contextHold = 0
		fe.renderProgress()
	}
	if fe.idx > 0 {
		// if we rendered anything, leave a newline
		fmt.Fprintln(stderr)
	}

	handleTelemetryErrorOutput(stderr, fe.output, fe.TelemetryError)

	if fe.msgPreFinalRender.Len() > 0 {
		fmt.Fprintln(stderr, "\n"+fe.msgPreFinalRender.String()+"\n")
	}
	renderPrimaryOutput(stderr, fe.db)
}

func (fe *frontendPlain) renderProgress() {
	rowsView := fe.db.RowsView(fe.FrontendOpts)

	// quickly sanity check the context - if a span from it has gone missing
	// from the db, or has been marked as passthrough, it will no longer appear
	// in the logs row!
	if len(fe.lastContext) > 0 {
		newLock := dagui.SpanID{}
		for _, spanID := range fe.lastContext {
			span, ok := fe.db.Spans.Map[spanID]
			if !ok || !span.Passthrough {
				// pass the lock to the last most-valid span
				break
			}

			newLock = span.ID

			if spanID == fe.lastContextLock {
				// don't accidentally lock further in the context than we were before
				break
			}
		}
		fe.lastContextLock = newLock
	}

	for _, row := range rowsView.Body {
		fe.renderRow(row)
	}
}

func (fe *frontendPlain) renderRow(tree *dagui.TraceTree) {
	span := tree.Span
	spanDt := fe.data[span.ID]
	if spanDt == nil {
		slog.Warn("spanDt is nil", "id", span.ID.String())
		return
	}
	if !spanDt.ready {
		// don't render! this span hasn't been exported yet
		return
	}

	if !spanDt.started {
		// render! this span has just started
		depth, ok := fe.renderContext(tree)
		if !ok {
			return
		}
		fe.renderStep(span, depth, false)
		fe.renderLogs(tree, depth)
		spanDt.started = true
	}

	// render all the children - it's important that we render the children
	// details first to avoid unnecessary context switches
	for _, child := range tree.Children {
		fe.renderRow(child)
	}

	if len(spanDt.logs) > 0 {
		lastVertex := fe.lastVertex()
		depth, ok := fe.renderContext(tree)
		if !ok {
			return
		}
		if tree.Span.ID != lastVertex {
			fe.renderStep(span, depth, spanDt.ended)
		}
		fe.renderLogs(tree, depth)
	}
	if !spanDt.ended && !tree.IsRunningOrChildRunning {
		// render! this span has finished
		// this renders last, so that we have the chance to render logs and
		// finished children first - this ensures we get a LIFO structure
		// to the logs which makes them easier to read
		depth, ok := fe.renderContext(tree)
		if !ok {
			return
		}
		fe.renderStep(span, depth, true)
		spanDt.ended = true

		// nothing else *should* happen with this step, so we can switch
		// context to the parent
		if tree.Parent == nil {
			fe.lastContextLock = dagui.SpanID{}
		} else {
			fe.lastContextLock = tree.Parent.Span.ID
		}
	}
}

func (fe *frontendPlain) renderStep(span *dagui.Span, depth int, done bool) {
	spanDt := fe.data[span.ID]
	if spanDt.idx == 0 {
		fe.idx++
		spanDt.idx = fe.idx
	}

	r := newRenderer(fe.db, plainMaxLiteralLen, fe.FrontendOpts)

	prefix := fe.stepPrefix(span, spanDt)
	fmt.Fprint(fe.output, prefix)
	r.indent(fe.output, depth)
	if spanCall := span.Call(); spanCall != nil {
		call := &callpbv1.Call{
			Field:          spanCall.Field,
			Args:           spanCall.Args,
			Type:           spanCall.Type,
			ReceiverDigest: spanCall.ReceiverDigest,
		}
		if done {
			call.Args = nil
			call.Type = nil
		}
		r.renderCall(fe.output, nil, call, prefix, false, depth-1, span.Internal, nil)
	} else {
		r.renderSpan(fe.output, nil, span.Name)
	}
	if done {
		if span.IsFailedOrCausedFailure() {
			fmt.Fprint(fe.output, fe.output.String(" ERROR").Foreground(termenv.ANSIRed))
		} else if span.IsCached() {
			fmt.Fprint(fe.output, fe.output.String(" CACHED").Foreground(termenv.ANSIBlue))
		} else {
			fmt.Fprint(fe.output, fe.output.String(" DONE").Foreground(termenv.ANSIGreen))
		}
		duration := dagui.FormatDuration(span.Activity.Duration(time.Now()))
		fmt.Fprint(fe.output, fe.output.String(fmt.Sprintf(" [%s]", duration)).Foreground(termenv.ANSIBrightBlack))
		r.renderMetrics(fe.output, span)

		if span.IsFailed() && span.Status.Description != "" {
			fmt.Fprintln(fe.output)
			fmt.Fprint(fe.output, prefix)
			r.indent(fe.output, depth)
			// print error description above it
			fmt.Fprintf(fe.output,
				fe.output.String("! %s").Foreground(termenv.ANSIYellow).String(),
				span.Status.Description,
			)
		}
	}
	fmt.Fprintln(fe.output)
}

func (fe *frontendPlain) renderLogs(row *dagui.TraceTree, depth int) {
	out := fe.output

	span := row.Span
	spanDt := fe.data[span.ID]

	r := newRenderer(fe.db, plainMaxLiteralLen, fe.FrontendOpts)

	prefix := fe.stepPrefix(span, spanDt)

	var logs []logLine
	if spanDt.logsPending && len(spanDt.logs) > 0 && row.IsRunningOrChildRunning {
		logs = spanDt.logs[:len(spanDt.logs)-1]
		spanDt.logs = spanDt.logs[len(spanDt.logs)-1:]
	} else {
		logs = spanDt.logs
		spanDt.logs = nil
	}

	for _, logLine := range logs {
		fmt.Fprint(out, prefix)
		r.indent(fe.output, depth)

		if !logLine.time.IsZero() {
			duration := dagui.FormatDuration(logLine.time.Sub(span.StartTime))
			fmt.Fprint(out, out.String(fmt.Sprintf("[%s] ", duration)).Foreground(termenv.ANSIBrightBlack))
		}
		pipe := out.String("|").Foreground(termenv.ANSIBrightBlack)
		fmt.Fprintln(out, pipe, strings.TrimSuffix(logLine.line.String(), "\n"))
	}
}

func (fe *frontendPlain) stepPrefix(span *dagui.Span, dt *spanData) string {
	prefix := fe.output.String(fmt.Sprintf("%-4d: ", dt.idx)).Foreground(termenv.ANSIBrightMagenta).String()
	if fe.Debug {
		prefix += fe.output.String(fmt.Sprintf("%s: ", span.ID.String())).Foreground(termenv.ANSIBrightBlack).String()
	}
	return prefix
}

func (fe *frontendPlain) renderContext(row *dagui.TraceTree) (int, bool) {
	now := time.Now()

	if row.Span.ID == fe.lastVertex() {
		// this is the last vertex we rendered, we're already in the right
		// context: attempt to renew the lock and return
		if now.Sub(fe.lastContextStartTime) < fe.contextHoldMax {
			fe.lastContextLock = fe.lastVertex()
			fe.lastContextTime = now
			return fe.lastContextDepth, true
		}
	}

	// determine the current context
	switchContext := fe.lastContextLock.IsValid()
	currentContext := []*dagui.TraceTree{}
	for parent := row; parent != nil; parent = parent.Parent {
		if switchContext && parent.Span.ID == fe.lastContextLock {
			// this span is a child to the last context
			switchContext = false
		}
		currentContext = append(currentContext, parent)
	}
	slices.Reverse(currentContext)

	if switchContext {
		// this context is not directly related to the last one, so we need to
		// context-switch
		if now.Sub(fe.lastContextTime) < fe.contextHold {
			// another context still has an exclusive hold
			return 0, false
		}
	}

	// insert whitespace when changing top-most context span
	if len(fe.lastContext) > 0 && len(currentContext) > 0 && currentContext[0].Span.ID != fe.lastContext[0] {
		fmt.Fprintln(fe.output)
	}

	// render the context
	depth := 0
	for _, i := range sampleContext(currentContext) {
		call := currentContext[i]

		show := true
		if i < len(fe.lastContext) {
			show = call.Span.ID != fe.lastContext[i]
		}
		if show {
			fe.renderStep(call.Span, depth, false)
		}
		depth += 1
	}

	fe.lastContext = make([]dagui.SpanID, 0, len(currentContext))
	for _, row := range currentContext {
		fe.lastContext = append(fe.lastContext, row.Span.ID)
	}
	fe.lastContextLock = fe.lastVertex()
	fe.lastContextDepth = depth
	fe.lastContextStartTime = now
	fe.lastContextTime = now
	return depth, true
}

func (fe *frontendPlain) lastVertex() dagui.SpanID {
	if len(fe.lastContext) == 0 {
		return dagui.SpanID{}
	}
	return fe.lastContext[len(fe.lastContext)-1]
}

// sampleContext selects vertices from a row context to display
func sampleContext(rows []*dagui.TraceTree) []int {
	if len(rows) == 0 {
		return nil
	}

	// don't ever sample the current row
	rows = rows[:len(rows)-1]
	if len(rows) == 0 {
		return nil
	}

	// NB: break glass for all the context
	// all := make([]int, len(rows))
	// for i := range rows {
	// 	all[i] = i
	// }
	// return all

	result := []int{}

	// find the first call
	for i := range len(rows) {
		row := rows[i]
		result = append(result, i)
		if row.Span.Call() != nil {
			break
		}
	}
	// iterate backwards to find the last call
	for i := len(rows) - 1; i > result[len(result)-1]; i-- {
		row := rows[i]
		if row.Span.Call() != nil {
			result = append(result, i)
			break
		}
	}

	return result
}
