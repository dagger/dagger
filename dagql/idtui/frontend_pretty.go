package idtui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/codes"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"

	"github.com/dagger/dagger/engine/slog"
)

type frontendPretty struct {
	FrontendOpts

	// updated by Run
	program     *tea.Program
	run         func(context.Context) error
	runCtx      context.Context
	interrupt   func()
	interrupted bool
	done        bool
	err         error

	// updated as events are written
	db           *DB
	logs         *prettyLogs
	eof          bool
	backgrounded bool
	autoFocus    bool
	focused      trace.SpanID
	zoomed       trace.SpanID
	focusedIdx   int
	rows         []*TraceRow
	rowsView     *RowsView

	// panels
	logsPanel *panel
	msgsPanel *panel

	// TUI state/config
	restore func()  // restore terminal
	fps     float64 // frames per second
	profile termenv.Profile
	window  tea.WindowSizeMsg // set by BubbleTea
	view    *strings.Builder  // rendered async

	// held to synchronize tea.Model with updates
	mu sync.Mutex
}

type panel struct {
	*termenv.Output
	vterm *Vterm
	buf   *strings.Builder
}

func newPanel(profile termenv.Profile) *panel {
	vterm := NewVterm()
	buf := new(strings.Builder)
	return &panel{
		Output: NewOutput(io.MultiWriter(vterm, buf), termenv.WithProfile(profile)),
		vterm:  vterm,
		buf:    buf,
	}
}

func New() Frontend {
	db := NewDB()

	profile := ColorProfile()

	return &frontendPretty{
		db:   db,
		logs: newPrettyLogs(),

		autoFocus: true,

		window:    tea.WindowSizeMsg{Width: -1, Height: -1}, // be clear that it's not set
		fps:       30,                                       // sane default, fine-tune if needed
		profile:   profile,
		view:      new(strings.Builder),
		logsPanel: newPanel(profile),
		msgsPanel: newPanel(profile),
	}
}

func (fe *frontendPretty) ConnectedToEngine(ctx context.Context, name string, version string, clientID string) {
	// noisy, so suppress this for now
}

func (fe *frontendPretty) ConnectedToCloud(ctx context.Context, url string, msg string) {
	out := NewOutput(nil, termenv.WithProfile(fe.profile))
	fmt.Fprintln(fe.msgsPanel, traceMessage(out, url, msg))
}

func traceMessage(out *termenv.Output, url string, msg string) string {
	buffer := &bytes.Buffer{}

	fmt.Fprint(buffer, out.String("Full trace at ").Bold().String())
	if out.Profile == termenv.Ascii {
		fmt.Fprint(buffer, url)
	} else {
		fmt.Fprint(buffer, out.Hyperlink(url, url))
	}
	if msg != "" {
		fmt.Fprintf(buffer, " (%s)", msg)
	}

	return buffer.String()
}

// Run starts the TUI, calls the run function, stops the TUI, and finally
// prints the primary output to the appropriate stdout/stderr streams.
func (fe *frontendPretty) Run(ctx context.Context, opts FrontendOpts, run func(context.Context) error) error {
	if opts.TooFastThreshold == 0 {
		opts.TooFastThreshold = 100 * time.Millisecond
	}
	if opts.GCThreshold == 0 {
		opts.GCThreshold = 1 * time.Second
	}
	fe.FrontendOpts = opts

	// redirect slog to the logs pane
	level := slog.LevelInfo
	if fe.Debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.PrettyLogger(fe.logsPanel, fe.profile, level))

	// find a TTY anywhere in stdio. stdout might be redirected, in which case we
	// can show the TUI on stderr.
	ttyIn, ttyOut := findTTYs()

	var runErr error
	if fe.Silent {
		// no TTY found; set a reasonable screen size for logs, and just run the
		// function
		fe.SetWindowSize(tea.WindowSizeMsg{
			Width:  300, // influences vterm width
			Height: 100, // theoretically noop, since we always render full logs
		})
		runErr = run(ctx)
	} else {
		// run the TUI until it exits and cleans up the TTY
		runErr = fe.runWithTUI(ctx, ttyIn, ttyOut, run)
	}

	// print the final output display to stderr
	if renderErr := fe.finalRender(); renderErr != nil {
		return renderErr
	}

	// return original err
	return runErr
}

