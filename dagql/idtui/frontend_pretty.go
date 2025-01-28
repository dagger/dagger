package idtui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/pkg/browser"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
)

type frontendPretty struct {
	dagui.FrontendOpts

	// don't show live progress; just print a full report at the end
	reportOnly bool

	// updated by Run
	program     *tea.Program
	run         func(context.Context) error
	runCtx      context.Context
	interrupt   context.CancelCauseFunc
	interrupted bool
	quitting    bool
	done        bool
	err         error

	// updated as events are written
	db           *dagui.DB
	logs         *prettyLogs
	eof          bool
	backgrounded bool
	autoFocus    bool
	debugged     dagui.SpanID
	focusedIdx   int
	rowsView     *dagui.RowsView
	rows         *dagui.Rows
	pressedKey   string
	pressedKeyAt time.Time

	// set when authenticated to Cloud
	cloudURL string

	// TUI state/config
	fps        float64 // frames per second
	profile    termenv.Profile
	window     tea.WindowSizeMsg // set by BubbleTea
	view       *strings.Builder  // rendered async
	viewOut    *termenv.Output
	browserBuf *strings.Builder // logs if browser fails
	stdin      io.Reader        // used by backgroundMsg for running terminal

	// held to synchronize tea.Model with updates
	mu sync.Mutex

	// messages to print before the final render
	msgPreFinalRender strings.Builder
}

func NewPretty() Frontend {
	return NewWithDB(dagui.NewDB())
}

func NewReporter() Frontend {
	fe := NewWithDB(dagui.NewDB())
	fe.reportOnly = true
	return fe
}

func NewWithDB(db *dagui.DB) *frontendPretty {
	profile := ColorProfile()
	view := new(strings.Builder)
	return &frontendPretty{
		db:        db,
		logs:      newPrettyLogs(),
		autoFocus: true,

		// set empty initial row state to avoid nil checks
		rowsView: &dagui.RowsView{},
		rows:     &dagui.Rows{BySpan: map[dagui.SpanID]*dagui.TraceRow{}},

		// initial TUI state
		window:     tea.WindowSizeMsg{Width: -1, Height: -1}, // be clear that it's not set
		fps:        30,                                       // sane default, fine-tune if needed
		profile:    profile,
		view:       view,
		viewOut:    NewOutput(view, termenv.WithProfile(profile)),
		browserBuf: new(strings.Builder),
	}
}

func (fe *frontendPretty) ConnectedToEngine(ctx context.Context, name string, version string, clientID string) {
	// noisy, so suppress this for now
}

func (fe *frontendPretty) SetCloudURL(ctx context.Context, url string, msg string, logged bool) {
	if fe.OpenWeb {
		if err := browser.OpenURL(url); err != nil {
			slog.Warn("failed to open URL", "url", url, "err", err)
		}
	}
	fe.mu.Lock()
	fe.cloudURL = url
	if msg != "" {
		slog.Warn(msg)
	}

	if cmdContext, ok := FromCmdContext(ctx); ok && cmdContext.printTraceLink {
		if logged {
			fe.msgPreFinalRender.WriteString(traceMessage(fe.profile, url, msg))
		} else if !skipLoggedOutTraceMsg() {
			fe.msgPreFinalRender.WriteString(fmt.Sprintf(loggedOutTraceMsg, url))
		}
	}

	fe.mu.Unlock()
}

func traceMessage(profile termenv.Profile, url string, msg string) string {
	buffer := &bytes.Buffer{}
	out := NewOutput(buffer, termenv.WithProfile(profile))

	fmt.Fprint(buffer, out.String("Full trace at ").Bold().String())
	fmt.Fprint(buffer, url)
	if msg != "" {
		fmt.Fprintf(buffer, " (%s)", msg)
	}

	return buffer.String()
}

// Run starts the TUI, calls the run function, stops the TUI, and finally
// prints the primary output to the appropriate stdout/stderr streams.
func (fe *frontendPretty) Run(ctx context.Context, opts dagui.FrontendOpts, run func(context.Context) error) error {
	if opts.TooFastThreshold == 0 {
		opts.TooFastThreshold = 100 * time.Millisecond
	}
	if opts.GCThreshold == 0 {
		opts.GCThreshold = 1 * time.Second
	}
	fe.FrontendOpts = opts

	if fe.reportOnly {
		fe.err = run(ctx)
	} else {
		// run the function wrapped in the TUI
		fe.err = fe.runWithTUI(ctx, run)
	}

	// print the final output display to stderr
	if renderErr := fe.FinalRender(os.Stderr); renderErr != nil {
		return renderErr
	}

	fe.db.WriteDot(opts.DotOutputFilePath, opts.DotFocusField, opts.DotShowInternal)

	// return original err
	return fe.err
}

