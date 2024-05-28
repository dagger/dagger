package idtui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/telemetry"
	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type frontendPlain struct {
	FrontendOpts
	spanFilter

	// db stores info about all the spans
	db   *DB
	data map[trace.SpanID]*spanData

	// idx is an incrementing counter to assign human-readable names to spans
	idx uint

	// lastContext stores the chain of parent spans for the span that was last
	// rendered, from shallowest to deepest
	lastContext []trace.SpanID
	// lastContextLock is the span in lastContext that is being held for the
	// contextHold duration (not always the last one rendered, since there are
	// a couple points we need to manually transfer the lock for best results)
	lastContextLock trace.SpanID
	// lastContextTime is the time at which the lastContext was rendered at
	lastContextTime time.Time
	// lastContextDepth is a cached value to indicate the depth of the
	// lastContext (since it may be relatively expensive to compute)
	lastContextDepth int

	// contextHold is the amount of time that a span is allowed exclusive
	// access for - during this amount of time after a render, no context
	// switches are allowed
	contextHold time.Duration

	// output is the target to render to
	output  *termenv.Output
	profile termenv.Profile

	// ticker keeps a constant frame rate
	ticker *time.Ticker

	// done is closed during shutdown
	done     chan struct{}
	doneOnce sync.Once

	mu sync.Mutex
}

type spanData struct {
	// ready indicates that the span is ready to be displayed - this allows to
	// start bufferings logs before we've actually exported the span itself
	ready bool
	// started indicates that the span has started and has been rendered for
	// the first time
	started bool
	// ended indicates that the span has copmleted and has been rendered for
	// the second time
	ended bool

	// idx is the human-readable number for this span
	idx uint
	// logs is a list of log lines pending printing for this span
	logs []logLine
}

type logLine struct {
	line string
	time time.Time
}

func NewPlain() Frontend {
	return &frontendPlain{
		db:   NewDB(),
		data: make(map[trace.SpanID]*spanData),

		spanFilter: spanFilter{
			tooFastThreshold: 100 * time.Millisecond,
		},

		profile:     ColorProfile(),
		output:      NewOutput(os.Stderr),
		contextHold: 1 * time.Second,

		done:   make(chan struct{}),
		ticker: time.NewTicker(50 * time.Millisecond),
	}
}

func (fe *frontendPlain) ConnectedToEngine(name string, version string) {
	if !fe.Silent {
		slog.Info("Connected to engine", "name", name, "version", version)
	}
}

func (fe *frontendPlain) ConnectedToCloud(url string) {
	if !fe.Silent {
		slog.Info("Connected to cloud", "url", url)
	}
}

func (fe *frontendPlain) Run(ctx context.Context, opts FrontendOpts, run func(context.Context) error) error {
	fe.FrontendOpts = opts

	// set default context logs
	ctx = telemetry.WithLogProfile(ctx, fe.profile)

	// redirect slog to the logs pane
	level := slog.LevelInfo
	if fe.Debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(telemetry.PrettyLogger(os.Stderr, fe.profile, level))

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

			// disable context holds, for this final render of *everything*
			fe.contextHold = 0
			fe.render()
		}()
	}

	runErr := run(ctx)
	fe.finalRender()
	return runErr
}

func (fe *frontendPlain) SetPrimary(spanID trace.SpanID) {
	fe.mu.Lock()
	fe.db.PrimarySpan = spanID
	fe.mu.Unlock()
}

func (fe *frontendPlain) Background(cmd tea.ExecCommand) error {
	return fmt.Errorf("not implemented")
}

func (fe *frontendPlain) Shutdown(ctx context.Context) error {
	fe.doneOnce.Do(func() {
		fe.ticker.Stop()
		close(fe.done)
	})
	return fe.db.Shutdown(ctx)
}

func (fe *frontendPlain) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	slog.Debug("frontend exporting", "spans", len(spans))
	for _, span := range spans {
		slog.Debug("frontend exporting span",
			"trace", span.SpanContext().TraceID(),
			"id", span.SpanContext().SpanID(),
			"parent", span.Parent().SpanID(),
			"span", span.Name(),
		)

		spanDt, ok := fe.data[span.SpanContext().SpanID()]
		if !ok {
			spanDt = &spanData{}
			fe.data[span.SpanContext().SpanID()] = spanDt
		}
		spanDt.ready = true
	}

	return fe.db.ExportSpans(ctx, spans)
}