func (fe *frontendPretty) SetPrimary(spanID trace.SpanID) {
	fe.mu.Lock()
	fe.db.PrimarySpan = spanID
	fe.mu.Unlock()
}

func (fe *frontendPretty) runWithTUI(ctx context.Context, ttyIn *os.File, ttyOut *os.File, run func(context.Context) error) error {
	var stdin io.Reader
	if ttyIn != nil {
		stdin = ttyIn

		// Bubbletea will just receive an `io.Reader` for its input rather than the
		// raw TTY *os.File, so we need to set up the TTY ourselves.
		ttyFd := int(ttyIn.Fd())
		oldState, err := term.MakeRaw(ttyFd)
		if err != nil {
			return err
		}
		fe.restore = func() { _ = term.Restore(ttyFd, oldState) }
		defer fe.restore()
	}

	// wire up the run so we can call it asynchronously with the TUI running
	fe.run = run
	// set up ctx cancellation so the TUI can interrupt via keypresses
	fe.runCtx, fe.interrupt = context.WithCancel(ctx)

	// keep program state so we can send messages to it
	fe.program = tea.NewProgram(fe,
		tea.WithInput(stdin),
		tea.WithOutput(ttyOut),
		// We set up the TTY ourselves, so Bubbletea's panic handler becomes
		// counter-productive.
		tea.WithoutCatchPanics(),
	)

	// run the program, which starts the callback async
	if _, err := fe.program.Run(); err != nil {
		return err
	}

	// if the ctx was canceled, we don't need to return whatever random garbage
	// error string we got back; just return the ctx err.
	if fe.runCtx.Err() != nil {
		return fe.runCtx.Err()
	}

	// return the run err result
	return fe.err
}

// finalRender is called after the program has finished running and prints the
// final output after the TUI has exited.
func (fe *frontendPretty) finalRender() error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	out := NewOutput(os.Stderr, termenv.WithProfile(fe.profile))

	if fe.Debug || fe.Verbosity > 0 || fe.err != nil {
		if fe.msgsPanel.buf.Len() > 0 {
			fmt.Fprintln(out, fe.msgsPanel.buf.String())
		}
	}

	if fe.logsPanel.buf.Len() > 0 {
		fmt.Fprintln(out, fe.logsPanel.buf.String())
	}

	if fe.Debug || fe.Verbosity > 0 || fe.err != nil {
		if renderedAny, err := fe.renderProgress(out, true); err != nil {
			return err
		} else if renderedAny {
			fmt.Fprintln(out)
		}
	}

	return renderPrimaryOutput(fe.db)
}

func (fe *frontendPretty) renderPanel(out *termenv.Output, panel *panel, full bool) error {
	if panel.vterm.UsedHeight() == 0 {
		return nil
	}
	if full {
		panel.vterm.SetHeight(fe.logsPanel.vterm.UsedHeight())
	} else {
		panel.vterm.SetHeight(10)
	}
	_, err := fmt.Fprintln(out, panel.vterm.View())
	return err
}

func (fe *frontendPretty) SpanExporter() sdktrace.SpanExporter {
	return FrontendSpanExporter{fe}
}

type FrontendSpanExporter struct {
	*frontendPretty
}

func (fe FrontendSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	defer fe.recalculateViewLocked() // recalculate view *after* updating the db
	slog.Debug("frontend exporting spans", "spans", len(spans))
	return fe.db.ExportSpans(ctx, spans)
}

func (fe *frontendPretty) Shutdown(ctx context.Context) error {
	if err := fe.db.Shutdown(ctx); err != nil {
		return err
	}
	return fe.Close()
}

func (fe *frontendPretty) LogExporter() sdklog.Exporter {
	return prettyLogExporter{fe}
}

type prettyLogExporter struct {
	*frontendPretty
}

func (fe prettyLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	slog.Debug("frontend exporting logs", "logs", len(logs))
	if err := fe.db.LogExporter().Export(ctx, logs); err != nil {
		return err
	}
	return fe.logs.Export(ctx, logs)
}

type eofMsg struct{}