func (fe *frontendPretty) Opts() *dagui.FrontendOpts {
	return &fe.FrontendOpts
}

func (fe *frontendPretty) SetCustomExit(fn func()) {
	fe.mu.Lock()
	fe.Opts().CustomExit = fn
	fe.mu.Unlock()
}

func (fe *frontendPretty) SetVerbosity(n int) {
	fe.mu.Lock()
	fe.Opts().Verbosity = n
	fe.mu.Unlock()
}

func (fe *frontendPretty) SetPrimary(spanID dagui.SpanID) {
	fe.mu.Lock()
	fe.db.SetPrimarySpan(spanID)
	fe.ZoomedSpan = spanID
	fe.FocusedSpan = spanID
	fe.recalculateViewLocked()
	fe.mu.Unlock()
}

func (fe *frontendPretty) RevealAllSpans() {
	fe.mu.Lock()
	fe.ZoomedSpan = dagui.SpanID{}
	fe.mu.Unlock()
}

func (fe *frontendPretty) runWithTUI(ctx context.Context, run func(context.Context) error) error {
	// wire up the run so we can call it asynchronously with the TUI running
	fe.run = run
	// set up ctx cancellation so the TUI can interrupt via keypresses
	fe.runCtx, fe.interrupt = context.WithCancelCause(ctx)

	opts := []tea.ProgramOption{
		tea.WithMouseCellMotion(),
	}

	in, out := findTTYs()
	if in == nil {
		tty, err := openInputTTY()
		if err != nil {
			return err
		}
		if tty != nil {
			in = tty
			defer tty.Close()
		}
	}
	opts = append(opts, tea.WithInput(in))
	// store in fe to use in backgroundMsg processing
	// which is used for terminal command
	fe.stdin = in

	if out != nil {
		opts = append(opts, tea.WithOutput(out))
	}

	// keep program state so we can send messages to it
	fe.program = tea.NewProgram(fe, opts...)

	// prevent browser.OpenURL from breaking the TUI if it fails
	browser.Stdout = fe.browserBuf
	browser.Stderr = fe.browserBuf

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

func (fe *frontendPretty) renderErrorLogs(out *termenv.Output, r *renderer) bool {
	if fe.rowsView == nil {
		return false
	}
	rowsView := fe.db.RowsView(dagui.FrontendOpts{
		ZoomedSpan: fe.db.PrimarySpan,
		Verbosity:  dagui.ShowCompletedVerbosity,
	})
	errTree := fe.db.CollectErrors(rowsView)
	var anyHasLogs bool
	dagui.WalkTree(errTree, func(row *dagui.TraceTree, _ int) bool {
		logs := fe.logs.Logs[row.Span.ID]
		if logs != nil && logs.UsedHeight() > 0 {
			anyHasLogs = true
			return true
		}
		return false
	})
	if anyHasLogs {
		fmt.Fprintln(out)
		fmt.Fprintln(out, out.String("Error logs:").Bold())
	}
	dagui.WalkTree(errTree, func(tree *dagui.TraceTree, _ int) bool {
		logs := fe.logs.Logs[tree.Span.ID]
		if logs != nil && logs.UsedHeight() > 0 {
			fmt.Fprintln(out)
			fe.renderStep(out, r, tree.Span, tree.Chained, 0, "")
			fe.renderLogs(out, r, logs, -1, logs.UsedHeight(), "")
			fe.renderStepError(out, r, tree.Span, 0, "")
		}
		return false
	})
	return len(errTree) > 0
}

// FinalRender is called after the program has finished running and prints the
// final output after the TUI has exited.
func (fe *frontendPretty) FinalRender(w io.Writer) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	// Render the full trace.
	fe.ZoomedSpan = fe.db.PrimarySpan
	if fe.reportOnly && fe.Verbosity < dagui.ExpandCompletedVerbosity {
		fe.Verbosity = dagui.ExpandCompletedVerbosity
	}
	fe.recalculateViewLocked()

	// Unfocus for the final render.
	fe.FocusedSpan = dagui.SpanID{}

	r := newRenderer(fe.db, fe.window.Width, fe.FrontendOpts)

	out := NewOutput(w, termenv.WithProfile(fe.profile))

	if fe.Debug || fe.Verbosity >= dagui.ShowCompletedVerbosity || fe.err != nil {
		fe.renderProgress(out, r, true, fe.window.Height, "")

		if fe.msgPreFinalRender.Len() > 0 {
			defer func() {
				fmt.Fprintln(os.Stderr)
				fmt.Fprintln(os.Stderr, fe.msgPreFinalRender.String())
			}()
		}
	}

	// If there are errors, show log output.
	if fe.err != nil {
		// Counter-intuitively, we don't want to render the primary output
		// when there's an error, because the error is better represented by
		// the progress output and error summary.
		if fe.renderErrorLogs(out, r) {
			return nil
		}
	}

	// Replay the primary output log to stdout/stderr.
	return renderPrimaryOutput(fe.db)
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

func (fe *frontendPretty) MetricExporter() sdkmetric.Exporter {
	return FrontendMetricExporter{fe}
}

type FrontendMetricExporter struct {
	*frontendPretty
}

func (fe FrontendMetricExporter) Export(ctx context.Context, resourceMetrics *metricdata.ResourceMetrics) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	return fe.db.MetricExporter().Export(ctx, resourceMetrics)
}

