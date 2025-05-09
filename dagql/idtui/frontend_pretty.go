package idtui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
	"github.com/pkg/browser"
	"github.com/vito/bubbline/editline"
	"github.com/vito/bubbline/history"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/slog"
)

var historyFile = filepath.Join(xdg.DataHome, "dagger", "histfile")

var ErrShellExited = errors.New("shell exited")
var ErrInterrupted = errors.New("interrupted")

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

	// updated by Shell
	shell           ShellHandler
	shellCtx        context.Context
	shellInterrupt  context.CancelCauseFunc
	promptFg        termenv.Color
	editline        *editline.Model
	editlineFocused bool
	flushed         map[dagui.SpanID]bool
	scrollback      *strings.Builder
	shellRunning    bool
	shellLock       sync.Mutex

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
	writer     io.Writer

	// held to synchronize tea.Model with updates
	mu sync.Mutex

	// messages to print before the final render
	msgPreFinalRender strings.Builder

	// Add prompt field
	activeBoolPrompt   *promptBool
	activeStringPrompt *promptString
}

func NewPretty(w io.Writer) Frontend {
	return NewWithDB(w, dagui.NewDB())
}

func NewReporter(w io.Writer) Frontend {
	fe := NewWithDB(w, dagui.NewDB())
	fe.reportOnly = true
	return fe
}

func NewWithDB(w io.Writer, db *dagui.DB) *frontendPretty {
	profile := ColorProfile()
	view := new(strings.Builder)
	return &frontendPretty{
		db:        db,
		logs:      newPrettyLogs(profile),
		autoFocus: true,

		// set empty initial row state to avoid nil checks
		rowsView: &dagui.RowsView{},
		rows:     &dagui.Rows{BySpan: map[dagui.SpanID]*dagui.TraceRow{}},

		// shell state
		flushed:    map[dagui.SpanID]bool{},
		scrollback: new(strings.Builder),

		// initial TUI state
		window:     tea.WindowSizeMsg{Width: -1, Height: -1}, // be clear that it's not set
		fps:        30,                                       // sane default, fine-tune if needed
		profile:    profile,
		view:       view,
		viewOut:    NewOutput(view, termenv.WithProfile(profile)),
		browserBuf: new(strings.Builder),
		writer:     w,
	}
}

type startShellMsg struct {
	ctx     context.Context
	handler ShellHandler
}

func (fe *frontendPretty) Shell(ctx context.Context, handler ShellHandler) {
	fe.program.Send(startShellMsg{
		ctx:     ctx,
		handler: handler,
	})
	<-ctx.Done()
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

	if fe.editline != nil && fe.shell != nil {
		if err := os.MkdirAll(filepath.Dir(historyFile), 0755); err != nil {
			slog.Error("failed to create history directory", "err", err)
		}
		if err := history.SaveHistory(fe.editline.GetHistory(), historyFile); err != nil {
			slog.Error("failed to save history", "err", err)
		}
	}

	// print the final output display to stderr
	if renderErr := fe.FinalRender(os.Stderr); renderErr != nil {
		return renderErr
	}

	fe.db.WriteDot(opts.DotOutputFilePath, opts.DotFocusField, opts.DotShowInternal)

	// return original err
	return fe.err
}

func (fe *frontendPretty) HandlePrompt(ctx context.Context, prompt string, dest any) error {
	switch x := dest.(type) {
	case *bool:
		return fe.handlePromptBool(ctx, prompt, x)
	case *string:
		return fe.handlePromptString(ctx, prompt, x)
	default:
		return fmt.Errorf("unsupported prompt destination type: %T", dest)
	}
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
		// return the cause, since it can hint the CLI to e.g. exit 0 if the
		// user is just pressing Ctrl+D in the shell
		return context.Cause(fe.runCtx)
	}

	// return the run err result
	return fe.err
}

func (fe *frontendPretty) renderErrorLogs(out TermOutput, r *renderer) bool {
	if fe.rowsView == nil {
		return false
	}
	rowsView := fe.db.RowsView(dagui.FrontendOpts{
		ZoomedSpan: fe.db.PrimarySpan,
		Verbosity:  dagui.ShowCompletedVerbosity,
	})
	errTree := fe.db.CollectErrors(rowsView)
	var anyHasLogs bool
	dagui.WalkTree(errTree, func(row *dagui.TraceTree, _ int) dagui.WalkDecision {
		logs := fe.logs.Logs[row.Span.ID]
		if logs != nil && logs.UsedHeight() > 0 {
			anyHasLogs = true
			return dagui.WalkStop
		}
		return dagui.WalkContinue
	})
	if anyHasLogs {
		fmt.Fprintln(out)
		fmt.Fprintln(out, out.String("Error logs:").Bold())
	}
	dagui.WalkTree(errTree, func(tree *dagui.TraceTree, _ int) dagui.WalkDecision {
		logs := fe.logs.Logs[tree.Span.ID]
		if logs != nil && logs.UsedHeight() > 0 {
			fmt.Fprintln(out)
			fe.renderStep(out, r, tree.Span, tree.Chained, 0, "")
			fe.renderLogs(out, r, logs, -1, logs.UsedHeight(), "")
			fe.renderStepError(out, r, tree.Span, 0, "")
		}
		return dagui.WalkContinue
	})
	return len(errTree) > 0
}