func (fe *frontendPretty) ForceFlush(context.Context) error {
	return nil
}

func (fe *frontendPretty) Close() error {
	if fe.program != nil {
		fe.program.Send(eofMsg{})
	}
	return nil
}

type backgroundMsg struct {
	cmd  tea.ExecCommand
	errs chan<- error
}

func (fe *frontendPretty) Background(cmd tea.ExecCommand) error {
	errs := make(chan error, 1)
	fe.program.Send(backgroundMsg{
		cmd:  cmd,
		errs: errs,
	})
	return <-errs
}

func (fe *frontendPretty) Render(out *termenv.Output) error {
	if err := fe.renderPanel(out, fe.msgsPanel, false); err != nil {
		return err
	}
	if err := fe.renderPanel(out, fe.logsPanel, false); err != nil {
		return err
	}
	if _, err := fe.renderProgress(out, false); err != nil {
		return err
	}
	return nil
}

func (fe *frontendPretty) recalculateViewLocked() {
	tree := CollectTree(fe.db.SpanOrder)
	fe.rowsView = CollectRowsView(tree)
}

type lineCountingWriter struct {
	io.Writer
	lines int
	max   int
}

func (w *lineCountingWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			w.lines++
		}
	}
	return w.Writer.Write(p)
}

func (fe *frontendPretty) renderedRowLines(row *TraceRow) []string {
	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(fe.profile))
	_ = fe.renderRow(out, row)
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

func (fe *frontendPretty) renderProgress(out *termenv.Output, full bool) (bool, error) {
	var renderedAny bool
	if fe.rowsView == nil {
		return false, nil
	}
	buf := new(strings.Builder)
	lineCounter := &lineCountingWriter{Writer: buf}
	if !full {
		lineCounter.max = fe.window.Height
	}
	if fe.rowsView.Primary != nil && !fe.done {
		if renderedAny {
			fmt.Fprintln(out)
		}
		renderLogs := fe.renderLogs(out, fe.rowsView.Primary, -1)
		if renderLogs {
			fmt.Fprintln(out)
			renderedAny = true
		}
	}

	rows := fe.rowsView.Rows(fe.FrontendOpts)
	fe.rows = rows

	if !fe.autoFocus {
		// must be manually focused
		// NOTE: it's possible for the focused span to go away
		lostFocus := true
		for _, row := range rows {
			if row.Span.ID == fe.focused {
				fe.focusedIdx = row.Index
				lostFocus = false
				break
			}
		}
		if lostFocus {
			fe.autoFocus = true
		}
	}

	if len(rows) < fe.focusedIdx {
		// durability: everything disappeared?
		fe.autoFocus = true
	}

	if len(rows) == 0 {
		// NB: this is a bit redundant with above, but feels better to decouple
		return renderedAny, nil
	}

	if fe.autoFocus && len(rows) > 0 {
		fe.focusedIdx = len(rows) - 1
		fe.focused = rows[fe.focusedIdx].Span.ID
	}

	before, focused, after := rows[:fe.focusedIdx], rows[fe.focusedIdx], rows[fe.focusedIdx+1:]
	lines := fe.renderedRowLines(focused)
	contextLines := (fe.window.Height - lineCounter.lines - len(lines)) / 2

	beforeLines := []string{}
	for len(beforeLines) < contextLines && len(before) > 0 {
		row := before[len(before)-1]
		before = before[:len(before)-1]
		beforeLines = append(fe.renderedRowLines(row), beforeLines...)
		if len(beforeLines) >= contextLines {
			beforeLines = beforeLines[len(beforeLines)-contextLines:]
			break
		}
	}
	lines = append(beforeLines, lines...)

	afterLines := []string{}
	for len(afterLines) < contextLines && len(after) > 0 {
		row := after[0]
		after = after[1:]
		afterLines = append(afterLines, fe.renderedRowLines(row)...)
		if len(afterLines) >= contextLines {
			afterLines = afterLines[:contextLines]
			break
		}
	}
	lines = append(lines, afterLines...)

	//
	// for _, row := range rows[focusedIdx:] {
	// 	if err := fe.renderRow(outBuf, row); err != nil {
	// 		return renderedAny, err
	// 	}
	// 	renderedAny = true
	// 	if lineCounter.max > 0 && lineCounter.lines >= lineCounter.max {
	// 		break
	// 	}
	// }
	fmt.Fprint(out, buf.String()+strings.Join(lines, "\n"))
	return renderedAny, nil
}