func (fe FrontendMetricExporter) Temporality(ik sdkmetric.InstrumentKind) metricdata.Temporality {
	return fe.db.Temporality(ik)
}

func (fe FrontendMetricExporter) Aggregation(ik sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return fe.db.Aggregation(ik)
}

func (fe FrontendMetricExporter) ForceFlush(context.Context) error {
	return nil
}

type backgroundMsg struct {
	cmd  tea.ExecCommand
	raw  bool
	errs chan<- error
}

func (fe *frontendPretty) Background(cmd tea.ExecCommand, raw bool) error {
	errs := make(chan error, 1)
	fe.program.Send(backgroundMsg{
		cmd:  cmd,
		raw:  raw,
		errs: errs,
	})
	return <-errs
}

var KeymapStyle = lipgloss.NewStyle().
	Foreground(lipgloss.ANSIColor(termenv.ANSIBrightBlack))

func (fe *frontendPretty) renderKeymap(out *termenv.Output, style lipgloss.Style) int {
	w := new(strings.Builder)
	type keyHelp struct {
		label string
		keys  []string
		show  bool
	}
	var quitMsg string
	if fe.interrupted {
		quitMsg = "quit!"
	} else {
		quitMsg = "quit"
	}

	var showedKey bool
	// Blank line prior to keymap
	for _, key := range []keyHelp{
		{out.Hyperlink(fe.cloudURL, "web"), []string{"w"}, fe.cloudURL != ""},
		{"move", []string{"←↑↓→", "up", "down", "left", "right", "h", "j", "k", "l"}, true},
		{"first", []string{"home"}, true},
		{"last", []string{"end", " "}, true},
		{"zoom", []string{"enter"}, true},
		{"unzoom", []string{"esc"}, fe.ZoomedSpan.IsValid() &&
			fe.ZoomedSpan != fe.db.PrimarySpan},
		{fmt.Sprintf("verbosity=%d", fe.Verbosity), []string{"+/-", "+", "-"}, true},
		{quitMsg, []string{"q", "ctrl+c"}, true},
	} {
		if !key.show {
			continue
		}
		mainKey := key.keys[0]
		if showedKey {
			fmt.Fprint(w, style.Render("  "))
		}
		keyStyle := style
		if time.Since(fe.pressedKeyAt) < 500*time.Millisecond {
			for _, k := range key.keys {
				if k == fe.pressedKey {
					keyStyle = keyStyle.Foreground(nil)
					// Reverse(true)
				}
			}
		}
		fmt.Fprint(w, keyStyle.Bold(true).Render(mainKey))
		fmt.Fprint(w, keyStyle.Render(": "+key.label))
		showedKey = true
	}
	res := w.String()
	fmt.Fprint(out, res)
	return lipgloss.Width(res)
}