// FinalRender is called after the program has finished running and prints the
// final output after the TUI has exited.
func (fe *frontendPretty) FinalRender(w io.Writer) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	// Print the scrollback.
	fmt.Fprint(w, fe.scrollback.String())

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
				fmt.Fprintln(w)
				handleTelemetryErrorOutput(w, out, fe.TelemetryError)
				fmt.Fprintln(os.Stderr, fe.msgPreFinalRender.String())
			}()
		}
	}

	// If there are errors, show log output.
	if fe.err != nil && fe.shell == nil {
		// Counter-intuitively, we don't want to render the primary output
		// when there's an error, because the error is better represented by
		// the progress output and error summary.
		if fe.renderErrorLogs(out, r) {
			return nil
		}
	}

	// Replay the primary output log to stdout/stderr.
	return renderPrimaryOutput(w, fe.db)
}

func (fe *frontendPretty) flush() {
	if fe.program != nil {
		go fe.program.Send(flushMsg{})
	}
}

func (fe *frontendPretty) SpanExporter() sdktrace.SpanExporter {
	return prettySpanExporter{fe}
}

type prettySpanExporter struct {
	*frontendPretty
}

// flushMsg is sent after spans are exported and the view is recalculated. When
// this message is received, top-level finished spans are printed to the
// scrollback.
type flushMsg struct{}

func (fe prettySpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	defer fe.flush()
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
	defer fe.flush()
	if err := fe.db.LogExporter().Export(ctx, logs); err != nil {
		return err
	}
	if err := fe.logs.Export(ctx, logs); err != nil {
		return err
	}
	return nil
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
	var showedKey bool
	// Blank line prior to keymap
	for _, key := range fe.keys(out) {
		if !key.Enabled() {
			continue
		}
		mainKey := key.Keys()[0]
		if showedKey {
			fmt.Fprint(w, style.Render(" · "))
		}
		keyStyle := style
		if time.Since(fe.pressedKeyAt) < 500*time.Millisecond {
			for _, k := range key.Keys() {
				if k == fe.pressedKey {
					keyStyle = keyStyle.Foreground(nil)
					// Reverse(true)
				}
			}
		}
		fmt.Fprint(w, keyStyle.Bold(true).Render(mainKey))
		fmt.Fprint(w, keyStyle.Render(" "+key.Help().Desc))
		showedKey = true
	}
	res := w.String()
	fmt.Fprint(out, res)
	return lipgloss.Width(res)
}

func (fe *frontendPretty) keys(out *termenv.Output) []key.Binding {
	if fe.editlineFocused {
		bnds := []key.Binding{
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "nav mode")),
		}
		if fe.shell != nil {
			bnds = append(bnds, fe.shell.KeyBindings()...)
		}
		return bnds
	}

	var quitMsg string
	if fe.interrupted {
		quitMsg = "quit!"
	} else {
		quitMsg = "quit"
	}

	return []key.Binding{
		key.NewBinding(key.WithKeys("i", "tab"),
			key.WithHelp("i", "input mode"),
			KeyEnabled(fe.shell != nil)),
		key.NewBinding(key.WithKeys("w"),
			key.WithHelp("w", out.Hyperlink(fe.cloudURL, "web")),
			KeyEnabled(fe.cloudURL != "")),
		key.NewBinding(key.WithKeys("←↑↓→", "up", "down", "left", "right", "h", "j", "k", "l"),
			key.WithHelp("←↑↓→", "move")),
		key.NewBinding(key.WithKeys("home"),
			key.WithHelp("home", "first")),
		key.NewBinding(key.WithKeys("end", " "),
			key.WithHelp("end", "last")),
		key.NewBinding(key.WithKeys("enter"),
			key.WithHelp("enter", "zoom"),
			KeyEnabled(fe.ZoomedSpan.IsValid() && fe.ZoomedSpan != fe.db.PrimarySpan)),
		key.NewBinding(key.WithKeys("+/-", "+", "-"),
			key.WithHelp("+/-", fmt.Sprintf("verbosity=%d", fe.Verbosity))),
		key.NewBinding(key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", quitMsg)),
	}
}

func KeyEnabled(enabled bool) key.BindingOpt {
	return func(b *key.Binding) {
		b.SetEnabled(enabled)
	}
}

