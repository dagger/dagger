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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
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
	"golang.org/x/term"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/dagql/idtui/multiprefixw"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/util/cleanups"
)

var historyFile = filepath.Join(xdg.DataHome, "dagger", "histfile")

var ErrShellExited = errors.New("shell exited")
var ErrInterrupted = errors.New("interrupted")

type frontendPretty struct {
	dagui.FrontendOpts

	dag *dagger.Client

	// used for animations
	now time.Time

	// don't show live progress; just print a full report at the end
	reportOnly bool

	// updated by Run
	program     *tea.Program
	run         func(context.Context) (cleanups.CleanupF, error)
	runCtx      context.Context
	interrupt   context.CancelCauseFunc
	interrupted bool
	quitting    bool
	done        bool
	err         error
	cleanup     func()

	// updated by Shell
	shell           ShellHandler
	shellCtx        context.Context
	shellInterrupt  context.CancelCauseFunc
	promptFg        termenv.Color
	promptErr       error
	editline        *editline.Model
	editlineFocused bool
	autoModeSwitch  bool
	flushed         map[dagui.SpanID]bool
	offscreen       map[dagui.SpanID]bool
	scrollback      scrollbackRows
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
	fps          float64 // frames per second
	spinner      *Rave
	profile      termenv.Profile
	window       tea.WindowSizeMsg // set by BubbleTea
	contentWidth int
	sidebarWidth int
	view         *strings.Builder // rendered async
	viewOut      *termenv.Output
	browserBuf   *strings.Builder      // logs if browser fails
	finalRender  bool                  // whether we're doing the final render
	shownErrs    map[dagui.SpanID]bool // which errors we've rendered
	stdin        io.Reader             // used by backgroundMsg for running terminal
	writer       io.Writer

	// content to show in the sidebar
	sidebar    []SidebarSection
	sidebarBuf *strings.Builder // logs if sidebar fails

	// held to synchronize tea.Model with updates
	mu sync.Mutex

	// messages to print before the final render
	msgPreFinalRender strings.Builder

	// Add prompt field
	form *huh.Form
}

func (fe *frontendPretty) SetClient(client *dagger.Client) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	fe.dag = client
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
		logs:      newPrettyLogs(profile, db),
		autoFocus: true,

		// set empty initial row state to avoid nil checks
		rowsView: &dagui.RowsView{},
		rows:     &dagui.Rows{BySpan: map[dagui.SpanID]*dagui.TraceRow{}},

		// shell state
		flushed:   map[dagui.SpanID]bool{},
		offscreen: map[dagui.SpanID]bool{},

		// initial TUI state
		window:     tea.WindowSizeMsg{Width: -1, Height: -1}, // be clear that it's not set
		fps:        30,                                       // sane default, fine-tune if needed
		spinner:    NewRave(),
		profile:    profile,
		view:       view,
		viewOut:    NewOutput(view, termenv.WithProfile(profile)),
		browserBuf: new(strings.Builder),
		sidebarBuf: new(strings.Builder),
		writer:     w,
		shownErrs:  map[dagui.SpanID]bool{},
	}
}

func (fe *frontendPretty) SetSidebarContent(section SidebarSection) {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	var updated bool
	for i, cur := range fe.sidebar {
		if cur.Title == section.Title {
			fe.sidebar[i] = section
			updated = true
			break
		}
	}
	if !updated {
		if section.Title == "" {
			fe.sidebar = append([]SidebarSection{section}, fe.sidebar...)
		} else {
			fe.sidebar = append(fe.sidebar, section)
		}
	}
	fe.renderSidebar()
}

func (fe *frontendPretty) viewSidebar() string {
	fe.renderSidebar()
	return fe.sidebarBuf.String()
}

var sidebarBG lipgloss.TerminalColor

func init() {
	// delegate sidebar background to editline background
	focusedStyle, _ := editline.DefaultStyles()
	editlineStyle := focusedStyle.Editor.CursorLine
	sidebarBG = editlineStyle.GetBackground()
}

