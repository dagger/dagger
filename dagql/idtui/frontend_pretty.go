package idtui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	"github.com/vito/progrock/ui"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/term"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/telemetry"
	"github.com/dagger/dagger/telemetry/sdklog"
)

type frontendPretty struct {
	FrontendOpts
	spanFilter

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
	rowsView     *RowsView

	// panels
	logsPanel *panel

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

		spanFilter: spanFilter{
			db:               db,
			tooFastThreshold: 100 * time.Millisecond,
			gcThreshold:      1 * time.Second,
		},

		fps:     30, // sane default, fine-tune if needed
		profile: profile,
		window:  tea.WindowSizeMsg{Width: -1, Height: -1}, // be clear that it's not set
		view:    new(strings.Builder),

		logsPanel: newPanel(profile),
	}
}

func (fe *frontendPretty) ConnectedToEngine(name string, version string) {
	// noisy, so suppress this for now
}

func (fe *frontendPretty) ConnectedToCloud(url string) {
	// noisy, so suppress this for now
}

// Run starts the TUI, calls the run function, stops the TUI, and finally
// prints the primary output to the appropriate stdout/stderr streams.
func (fe *frontendPretty) Run(ctx context.Context, opts FrontendOpts, run func(context.Context) error) error {
	fe.FrontendOpts = opts

	// set default context logs
	ctx = telemetry.WithLogProfile(ctx, fe.profile)

	// redirect slog to the logs pane
	level := slog.LevelInfo
	if fe.Debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(telemetry.PrettyLogger(fe.logsPanel, fe.profile, level))

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
	// NOTE: establish color cache before we start consuming stdin
	out := NewOutput(ttyOut, termenv.WithProfile(fe.profile), termenv.WithColorCache(true))

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
		tea.WithOutput(out),
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

	fe.recalculateView()

	out := NewOutput(os.Stderr, termenv.WithProfile(fe.profile))

	if fe.logsPanel.buf.Len() > 0 {
		fmt.Fprintln(out, fe.logsPanel.buf.String())
	}

	if fe.Debug || fe.Verbosity > 0 || fe.err != nil {
		if renderedAny, err := fe.renderProgress(out); err != nil {
			return err
		} else if renderedAny {
			fmt.Fprintln(out)
		}
	}

	return renderPrimaryOutput(fe.db)
}

func (fe *frontendPretty) renderMessages(out *termenv.Output, full bool) (bool, error) {
	if fe.logsPanel.vterm.UsedHeight() == 0 {
		return false, nil
	}
	if full {
		fe.logsPanel.vterm.SetHeight(fe.logsPanel.vterm.UsedHeight())
	} else {
		fe.logsPanel.vterm.SetHeight(10)
	}
	_, err := fmt.Fprintln(out, fe.logsPanel.vterm.View())
	return true, err
}

func (fe *frontendPretty) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
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
	}

	return fe.db.ExportSpans(ctx, spans)
}

func (fe *frontendPretty) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	slog.Debug("frontend exporting logs", "logs", len(logs))

	if err := fe.db.ExportLogs(ctx, logs); err != nil {
		return err
	}
	return fe.logs.ExportLogs(ctx, logs)
}

func (fe *frontendPretty) Shutdown(ctx context.Context) error {
	if err := fe.db.Shutdown(ctx); err != nil {
		return err
	}
	return fe.Close()
}

type eofMsg struct{}

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
	fe.recalculateView()
	if _, err := fe.renderMessages(out, false); err != nil {
		return err
	}
	if _, err := fe.renderProgress(out); err != nil {
		return err
	}
	return nil
}

func (fe *frontendPretty) recalculateView() {
	steps := CollectSpans(fe.db, trace.TraceID{})
	rows := CollectRows(steps)
	fe.rowsView = CollectRowsView(rows)
}