func (fe *frontendPretty) Render(out TermOutput) error {
	progHeight := fe.window.Height

	if fe.editline != nil {
		progHeight -= lipgloss.Height(fe.editlineView())
	}

	r := newRenderer(fe.db, fe.window.Width, fe.FrontendOpts)

	var progPrefix string
	if fe.rowsView != nil && fe.rowsView.Zoomed != nil && fe.rowsView.Zoomed.ID != fe.db.PrimarySpan {
		fe.renderStep(out, r, fe.rowsView.Zoomed, false, 0, "")
		progHeight -= 1
		progPrefix = "  "
	}

	below := new(strings.Builder)
	var countOut TermOutput = NewOutput(below, termenv.WithProfile(fe.profile))

	if fe.activeBoolPrompt != nil {
		fmt.Fprint(countOut, fe.viewBoolPrompt(countOut))
		fmt.Fprintln(countOut)
	}

	if fe.activeStringPrompt != nil {
		fmt.Fprintln(countOut)
		fmt.Fprint(countOut, fe.viewStringPrompt())
	}

	if fe.editline == nil {
		fmt.Fprint(countOut, fe.viewKeymap())
	}

	if logs := fe.logs.Logs[fe.ZoomedSpan]; logs != nil && logs.UsedHeight() > 0 {
		fmt.Fprintln(below)
		fe.renderLogs(countOut, r, logs, -1, fe.window.Height/3, progPrefix)
	}

	belowOut := strings.TrimRight(below.String(), "\n")
	progHeight -= lipgloss.Height(belowOut)

	if fe.renderProgress(out, r, false, progHeight, progPrefix) {
		fmt.Fprintln(out)
	}

	fmt.Fprint(out, belowOut)
	return nil
}

func (fe *frontendPretty) viewStringPrompt() string {
	return fe.activeStringPrompt.message.View()
}

func (fe *frontendPretty) viewBoolPrompt(out TermOutput) string {
	message := fe.activeBoolPrompt.message.View()
	width := lipgloss.Width(message)

	// Render Yes/No buttons
	yesStyles := []termenv.Style{
		out.String(" "),
		out.String("Y").Underline(),
		out.String("ES "),
	}
	noStyles := []termenv.Style{
		out.String(" "),
		out.String("N").Underline(),
		out.String("O "),
	}
	for i, style := range yesStyles {
		yesStyles[i] = style.Foreground(termenv.ANSIGreen)
	}
	for i, style := range noStyles {
		noStyles[i] = style.Foreground(termenv.ANSIRed)
	}
	if fe.activeBoolPrompt.yessing {
		for i, style := range yesStyles {
			yesStyles[i] = style.Bold().Reverse()
		}
	} else {
		for i, style := range noStyles {
			noStyles[i] = style.Bold().Reverse()
		}
	}

	var yes, no string
	for _, style := range yesStyles {
		yes += style.String()
	}
	for _, style := range noStyles {
		no += style.String()
	}

	bubble := lipgloss.JoinVertical(
		lipgloss.Left,
		message,
		lipgloss.PlaceHorizontal(width, lipgloss.Center,
			fmt.Sprintf("%s %s", yes, no),
		),
	)

	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(ANSIBrightBlack).
		Padding(1, 2).
		Render(bubble)
}

func (fe *frontendPretty) viewKeymap() string {
	outBuf := new(strings.Builder)
	out := NewOutput(outBuf, termenv.WithProfile(fe.profile))
	fmt.Fprint(out, KeymapStyle.Render(strings.Repeat(HorizBar, 1)))
	fmt.Fprint(out, KeymapStyle.Render(" "))
	fe.renderKeymap(out, KeymapStyle)
	fmt.Fprint(out, KeymapStyle.Render(" "))
	if rest := fe.window.Width - lipgloss.Width(outBuf.String()); rest > 0 {
		fmt.Fprint(out, KeymapStyle.Render(strings.Repeat(HorizBar, rest)))
	}
	return outBuf.String()
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
	if buf.String() == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
}

func (fe *frontendPretty) renderProgress(out TermOutput, r *renderer, full bool, height int, prefix string) (rendered bool) {
	if fe.rowsView == nil {
		return
	}

	rows := fe.rows

	if full {
		for _, row := range rows.Order {
			if fe.renderRow(out, r, row, "") {
				rendered = true
			}
		}
		return
	}

	lines := fe.renderLines(r, height, prefix)
	if len(lines) > 0 {
		rendered = true
	}

	fmt.Fprint(out, strings.Join(lines, "\n"))
	return
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
	if fe.editline != nil {
		prog := ""
		if fe.scrollback.Len() > 0 {
			prog += fe.scrollback.String()
			// prog += "> " + strings.ReplaceAll(
			// 	strings.TrimSuffix(fe.scrollback.String(), "\n"),
			// 	"\n",
			// 	"\n> ",
			// ) + "\n"
		}
		prog += "\n"
		if view := strings.TrimSpace(fe.view.String()); view != "" {
			prog += view
			prog += "\n"
			if fe.activeStringPrompt == nil {
				prog += "\n"
			}
		}
		return prog + fe.editlineView()
	}
	return fe.view.String()
}