func (fe *frontendPretty) Render(out *termenv.Output) error {
	progHeight := fe.window.Height

	r := newRenderer(fe.db, fe.window.Width, fe.FrontendOpts)

	var progPrefix string
	if fe.rowsView != nil && fe.rowsView.Zoomed != nil && fe.rowsView.Zoomed.ID != fe.db.PrimarySpan {
		fe.renderStep(out, r, fe.rowsView.Zoomed, false, 0, "")
		progHeight -= 1
		progPrefix = "  "
	}

	below := new(strings.Builder)
	countOut := NewOutput(below, termenv.WithProfile(fe.profile))

	fmt.Fprint(countOut, KeymapStyle.Render(strings.Repeat(HorizBar, 1)))
	fmt.Fprint(countOut, KeymapStyle.Render(" "))
	fe.renderKeymap(countOut, KeymapStyle)
	fmt.Fprint(countOut, KeymapStyle.Render(" "))
	if rest := fe.window.Width - lipgloss.Width(below.String()); rest > 0 {
		fmt.Fprint(countOut, KeymapStyle.Render(strings.Repeat(HorizBar, rest)))
	}

	if logs := fe.logs.Logs[fe.ZoomedSpan]; logs != nil && logs.UsedHeight() > 0 {
		fmt.Fprintln(below)
		fe.renderLogs(countOut, r, logs, -1, fe.window.Height/3, progPrefix)
	}

	belowOut := strings.TrimRight(below.String(), "\n")
	progHeight -= lipgloss.Height(belowOut)

	fe.renderProgress(out, r, false, progHeight, progPrefix)
	fmt.Fprintln(out)

	fmt.Fprint(out, belowOut)
	return nil
}

func (fe *frontendPretty) recalculateViewLocked() {
	fe.rowsView = fe.db.RowsView(fe.FrontendOpts)
	fe.rows = fe.rowsView.Rows(fe.FrontendOpts)
	if len(fe.rows.Order) == 0 {
		fe.focusedIdx = -1
		fe.FocusedSpan = dagui.SpanID{}
		return
	}
	if len(fe.rows.Order) < fe.focusedIdx {
		// durability: everything disappeared?
		fe.autoFocus = true
	}
	if fe.autoFocus {
		fe.focusedIdx = len(fe.rows.Order) - 1
		fe.FocusedSpan = fe.rows.Order[fe.focusedIdx].Span.ID
	} else if row := fe.rows.BySpan[fe.FocusedSpan]; row != nil {
		fe.focusedIdx = row.Index
	} else {
		// lost focus somehow
		fe.autoFocus = true
		fe.recalculateViewLocked()
	}
}