func (fe *frontendPretty) renderSidebar() {
	fe.setWindowSizeLocked(fe.window)
	if fe.sidebarWidth == 0 {
		// sidebar not displayed; don't bother
		return
	}

	fe.sidebarBuf.Reset()

	for i, section := range fe.sidebar {
		content := section.Content
		if section.ContentFunc != nil {
			content = section.ContentFunc(fe.sidebarWidth)
		}

		if content == "" {
			// Section became empty (e.g. changes synced); don't show it
			continue
		}

		if i > 0 {
			fe.sidebarBuf.WriteString("\n")
			fe.sidebarBuf.WriteString("\n")
		}

		keymap := new(strings.Builder)
		if section.Title != "" {
			fe.sidebarBuf.WriteString(fe.viewOut.String(section.Title).
				Foreground(termenv.ANSIBrightBlack).String())
		}

		filler := fe.sidebarWidth - len(section.Title)
		filler -= 6 // 1 border + 2 spaces * 2 sides + 1 space between title and bar
		if len(section.KeyMap) > 0 {
			filler -= fe.renderKeymap(keymap,
				KeymapStyle.Background(sidebarBG),
				section.KeyMap)
			filler -= 1 // space between bar and keymap
		}

		if filler > 0 {
			horizBar := fe.viewOut.String(strings.Repeat(HorizBar, filler)).String()
			fe.sidebarBuf.WriteString(
				lipgloss.NewStyle().
					Foreground(ANSIBrightBlack).
					Background(sidebarBG).
					Render(" " + horizBar + " "),
			)
		}

		fe.sidebarBuf.WriteString(keymap.String())
		fe.sidebarBuf.WriteString("\n\n")

		// reset everything but the background
		content = strings.ReplaceAll(content, reset, termenv.CSI+"39;22;23;24;25;27;28;29m")

		fe.sidebarBuf.WriteString(strings.TrimRight(content, "\n"))
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
func (fe *frontendPretty) Run(ctx context.Context, opts dagui.FrontendOpts, run func(context.Context) (cleanups.CleanupF, error)) error {
	if opts.TooFastThreshold == 0 {
		opts.TooFastThreshold = 100 * time.Millisecond
	}
	if opts.GCThreshold == 0 {
		opts.GCThreshold = 1 * time.Second
	}
	fe.FrontendOpts = opts

	if fe.reportOnly {
		cleanup, err := run(ctx)
		if cleanup != nil {
			err = errors.Join(err, cleanup())
		}
		fe.err = err
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

func (fe *frontendPretty) HandlePrompt(ctx context.Context, title, prompt string, dest any) error {
	switch x := dest.(type) {
	case *bool:
		return fe.handlePromptBool(ctx, title, prompt, x)
	case *string:
		return fe.handlePromptString(ctx, title, prompt, x)
	default:
		return fmt.Errorf("unsupported prompt destination type: %T", dest)
	}
}

func (fe *frontendPretty) HandleForm(ctx context.Context, form *huh.Form) error {
	done := make(chan struct{}, 1)

	fe.program.Send(promptMsg{
		form: form,
		result: func(f *huh.Form) {
			close(done)
		},
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (fe *frontendPretty) Opts() *dagui.FrontendOpts {
	return &fe.FrontendOpts
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

func (fe *frontendPretty) runWithTUI(ctx context.Context, run func(context.Context) (cleanups.CleanupF, error)) (rerr error) {
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

// FinalRender is called after the program has finished running and prints the
// final output after the TUI has exited.
func (fe *frontendPretty) FinalRender(w io.Writer) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()

	// Hint for future rendering that this is the final, non-interactive render
	// (so don't show key hints etc.)
	fe.finalRender = true

	// Render the full trace.
	fe.ZoomedSpan = fe.db.PrimarySpan
	fe.recalculateViewLocked()

	// Unfocus for the final render.
	fe.FocusedSpan = dagui.SpanID{}

	r := newRenderer(fe.db, fe.contentWidth/2, fe.FrontendOpts)

	out := NewOutput(w, termenv.WithProfile(fe.profile))

	if fe.Debug || fe.Verbosity >= dagui.ShowCompletedVerbosity || fe.err != nil {
		fe.renderProgress(out, r, fe.window.Height, "")

		if fe.msgPreFinalRender.Len() > 0 {
			defer func() {
				fmt.Fprintln(w)
				handleTelemetryErrorOutput(w, out, fe.TelemetryError)
				fmt.Fprintln(os.Stderr, fe.msgPreFinalRender.String())
			}()
		}
	}

	if fe.err != nil && fe.shell == nil {
		if fe.hasShownRootError() {
			// If we've already shown the root cause error for the command, we can
			// skip displaying the primary output and error, since it's just a poorer
			// representation of the same error (`Error: input: ...`)
			return ExitError{Code: 1, Original: fe.err}
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

const keypressDuration = 500 * time.Millisecond

func (fe *frontendPretty) renderKeymap(out io.Writer, style lipgloss.Style, keys []key.Binding) int {
	w := new(strings.Builder)
	var showedKey bool
	// Blank line prior to keymap
	for _, key := range keys {
		mainKey := key.Keys()[0]
		var pressed bool
		if time.Since(fe.pressedKeyAt) < keypressDuration {
			pressed = slices.Contains(key.Keys(), fe.pressedKey)
		}
		if !key.Enabled() && !pressed {
			continue
		}
		keyStyle := style
		if pressed {
			keyStyle = keyStyle.Foreground(nil)
		}
		if showedKey {
			fmt.Fprint(w, style.Render(" "+DotTiny+" "))
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
	if fe.form != nil {
		return fe.form.KeyBinds()
	}

	if fe.editlineFocused {
		bnds := []key.Binding{
			key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "nav mode")),
		}
		if fe.shell != nil {
			bnds = append(bnds, fe.shell.KeyBindings(out)...)
		}
		return bnds
	}

	var quitMsg string
	if fe.interrupted {
		quitMsg = "quit!"
	} else if fe.shell != nil {
		quitMsg = "interrupt"
	} else {
		quitMsg = "quit"
	}

	noExitHelp := "no exit"
	if fe.NoExit {
		color := termenv.ANSIYellow
		if fe.done || fe.interrupted {
			color = termenv.ANSIRed
		}
		noExitHelp = out.String(noExitHelp).Foreground(color).String()
	}
	var focused *dagui.Span
	if fe.FocusedSpan.IsValid() {
		focused = fe.db.Spans.Map[fe.FocusedSpan]
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
		key.NewBinding(key.WithKeys("+/-", "+", "-"),
			key.WithHelp("+/-", fmt.Sprintf("verbosity=%d", fe.Verbosity))),
		key.NewBinding(key.WithKeys("E"),
			key.WithHelp("E", noExitHelp)),
		key.NewBinding(key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", quitMsg)),
		key.NewBinding(key.WithKeys("esc"),
			key.WithHelp("esc", "unzoom"),
			KeyEnabled(fe.ZoomedSpan.IsValid() && fe.ZoomedSpan != fe.db.PrimarySpan)),
		key.NewBinding(key.WithKeys("r"),
			key.WithHelp("r", "go to error"),
			KeyEnabled(focused != nil && focused.ErrorOrigin != nil)),
		key.NewBinding(key.WithKeys("t"),
			key.WithHelp("t", "start terminal"),
			KeyEnabled(focused != nil && fe.terminalCallback(focused) != nil),
		),
	}
}

func KeyEnabled(enabled bool) key.BindingOpt {
	return func(b *key.Binding) {
		b.SetEnabled(enabled)
	}
}

func (fe *frontendPretty) Render(out TermOutput) error {
	progHeight := fe.window.Height

	r := newRenderer(fe.db, fe.contentWidth/2, fe.FrontendOpts)

	var progPrefix string
	if fe.rowsView != nil && fe.rowsView.Zoomed != nil && fe.rowsView.Zoomed.ID != fe.db.PrimarySpan {
		fe.renderStep(out, r, &dagui.TraceRow{
			Span:     fe.rowsView.Zoomed,
			Expanded: true,
		}, "")
		progHeight -= 1
		progPrefix = "  "
	}

	below := new(strings.Builder)
	if logs := fe.logs.Logs[fe.ZoomedSpan]; logs != nil && logs.UsedHeight() > 0 {
		logs.SetHeight(fe.window.Height / 3)
		logs.SetPrefix(progPrefix)
		fmt.Fprint(below, logs.View())
	}

	if below.Len() > 0 {
		progHeight -= lipgloss.Height(below.String())
	}

	if fe.editline != nil {
		progHeight -= lipgloss.Height(fe.editlineView())
	}

	if fe.form != nil {
		progHeight -= lipgloss.Height(fe.formView())
	}

	progHeight -= lipgloss.Height(fe.keymapView())
	progHeight -= 1 // mind the gap between progress and logs

	if fe.renderProgress(out, r, progHeight, progPrefix) {
		fmt.Fprintln(out)
	}

	if below.Len() > 0 {
		fmt.Fprint(out, below.String())
		fmt.Fprintln(out)
	}

	return nil
}

func (fe *frontendPretty) keymapView() string {
	outBuf := new(strings.Builder)
	out := NewOutput(outBuf, termenv.WithProfile(fe.profile))
	if fe.UsingCloudEngine {
		fmt.Fprint(out, lipgloss.NewStyle().
			Foreground(lipgloss.ANSIColor(termenv.ANSIBrightMagenta)).
			Render(CloudIcon+" cloud"))
		fmt.Fprint(out, KeymapStyle.Render(" "+VertBoldDash3+" "))
	}
	fe.renderKeymap(out, KeymapStyle, fe.keys(out))
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

func (fe *frontendPretty) renderProgress(out TermOutput, r *renderer, height int, prefix string) (rendered bool) {
	if fe.rowsView == nil {
		return
	}

	rows := fe.rows

	if fe.finalRender {
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

	for _, line := range lines {
		fmt.Fprintln(out, line)
	}

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

	// Get sidebar content
	sidebarContent := fe.viewSidebar()

	var mainView string
	if fe.editline != nil {
		mainView += fe.view.String()
		mainView += fe.editlineView()
	} else {
		mainView += fe.view.String()
	}
	if fe.form != nil {
		mainView += fe.formView()
	}
	if !strings.HasSuffix(mainView, "\n") {
		mainView += "\n"
	}
	mainView += fe.keymapView()

	if sidebarContent != "" {
		// If we have sidebar content, create a two-pane layout
		return fe.renderWithSidebar(mainView, sidebarContent)
	}

	return mainView
}

func haircut(s string, maxHeight int) (string, int) {
	s = strings.TrimRight(s, "\n")
	lines := strings.Split(s, "\n")
	height := len(lines) + 1
	if height <= maxHeight {
		return s, height - 1
	}
	remove := height - maxHeight - 1
	return strings.Join(lines[remove:], "\n"), maxHeight
}

// renderWithSidebar creates a two-pane layout with main content on the left and sidebar on the right
func (fe *frontendPretty) renderWithSidebar(mainContent, sidebarContent string) string {
	if fe.sidebarWidth == 0 {
		// If window is too narrow for sidebar, just show main content
		return mainContent
	}

	contentView, contentHeight := haircut(mainContent, fe.window.Height)

	styledSidebar := lipgloss.NewStyle().
		Width(fe.sidebarWidth).
		MaxHeight(fe.window.Height).
		Height(contentHeight).
		Background(sidebarBG).
		Border(lipgloss.Border{
			Left: BorderLeft, // use a line that hugs the background
		}, false, false, false, true).
		BorderForeground(ANSIBrightBlack).
		Padding(1, 2).
		Render(sidebarContent)

	styledContent := lipgloss.NewStyle().
		MaxWidth(fe.contentWidth).
		MaxHeight(fe.window.Height).
		Render(contentView)

	return lipgloss.JoinHorizontal(
		lipgloss.Bottom,
		styledContent,
		styledSidebar,
	)
}

func (fe *frontendPretty) editlineView() string {
	view := fe.editline.View()
	if fe.promptErr != nil {
		view = fe.viewOut.String("ERROR: "+fe.promptErr.Error()).Foreground(termenv.ANSIBrightRed).String() + "\n" + view
	}
	return view
}

func (fe *frontendPretty) formView() string {
	return fe.form.View() + "\n\n"
}

type doneMsg struct {
	err error
}

func (fe *frontendPretty) spawn() (msg tea.Msg) {
	cleanup, err := fe.run(fe.runCtx)
	return cleanupMsg{
		cleanup: func() {
			err := cleanup()
			if err != nil {
				slog.Error("cleanup failed", "err", err)
			}
		},
		msg: doneMsg{err},
	}
}

type cleanupMsg struct {
	cleanup func()
	msg     tea.Msg
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
	case cleanupMsg:
		if !fe.NoExit || fe.interrupted {
			go func() {
				msg.cleanup()
				fe.program.Send(msg.msg)
			}()
		} else {
			fe.cleanup = msg.cleanup
			go fe.program.Send(msg.msg)
		}
		return fe, nil

	case doneMsg: // run finished
		slog.Debug("run finished", "err", msg.err)
		fe.done = true
		fe.err = msg.err
		if fe.eof && (!fe.NoExit || fe.interrupted) {
			fe.quitting = true
			return fe, tea.Quit
		}
		return fe, nil

	case eofMsg: // received end of updates
		slog.Debug("got EOF")
		fe.eof = true
		if fe.done && (!fe.NoExit || fe.interrupted) {
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
			tea.Printf(`Dagger interactive shell. Type ".help" for more information. Press Ctrl+D to exit.`+"\n"),
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
		return fe, fe.offloadUpdates(msg)

	case editline.InputCompleteMsg:
		if !fe.editlineFocused {
			return fe, nil
		}

		// reset prompt error state
		fe.promptErr = nil

		value := fe.editline.Value()
		fe.editline.AddHistoryEntry(value)
		fe.promptFg = termenv.ANSIYellow
		promptCmd := fe.updatePrompt()

		// reset now that we've accepted input
		fe.editline.Reset()
		if fe.shell != nil {
			ctx, cancel := context.WithCancelCause(fe.shellCtx)
			fe.shellInterrupt = cancel
			fe.shellRunning = true

			// switch back to following the bottom and re-enter nav mode
			fe.goEnd()
			fe.enterNavMode(true)

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
		// show error result above the prompt
		fe.promptErr = msg.err
		if msg.err == nil {
			fe.promptFg = termenv.ANSIGreen
		} else {
			fe.promptFg = termenv.ANSIRed
		}
		var cmd tea.Cmd
		if fe.autoModeSwitch {
			cmd = tea.Batch(cmd, fe.enterInsertMode(true))
		}
		cmd = tea.Batch(cmd, fe.updatePrompt())
		fe.shellRunning = false
		return fe, cmd

	case UpdatePromptMsg:
		return fe, fe.updatePrompt()

	case tea.KeyMsg:
		switch {
		// Handle prompt input if there's an active prompt
		case fe.form != nil:
			return fe, fe.offloadUpdates(msg)
		// send all input to editline if it's focused
		case fe.editlineFocused:
			return fe, fe.handleEditlineKey(msg)
		default:
			return fe, fe.handleNavKey(msg)
		}

	case tea.WindowSizeMsg:
		fe.setWindowSizeLocked(msg)
		return fe, fe.offloadUpdates(msg)

	case frameMsg:
		fe.now = time.Time(msg)
		fe.renderLocked()
		fe, flushCmd := fe.flushScrollback()
		// NB: take care not to forward Frame downstream, since that will result
		// in runaway ticks. instead inner components should send a SetFpsMsg to
		// adjust the outermost layer.
		return fe, tea.Batch(flushCmd, frame(fe.fps))

	case promptMsg:
		form := msg.form
		form.SubmitCmd = func() tea.Msg {
			msg.result(msg.form)
			return promptDone{}
		}
		form.CancelCmd = func() tea.Msg {
			msg.result(msg.form)
			return promptDone{}
		}
		fe.form = form.WithTheme(huh.ThemeBase16()).WithShowHelp(false)
		return fe, fe.form.Init()

	case promptDone:
		fe.form = nil
		return fe, nil

	default:
		return fe, fe.offloadUpdates(msg)
	}
}

// offloadUpdates delegates messages to embedded components, whether they're
// Bubbletea built-in messages (tea.KeyMsg) or internal messages to those
// components
func (fe *frontendPretty) offloadUpdates(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	if fe.form != nil {
		form, cmd := fe.form.Update(msg)
		cmds = append(cmds, cmd)
		if f, ok := form.(*huh.Form); ok {
			fe.form = f
		}
	}
	{
		// spinner messages
		m, cmd := fe.spinner.Update(msg)
		fe.spinner = m.(*Rave)
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

type promptDone struct{}

func (fe *frontendPretty) enterNavMode(auto bool) {
	fe.autoModeSwitch = auto
	fe.editlineFocused = false
	fe.editline.Blur()
	fe.renderLocked()
}

func (fe *frontendPretty) enterInsertMode(auto bool) tea.Cmd {
	fe.autoModeSwitch = auto
	if fe.editline != nil {
		fe.editlineFocused = true
		fe.updatePrompt()
		fe.renderLocked()
		return fe.editline.Focus()
	}
	return nil
}

func (fe *frontendPretty) terminal() {
	if !fe.FocusedSpan.IsValid() {
		return
	}
	focused := fe.db.Spans.Map[fe.FocusedSpan]
	if focused == nil {
		return
	}

	callback := fe.terminalCallback(focused)
	if callback != nil {
		go func() {
			err := callback()
			if err != nil {
				slog.Error("failed to open terminal for span", err)
			}
		}()
	}
}

func (fe *frontendPretty) terminalCallback(span *dagui.Span) func() error {
	if fe.dag == nil {
		// we haven't got a dag client, so can't open a terminal
		return nil
	}

	// NOTE: this func is in the hot-path, so just use the call info to
	// determine if we can create a callback - the actual callback can do the
	// expensive id reconstruction
	call := span.Call()
	if call == nil {
		return nil
	}

	switch call.Type.NamedType {
	case "Container":
		if span.IsRunning() {
			break
		}
		return func() error {
			id, err := loadIDFromSpan(span)
			if err != nil {
				return err
			}
			_, err = fe.dag.LoadContainerFromID(dagger.ContainerID(id)).Terminal().Sync(fe.runCtx)
			return err
		}
	case "Directory":
		if span.IsRunning() {
			break
		}
		return func() error {
			id, err := loadIDFromSpan(span)
			if err != nil {
				return err
			}
			_, err = fe.dag.LoadDirectoryFromID(dagger.DirectoryID(id)).Terminal().Sync(fe.runCtx)
			return err
		}
	case "Service":
		return func() error {
			id, err := loadIDFromSpan(span)
			if err != nil {
				return err
			}
			_, err = fe.dag.LoadServiceFromID(dagger.ServiceID(id)).Terminal().Sync(fe.runCtx)
			return err
		}
	}

	return nil
}

func loadIDFromSpan(span *dagui.Span) (string, error) {
	callID, err := span.CallID()
	if err != nil {
		return "", err
	}
	id, err := callID.Encode()
	if err != nil {
		return "", err
	}
	return id, nil
}

func (fe *frontendPretty) handleEditlineKey(msg tea.KeyMsg) (cmd tea.Cmd) {
	defer func() {
		// update the prompt in all cases since e.g. going through history
		// can change it
		cmd = tea.Sequence(cmd, fe.updatePrompt())
	}()
	fe.pressedKey = msg.String()
	fe.pressedKeyAt = time.Now()
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
		fe.enterNavMode(false)
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
		if fe.shell != nil {
			cmd := fe.shell.ReactToInput(fe.shellCtx, msg, true, fe.editline)
			if cmd != nil {
				return cmd
			}
		}
	}
	el, cmd := fe.editline.Update(msg)
	fe.editline = el.(*editline.Model)
	return cmd
}

//nolint:gocyclo // splitting this up doesn't feel more readable
func (fe *frontendPretty) handleNavKey(msg tea.KeyMsg) tea.Cmd {
	lastKey := fe.pressedKey
	fe.pressedKey = msg.String()
	fe.pressedKeyAt = time.Now()
	switch msg.String() {
	case "q", "ctrl+c":
		if fe.shell != nil {
			// in shell mode, always just interrupt, don't quit; use Ctrl+D to quit
			if fe.shellInterrupt != nil {
				fe.shellInterrupt(errors.New("interrupted"))
			}
		} else {
			return fe.quit(ErrInterrupted)
		}
	case "ctrl+\\": // SIGQUIT
		fe.program.ReleaseTerminal()
		sigquit()
		return nil
	case "E":
		fe.NoExit = !fe.NoExit
		return nil
	case "down", "j":
		fe.goDown()
		return nil
	case "up", "k":
		fe.goUp()
		return nil
	case "left", "h":
		fe.closeOrGoOut()
		return nil
	case "right", "l":
		fe.openOrGoIn()
		return nil
	case "home":
		fe.goStart()
		return nil
	case "end", "G", " ":
		fe.goEnd()
		fe.pressedKey = "end"
		fe.pressedKeyAt = time.Now()
		return nil
	case "r":
		fe.goErrorOrigin()
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
		return fe.enterInsertMode(false)
	case "t":
		fe.terminal()
		return nil
	default:
		if fe.shell != nil {
			cmd := fe.shell.ReactToInput(fe.shellCtx, msg, false, fe.editline)
			if cmd != nil {
				return cmd
			}
		}
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

	return fe.offloadUpdates(msg)
}

func (fe *frontendPretty) initEditline() {
	// create the editline
	fe.editline = editline.New(fe.contentWidth, fe.window.Height)
	fe.editline.HideKeyMap = true
	fe.editlineFocused = true
	// HACK: for some reason editline's first paint is broken (only shows
	// first 2 chars of prompt, doesn't show cursor). Sending it a message
	// - any message - fixes it.
	fe.editline.Update(nil)
}

type UpdatePromptMsg struct{}

type scrollbackRow struct {
	row *dagui.TraceRow
	buf strings.Builder
}

type scrollbackRows []*scrollbackRow

func (sr scrollbackRows) String() string {
	var buf strings.Builder
	for _, r := range sr {
		buf.WriteString(r.buf.String())
	}
	return buf.String()
}

func (sr scrollbackRows) Len() int {
	return len(sr)
}

func (sr *scrollbackRows) Reset() {
	*sr = nil
}

func (fe *frontendPretty) flushScrollback() (*frontendPretty, tea.Cmd) {
	if fe.shell == nil {
		return fe, nil
	}
	if fe.shellRunning || !fe.editlineFocused {
		// there won't be anything to flush so long as the shell is running or the
		// user is in nav mode
		return fe, nil
	}

	// Calculate visible area height
	visibleHeight := fe.window.Height
	if fe.editline != nil {
		visibleHeight -= lipgloss.Height(fe.editlineView())
	}

	r := newRenderer(fe.db, fe.contentWidth/2, fe.FrontendOpts)

	var anyFlushed bool
	for _, row := range fe.rows.Order {
		// Skip if row is already flushed
		if fe.flushed[row.Span.ID] {
			continue
		}
		if row.IsRunningOrChildRunning || !fe.logsDone(row.Span.ID, row.Depth == 0) || row.Span.IsPending() {
			break
		}
		sb := &scrollbackRow{row: row}
		out := NewOutput(&sb.buf, termenv.WithProfile(fe.profile))
		fe.renderRow(out, r, row, "")
		fe.scrollback = append(fe.scrollback, sb)
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

	// Build new buffers
	var onscreen scrollbackRows
	var offscreen strings.Builder
	for _, sb := range fe.scrollback {
		if offscreenHeight > 0 {
			fmt.Fprint(&offscreen, sb.buf.String())
			offscreenHeight -= lipgloss.Height(sb.buf.String())
			fe.offscreen[sb.row.Span.ID] = true
		} else {
			onscreen = append(onscreen, sb)
		}
	}

	// Replace scrollback with remaining visible lines
	fe.scrollback = onscreen

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
	if fe.cleanup != nil {
		cleanup := fe.cleanup
		fe.cleanup = nil // prevent double cleanup
		go func() {
			cleanup()
			fe.quitting = true
			fe.program.Quit()
		}()
	} else if fe.interrupted {
		slog.Warn("exiting immediately")
		fe.quitting = true
		return tea.Quit
	} else {
		slog.Warn("canceling... (press again to exit immediately)")
		fe.interrupted = true
		fe.interrupt(interruptErr)
	}
	return nil
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
	focused := fe.rows.BySpan[fe.FocusedSpan]
	if focused == nil {
		return
	}
	parent := focused.Parent
	if parent == nil {
		return
	}
	fe.FocusedSpan = parent.Span.ID
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

func (fe *frontendPretty) closeOrGoOut() {
	// Only toggle if we have a valid focused span
	if fe.FocusedSpan.IsValid() {
		// Get the either explicitly set or defaulted value
		var isExpanded bool
		if row, ok := fe.rows.BySpan[fe.FocusedSpan]; ok {
			isExpanded = row.Expanded
		}
		if !isExpanded {
			// already closed; move up
			fe.goOut()
			return
		}
		// Toggle the expanded state for the focused span
		fe.setExpanded(fe.FocusedSpan, !isExpanded)
		// Recalculate view to reflect changes
		fe.recalculateViewLocked()
	}
}

func (fe *frontendPretty) openOrGoIn() {
	// Only toggle if we have a valid focused span
	if fe.FocusedSpan.IsValid() {
		// Get the either explicitly set or defaulted value
		var isExpanded bool
		if row, ok := fe.rows.BySpan[fe.FocusedSpan]; ok {
			isExpanded = row.Expanded
		}
		if isExpanded {
			// already expanded; go in
			fe.goIn()
			return
		}
		// Toggle the expanded state for the focused span
		fe.setExpanded(fe.FocusedSpan, true)
		// Recalculate view to reflect changes
		fe.recalculateViewLocked()
	}
}

func (fe *frontendPretty) goErrorOrigin() {
	fe.autoFocus = false
	focused := fe.db.Spans.Map[fe.FocusedSpan]
	if focused == nil {
		return
	}
	if focused.ErrorOrigin == nil {
		return
	}
	fe.FocusedSpan = focused.ErrorOrigin.ID
	focusedRow := fe.rowsView.BySpan[fe.FocusedSpan]
	if focusedRow == nil {
		return
	}
	for cur := focusedRow.Parent; cur != nil; cur = cur.Parent {
		// expand parents of target span
		fe.setExpanded(cur.Span.ID, true)
	}
	fe.recalculateViewLocked()
}

const sidebarMinWidth = 30
const sidebarMaxWidth = 50

func (fe *frontendPretty) setWindowSizeLocked(msg tea.WindowSizeMsg) {
	fe.window = msg
	if len(fe.sidebar) > 0 {
		fe.sidebarWidth = max(sidebarMinWidth, min(sidebarMaxWidth, fe.window.Width/3))
	} else {
		fe.sidebarWidth = 0
	}
	fe.contentWidth = msg.Width - fe.sidebarWidth
	if fe.contentWidth < 0 {
		fe.contentWidth = msg.Width
		fe.sidebarWidth = 0
	}
	fe.logs.SetWidth(fe.contentWidth)
	if fe.editline != nil {
		fe.editline.SetSize(fe.contentWidth, msg.Height)
		fe.editline.Update(nil) // bleh
	}
}

func (fe *frontendPretty) setExpanded(id dagui.SpanID, expanded bool) {
	if fe.SpanExpanded == nil {
		fe.SpanExpanded = make(map[dagui.SpanID]bool)
	}
	fe.SpanExpanded[id] = expanded
	fe.recalculateViewLocked()
}

func (fe *frontendPretty) renderLocked() {
	fe.view.Reset()
	fe.Render(fe.viewOut)
}

func (fe *frontendPretty) renderRow(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) bool { //nolint: gocyclo
	if fe.offscreen[row.Span.ID] && fe.editlineFocused {
		return false
	}
	if fe.shell != nil {
		if row.Depth == 0 {
			// navigating history and there's a previous row
			if (!fe.editlineFocused && row.Previous != nil) ||
				(row.Previous != nil && !fe.offscreen[row.Previous.Span.ID]) {
				fmt.Fprintln(out, out.String(prefix))
			}
		}
	} else if row.PreviousVisual != nil &&
		row.PreviousVisual.Depth >= row.Depth &&
		!row.Chained &&
		( // ensure gaps after last nested child
		row.PreviousVisual.Depth > row.Depth ||
			// ensure gaps before unchained calls
			row.Span.Call() != nil ||
			// ensure gaps before checks
			row.Span.CheckName != "" ||
			// ensure gaps between calls and non-calls
			(row.PreviousVisual.Span.Call() != nil && row.Span.Call() == nil) ||
			// ensure gaps between messages
			(row.PreviousVisual.Span.Message != "" && row.Span.Message != "") ||
			// ensure gaps going from tool calls to messages
			(row.PreviousVisual.Span.Message == "" && row.Span.Message != "")) {
		fmt.Fprint(out, prefix)
		r.fancyIndent(out, row.PreviousVisual, false, false)
		fmt.Fprintln(out)
	}
	span := row.Span
	isFocused := span.ID == fe.FocusedSpan && !fe.editlineFocused
	fe.renderStep(out, r, row, prefix)
	if span.Message == "" && // messages are displayed in renderStep
		(row.Expanded || row.Span.LLMTool != "") {
		fe.renderStepLogs(out, r, row, prefix, isFocused)
	} else if (row.Span.RollUpLogs || fe.shell != nil) && row.Depth == 0 && !row.Expanded {
		// in shell mode, we print top-level command logs unindented, like shells
		// usually does
		if logs := fe.logs.Logs[row.Span.ID]; logs != nil && logs.UsedHeight() > 0 {
			if fe.shell != nil {
				unindent := *row
				unindent.Depth = -1
				fe.renderLogs(out, r, &unindent, logs, logs.UsedHeight(), prefix, false)
			} else if row.Span.RollUpLogs && row.IsRunningOrChildRunning {
				// Only show rolled-up logs while the span is running.
				fe.renderStepLogs(out, r, row, prefix, isFocused)
			}
		}
	}
	if row.ShouldShowCause() {
		fe.renderErrorCause(out, r, row, prefix)
	} else {
		fe.renderStepError(out, r, row, prefix)
	}
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

// sync this with core.llmLogsLastLines to ensure user and LLM sees the same
// thing
const llmLogsLastLines = 8

func (fe *frontendPretty) renderStepLogs(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, focused bool) bool {
	limit := fe.window.Height / 3
	if row.Span.LLMTool != "" && !row.Expanded {
		limit = llmLogsLastLines
	}
	if logs := fe.logs.Logs[row.Span.ID]; logs != nil {
		return fe.renderLogs(out, r, row, logs, limit, prefix, focused)
	}
	return false
}

func spanIsVisible(span *dagui.Span, row *dagui.TraceRow) bool {
	for row := row.PreviousVisual; row != nil; row = row.PreviousVisual {
		if rowIsOrRevealsSpan(span, row) {
			return true
		}
	}
	for row := row.NextVisual; row != nil; row = row.NextVisual {
		if rowIsOrRevealsSpan(span, row) {
			return true
		}
	}
	return false
}

func rowIsOrRevealsSpan(span *dagui.Span, row *dagui.TraceRow) bool {
	// Simple case: we found the span
	return row.Span.ID == span.ID ||
		// Complex case: the span is an error; did we show it from another collapsed
		// span that bubbled it up?
		(row.ShouldShowCause() && row.Span.ErrorOrigin.ID == span.ID)
}

func (fe *frontendPretty) renderErrorCause(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) {
	rootCause := row.Span.ErrorOrigin

	rootCauseTree := fe.rowsView.BySpan[rootCause.ID]
	if rootCauseTree == nil {
		// error origin has no tree, likely due to internal/hidden spans
		// create a synthetic tree by walking span parents
		var syntheticParents []*dagui.Span
		for current := rootCause; current != nil && current.ParentID.IsValid(); {
			parent := fe.db.Spans.Map[current.ParentID]
			if parent == nil {
				break
			}
			syntheticParents = append(syntheticParents, parent)
			current = parent
			// Stop if we reach the current row's span or a boundary
			if parent.ID == row.Span.ID {
				break
			}
		}

		// Create synthetic tree structure
		rootCauseTree = &dagui.TraceTree{
			Span: rootCause,
		}

		// Build parent chain
		current := rootCauseTree
		for i := len(syntheticParents) - 1; i >= 0; i-- {
			parent := &dagui.TraceTree{
				Span: syntheticParents[i],
			}
			current.Parent = parent
			current = parent
		}
	}

	rootCauseRow := &dagui.TraceRow{
		Span:     rootCause,
		Chained:  false,
		Expanded: true,
		Depth:    row.Depth,
	}

	var parents []*dagui.TraceRow
	for p := rootCauseTree.Parent; p != nil; p = p.Parent {
		if p.Span.ID == row.Span.ID {
			break
		}
		parentRow := &dagui.TraceRow{
			Span:     p.Span,
			Chained:  p.Chained,
			Depth:    row.Depth,
			Expanded: true,
		}
		parents = append(parents, parentRow)
	}

	indent := strings.Repeat("  ", row.Depth)
	if !fe.finalRender {
		indent += "  "
	}

	renderStepWithoutToggle := func(out TermOutput, row *dagui.TraceRow, skipStatus bool) {
		fmt.Fprint(out, prefix, indent)
		fe.renderStepTitle(out, r, row, prefix+indent, skipStatus)
		fmt.Fprintln(out)
	}
	slices.Reverse(parents)
	context := new(strings.Builder)
	noColorOut := termenv.NewOutput(context, termenv.WithProfile(termenv.Ascii))
	for _, p := range parents {
		renderStepWithoutToggle(noColorOut, p, true)
	}
	fmt.Fprint(out, out.String(context.String()).Foreground(termenv.ANSIBrightBlack).Faint())
	renderStepWithoutToggle(out, rootCauseRow, false)
	if logs := fe.logs.Logs[rootCauseRow.Span.ID]; logs != nil {
		row := *rootCauseRow
		height := fe.window.Height / 3
		if fe.finalRender {
			height = 0
			row.Depth = -1
		}
		fe.renderLogs(out, r, &row, logs, height, prefix+indent, false)
	}
	fe.renderStepError(out, r, rootCauseRow, prefix+indent)

	fe.shownErrs[rootCause.ID] = true
}

func (fe *frontendPretty) hasShownRootError() bool {
	originCtx := telemetry.ErrorOrigin(fe.err)
	if !originCtx.IsValid() {
		return false
	}
	if !fe.shownErrs[dagui.SpanID{SpanID: originCtx.SpanID()}] {
		return false
	}
	return true
}

func (fe *frontendPretty) renderStepError(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) {
	if row.Span.ErrorOrigin != nil &&
		spanIsVisible(row.Span.ErrorOrigin, row) {
		// span's error originated elsewhere; don't repeat the message, the ERROR status
		// links to its origin instead
		return
	}
	fe.shownErrs[row.Span.ID] = true
	errorCounts := map[string]int{}
	for _, span := range row.Span.Errors().Order {
		errText := span.Status.Description
		if errText == "" {
			continue
		}
		errorCounts[errText]++
	}
	type errWithCount struct {
		text  string
		count int
	}
	var counts []errWithCount
	for errText, count := range errorCounts {
		counts = append(counts, errWithCount{errText, count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].count == counts[j].count {
			return counts[i].text < counts[j].text
		}
		return counts[i].count > counts[j].count
	})
	for _, c := range counts {
		errText, count := c.text, c.count
		// Calculate available width for text
		prefixWidth := lipgloss.Width(prefix)
		indentWidth := row.Depth * 2 // Assuming indent is 2 spaces per depth level
		markerWidth := 2             // "! " prefix
		availableWidth := fe.contentWidth - prefixWidth - indentWidth - markerWidth
		if availableWidth > 0 {
			errText = cellbuf.Wrap(errText, availableWidth, "")
		}

		if count > 1 {
			errText += "\n" + out.String(fmt.Sprintf("x%d", count)).Bold().String()
		}

		// Print each wrapped line with proper indentation
		first := true
		for line := range strings.SplitSeq(strings.TrimSpace(errText), "\n") {
			fmt.Fprint(out, prefix)
			r.fancyIndent(out, row, false, false)
			var symbol string
			if first {
				symbol = "!"
			} else {
				symbol = " "
			}
			fmt.Fprintf(out,
				out.String("%s %s").Foreground(termenv.ANSIRed).String(),
				symbol,
				line,
			)
			fmt.Fprintln(out)
			first = false
		}
	}
}

func (fe *frontendPretty) renderStepTitle(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string, abridged bool) error {
	span := row.Span
	chained := row.Chained
	depth := row.Depth
	isFocused := span.ID == fe.FocusedSpan && !fe.editlineFocused && fe.form == nil

	if !abridged {
		fe.renderStatusIcon(out, row)
		fmt.Fprint(out, " ")
	} else {
		fmt.Fprint(out, "  ")
	}

	if r.Debug {
		fmt.Fprintf(out, out.String("%s ").Foreground(termenv.ANSIBrightBlack).String(), span.ID)
	}

	var empty bool
	if span.Message != "" {
		// when a span represents a message, we don't need to print its name
		//
		// NOTE: arguably this should be opt-in, but it's not clear how the
		// span name relates to the message in all cases; is it the
		// subject? or author? better to be explicit with attributes.
		if fe.renderStepLogs(out, r, row, prefix, isFocused) {
			if span.LLMRole == telemetry.LLMRoleUser {
				// Bail early if we printed a user message span; these don't have any
				// further information to show. Duration is always 0, metrics are empty,
				// status is always OK.
				return nil
			}
			r.fancyIndent(out, row, false, false)
			bar := out.String(VertBoldBar).Foreground(restrainedStatusColor(span))
			if isFocused {
				bar = hl(bar)
			}
			fmt.Fprint(out, bar)
		} else {
			empty = true
		}
	} else if call := span.Call(); call != nil {
		if err := r.renderCall(out, span, call, prefix, chained, depth+1, span.Internal, row); err != nil {
			return err
		}
	} else if span != nil {
		if span.Name == "" {
			empty = true
		}
		if err := r.renderSpan(out, span, span.Name); err != nil {
			return err
		}
	}

	if span != nil {
		// TODO: when a span has child spans that have progress, do 2-d progress
		// fe.renderVertexTasks(out, span, depth)
		r.renderDuration(out, span, !empty)

		// Render RollUp dots after status/duration for collapsed RollUp spans
		if span.RollUpSpans {
			dots := fe.renderRollUpDots(out, span, row, prefix, fe.FrontendOpts)
			if dots != "" {
				fmt.Fprint(out, " ")
				fmt.Fprint(out, dots)
			}
		}

		if !abridged {
			fe.renderStatus(out, span)
			r.renderMetrics(out, span)

			summary := map[string]int{}
			for effect := range span.EffectSpans {
				if effect.Passthrough {
					// Don't show spans which are aggressively hidden.
					continue
				}
				icon, isInteresting := fe.statusIcon(effect)
				if !isInteresting {
					// summarize boring statuses, rather than showing them in full
					summary[icon]++
					continue
				}
				fmt.Fprintf(out, " %s ", out.String(icon).Foreground(statusColor(effect)))
				r.renderSpan(out, effect, effect.Name)
			}

			for _, icon := range statusOrder {
				count := summary[icon]
				if count > 0 {
					color := statusColors[icon]
					fmt.Fprintf(out, " %s %s",
						out.String(icon).Foreground(color).Faint(),
						out.String(strconv.Itoa(count)).Faint())
				}
			}
		}
	}

	return nil
}

func (fe *frontendPretty) renderStep(out TermOutput, r *renderer, row *dagui.TraceRow, prefix string) error {
	span := row.Span
	isFocused := span.ID == fe.FocusedSpan && !fe.editlineFocused && fe.form == nil

	fmt.Fprint(out, prefix)
	r.fancyIndent(out, row, false, true)

	if !fe.finalRender {
		fe.renderToggler(out, row, isFocused)
		fmt.Fprint(out, " ")
	}

	if err := fe.renderStepTitle(out, r, row, prefix, false); err != nil {
		return err
	}

	fmt.Fprintln(out)

	return nil
}

var statusOrder = []string{
	DotFilled,
	IconSuccess,
	IconCached,
	IconSkipped,
	DotEmpty,
}

var statusColors = map[string]termenv.Color{
	DotHalf:     termenv.ANSIYellow,
	IconCached:  termenv.ANSIBlue,
	IconSkipped: termenv.ANSIBrightBlack,
	IconFailure: termenv.ANSIRed,
	DotEmpty:    termenv.ANSIBrightBlack,
	DotFilled:   termenv.ANSIGreen,
	IconSuccess: termenv.ANSIGreen,
}

// brailleDots maps a count (0-8) to a Braille unicode character showing that many dots
// Braille patterns "pile up" from bottom to top, left to right
var brailleDots = []rune{
	' ',      // 0 dots: empty space
	'\u2840', // 1 dot:  ⡀ (bottom-left)
	'\u2844', // 2 dots: ⡄ (bottom-left, top-left)
	'\u2846', // 3 dots: ⡆ (bottom-left, top-left, middle-left)
	'\u2847', // 4 dots: ⡇ (left column full)
	'\u28C7', // 5 dots: ⣇ (left column + bottom-right)
	'\u28E7', // 6 dots: ⣧ (left column + bottom-right, top-right)
	'\u28F7', // 7 dots: ⣷ (left column + bottom-right, top-right, middle-right)
	'\u28FF', // 8 dots: ⣿ (all dots filled)
}

// renderRollUpDots renders a visual summary of child span states using pre-computed state
func (fe *frontendPretty) renderRollUpDots(out TermOutput, span *dagui.Span, row *dagui.TraceRow, prefix string, _ dagui.FrontendOpts) string {
	if !span.RollUpSpans {
		return ""
	}

	// Use pre-computed state instead of computing on every frame
	state := span.RollUpState()
	if state == nil {
		return ""
	}

	// Calculate available width for dots
	// Account for: prefix + indent (2 spaces per depth) + toggler + space + span name (rough estimate)
	prefixWidth := lipgloss.Width(prefix)
	indentWidth := row.Depth * 2
	togglerWidth := 2 // toggler icon + space
	nameWidth := lipgloss.Width(span.Name)

	// Estimate width used by duration, metrics, status, effect summary
	// This is a rough estimate - duration ~10 chars, status ~10 chars
	extraWidth := 25

	usedWidth := prefixWidth + indentWidth + togglerWidth + nameWidth + extraWidth
	// Need at least some space for dots (minimum 5 characters for " " + 1 braille char)
	availableWidth := max(fe.contentWidth-usedWidth, 5)

	// Calculate total spans across all statuses
	totalSpans := state.SuccessCount + state.CachedCount + state.FailedCount +
		state.CanceledCount + state.RunningCount + state.PendingCount

	if totalSpans == 0 {
		return ""
	}

	// Each Braille char packs 8 dots. Calculate how many chars we can fit.
	// Reserve 1 char for spacing between groups.
	maxChars := availableWidth
	maxDots := maxChars * 8

	// Calculate scale factor: how many spans per dot
	// Start at 1:1, then scale up as needed (1:1, 2:1, 3:1, 4:1, 5:1, 10:1, etc.)
	scale := 1
	for totalSpans/scale > maxDots {
		if scale < 5 {
			scale++
		} else {
			scale = (scale/5 + 1) * 5 // Jump by 5s after reaching 5
		}
	}

	var result strings.Builder

	// Helper to render a group of dots with a given count and color
	renderGroup := func(count int, color termenv.Color) {
		if count == 0 {
			return
		}
		// Scale down the count
		dotCount := (count + scale - 1) / scale // Round up
		for i := 0; i < dotCount; i += 8 {
			dotsInChar := min(dotCount-i, 8)
			braille := string(brailleDots[dotsInChar])
			styled := out.String(braille).Foreground(color)
			result.WriteString(styled.String())
		}
	}

	// Show scale indicator if we're not at 1:1
	if scale > 1 {
		scaleIndicator := fmt.Sprintf("%d×", scale)
		styled := out.String(scaleIndicator).Foreground(termenv.ANSIBrightBlack).Faint()
		result.WriteString(styled.String())
	}

	// Render in order: success, cached, failed, canceled, running, pending
	// This creates a "settling" effect from right to left as tasks start and complete
	renderGroup(state.SuccessCount, termenv.ANSIGreen)
	renderGroup(state.CachedCount, termenv.ANSIBlue)
	renderGroup(state.FailedCount, termenv.ANSIRed)
	renderGroup(state.CanceledCount, termenv.ANSIBrightBlack)
	renderGroup(state.RunningCount, termenv.ANSIYellow)
	renderGroup(state.PendingCount, termenv.ANSIBrightBlack)

	return result.String()
}

// statusIcon returns an icon indicating the span's status, and a bool
// indicating whether it's interesting enough to reveal at a summary level
func (fe *frontendPretty) statusIcon(span *dagui.Span) (string, bool) {
	if span.IsRunningOrEffectsRunning() {
		return fe.spinner.ViewFancy(fe.now), true
	} else if span.IsCached() {
		return IconCached, false
	} else if span.IsCanceled() {
		return IconSkipped, false
	} else if span.IsFailedOrCausedFailure() {
		return IconFailure, true
	} else if span.IsPending() {
		return DotEmpty, false
	} else {
		return IconSuccess, false
	}
}

func (fe *frontendPretty) renderToggler(out TermOutput, row *dagui.TraceRow, isFocused bool) {
	// First, render chevron or placeholder for alignment
	var chevron termenv.Style
	if row.HasChildren || row.Span.HasLogs {
		if row.Expanded {
			chevron = out.String(CaretDownFilled)
		} else {
			chevron = out.String(CaretRightFilled)
		}
	} else {
		// Use a placeholder symbol for items without children
		chevron = out.String(DotFilled)
	}

	// Match the color of the span
	chevron = chevron.Foreground(termenv.ANSIBrightBlack)

	// Apply focus highlighting to chevron only
	if isFocused {
		chevron = hl(chevron.Foreground(statusColor(row.Span)))
	}
	fmt.Fprint(out, chevron.String())
}

func (fe *frontendPretty) renderStatusIcon(out TermOutput, row *dagui.TraceRow) {
	// Then render the status icon (without focus highlighting)
	var statusIcon termenv.Style
	icon, _ := fe.statusIcon(row.Span)
	statusIcon = out.String(icon)
	statusIcon = statusIcon.Foreground(statusColor(row.Span))

	if row.Span.Message != "" {
		switch row.Span.LLMRole {
		case telemetry.LLMRoleUser:
			statusIcon = out.String(Block).Foreground(termenv.ANSIMagenta)
		case telemetry.LLMRoleAssistant:
			statusIcon = out.String(VertBoldBar).Foreground(termenv.ANSIMagenta)
		}
	}

	fmt.Fprint(out, statusIcon.String())
}

func (fe *frontendPretty) renderStatus(out TermOutput, span *dagui.Span) {
	if span.CheckPassed {
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("OK").Foreground(termenv.ANSIGreen))
	} else if span.IsFailedOrCausedFailure() && !span.IsCanceled() {
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("ERROR").Foreground(termenv.ANSIRed))
		if span.ErrorOrigin != nil && !fe.reportOnly && !fe.finalRender {
			color := termenv.ANSIBrightBlack
			if time.Since(fe.pressedKeyAt) < keypressDuration && fe.FocusedSpan == span.ErrorOrigin.ID {
				color = termenv.ANSIWhite
			}
			fmt.Fprintf(out, " %s %s",
				out.String("r").Foreground(color).Bold(),
				out.String("jump ↴").Foreground(color),
			)
		}
	} else if !span.IsRunningOrEffectsRunning() && span.IsCached() {
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("CACHED").Foreground(termenv.ANSIBlue))
	}
}

func (fe *frontendPretty) renderLogs(out TermOutput, r *renderer, row *dagui.TraceRow, logs *Vterm, height int, prefix string, focused bool) bool {
	span := row.Span
	depth := row.Depth

	Pipe := out.String(VertBoldBar).Foreground(restrainedStatusColor(span))
	Dashed := out.String(VertBoldDash3).Foreground(restrainedStatusColor(span))
	if focused {
		Pipe = hl(Pipe)
		Dashed = hl(Dashed)
	}

	if depth == -1 {
		// clear prefix when zoomed
		logs.SetPrefix(prefix)
	} else {
		pipeBuf := new(strings.Builder)
		fmt.Fprint(pipeBuf, prefix)
		indentOut := NewOutput(pipeBuf, termenv.WithProfile(fe.profile))
		r.fancyIndent(indentOut, row, false, false)
		fmt.Fprint(indentOut, Pipe)
		fmt.Fprint(indentOut, out.String(" "))
		logs.SetPrefix(pipeBuf.String())
	}
	if height <= 0 {
		height = logs.UsedHeight()
	}
	trimmed := logs.UsedHeight() - height
	if trimmed > 0 {
		fmt.Fprint(out, prefix)
		r.fancyIndent(out, row, false, false)
		fmt.Fprint(out, Dashed)
		fmt.Fprint(out, out.String(" "))
		fmt.Fprint(out, out.String("...").Foreground(termenv.ANSIBrightBlack))
		fmt.Fprintf(out, out.String("%d").Foreground(termenv.ANSIBrightBlack).Bold().String(), trimmed)
		fmt.Fprintln(out, out.String(" lines hidden...").Foreground(termenv.ANSIBrightBlack))
	}
	logs.SetHeight(height)
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
	DB            *dagui.DB
	Logs          map[dagui.SpanID]*Vterm
	PrefixWriters map[dagui.SpanID]*multiprefixw.Writer
	LogWidth      int
	SawEOF        map[dagui.SpanID]bool
	Profile       termenv.Profile
	Output        TermOutput
}

func newPrettyLogs(profile termenv.Profile, db *dagui.DB) *prettyLogs {
	return &prettyLogs{
		DB:            db,
		Logs:          make(map[dagui.SpanID]*Vterm),
		PrefixWriters: make(map[dagui.SpanID]*multiprefixw.Writer),
		LogWidth:      -1,
		Profile:       profile,
		SawEOF:        make(map[dagui.SpanID]bool),
		Output:        termenv.NewOutput(io.Discard, termenv.WithProfile(profile)),
	}
}

func (l *prettyLogs) Export(ctx context.Context, logs []sdklog.Record) error {
	for _, log := range logs {
		// Check for Markdown content type
		contentType := ""
		eof := false
		verbose := false
		global := false
		for attr := range log.WalkAttributes {
			switch attr.Key {
			case telemetry.ContentTypeAttr:
				contentType = attr.Value.AsString()
			case telemetry.StdioEOFAttr:
				eof = attr.Value.AsBool()
			case telemetry.LogsGlobalAttr:
				global = attr.Value.AsBool()
			case telemetry.LogsVerboseAttr:
				verbose = attr.Value.AsBool()
			}
		}

		if eof && log.SpanID().IsValid() {
			l.SawEOF[dagui.SpanID{SpanID: log.SpanID()}] = true
			continue
		}

		targetID := log.SpanID()

		spanID := dagui.SpanID{SpanID: targetID}
		pw, rolledUp := l.findRollUpSpan(spanID)
		if rolledUp && !verbose && !global {
			var context string
			span, ok := l.DB.Spans.Map[spanID]
			if ok {
				context = l.extractSpanContext(span)
			} else {
				context = targetID.String()
			}
			pw.Prefix = l.Output.String("["+context+"]").Foreground(termenv.ANSICyan).String() + " "
			fmt.Fprint(pw, log.Body().AsString())
		}

		vterm := l.spanLogs(spanID)
		if contentType == "text/markdown" {
			_, _ = vterm.WriteMarkdown([]byte(log.Body().AsString()))
		} else {
			_, _ = fmt.Fprint(vterm, log.Body().AsString())
		}
	}
	return nil
}

// extractSpanContext extracts a meaningful context label from a span
func (l *prettyLogs) extractSpanContext(span *dagui.Span) string {
	call := span.Call()
	if call == nil {
		return span.Name
	}

	// Handle withExec: extract first argument (the command)
	if call.Field == "withExec" {
		if len(call.Args) > 0 && call.Args[0].Name == "args" {
			// The args value is a list literal
			if argList := call.Args[0].Value.GetList(); argList != nil {
				if len(argList.Values) > 0 {
					// Extract just the command name (first element of the list)
					cmd := argList.Values[0].GetString_()
					if cmd != "" {
						return cmd
					}
				}
			}
		}
		return "exec"
	}

	// For function calls, use the function name
	if call.Field != "" {
		return call.Field
	}

	// Fallback to span name
	return span.Name
}

func (l *prettyLogs) findRollUpSpan(origID dagui.SpanID) (*multiprefixw.Writer, bool) {
	id := origID
	for {
		span := l.DB.Spans.Map[id]
		if span == nil {
			break
		}
		if span.Boundary || span.Encapsulate || span.Internal {
			break
		}
		if span.RollUpLogs {
			// Found a roll-up span; find-or-create a prefixed writer for it.
			pw, found := l.PrefixWriters[id]
			if !found {
				vterm := l.spanLogs(id)
				pw = multiprefixw.New(vterm)
				l.PrefixWriters[id] = pw
			}
			return pw, true
		}
		if span.ParentID.IsValid() {
			// Keep walking upward
			id = span.ParentID
		} else {
			break
		}
	}
	return nil, false
}

func (l *prettyLogs) spanLogs(spanID dagui.SpanID) *Vterm {
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
	ColorProfile() termenv.Profile
}

type promptMsg struct {
	form   *huh.Form
	result func(*huh.Form)
}

func (fe *frontendPretty) handlePromptBool(ctx context.Context, title, message string, dest *bool) error {
	done := make(chan struct{})

	fe.program.Send(promptMsg{
		form: NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(title).
					Description(strings.TrimSpace((&Markdown{
						Content: message,
						Width:   fe.window.Width,
					}).View())).
					Value(dest),
			),
		),
		result: func(f *huh.Form) {
			close(done)
		},
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (fe *frontendPretty) handlePromptString(ctx context.Context, title, message string, dest *string) error {
	done := make(chan struct{})

	fe.program.Send(promptMsg{
		form: NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title(title).
					Description(strings.TrimSpace((&Markdown{
						Content: message,
						Width:   fe.window.Width,
					}).View())).
					Value(dest),
			),
		),
		result: func(f *huh.Form) {
			close(done)
		},
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
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