func (fe *frontendPretty) editlineView() string {
	return fe.editline.View() + "\n" + fe.viewKeymap()
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

type shellDoneMsg struct {
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

	case startShellMsg:
		fe.shell = msg.handler
		fe.shellCtx = msg.ctx
		fe.promptFg = termenv.ANSIGreen

		fe.initEditline()

		// restore history
		fe.editline.MaxHistorySize = 1000
		if history, err := history.LoadHistory(historyFile); err == nil {
			fe.editline.SetHistory(history)
		}
		fe.editline.HistoryEncoder = msg.handler

		// wire up auto completion
		fe.editline.AutoComplete = msg.handler.AutoComplete

		// if input ends with a pipe, then it's not complete
		fe.editline.CheckInputComplete = msg.handler.IsComplete

		// put the bowtie on
		promptCmd := fe.updatePrompt()

		return fe, tea.Batch(
			tea.Printf(`Dagger interactive shell. Type ".help" for more information. Press Ctrl+D to exit.`),
			fe.editline.Focus(),
			tea.DisableMouse,
			promptCmd,
		)

	case flushMsg:
		return fe.flushScrollback()

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

	case editline.InputCompleteMsg:
		if !fe.editlineFocused {
			return fe, nil
		}
		value := fe.editline.Value()
		fe.editline.AddHistoryEntry(value)
		fe.promptFg = termenv.ANSIYellow
		promptCmd := fe.updatePrompt()

		// reset now that we've accepted input
		fe.editline.Reset()
		if fe.activeStringPrompt != nil {
			fe.activeStringPrompt.result <- value
			fe.editline = nil
			fe.activeStringPrompt = nil
			fe.editlineFocused = false
		} else if fe.shell != nil {
			ctx, cancel := context.WithCancelCause(fe.shellCtx)
			fe.shellInterrupt = cancel
			fe.shellRunning = true

			return fe, tea.Batch(
				promptCmd,
				func() tea.Msg {
					fe.shellLock.Lock()
					defer fe.shellLock.Unlock()
					return shellDoneMsg{fe.shell.Handle(ctx, value)}
				},
			)
		}
		return fe, nil

	case shellDoneMsg:
		if msg.err == nil {
			fe.promptFg = termenv.ANSIGreen
		} else {
			fe.promptFg = termenv.ANSIRed
		}
		promptCmd := fe.updatePrompt()
		// NOTE: we switch back from the alt screen only when the scrollback is
		// written
		fe.shellRunning = false
		return fe, promptCmd

	case UpdatePromptMsg:
		return fe, fe.updatePrompt()

	case tea.KeyMsg:
		switch {
		// Handle prompt input if there's an active prompt
		case fe.activeBoolPrompt != nil:
			return fe, fe.handleBoolPromptKey(msg)
		// send all input to editline if it's focused
		case fe.editlineFocused:
			return fe, fe.handleEditlineKey(msg)
		default:
			return fe, fe.handleNavKey(msg)
		}

	case tea.WindowSizeMsg:
		fe.setWindowSizeLocked(msg)
		return fe, nil

	case frameMsg:
		fe.renderLocked()
		fe, flushCmd := fe.flushScrollback()
		// NB: take care not to forward Frame downstream, since that will result
		// in runaway ticks. instead inner components should send a SetFpsMsg to
		// adjust the outermost layer.
		return fe, tea.Batch(flushCmd, frame(fe.fps))

	case promptBool:
		fe.activeBoolPrompt = &msg
		return fe, nil

	case promptString:
		fe.activeStringPrompt = &msg
		fe.initEditline()
		return fe, fe.updatePrompt()

	default:
		return fe, nil
	}
}

func (fe *frontendPretty) handleBoolPromptKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "left", "h", "right", "l":
		fe.activeBoolPrompt.yessing = !fe.activeBoolPrompt.yessing
	case "enter":
		result := fe.activeBoolPrompt.result
		choice := fe.activeBoolPrompt.yessing
		fe.activeBoolPrompt = nil
		return func() tea.Msg {
			result <- choice
			close(result)
			return nil
		}
	case "n", "N":
		result := fe.activeBoolPrompt.result
		fe.activeBoolPrompt = nil
		return func() tea.Msg {
			result <- false
			close(result)
			return nil
		}
	case "y", "Y":
		result := fe.activeBoolPrompt.result
		fe.activeBoolPrompt = nil
		return func() tea.Msg {
			result <- true
			close(result)
			return nil
		}
	}
	return nil
}

func (fe *frontendPretty) handleEditlineKey(msg tea.KeyMsg) (cmd tea.Cmd) {
	defer func() {
		// update the prompt in all cases since e.g. going through history
		// can change it
		cmd = tea.Sequence(cmd, fe.updatePrompt())
	}()
	switch msg.String() {
	case "ctrl+d":
		if fe.editline.Value() == "" {
			return fe.quit(ErrShellExited)
		}
	case "ctrl+c":
		if fe.shellInterrupt != nil {
			fe.shellInterrupt(errors.New("interrupted"))
		}
		fe.editline.Reset()
	case "ctrl+l":
		return fe.clearScrollback()
	case "esc":
		fe.editlineFocused = false
		fe.editline.Blur()
		return nil
	case "alt++", "alt+=":
		fe.Verbosity++
		fe.recalculateViewLocked()
		return nil
	case "alt+-":
		fe.Verbosity--
		fe.recalculateViewLocked()
		return nil
	default:
		if fe.shell != nil && fe.editline.AtStart() {
			cmd := fe.shell.ReactToInput(fe.runCtx, msg)
			if cmd != nil {
				return cmd
			}
		}
	}
	el, cmd := fe.editline.Update(msg)
	fe.editline = el.(*editline.Model)
	return cmd
}