func (fe *frontendPretty) renderedRowLines(r *renderer, row *dagui.TraceRow, prefix string) []string {
	buf := new(strings.Builder)
	out := NewOutput(buf, termenv.WithProfile(fe.profile))
	fe.renderRow(out, r, row, prefix)
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

func (fe *frontendPretty) renderProgress(out *termenv.Output, r *renderer, full bool, height int, prefix string) {
	if fe.rowsView == nil {
		return
	}

	rows := fe.rows

	if full {
		for _, row := range rows.Order {
			fe.renderRow(out, r, row, "")
		}
		return
	}

	lines := fe.renderLines(r, height, prefix)

	fmt.Fprint(out, strings.Join(lines, "\n"))
}

func (fe *frontendPretty) renderLines(r *renderer, height int, prefix string) []string {
	rows := fe.rows
	if len(rows.Order) == 0 {
		return []string{}
	}
	if fe.focusedIdx == -1 {
		fe.autoFocus = true
		fe.focusedIdx = len(rows.Order) - 1
	}

	before, focused, after :=
		rows.Order[:fe.focusedIdx],
		rows.Order[fe.focusedIdx],
		rows.Order[fe.focusedIdx+1:]

	beforeLines := []string{}
	focusedLines := fe.renderedRowLines(r, focused, prefix)
	afterLines := []string{}
	renderBefore := func() {
		row := before[len(before)-1]
		before = before[:len(before)-1]
		beforeLines = append(fe.renderedRowLines(r, row, prefix), beforeLines...)
	}
	renderAfter := func() {
		row := after[0]
		after = after[1:]
		afterLines = append(afterLines, fe.renderedRowLines(r, row, prefix)...)
	}
	totalLines := func() int {
		return len(beforeLines) + len(focusedLines) + len(afterLines)
	}

	// fill in context surrounding the focused row
	contextLines := (height - len(focusedLines))
	if contextLines <= 0 {
		// lines already meets/exceeds height, just show them
		return focusedLines
	}

	beforeTargetLines := contextLines / 2
	var afterTargetLines int
	if contextLines%2 == 0 {
		afterTargetLines = beforeTargetLines
	} else {
		afterTargetLines = beforeTargetLines + 1
	}
	for len(beforeLines) < beforeTargetLines && len(before) > 0 {
		renderBefore()
	}
	for len(afterLines) < afterTargetLines && len(after) > 0 {
		renderAfter()
	}

	if total := totalLines(); total > height {
		extra := total - height
		if len(beforeLines) >= beforeTargetLines && len(afterLines) >= afterTargetLines {
			// exceeded the height, so trim the context
			if len(beforeLines) > beforeTargetLines {
				beforeLines = beforeLines[len(beforeLines)-beforeTargetLines:]
			}
			if len(afterLines) > afterTargetLines {
				afterLines = afterLines[:afterTargetLines]
			}
		} else if len(beforeLines) >= beforeTargetLines {
			beforeLines = beforeLines[extra:]
		} else if len(afterLines) >= afterTargetLines {
			afterLines = afterLines[:len(afterLines)-extra]
		}
	} else {
		// fill in the rest of the screen if there's not enough to fill both sides
		for totalLines() < height && (len(before) > 0 || len(after) > 0) {
			switch {
			case len(before) > 0:
				renderBefore()
				if total := totalLines(); total > height {
					extra := total - height
					beforeLines = beforeLines[extra:]
				}
			case len(after) > 0:
				renderAfter()
				if total := totalLines(); total > height {
					extra := total - height
					afterLines = afterLines[:len(afterLines)-extra]
				}
			}
		}
	}

	// finally, print all the lines
	focusedLines = append(beforeLines, focusedLines...)
	focusedLines = append(focusedLines, afterLines...)
	return focusedLines
}

func (fe *frontendPretty) focus(row *dagui.TraceRow) {
	if row == nil {
		return
	}
	fe.FocusedSpan = row.Span.ID
	fe.focusedIdx = row.Index
	fe.recalculateViewLocked()
}

func (fe *frontendPretty) Init() tea.Cmd {
	return tea.Batch(
		frame(fe.fps),
		fe.spawn,
	)
}

func (fe *frontendPretty) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
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
	if fe.quitting {
		// print nothing; make way for the pristine output in the final render
		return ""
	}
	return fe.view.String()
}

type doneMsg struct {
	err error
}

func (fe *frontendPretty) spawn() (msg tea.Msg) {
	return doneMsg{fe.run(fe.runCtx)}
}

type backgroundDoneMsg struct {
	backgroundMsg
	err error
}