func (fe *frontendPlain) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	slog.Debug("frontend exporting logs", "logs", len(logs))

	err := fe.db.ExportLogs(ctx, logs)
	if err != nil {
		return err
	}
	for _, log := range logs {
		spanDt, ok := fe.data[log.SpanID]
		if !ok {
			spanDt = &spanData{}
			fe.data[log.SpanID] = spanDt
		}

		body := log.Body().AsString()
		body = strings.TrimSuffix(body, "\n")
		for _, line := range strings.Split(body, "\n") {
			spanDt.logs = append(spanDt.logs, logLine{line, log.Timestamp()})
		}
	}
	return nil
}

func (fe *frontendPlain) render() {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	steps := CollectSpans(fe.db, trace.TraceID{})
	rows := CollectRows(steps)
	logsView := CollectLogsView(rows)

	// quickly sanity check the context - if a span from it has gone missing
	// from the db, or has been marked as passthrough, it will no longer appear
	// in the logs row!
	if len(fe.lastContext) > 0 {
		newLock := trace.SpanID{}
		for _, spanID := range fe.lastContext {
			span, ok := fe.db.Spans[spanID]
			if !ok || !span.Passthrough {
				// pass the lock to the last most-valid span
				break
			}

			newLock = span.SpanContext().SpanID()

			if spanID == fe.lastContextLock {
				// don't accidentally lock further in the context than we were before
				break
			}
		}
		fe.lastContextLock = newLock
	}

	for _, row := range logsView.Body {
		fe.renderRow(row)
	}
}

func (fe *frontendPlain) finalRender() {
	if fe.Debug || fe.Verbosity > 0 {
		fe.render()
	}

	fe.mu.Lock()
	defer fe.mu.Unlock()
	renderPrimaryOutput(fe.db)
}

func (fe *frontendPlain) renderRow(row *TraceRow) {
	if !fe.shouldShow(fe.FrontendOpts, row) && !fe.Debug {
		return
	}

	span := row.Span
	spanDt := fe.data[span.SpanContext().SpanID()]
	if !spanDt.ready {
		// don't render! this span hasn't been exported yet
		return
	}

	if !spanDt.started {
		// render! this span has just started
		depth, ok := fe.renderContext(row)
		if !ok {
			return
		}
		fe.renderStep(span, depth, false)
		fe.renderLogs(span, depth)
		spanDt.started = true
	}

	// render all the children - it's important that we render the children
	// details first to avoid unnecessary context switches
	for _, child := range row.Children {
		fe.renderRow(child)
	}

	if len(spanDt.logs) > 0 {
		lastVertex := fe.lastVertex()
		depth, ok := fe.renderContext(row)
		if !ok {
			return
		}
		if row.Span.SpanContext().SpanID() != lastVertex {
			fe.renderStep(span, depth, spanDt.ended)
		}
		fe.renderLogs(span, depth)
	}
	if !spanDt.ended && !row.IsRunning {
		// render! this span has finished
		// this renders last, so that we have the chance to render logs and
		// finished children first - this ensures we get a LIFO structure
		// to the logs which makes them easier to read
		depth, ok := fe.renderContext(row)
		if !ok {
			return
		}
		fe.renderStep(span, depth, true)
		spanDt.ended = true

		// nothing else *should* happen with this step, so we can switch
		// context to the parent
		if row.Parent == nil {
			fe.lastContextLock = trace.SpanID{}
		} else {
			fe.lastContextLock = row.Parent.Span.SpanContext().SpanID()
		}
	}
}

func (fe *frontendPlain) renderStep(span *Span, depth int, done bool) {
	spanDt := fe.data[span.SpanContext().SpanID()]
	if spanDt.idx == 0 {
		fe.idx++
		spanDt.idx = fe.idx
	}

	r := renderer{db: fe.db, width: -1}

	prefix := fe.output.String(fmt.Sprintf("%-4d: ", spanDt.idx)).Foreground(termenv.ANSIBrightMagenta).String()
	if span.Call != nil {
		call := &callpbv1.Call{
			Field:          span.Call.Field,
			Args:           span.Call.Args,
			Type:           span.Call.Type,
			ReceiverDigest: span.Call.ReceiverDigest,
		}
		if done {
			call.Args = nil
		}
		r.renderCall(fe.output, nil, call, prefix, depth, false)
	} else {
		r.renderVertex(fe.output, nil, span.Name(), prefix, depth)
	}
	if done {
		if span.Status().Code == codes.Error {
			fmt.Fprint(fe.output, fe.output.String(" ERROR").Foreground(termenv.ANSIYellow))
		} else {
			fmt.Fprint(fe.output, fe.output.String(" DONE").Foreground(termenv.ANSIGreen))
		}
		duration := fmtDuration(span.EndTime().Sub(span.StartTime()))
		fmt.Fprint(fe.output, fe.output.String(fmt.Sprintf(" [%s]", duration)).Foreground(termenv.ANSIBrightBlack))

		if span.Status().Code == codes.Error && span.Status().Description != "" {
			fmt.Fprintln(fe.output)
			fmt.Fprint(fe.output, prefix)
			r.indent(fe.output, depth)
			// print error description above it
			fmt.Fprintf(fe.output,
				fe.output.String("! %s").Foreground(termenv.ANSIYellow).String(),
				span.Status().Description,
			)
		}
	}
	fmt.Fprintln(fe.output)
}