func (fe *frontendPretty) handleNavKey(msg tea.KeyMsg) tea.Cmd {
	lastKey := fe.pressedKey
	fe.pressedKey = msg.String()
	fe.pressedKeyAt = time.Now()
	switch msg.String() {
	case "q", "ctrl+c":
		return fe.quit(ErrInterrupted)
	case "ctrl+\\": // SIGQUIT
		fe.program.ReleaseTerminal()
		sigquit()
		return nil
	case "down", "j":
		fe.goDown()
		return nil
	case "up", "k":
		fe.goUp()
		return nil
	case "left", "h":
		fe.goOut()
		return nil
	case "right", "l":
		fe.goIn()
		return nil
	case "home":
		fe.goStart()
		return nil
	case "end", "G", " ":
		fe.goEnd()
		fe.pressedKey = "end"
		fe.pressedKeyAt = time.Now()
		return nil
	case "esc":
		fe.ZoomedSpan = fe.db.PrimarySpan
		fe.recalculateViewLocked()
		return nil
	case "+", "=":
		fe.FrontendOpts.Verbosity++
		fe.recalculateViewLocked()
		return nil
	case "-":
		fe.FrontendOpts.Verbosity--
		if fe.FrontendOpts.Verbosity < -1 {
			fe.FrontendOpts.Verbosity = -1
		}
		fe.recalculateViewLocked()
		return nil
	case "w":
		if fe.cloudURL == "" {
			return nil
		}
		url := fe.cloudURL
		if fe.ZoomedSpan.IsValid() && fe.ZoomedSpan != fe.db.PrimarySpan {
			url += "?span=" + fe.ZoomedSpan.String()
		}
		if fe.FocusedSpan.IsValid() && fe.FocusedSpan != fe.db.PrimarySpan {
			url += "#" + fe.FocusedSpan.String()
		}
		return func() tea.Msg {
			if err := browser.OpenURL(url); err != nil {
				slog.Warn("failed to open URL",
					"url", url,
					"err", err,
					"output", fe.browserBuf.String())
			}
			return nil
		}
	case "?":
		if fe.debugged == fe.FocusedSpan {
			fe.debugged = dagui.SpanID{}
		} else {
			fe.debugged = fe.FocusedSpan
		}
		return nil
	case "enter":
		fe.ZoomedSpan = fe.FocusedSpan
		fe.recalculateViewLocked()
		return nil
	case "tab", "i":
		if fe.editline != nil {
			fe.editlineFocused = true
			fe.updatePrompt()
			return fe.editline.Focus()
		}
		return nil
	}

	switch lastKey { //nolint:gocritic
	case "g":
		switch msg.String() { //nolint:gocritic
		case "g":
			fe.goStart()
			fe.pressedKey = "home"
			fe.pressedKeyAt = time.Now()
			return nil
		}
	}

	return nil
}

func (fe *frontendPretty) initEditline() {
	// create the editline
	fe.editline = editline.New(fe.window.Width, fe.window.Height)
	fe.editline.HideKeyMap = true
	fe.editlineFocused = true
	// HACK: for some reason editline's first paint is broken (only shows
	// first 2 chars of prompt, doesn't show cursor). Sending it a message
	// - any message - fixes it.
	fe.editline.Update(nil)
}

type UpdatePromptMsg struct{}

type promptBool struct {
	message *Markdown
	result  chan bool
	yessing bool
}

type promptString struct {
	message *Markdown
	result  chan string
}