func (fe *frontendPretty) focus(row *TraceRow) {
	spanID := row.Span.ID
	if spanID == fe.focused {
		return
	}
	fe.focused = spanID
	fe.focusedIdx = row.Index
}

func (fe *frontendPretty) Init() tea.Cmd {
	return tea.Batch(
		frame(fe.fps),
		fe.spawn,
	)
}

func (fe *frontendPretty) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	fe, cmd := fe.update(msg)
	cmds = append(cmds, cmd)

	// fe.viewport, cmd = fe.viewport.Update(msg)
	// cmds = append(cmds, cmd)
	//
	return fe, tea.Batch(cmds...)
}

func (fe *frontendPretty) View() string {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	if fe.backgrounded {
		// if we've been backgrounded, show nothing, so a user's shell session
		// doesn't have any garbage before/after
		return ""
	}
	if fe.done && fe.eof {
		// print nothing; make way for the pristine output in the final render
		return ""
	}
	return fe.view.String()
}

type doneMsg struct {
	err error
}

func (fe *frontendPretty) spawn() (msg tea.Msg) {
	defer func() {
		if r := recover(); r != nil {
			fe.restore()
			panic(r)
		}
	}()
	return doneMsg{fe.run(fe.runCtx)}
}

type backgroundDoneMsg struct{}

func (fe *frontendPretty) update(msg tea.Msg) (*frontendPretty, tea.Cmd) {
	switch msg := msg.(type) {
	case doneMsg: // run finished
		slog.Debug("run finished", "err", msg.err)
		fe.done = true
		fe.err = msg.err
		if fe.eof {
			return fe, tea.Quit
		}
		return fe, nil

	case eofMsg: // received end of updates
		slog.Debug("got EOF")
		fe.eof = true
		if fe.done {
			return fe, tea.Quit
		}
		return fe, nil

	case backgroundMsg:
		fe.backgrounded = true
		return fe, tea.Exec(msg.cmd, func(err error) tea.Msg {
			msg.errs <- err
			return backgroundDoneMsg{}
		})

	case backgroundDoneMsg:
		return fe, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			if fe.interrupted {
				slog.Warn("exiting immediately")
				return fe, tea.Quit
			} else {
				slog.Warn("canceling... (press again to exit immediately)")
			}
			fe.interrupt()
			fe.interrupted = true
			return fe, nil // tea.Quit is deferred until we receive doneMsg
		case "ctrl+\\": // SIGQUIT
			fe.restore()
			sigquit()
			return fe, nil
		case "a":
			fe.autoFocus = true
			return fe, nil
		// case "h", "left":
		case "j", "down":
			fe.autoFocus = false
			newIdx := fe.focusedIdx + 1
			if newIdx >= len(fe.rows) {
				// at bottom
				return fe, nil
			}
			fe.focus(fe.rows[newIdx])
			return fe, nil
		case "k", "up":
			fe.autoFocus = false
			newIdx := fe.focusedIdx - 1
			if newIdx < 0 || newIdx >= len(fe.rows) {
				return fe, nil
			}
			fe.focus(fe.rows[newIdx])
			return fe, nil
		// case "l", "right":
		case "home":
			if len(fe.rows) > 0 {
				fe.focus(fe.rows[0])
			}
			return fe, nil
		case "end":
			fe.autoFocus = true
			if len(fe.rows) > 0 {
				fe.focus(fe.rows[len(fe.rows)-1])
			}
			return fe, nil
		case "enter":
			fe.zoomed = fe.focused
			fe.recalculateViewLocked()
			return fe, nil
		default:
			return fe, nil
		}

	case tea.WindowSizeMsg:
		fe.SetWindowSize(msg)
		return fe, nil

	case frameMsg:
		fe.render()
		// NB: take care not to forward Frame downstream, since that will result
		// in runaway ticks. instead inner components should send a SetFpsMsg to
		// adjust the outermost layer.
		return fe, frame(fe.fps)

	default:
		return fe, nil
	}
}