func (fe *frontendPretty) update(msg tea.Msg) (*frontendPretty, tea.Cmd) { //nolint: gocyclo
	switch msg := msg.(type) {
	case doneMsg: // run finished
		slog.Debug("run finished", "err", msg.err)
		fe.done = true
		fe.err = msg.err
		if fe.eof && !fe.NoExit {
			fe.quitting = true
			return fe, tea.Quit
		}
		return fe, nil

	case eofMsg: // received end of updates
		slog.Debug("got EOF")
		fe.eof = true
		if fe.done && !fe.NoExit {
			fe.quitting = true
			return fe, tea.Quit
		}
		return fe, nil

	case backgroundMsg:
		fe.backgrounded = true
		cmd := msg.cmd

		if msg.raw {
			var restore = func() error { return nil }
			cmd = &wrapCommand{
				ExecCommand: cmd,
				before: func() error {
					if stdin, ok := fe.stdin.(*os.File); ok {
						oldState, err := term.MakeRaw(int(stdin.Fd()))
						if err != nil {
							return err
						}
						restore = func() error { return term.Restore(int(stdin.Fd()), oldState) }
					}

					return nil
				},
				after: func() error {
					return restore()
				},
			}
		}
		return fe, tea.Exec(cmd, func(err error) tea.Msg {
			return backgroundDoneMsg{msg, err}
		})

	case backgroundDoneMsg:
		fe.backgrounded = false
		msg.errs <- msg.err
		return fe, nil

	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelDown:
			fe.goDown()
			fe.pressedKey = "down"
			fe.pressedKeyAt = time.Now()
		case tea.MouseButtonWheelUp:
			fe.goUp()
			fe.pressedKey = "up"
			fe.pressedKeyAt = time.Now()
		}
		return fe, nil

	case tea.KeyMsg:
		lastKey := fe.pressedKey
		fe.pressedKey = msg.String()
		fe.pressedKeyAt = time.Now()
		switch msg.String() {
		case "q", "ctrl+c":
			if fe.CustomExit != nil {
				fe.CustomExit()
				return fe, nil
			}

			if fe.done && fe.eof {
				fe.quitting = true
				// must have configured NoExit, and now they want
				// to exit manually
				return fe, tea.Quit
			}
			if fe.interrupted {
				slog.Warn("exiting immediately")
				fe.quitting = true
				return fe, tea.Quit
			} else {
				slog.Warn("canceling... (press again to exit immediately)")
			}
			fe.interrupted = true
			fe.interrupt(errors.New("interrupted"))
			return fe, nil // tea.Quit is deferred until we receive doneMsg
		case "ctrl+\\": // SIGQUIT
			fe.program.ReleaseTerminal()
			sigquit()
			return fe, nil
		case "down", "j":
			fe.goDown()
			return fe, nil
		case "up", "k":
			fe.goUp()
			return fe, nil
		case "left", "h":
			fe.goOut()
			return fe, nil
		case "right", "l":
			fe.goIn()
			return fe, nil
		case "home":
			fe.goStart()
			return fe, nil
		case "end", "G", " ":
			fe.goEnd()
			fe.pressedKey = "end"
			fe.pressedKeyAt = time.Now()
			return fe, nil
		case "esc":
			fe.ZoomedSpan = fe.db.PrimarySpan
			fe.recalculateViewLocked()
			return fe, nil
		case "+", "=":
			fe.FrontendOpts.Verbosity++
			fe.recalculateViewLocked()
			return fe, nil
		case "-":
			fe.FrontendOpts.Verbosity--
			if fe.FrontendOpts.Verbosity < 0 {
				fe.FrontendOpts.Verbosity = 0
			}
			fe.recalculateViewLocked()
			return fe, nil
		case "w":
			if fe.cloudURL == "" {
				return fe, nil
			}
			url := fe.cloudURL
			if fe.ZoomedSpan.IsValid() && fe.ZoomedSpan != fe.db.PrimarySpan {
				url += "?span=" + fe.ZoomedSpan.String()
			}
			if fe.FocusedSpan.IsValid() && fe.FocusedSpan != fe.db.PrimarySpan {
				url += "#" + fe.FocusedSpan.String()
			}
			return fe, func() tea.Msg {
				if err := browser.OpenURL(url); err != nil {
					slog.Warn("failed to open URL",
						"url", url,
						"err", err,
						"output", fe.browserBuf.String())
				}
				return nil
			}
		case "?":
			fe.debugged = fe.FocusedSpan
			return fe, nil
		case "enter":
			fe.ZoomedSpan = fe.FocusedSpan
			fe.recalculateViewLocked()
			return fe, nil
		}

		switch lastKey { //nolint:gocritic
		case "g":
			switch msg.String() { //nolint:gocritic
			case "g":
				fe.goStart()
				fe.pressedKey = "home"
				fe.pressedKeyAt = time.Now()
				return fe, nil
			}
		}

		return fe, nil
	case tea.WindowSizeMsg:
		fe.setWindowSizeLocked(msg)
		return fe, nil

	case frameMsg:
		fe.renderLocked()
		// NB: take care not to forward Frame downstream, since that will result
		// in runaway ticks. instead inner components should send a SetFpsMsg to
		// adjust the outermost layer.
		return fe, frame(fe.fps)

	default:
		return fe, nil
	}
}

func (fe *frontendPretty) goStart() {
	fe.autoFocus = false
	if len(fe.rows.Order) > 0 {
		fe.focus(fe.rows.Order[0])
	}
}