func (fe *frontendPretty) flushScrollback() (*frontendPretty, tea.Cmd) {
	if fe.shell == nil {
		return fe, nil
	}
	if fe.shellRunning {
		// there won't be anything to flush so long as the shell is running
		return fe, nil
	}

	// Calculate visible area height
	visibleHeight := fe.window.Height
	if fe.editline != nil {
		visibleHeight -= lipgloss.Height(fe.editlineView())
	}

	// Create buffer for flushing
	out := NewOutput(fe.scrollback, termenv.WithProfile(fe.profile))
	r := newRenderer(fe.db, 100, fe.FrontendOpts)

	var anyFlushed bool
	for _, row := range fe.rows.Order {
		// Skip if row is already flushed
		if fe.flushed[row.Span.ID] {
			continue
		}
		if row.IsRunningOrChildRunning || !fe.logsDone(row.Span.ID, row.Depth == 0) || row.Span.IsPending() {
			break
		}
		if row.Depth == 0 {
			// NOTE: better to do this above a root span than after its last child,
			// since we sometimes will have a child show up late if a tree finishes
			// quickly.
			fmt.Fprintln(out)
		}
		fe.renderRow(out, r, row, "")
		fe.flushed[row.Span.ID] = true
		anyFlushed = true
	}
	if !anyFlushed {
		return fe, nil
	}

	// If nothing was written, we're done
	if fe.scrollback.Len() == 0 {
		dbg.Println("scrollback is empty")
		return fe, nil
	}

	// Calculate how many lines need to be flushed
	scrollbackHeight := lipgloss.Height(fe.scrollback.String())
	if scrollbackHeight < visibleHeight {
		return fe, nil
	}

	offscreenHeight := scrollbackHeight - visibleHeight
	dbg.Printf("!!! height=%d, offscreenHeight=%d, visibleHeight=%d, scrollbackHeight=%d",
		fe.window.Height,
		offscreenHeight,
		visibleHeight,
		scrollbackHeight)

	// Split into lines, being careful to preserve empty lines
	lines := strings.Split(fe.scrollback.String(), "\n")

	// Build new buffers
	offscreen := new(strings.Builder)
	screen := new(strings.Builder)

	// Write lines to appropriate buffers
	for i, line := range lines {
		if offscreenHeight > 0 {
			fmt.Fprint(offscreen, line)
			if i < len(lines)-1 {
				fmt.Fprintln(offscreen)
			}
			offscreenHeight--
		} else {
			fmt.Fprint(screen, line)
			if i < len(lines)-1 {
				fmt.Fprintln(screen)
			}
		}
	}

	// Replace scrollback with remaining visible lines
	fe.scrollback = screen

	// Return command to print offscreen lines
	msg := strings.TrimSuffix(offscreen.String(), "\n")
	dbg.Println("flushing offscreen lines", strconv.Quote(msg))
	return fe, tea.Printf("%s", msg)
}

func (fe *frontendPretty) clearScrollback() tea.Cmd {
	scrollback := strings.TrimSuffix(fe.scrollback.String(), "\n")
	fe.scrollback.Reset()
	return tea.Sequence(
		tea.Printf("%s", scrollback),
		func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return tea.ClearScreen()
		},
	)
}

func (fe *frontendPretty) updatePrompt() tea.Cmd {
	var cmd tea.Cmd
	if fe.shell != nil {
		fe.editline.Prompt, cmd = fe.shell.Prompt(fe.runCtx, fe.viewOut, fe.promptFg)
	}
	fe.editline.UpdatePrompt()
	return cmd
}

func (fe *frontendPretty) quit(interruptErr error) tea.Cmd {
	if fe.CustomExit != nil {
		fe.CustomExit()
		return nil
	}

	if fe.done && fe.eof {
		fe.quitting = true
		// must have configured NoExit, and now they want
		// to exit manually
		return tea.Quit
	}
	if fe.interrupted {
		slog.Warn("exiting immediately")
		fe.quitting = true
		return tea.Quit
	} else {
		slog.Warn("canceling... (press again to exit immediately)")
	}
	fe.interrupted = true
	fe.interrupt(interruptErr)
	return nil // tea.Quit is deferred until we receive doneMsg
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
	if fe.editline != nil {
		fe.editline.SetSize(msg.Width, msg.Height)
	}
	if fe.activeBoolPrompt != nil {
		fe.activeBoolPrompt.message.Width = msg.Width
	}
	if fe.activeStringPrompt != nil {
		fe.activeStringPrompt.message.Width = msg.Width
	}
}

func (fe *frontendPretty) renderLocked() {
	fe.view.Reset()
	fe.Render(fe.viewOut)
}