func (fe *frontendPretty) SetWindowSize(msg tea.WindowSizeMsg) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	fe.window = msg
	fe.logs.SetWidth(msg.Width)
	fe.logsPanel.vterm.SetWidth(msg.Width)
	fe.msgsPanel.vterm.SetWidth(msg.Width)
}

func (fe *frontendPretty) render() {
	fe.mu.Lock()
	fe.view.Reset()
	fe.Render(NewOutput(fe.view, termenv.WithProfile(fe.profile)))
	fe.mu.Unlock()
}

func (fe *frontendPretty) renderRow(out *termenv.Output, row *TraceRow) error {
	fe.renderStep(out, row.Span, row.Depth)
	if row.IsRunningOrChildRunning {
		fe.renderLogs(out, row.Span, row.Depth+1) // HACK: extra depth to account for focus indicator
	}
	return nil
}

func (fe *frontendPretty) renderStep(out *termenv.Output, span *Span, depth int) error {
	r := newRenderer(fe.db, fe.window.Width, fe.FrontendOpts)

	isFocused := span.ID == fe.focused

	var prefix string
	if isFocused {
		prefix = termenv.String("▐ ").Foreground(termenv.ANSIYellow).String()
	} else {
		prefix = "  "
	}

	id := span.Call
	if id != nil {
		if err := r.renderCall(out, span, id, prefix, depth, false, span.Internal); err != nil {
			return err
		}
	} else if span != nil {
		if err := r.renderVertex(out, span, span.Name(), prefix, depth); err != nil {
			return err
		}
	}
	fmt.Fprintln(out)

	if span.Status().Code == codes.Error && span.Status().Description != "" {
		r.indent(out, depth+1) // HACK: +1 for focus prefix
		// print error description above it
		fmt.Fprintf(out,
			out.String("! %s\n").Foreground(termenv.ANSIYellow).String(),
			span.Status().Description,
		)
	}

	return nil
}

func (fe *frontendPretty) renderLogs(out *termenv.Output, span *Span, depth int) bool {
	if logs, ok := fe.logs.Logs[span.ID]; ok {
		pipe := out.String(VertBoldBar).Foreground(termenv.ANSIBrightBlack)
		if depth != -1 {
			logs.SetPrefix(strings.Repeat("  ", depth) + pipe.String() + " ")
		}
		logs.SetHeight(fe.window.Height / 3)
		fmt.Fprint(out, logs.View())
		return logs.UsedHeight() > 0
	}
	return false
}

type prettyLogs struct {
	Logs     map[trace.SpanID]*Vterm
	LogWidth int
}

func newPrettyLogs() *prettyLogs {
	return &prettyLogs{
		Logs:     make(map[trace.SpanID]*Vterm),
		LogWidth: -1,
	}
}

func (l *prettyLogs) Export(ctx context.Context, logs []sdklog.Record) error {
	for _, log := range logs {
		slog.Debug("exporting log", "span", log.SpanID, "body", log.Body().AsString())

		// render vterm for TUI
		_, _ = fmt.Fprint(l.spanLogs(log.SpanID()), log.Body().AsString())
	}
	return nil
}

func (l *prettyLogs) spanLogs(id trace.SpanID) *Vterm {
	term, found := l.Logs[id]
	if !found {
		term = NewVterm()
		if l.LogWidth > -1 {
			term.SetWidth(l.LogWidth)
		}
		l.Logs[id] = term
	}
	return term
}

func (l *prettyLogs) SetWidth(width int) {
	l.LogWidth = width
	for _, vt := range l.Logs {
		vt.SetWidth(width)
	}
}

func (l *prettyLogs) Shutdown(ctx context.Context) error {
	return nil
}

func findTTYs() (in *os.File, out *os.File) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		in = os.Stdin
	}
	for _, f := range []*os.File{os.Stderr, os.Stdout} {
		if term.IsTerminal(int(f.Fd())) {
			out = f
			break
		}
	}
	return
}

type frameMsg time.Time

func frame(fps float64) tea.Cmd {
	return tea.Tick(time.Duration(float64(time.Second)/fps), func(t time.Time) tea.Msg {
		return frameMsg(t)
	})
}