func (fe *frontendPlain) renderLogs(span *Span, depth int) {
	out := fe.output

	spanDt := fe.data[span.SpanContext().SpanID()]

	r := renderer{db: fe.db, width: -1}

	for _, logLine := range spanDt.logs {
		fmt.Fprint(out, out.String(fmt.Sprintf("%-4d: ", spanDt.idx)).Foreground(termenv.ANSIBrightMagenta))
		r.indent(fe.output, depth)
		duration := fmtDuration(logLine.time.Sub(span.StartTime()))
		fmt.Fprint(out, out.String(fmt.Sprintf("[%s] ", duration)).Foreground(termenv.ANSIBrightBlack))
		pipe := out.String("|").Foreground(termenv.ANSIBrightBlack)
		fmt.Fprintln(out, pipe, logLine.line)
	}
	spanDt.logs = nil
}

func (fe *frontendPlain) renderContext(row *TraceRow) (int, bool) {
	if row.Span.SpanContext().SpanID() == fe.lastVertex() {
		// this is the last vertex we rendered, we're already in the right context
		return fe.lastContextDepth, true
	}

	// determine the current context
	switchContext := fe.lastContextLock.IsValid()
	currentContext := []*TraceRow{}
	for parent := row; parent != nil; parent = parent.Parent {
		if switchContext && parent.Span.SpanContext().SpanID() == fe.lastContextLock {
			// this span is a child to the last context
			switchContext = false
		}
		currentContext = append(currentContext, parent)
	}
	slices.Reverse(currentContext)

	now := time.Now()
	if switchContext {
		// this context is not directly related to the last one, so we need to
		// context-switch
		if now.Sub(fe.lastContextTime) < fe.contextHold {
			// another context still has an exclusive hold
			return 0, false
		}
	}

	// insert whitespace when changing top-most context span
	if len(fe.lastContext) > 0 && len(currentContext) > 0 && currentContext[0].Span.SpanContext().SpanID() != fe.lastContext[0] {
		fmt.Fprintln(fe.output)
	}

	// render the context
	depth := 0
	for _, i := range sampleContext(currentContext) {
		call := currentContext[i]

		show := true
		if i < len(fe.lastContext) {
			show = call.Span.SpanContext().SpanID() != fe.lastContext[i]
		}
		if show {
			fe.renderStep(call.Span, depth, false)
		}
		depth += 1
	}

	fe.lastContext = make([]trace.SpanID, 0, len(currentContext))
	for _, row := range currentContext {
		fe.lastContext = append(fe.lastContext, row.Span.SpanContext().SpanID())
	}
	fe.lastContextLock = fe.lastVertex()
	fe.lastContextDepth = depth
	fe.lastContextTime = now
	return depth, true
}

func (fe *frontendPlain) lastVertex() trace.SpanID {
	if len(fe.lastContext) == 0 {
		return trace.SpanID{}
	}
	return fe.lastContext[len(fe.lastContext)-1]
}

// sampleContext selects vertices from a row context to display
func sampleContext(rows []*TraceRow) []int {
	if len(rows) == 0 {
		return nil
	}

	// don't ever sample the current row
	rows = rows[:len(rows)-1]

	// NB: break glass for all the context
	// all := make([]int, len(rows))
	// for i := range rows {
	// 	all[i] = i
	// }
	// return all

	// find the first vertex
	first := -1
	if len(rows) > 0 {
		first = 0
	}
	// iterate backwards to find the last call
	last := -1
	for i := len(rows) - 1; i > first; i-- {
		row := rows[i]
		if row.Span.Call != nil {
			last = i
			break
		}
	}

	switch {
	case first == -1:
		return []int{}
	case last == -1:
		return []int{first}
	default:
		return []int{first, last}
	}
}