//nolint:gocyclo
func (fe *frontendPretty) renderRow(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) bool {
	if fe.flushed[row.Span.ID] && fe.editlineFocused {
		return false
	}
	focused := row.Span.ID == fe.FocusedSpan && !fe.editlineFocused
	if fe.shell != nil {
		if row.IsLastChild() {
			defer func() {
				root := row.Root()
				if logs := fe.logs.Logs[root.Span.ID]; logs != nil && logs.UsedHeight() > 0 {
					logDepth := 0
					if fe.Verbosity < dagui.ExpandCompletedVerbosity {
						logDepth = -1
					}
					fe.renderLogs(out, r, logs, logDepth, logs.UsedHeight(), prefix)
				}
				fe.renderStepError(out, r, root.Span, 0, prefix)
			}()
		}
		if row.Depth == 0 {
			// navigating history and there's a previous row
			if (!fe.editlineFocused && row.Previous != nil) ||
				(row.Previous != nil && !fe.flushed[row.Previous.Span.ID]) {
				fmt.Fprintln(out, out.String(prefix))
			}
			fe.renderStep(out, r, row.Span, row.Chained, row.Depth, prefix)
			fe.renderDebug(out, row.Span, prefix+Block25+" ", false)
			return true
		}
	}
	if row.Previous != nil &&
		row.Previous.Depth >= row.Depth &&
		!row.Chained &&
		( // ensure gaps after last nested child
		row.Previous.Depth > row.Depth ||
			// ensure gaps before unchained calls
			row.Span.Call() != nil ||
			// ensure gaps between calls and non-calls
			(row.Previous.Span.Call() != nil && row.Span.Call() == nil) ||
			// ensure gaps between messages
			(row.Previous.Span.Message != "" && row.Span.Message != "") ||
			// ensure gaps going from tool calls to messages
			(row.Previous.Span.Message == "" && row.Span.Message != "")) {
		fmt.Fprint(out, prefix)
		r.indent(out, row.Depth)
		fmt.Fprintln(out)
	}
	span := row.Span
	if span.Message != "" {
		// when a span represents a message, we don't need to print its name
		//
		// NOTE: arguably this should be opt-in, but it's not clear how the
		// span name relates to the message in all cases; is it the
		// subject? or author? better to be explicit with attributes.
		r.indent(out, row.Depth)
		r.renderStatus(out, span, focused, row.Chained)
		if fe.renderStepLogs(out, r, row, prefix) {
			r.indent(out, row.Depth)
			fmt.Fprint(out, out.String(VertBoldBar).Foreground(termenv.ANSIBrightBlack))
		} else {
			// no logs were printed, so snug the duration up against the emoji
			fmt.Fprint(out, "\b")
		}
		r.renderDuration(out, span)
		r.renderMetrics(out, span)
		fmt.Fprintln(out)
	} else {
		fe.renderStep(out, r, row.Span, row.Chained, row.Depth, prefix)
		if row.IsRunningOrChildRunning ||
			row.Span.IsFailedOrCausedFailure() ||
			row.Span.IsCanceled() ||
			fe.Verbosity >= dagui.ExpandCompletedVerbosity {
			fe.renderStepLogs(out, r, row, prefix)
		}
	}
	fe.renderStepError(out, r, row.Span, row.Depth, prefix)
	fe.renderDebug(out, row.Span, prefix+Block25+" ", false)
	return true
}

func (fe *frontendPretty) renderDebug(out TermOutput, span *dagui.Span, prefix string, force bool) {
	if span.ID != fe.debugged && !force {
		return
	}
	vt := NewVterm(fe.profile)
	vt.WriteMarkdown([]byte("## Span\n"))
	vt.SetPrefix(prefix)
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.Encode(span.Snapshot())
	vt.WriteMarkdown([]byte("```json\n" + strings.TrimSpace(buf.String()) + "\n```"))
	if len(span.EffectIDs) > 0 {
		vt.WriteMarkdown([]byte("\n\n## Installed effects\n\n"))
		for _, id := range span.EffectIDs {
			vt.WriteMarkdown([]byte("- " + id + "\n"))
			if spans := fe.db.EffectSpans[id]; spans != nil {
				for _, effect := range spans.Order {
					vt.WriteMarkdown([]byte("  - " + effect.Name + "\n"))
				}
			}
		}
	}
	if len(span.RevealedSpans.Order) > 0 {
		vt.WriteMarkdown([]byte("\n\n## Revealed spans\n\n"))
		for _, revealed := range span.RevealedSpans.Order {
			vt.WriteMarkdown([]byte("- " + revealed.Name + "\n"))
		}
	}
	fmt.Fprint(out, prefix+vt.View())
}

func (fe *frontendPretty) renderStepLogs(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) bool {
	if logs := fe.logs.Logs[row.Span.ID]; logs != nil {
		return fe.renderLogs(out, r,
			logs,
			row.Depth,
			fe.window.Height/3,
			prefix,
		)
	}
	return false
}