func (fe *frontendPretty) goEnd() {
	fe.autoFocus = true
	if len(fe.rows.Order) > 0 {
		fe.focus(fe.rows.Order[len(fe.rows.Order)-1])
	}
}

func (fe *frontendPretty) goUp() {
	fe.autoFocus = false
	newIdx := fe.focusedIdx - 1
	if newIdx < 0 || newIdx >= len(fe.rows.Order) {
		return
	}
	fe.focus(fe.rows.Order[newIdx])
}

func (fe *frontendPretty) goDown() {
	fe.autoFocus = false
	newIdx := fe.focusedIdx + 1
	if newIdx >= len(fe.rows.Order) {
		// at bottom
		return
	}
	fe.focus(fe.rows.Order[newIdx])
}

func (fe *frontendPretty) goOut() {
	fe.autoFocus = false
	focused := fe.db.Spans.Map[fe.FocusedSpan]
	if focused == nil {
		return
	}
	parent := focused.VisibleParent(fe.FrontendOpts)
	if parent == nil {
		return
	}
	fe.FocusedSpan = parent.ID
	// targeted the zoomed span; zoom on its parent instead
	if fe.FocusedSpan == fe.ZoomedSpan {
		zoomedParent := parent.VisibleParent(fe.FrontendOpts)
		if zoomedParent != nil {
			fe.ZoomedSpan = zoomedParent.ID
		}
	}
	fe.recalculateViewLocked()
}

func (fe *frontendPretty) goIn() {
	fe.autoFocus = false
	newIdx := fe.focusedIdx + 1
	if newIdx >= len(fe.rows.Order) {
		// at bottom
		return
	}
	cur := fe.rows.Order[fe.focusedIdx]
	next := fe.rows.Order[newIdx]
	if next.Depth <= cur.Depth {
		// has no children
		return
	}
	fe.focus(next)
}

func (fe *frontendPretty) setWindowSizeLocked(msg tea.WindowSizeMsg) {
	fe.window = msg
	fe.logs.SetWidth(msg.Width)
}

func (fe *frontendPretty) renderLocked() {
	fe.view.Reset()
	fe.Render(fe.viewOut)
}

func (fe *frontendPretty) renderRow(out *termenv.Output, r *renderer, row *dagui.TraceRow, prefix string) {
	if row.Previous != nil &&
		row.Previous.Depth >= row.Depth &&
		!row.Chained &&
		(row.Previous.Depth > row.Depth || row.Span.Call != nil ||
			(row.Previous.Span.Call != nil && row.Span.Call == nil)) {
		fmt.Fprint(out, prefix)
		r.indent(out, row.Depth)
		fmt.Fprintln(out)
	}
	fe.renderStep(out, r, row.Span, row.Chained, row.Depth, prefix)
	fe.renderStepLogs(out, r, row, prefix)
	fe.renderStepError(out, r, row.Span, row.Depth, prefix)
}

func (fe *frontendPretty) renderStepLogs(out *termenv.Output, r *renderer, row *dagui.TraceRow, prefix string) {
	if row.IsRunningOrChildRunning || row.Span.IsFailedOrCausedFailure() || fe.Verbosity >= dagui.ExpandCompletedVerbosity {
		if logs := fe.logs.Logs[row.Span.ID]; logs != nil {
			fe.renderLogs(out, r,
				logs,
				row.Depth,
				fe.window.Height/3,
				prefix,
			)
		}
	}
}

func (fe *frontendPretty) renderStepError(out *termenv.Output, r *renderer, span *dagui.Span, depth int, prefix string) {
	for _, span := range span.Errors().Order {
		// only print the first line
		for _, line := range strings.Split(span.Status.Description, "\n") {
			if line == "" {
				continue
			}
			fmt.Fprint(out, prefix)
			r.indent(out, depth)
			fmt.Fprintf(out,
				out.String("! %s").Foreground(termenv.ANSIYellow).String(),
				line,
			)
			fmt.Fprintln(out)
		}
	}
}