func (fe *frontendPretty) renderProgress(out *termenv.Output) (bool, error) {
	var renderedAny bool
	if fe.rowsView == nil {
		return false, nil
	}
	for _, row := range fe.rowsView.Body {
		if fe.Debug || fe.shouldShow(fe.FrontendOpts, row) {
			if err := fe.renderRow(out, row, 0); err != nil {
				return renderedAny, err
			}
			renderedAny = true
		}
	}
	if fe.rowsView.Primary != nil && !fe.done {
		if renderedAny {
			fmt.Fprintln(out)
		}
		fe.renderLogs(out, fe.rowsView.Primary, -1)
		renderedAny = true
	}
	return renderedAny, nil
}

func (fe *frontendPretty) Init() tea.Cmd {
	return tea.Batch(
		ui.Frame(fe.fps),
		fe.spawn,
	)
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

func (fe *frontendPretty) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		default:
			return fe, nil
		}

	case tea.WindowSizeMsg:
		fe.SetWindowSize(msg)
		return fe, nil

	case ui.FrameMsg:
		fe.render()
		// NB: take care not to forward Frame downstream, since that will result
		// in runaway ticks. instead inner components should send a SetFpsMsg to
		// adjust the outermost layer.
		return fe, ui.Frame(fe.fps)

	default:
		return fe, nil
	}
}

func (fe *frontendPretty) SetWindowSize(msg tea.WindowSizeMsg) {
	fe.window = msg
	fe.logs.SetWidth(msg.Width)
	fe.logsPanel.vterm.SetWidth(msg.Width)
}

func (fe *frontendPretty) render() {
	fe.mu.Lock()
	fe.view.Reset()
	fe.Render(NewOutput(fe.view, termenv.WithProfile(fe.profile)))
	fe.mu.Unlock()
}

func (fe *frontendPretty) View() string {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	view := fe.view.String()
	if fe.backgrounded {
		// if we've been backgrounded, show nothing, so a user's shell session
		// doesn't have any garbage before/after
		return ""
	}
	if fe.done && fe.eof {
		// print nothing; make way for the pristine output in the final render
		return ""
	}
	return view
}

func (fe *frontendPretty) renderRow(out *termenv.Output, row *TraceRow, depth int) error {
	if !fe.shouldShow(fe.FrontendOpts, row) && !fe.Debug {
		return nil
	}
	if !row.Span.Passthrough {
		fe.renderStep(out, row.Span, depth)
		fe.renderLogs(out, row.Span, depth)
		depth++
	}
	for _, child := range row.Children {
		if err := fe.renderRow(out, child, depth); err != nil {
			return err
		}
	}
	return nil
}

func (fe *frontendPretty) renderStep(out *termenv.Output, span *Span, depth int) error {
	r := renderer{db: fe.db, width: fe.window.Width}

	id := span.Call
	if id != nil {
		if err := r.renderCall(out, span, id, "", depth, false); err != nil {
			return err
		}
	} else if span != nil {
		if err := r.renderVertex(out, span, span.Name(), "", depth); err != nil {
			return err
		}
	}
	fmt.Fprintln(out)

	if span.Status().Code == codes.Error && span.Status().Description != "" {
		r.indent(out, depth)
		// print error description above it
		fmt.Fprintf(out,
			out.String("! %s\n").Foreground(termenv.ANSIYellow).String(),
			span.Status().Description,
		)
	}
	return nil
}

func (fe *frontendPretty) renderLogs(out *termenv.Output, span *Span, depth int) {
	if logs, ok := fe.logs.Logs[span.SpanContext().SpanID()]; ok {
		pipe := out.String(ui.VertBoldBar).Foreground(termenv.ANSIBrightBlack)
		if depth != -1 {
			logs.SetPrefix(strings.Repeat("  ", depth) + pipe.String() + " ")
		}
		logs.SetHeight(fe.window.Height / 3)
		fmt.Fprint(out, logs.View())
	}
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

var _ sdklog.LogExporter = (*prettyLogs)(nil)

func (l *prettyLogs) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	for _, log := range logs {
		slog.Debug("exporting log", "span", log.SpanID, "body", log.Body().AsString())

		// render vterm for TUI
		_, _ = fmt.Fprint(l.spanLogs(log.SpanID), log.Body().AsString())
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