func (fe *frontendPretty) renderStepError(out TermOutput, r *renderer, span *dagui.Span, depth int, prefix string) {
	for _, span := range span.Errors().Order {
		errText := span.Status.Description
		if errText == "" {
			continue
		}

		// Calculate available width for text
		prefixWidth := lipgloss.Width(prefix)
		indentWidth := depth * 2 // Assuming indent is 2 spaces per depth level
		markerWidth := 2         // "! " prefix
		availableWidth := fe.window.Width - prefixWidth - indentWidth - markerWidth
		if availableWidth > 0 {
			errText = cellbuf.Wrap(errText, availableWidth, "")
		}

		// Print each wrapped line with proper indentation
		for _, line := range strings.Split(errText, "\n") {
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

func (fe *frontendPretty) renderStep(out TermOutput, r *renderer, span *dagui.Span, chained bool, depth int, prefix string) error {
	isFocused := span.ID == fe.FocusedSpan && !fe.editlineFocused

	if call := span.Call(); call != nil {
		if err := r.renderCall(out, span, call, prefix, chained, depth, false, span.Internal, isFocused); err != nil {
			return err
		}
	} else if span != nil {
		if err := r.renderSpan(out, span, span.Name, prefix, depth, isFocused, chained); err != nil {
			return err
		}
	}
	fmt.Fprintln(out)

	return nil
}

func (fe *frontendPretty) renderLogs(out TermOutput, r *renderer, logs *Vterm, depth int, height int, prefix string) bool {
	pipe := out.String(VertBoldBar).Foreground(termenv.ANSIBrightBlack)
	if depth == -1 {
		// clear prefix when zoomed
		logs.SetPrefix(prefix)
	} else {
		buf := new(strings.Builder)
		fmt.Fprint(buf, prefix)
		indentOut := NewOutput(buf, termenv.WithProfile(fe.profile))
		r.indent(indentOut, depth)
		fmt.Fprint(indentOut, pipe)
		fmt.Fprint(indentOut, out.String(" "))
		logs.SetPrefix(buf.String())
	}
	if height <= 0 {
		logs.SetHeight(logs.UsedHeight())
	} else {
		logs.SetHeight(height)
	}
	view := logs.View()
	if view == "" {
		return false
	}
	fmt.Fprint(out, view)
	return true
}

func (fe *frontendPretty) logsDone(id dagui.SpanID, waitForLogs bool) bool {
	if fe.logs == nil {
		// no logs to begin with
		return true
	}
	if _, ok := fe.logs.Logs[id]; !ok && !waitForLogs {
		// no logs to begin with
		return true
	}
	return fe.logs.SawEOF[id]
}

type prettyLogs struct {
	Logs     map[dagui.SpanID]*Vterm
	LogWidth int
	SawEOF   map[dagui.SpanID]bool
	Profile  termenv.Profile
}

func newPrettyLogs(profile termenv.Profile) *prettyLogs {
	return &prettyLogs{
		Logs:     make(map[dagui.SpanID]*Vterm),
		LogWidth: -1,
		Profile:  profile,
		SawEOF:   make(map[dagui.SpanID]bool),
	}
}

func (l *prettyLogs) Export(ctx context.Context, logs []sdklog.Record) error {
	for _, log := range logs {
		// Check for Markdown content type
		contentType := ""
		eof := false
		for attr := range log.WalkAttributes {
			if attr.Key == telemetry.ContentTypeAttr {
				contentType = attr.Value.AsString()
			}
			if attr.Key == telemetry.StdioEOFAttr {
				eof = attr.Value.AsBool()
			}
		}

		if eof && log.SpanID().IsValid() {
			l.SawEOF[dagui.SpanID{SpanID: log.SpanID()}] = true
			continue
		}

		vterm := l.spanLogs(log.SpanID())

		if contentType == "text/markdown" {
			_, _ = vterm.WriteMarkdown([]byte(log.Body().AsString()))
		} else {
			_, _ = fmt.Fprint(vterm, log.Body().AsString())
		}
	}
	return nil
}

func (l *prettyLogs) spanLogs(id trace.SpanID) *Vterm {
	spanID := dagui.SpanID{SpanID: id}
	term, found := l.Logs[spanID]
	if !found {
		term = NewVterm(l.Profile)
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

// TermOutput is an interface that captures the methods we need from termenv.Output
type TermOutput interface {
	io.Writer
	String(...string) termenv.Style
}

func (fe *frontendPretty) handlePromptBool(ctx context.Context, message string, dest *bool) error {
	result := make(chan bool, 1)

	fe.program.Send(tea.Msg(promptBool{
		message: &Markdown{
			Content: message,
			Width:   min(max(fe.window.Width/2, 50), 80),
		},
		result:  result,
		yessing: *dest,
	}))

	select {
	case <-ctx.Done():
		return ctx.Err()
	case val := <-result:
		*dest = val
		return nil
	}
}

func (fe *frontendPretty) handlePromptString(ctx context.Context, message string, dest *string) error {
	result := make(chan string, 1)

	fe.program.Send(tea.Msg(promptString{
		message: &Markdown{
			Content: message,
			Width:   fe.window.Width,
		},
		result: result,
	}))

	select {
	case <-ctx.Done():
		return ctx.Err()
	case val := <-result:
		*dest = val
		return nil
	}
}

func handleTelemetryErrorOutput(w io.Writer, to *termenv.Output, err error) {
	if err != nil {
		fmt.Fprintf(w, "%s - %s\n(%s)\n", to.String("WARN").Foreground(termenv.ANSIYellow), "failures detected while emitting telemetry. trace information incomplete", err.Error())
		fmt.Fprintln(w)
	}
}

var (
	ANSIBlack         = lipgloss.Color("0")
	ANSIRed           = lipgloss.Color("1")
	ANSIGreen         = lipgloss.Color("2")
	ANSIYellow        = lipgloss.Color("3")
	ANSIBlue          = lipgloss.Color("4")
	ANSIMagenta       = lipgloss.Color("5")
	ANSICyan          = lipgloss.Color("6")
	ANSIWhite         = lipgloss.Color("7")
	ANSIBrightBlack   = lipgloss.Color("8")
	ANSIBrightRed     = lipgloss.Color("9")
	ANSIBrightGreen   = lipgloss.Color("10")
	ANSIBrightYellow  = lipgloss.Color("11")
	ANSIBrightBlue    = lipgloss.Color("12")
	ANSIBrightMagenta = lipgloss.Color("13")
	ANSIBrightCyan    = lipgloss.Color("14")
	ANSIBrightWhite   = lipgloss.Color("15")
)