func (fe *frontendPretty) renderStep(out *termenv.Output, r *renderer, span *dagui.Span, chained bool, depth int, prefix string) error {
	isFocused := span.ID == fe.FocusedSpan

	id := span.Call
	if id != nil {
		if err := r.renderCall(out, span, id, prefix, chained, depth, false, span.Internal, isFocused); err != nil {
			return err
		}
	} else if span != nil {
		if err := r.renderSpan(out, span, span.Name, prefix, depth, isFocused); err != nil {
			return err
		}
	}
	fmt.Fprintln(out)

	if span.ID == fe.debugged {
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? version: %d\n", span.Version)
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? earliest running: %s\n", span.Activity.EarliestRunning)
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? encapsulate: %v\n", span.Encapsulate)
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? encapsulated: %v\n", span.Encapsulated)
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? internal: %v\n", span.Internal)
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? canceled: %v\n", span.Canceled)
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? passthrough: %v\n", span.Passthrough)
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? ignore: %v\n", span.Ignore)
		pending, reasons := span.PendingReason()
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? pending: %v\n", pending)
		for _, reason := range reasons {
			r.indent(out, depth+1)
			fmt.Fprintln(out, prefix+"- "+reason)
		}
		cached, reasons := span.CachedReason()
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? cached: %v\n", cached)
		for _, reason := range reasons {
			r.indent(out, depth+1)
			fmt.Fprintln(out, prefix+"- "+reason)
		}
		failed, reasons := span.FailedReason()
		r.indent(out, depth+1)
		fmt.Fprintf(out, prefix+"? failed: %v\n", failed)
		for _, reason := range reasons {
			r.indent(out, depth+1)
			fmt.Fprintln(out, prefix+"- "+reason)
		}
		if span.EffectID != "" {
			r.indent(out, depth+1)
			fmt.Fprintf(out, prefix+"? is effect: %s\n", span.EffectID)
		}
		if len(span.EffectIDs) > 0 {
			r.indent(out, depth+1)
			fmt.Fprintf(out, prefix+"? installed effects: %d\n", len(span.EffectIDs))
			for _, id := range span.EffectIDs {
				r.indent(out, depth+1)
				fmt.Fprintln(out, prefix+" - "+id)
				if spans := fe.db.EffectSpans[id]; spans != nil {
					for _, effect := range spans.Order {
						r.indent(out, depth+1)
						fmt.Fprintln(out, prefix+"   - "+effect.Name)
					}
				}
			}
		}
	}

	return nil
}

func (fe *frontendPretty) renderLogs(out *termenv.Output, r *renderer, logs *Vterm, depth int, height int, prefix string) {
	pipe := out.String(VertBoldBar).Foreground(termenv.ANSIBrightBlack)
	if depth == -1 {
		// clear prefix when zoomed
		logs.SetPrefix(prefix)
	} else {
		buf := new(strings.Builder)
		fmt.Fprint(buf, prefix)
		indentOut := NewOutput(buf, termenv.WithProfile(fe.profile))
		r.indent(indentOut, depth)
		fmt.Fprint(indentOut, pipe.String()+" ")
		logs.SetPrefix(buf.String())
	}
	if height <= 0 {
		logs.SetHeight(logs.UsedHeight())
	} else {
		logs.SetHeight(height)
	}
	fmt.Fprint(out, logs.View())
}

type prettyLogs struct {
	Logs     map[dagui.SpanID]*Vterm
	LogWidth int
}

func newPrettyLogs() *prettyLogs {
	return &prettyLogs{
		Logs:     make(map[dagui.SpanID]*Vterm),
		LogWidth: -1,
	}
}

func (l *prettyLogs) Export(ctx context.Context, logs []sdklog.Record) error {
	for _, log := range logs {
		// render vterm for TUI
		_, _ = fmt.Fprint(l.spanLogs(log.SpanID()), log.Body().AsString())
	}
	return nil
}

func (l *prettyLogs) spanLogs(id trace.SpanID) *Vterm {
	spanID := dagui.SpanID{SpanID: id}
	term, found := l.Logs[spanID]
	if !found {
		term = NewVterm()
		if l.LogWidth > -1 {
			term.SetWidth(l.LogWidth)
		}
		l.Logs[spanID] = term
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

func findTTYs() (in io.Reader, out io.Writer) {
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

type wrapCommand struct {
	tea.ExecCommand
	before func() error
	after  func() error
}

var _ tea.ExecCommand = (*wrapCommand)(nil)

func (ts *wrapCommand) Run() error {
	if err := ts.before(); err != nil {
		return err
	}
	err := ts.ExecCommand.Run()
	if err2 := ts.after(); err == nil {
		err = err2
	}
	return err
}
