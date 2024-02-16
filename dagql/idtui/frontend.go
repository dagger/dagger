package idtui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/idproto"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"github.com/vito/midterm"
	"github.com/vito/progrock"
	"github.com/vito/progrock/console"
	"github.com/vito/progrock/ui"
	"golang.org/x/term"
)

const PrimaryVertex = "primary"

var consoleSink = os.Stderr

type Frontend struct {
	// Debug tells the frontend to show everything and do one big final render.
	Debug bool

	// Plain tells the frontend to render plain console output instead of using a
	// TUI. This will be automatically set to true if a TTY is not found.
	Plain bool

	// Silent tells the frontend to not display progress at all.
	Silent bool

	// updated by Run
	program   *tea.Program
	in        *swappableWriter
	out       *termenv.Output
	run       func(context.Context) error
	runCtx    context.Context
	interrupt func()
	done      bool
	err       error

	// updated via progrock.Writer interface
	db    *DB
	eof   bool
	steps []*Step
	rows  []*TraceRow

	// progrock logging
	messages  *Vterm
	messagesW *termenv.Output

	// primaryVtx is the primary vertex whose output is printed directly to
	// stdout/stderr on exit after cleaning up the TUI.
	primaryVtx  *progrock.Vertex
	primaryLogs []*progrock.VertexLog

	// plainConsole is the writer to forward events to when in plain mode.
	plainConsole progrock.Writer

	// TUI state/config
	fps               float64 // frames per second
	profile           termenv.Profile
	window            tea.WindowSizeMsg     // set by BubbleTea
	view              *strings.Builder      // rendered async
	logs              map[string]*Vterm     // vertex logs
	zoomed            map[string]*zoomState // interactive zoomed terminals
	currentZoom       *zoomState            // current zoomed terminal
	scrollbackQueue   []tea.Cmd             // queue of tea.Printlns for scrollback
	scrollbackQueueMu sync.Mutex            // need a separate lock for this

	// held to synchronize tea.Model and progrock.Writer
	mu sync.Mutex
}

type zoomState struct {
	Input  io.Writer
	Output *midterm.Terminal
}

func New() *Frontend {
	logs := NewVterm()
	profile := ui.ColorProfile()
	idproto.EnableDigestCache()
	return &Frontend{
		db: NewDB(),

		fps:       30, // sane default, fine-tune if needed
		profile:   profile,
		window:    tea.WindowSizeMsg{Width: -1, Height: -1}, // be clear that it's not set
		view:      new(strings.Builder),
		logs:      make(map[string]*Vterm),
		zoomed:    make(map[string]*zoomState),
		messages:  logs,
		messagesW: ui.NewOutput(logs, termenv.WithProfile(profile)),
	}
}

// Run starts the TUI, calls the run function, stops the TUI, and finally
// prints the primary output to the appropriate stdout/stderr streams.
func (fe *Frontend) Run(ctx context.Context, run func(context.Context) error) error {
	// find a TTY anywhere in stdio. stdout might be redirected, in which case we
	// can show the TUI on stderr.
	tty, isTTY := findTTY()
	if !isTTY {
		// Simplify logic elsewhere by just setting Plain to true.
		fe.Plain = true
	}

	var runErr error
	if fe.Plain || fe.Silent {
		// no TTY found; default to console
		runErr = fe.runWithoutTUI(ctx, tty, run)
	} else {
		// run the TUI until it exits and cleans up the TTY
		runErr = fe.runWithTUI(ctx, tty, run)
	}

	// print the final output display to stderr
	if renderErr := fe.finalRender(); renderErr != nil {
		return renderErr
	}

	// return original err
	return runErr
}

// ConnectedToEngine is called when the CLI connects to an engine.
func (fe *Frontend) ConnectedToEngine(name string) {
	if !fe.Silent && fe.Plain {
		fmt.Fprintln(consoleSink, "Connected to engine", name)
	}
}

// ConnectedToCloud is called when the CLI has started emitting events to
// The Cloud.
func (fe *Frontend) ConnectedToCloud(cloudURL string) {
	if !fe.Silent && fe.Plain {
		fmt.Fprintln(consoleSink, "Dagger Cloud URL:", cloudURL)
	}
}

func (fe *Frontend) runWithTUI(ctx context.Context, tty *os.File, run func(context.Context) error) error {
	// NOTE: establish color cache before we start consuming stdin
	fe.out = ui.NewOutput(tty, termenv.WithProfile(fe.profile), termenv.WithColorCache(true))

	// in order to allow the TUI to receive user input but _also_ allow an
	// interactive terminal to receive keyboard input, we pipe the user input
	// to an io.Writer that can have its destination swapped between the TUI
	// and the remote terminal.
	inR, inW := io.Pipe()
	fe.in = &swappableWriter{original: inW}

	// Bubbletea will just receive an `io.Reader` for its input rather than the
	// raw TTY *os.File, so we need to set up the TTY ourselves.
	ttyFd := int(tty.Fd())
	oldState, err := term.MakeRaw(ttyFd)
	if err != nil {
		return err
	}
	defer term.Restore(ttyFd, oldState) //nolint: errcheck

	// start piping from the TTY to our swappable writer.
	go io.Copy(fe.in, tty) //nolint: errcheck

	// support scrollable viewport
	// fe.out.EnableMouseCellMotion()

	// wire up the run so we can call it asynchronously with the TUI running
	fe.run = run
	// set up ctx cancellation so the TUI can interrupt via keypresses
	fe.runCtx, fe.interrupt = context.WithCancel(ctx)

	// keep program state so we can send messages to it
	fe.program = tea.NewProgram(fe,
		tea.WithInput(inR),
		tea.WithOutput(fe.out),
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

func (fe *Frontend) runWithoutTUI(ctx context.Context, tty *os.File, run func(context.Context) error) error {
	if !fe.Silent {
		opts := []console.WriterOpt{
			console.ShowInternal(fe.Debug),
		}
		if fe.Debug {
			opts = append(opts, console.WithMessageLevel(progrock.MessageLevel_DEBUG))
		}
		fe.plainConsole = console.NewWriter(consoleSink, opts...)
	}
	return run(ctx)
}

// finalRender is called after the program has finished running and prints the
// final output after the TUI has exited.
func (fe *Frontend) finalRender() error {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	out := termenv.NewOutput(os.Stderr)

	if fe.Debug || fe.err != nil {
		if renderedAny, err := fe.renderProgress(out); err != nil {
			return err
		} else if renderedAny {
			fmt.Fprintln(out)
		}
	}

	if zoom := fe.currentZoom; zoom != nil {
		if renderedAny, err := fe.renderZoomed(out, zoom); err != nil {
			return err
		} else if renderedAny {
			fmt.Fprintln(out)
		}
	}

	if renderedAny, err := fe.renderMessages(out, true); err != nil {
		return err
	} else if renderedAny {
		fmt.Fprintln(out)
	}

	return fe.renderPrimaryOutput()
}

func (fe *Frontend) renderMessages(out *termenv.Output, full bool) (bool, error) {
	if fe.messages.UsedHeight() == 0 {
		return false, nil
	}
	if full {
		fe.messages.SetHeight(fe.messages.UsedHeight())
	} else {
		fe.messages.SetHeight(10)
	}
	_, err := fmt.Fprint(out, fe.messages.View())
	return true, err
}

func (fe *Frontend) renderPrimaryOutput() error {
	var trailingLn bool
	for _, l := range fe.primaryLogs {
		if bytes.HasSuffix(l.Data, []byte("\n")) {
			trailingLn = true
		}
		switch l.Stream {
		case progrock.LogStream_STDOUT:
			if _, err := os.Stdout.Write(l.Data); err != nil {
				return err
			}
		case progrock.LogStream_STDERR:
			if _, err := os.Stderr.Write(l.Data); err != nil {
				return err
			}
		}
	}
	if !trailingLn && term.IsTerminal(int(os.Stdout.Fd())) {
		// NB: ensure there's a trailing newline if stdout is a TTY, so we don't
		// encourage module authors to add one of their own
		fmt.Fprintln(os.Stdout)
	}
	return nil
}

func (fe *Frontend) redirectStdin(st *zoomState) {
	if st == nil {
		fe.in.Restore()
		// restore scrolling as we transition back to the DAG UI, since an app
		// may have disabled it
		// fe.out.EnableMouseCellMotion()
	} else {
		// disable mouse events, can't assume zoomed input wants it (might be
		// regular shell like sh)
		// fe.out.DisableMouseCellMotion()
		fe.in.SetOverride(st.Input)
	}
}

func findTTY() (*os.File, bool) {
	// some of these may be redirected
	for _, f := range []*os.File{os.Stderr, os.Stdout, os.Stdin} {
		if term.IsTerminal(int(f.Fd())) {
			return f, true
		}
	}
	return nil, false
}

type swappableWriter struct {
	original io.Writer
	override io.Writer
	sync.Mutex
}

func (w *swappableWriter) SetOverride(to io.Writer) {
	w.Lock()
	w.override = to
	w.Unlock()
}

func (w *swappableWriter) Restore() {
	w.SetOverride(nil)
}

func (w *swappableWriter) Write(p []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	if w.override != nil {
		return w.override.Write(p)
	}
	return w.original.Write(p)
}

var _ progrock.Writer = (*Frontend)(nil)

func (fe *Frontend) WriteStatus(update *progrock.StatusUpdate) error {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	if err := fe.db.WriteStatus(update); err != nil {
		return err
	}
	if fe.plainConsole != nil {
		if err := fe.plainConsole.WriteStatus(update); err != nil {
			return err
		}
	}
	for _, v := range update.Vertexes {
		_, isZoomed := fe.zoomed[v.Id]
		if v.Zoomed && !isZoomed {
			fe.initZoom(v)
		} else if isZoomed {
			fe.releaseZoom(v)
		}
		if v.Id == PrimaryVertex {
			fe.primaryVtx = v
		}
	}
	for _, l := range update.Logs {
		if l.Vertex == PrimaryVertex {
			fe.primaryLogs = append(fe.primaryLogs, l)
		}
		var w io.Writer
		if t, found := fe.zoomed[l.Vertex]; found {
			w = t.Output
		} else {
			w = fe.vertexLogs(l.Vertex)
		}
		_, err := w.Write(l.Data)
		if err != nil {
			return fmt.Errorf("write logs: %w", err)
		}
	}
	for _, msg := range update.Messages {
		if fe.Debug || msg.Level > progrock.MessageLevel_DEBUG {
			progrock.WriteMessage(fe.messagesW, msg)
		}
	}
	if len(update.Vertexes) > 0 {
		fe.steps = CollectSteps(fe.db)
		fe.rows = CollectRows(fe.steps)
	}
	return nil
}

func (fe *Frontend) vertexLogs(id string) *Vterm {
	term, found := fe.logs[id]
	if !found {
		term = NewVterm()
		if fe.window.Width != -1 {
			term.SetWidth(fe.window.Width)
		}
		fe.logs[id] = term
	}
	return term
}

var (
	// what's a little global state between friends?
	termSetups  = map[string]progrock.TermSetupFunc{}
	termSetupsL = new(sync.Mutex)
)

func setupTerm(vtxID string, vt *midterm.Terminal) io.Writer {
	termSetupsL.Lock()
	defer termSetupsL.Unlock()
	setup, ok := termSetups[vtxID]
	if ok && setup != nil {
		return setup(vt)
	}
	return nil
}

// Zoomed marks the vertex as zoomed, indicating it should take up as much
// screen space as possible.
func Zoomed(setup progrock.TermSetupFunc) progrock.VertexOpt {
	return progrock.VertexOptFunc(func(vertex *progrock.Vertex) {
		termSetupsL.Lock()
		termSetups[vertex.Id] = setup
		termSetupsL.Unlock()
		vertex.Zoomed = true
	})
}

type scrollbackMsg struct {
	Line string
}

func (fe *Frontend) initZoom(v *progrock.Vertex) {
	var vt *midterm.Terminal
	if fe.window.Height == -1 || fe.window.Width == -1 {
		vt = midterm.NewAutoResizingTerminal()
	} else {
		vt = midterm.NewTerminal(fe.window.Height, fe.window.Width)
	}
	vt.OnScrollback(func(line midterm.Line) {
		fe.scrollbackQueueMu.Lock()
		fe.scrollbackQueue = append(fe.scrollbackQueue, tea.Println(line.Display()))
		fe.scrollbackQueueMu.Unlock()
	})
	vt.Raw = true
	w := setupTerm(v.Id, vt)
	st := &zoomState{
		Output: vt,
		Input:  w,
	}
	fe.zoomed[v.Id] = st
	fe.currentZoom = st
	fe.redirectStdin(st)
}

func (fe *Frontend) releaseZoom(vtx *progrock.Vertex) {
	delete(fe.zoomed, vtx.Id)
}

type eofMsg struct{}

func (fe *Frontend) Close() error {
	if fe.program != nil {
		fe.program.Send(eofMsg{})
	} else if fe.plainConsole != nil {
		if err := fe.plainConsole.Close(); err != nil {
			return err
		}
		fmt.Fprintln(consoleSink)
	}
	return nil
}

func (fe *Frontend) Render(out *termenv.Output) error {
	// if we're zoomed, render the zoomed terminal and nothing else, but only
	// after we've actually seen output from it.
	if fe.currentZoom != nil && fe.currentZoom.Output.UsedHeight() > 0 {
		_, err := fe.renderZoomed(out, fe.currentZoom)
		return err
	}
	if _, err := fe.renderProgress(out); err != nil {
		return err
	}
	if _, err := fe.renderMessages(out, false); err != nil {
		return err
	}
	return nil
}

func (fe *Frontend) renderProgress(out *termenv.Output) (bool, error) {
	var renderedAny bool
	for _, row := range fe.rows {
		if row.Step.Digest == PrimaryVertex && fe.done {
			// primary vertex is displayed below the fold instead
			continue
		}
		if fe.Debug || row.IsInteresting() {
			if err := fe.renderRow(out, row); err != nil {
				return renderedAny, err
			}
			renderedAny = true
		}
	}
	return renderedAny, nil
}

func (fe *Frontend) renderZoomed(out *termenv.Output, st *zoomState) (bool, error) {
	var renderedAny bool
	for i := 0; i < st.Output.UsedHeight(); i++ {
		if i > 0 {
			fmt.Fprintln(out)
		}
		if err := st.Output.RenderLine(out, i); err != nil {
			return renderedAny, err
		}
		renderedAny = true
	}
	return renderedAny, nil
}

var _ tea.Model = (*Frontend)(nil)

func (fe *Frontend) Init() tea.Cmd {
	return tea.Batch(
		ui.Frame(fe.fps),
		fe.spawn,
	)
}

type doneMsg struct {
	err error
}

func (fe *Frontend) spawn() tea.Msg {
	return doneMsg{fe.run(fe.runCtx)}
}

func (fe *Frontend) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doneMsg: // run finished
		fe.done = true
		fe.err = msg.err
		if fe.eof {
			return fe, tea.Quit
		}
		return fe, nil

	case eofMsg: // received end of updates
		fe.eof = true
		if fe.done {
			return fe, tea.Quit
		}
		return fe, nil

	case scrollbackMsg:
		return fe, tea.Println(msg.Line)

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			fe.interrupt()
			return fe, nil // tea.Quit is deferred until we receive doneMsg
		default:
			return fe, nil
		}

	case tea.WindowSizeMsg:
		fe.window = msg
		for _, st := range fe.zoomed {
			st.Output.Resize(msg.Height, msg.Width)
		}
		for _, vt := range fe.logs {
			vt.SetWidth(msg.Width)
		}
		fe.messages.SetWidth(msg.Width)
		return fe, nil

	case ui.FrameMsg:
		fe.render()
		// NB: take care not to forward Frame downstream, since that will result
		// in runaway ticks. instead inner components should send a SetFpsMsg to
		// adjust the outermost layer.
		fe.scrollbackQueueMu.Lock()
		queue := fe.scrollbackQueue
		fe.scrollbackQueue = nil
		fe.scrollbackQueueMu.Unlock()
		return fe, tea.Sequence(append(queue, ui.Frame(fe.fps))...)

	default:
		return fe, nil
	}
}

func (fe *Frontend) render() {
	fe.mu.Lock()
	fe.view.Reset()
	fe.Render(ui.NewOutput(fe.view, termenv.WithProfile(fe.profile)))
	fe.mu.Unlock()
}

func (fe *Frontend) View() string {
	fe.mu.Lock()
	defer fe.mu.Unlock()
	view := fe.view.String()
	if fe.done && fe.eof {
		// print nothing; make way for the pristine output in the final render
		return ""
	}
	return view
}

// DumpID is exposed for troubleshooting.
func (fe *Frontend) DumpID(out *termenv.Output, id *idproto.ID) error {
	if id.Base != nil {
		if err := fe.DumpID(out, id.Base); err != nil {
			return err
		}
	}
	return fe.renderID(out, nil, id, 0, false)
}

func (fe *Frontend) renderRow(out *termenv.Output, row *TraceRow) error {
	if fe.Debug || row.IsInteresting() {
		fe.renderStep(out, row.Step, row.Depth())
		fe.renderLogs(out, row)
	}
	for _, child := range row.Children {
		if err := fe.renderRow(out, child); err != nil {
			return err
		}
	}
	return nil
}

func (fe *Frontend) renderStep(out *termenv.Output, step *Step, depth int) error {
	id := step.ID()
	vtx := step.db.MostInterestingVertex(step.Digest)
	if id != nil {
		if err := fe.renderID(out, vtx, id, depth, false); err != nil {
			return err
		}
	} else if vtx != nil {
		if err := fe.renderVertex(out, vtx, depth); err != nil {
			return err
		}
	}
	return nil
}

func (fe *Frontend) renderLogs(out *termenv.Output, row *TraceRow) {
	if logs, ok := fe.logs[row.Step.Digest]; ok {
		pipe := out.String(ui.VertBoldBar).Foreground(termenv.ANSIBrightBlack)
		logs.SetPrefix(strings.Repeat("  ", row.Depth()) + pipe.String() + " ")
		logs.SetHeight(fe.window.Height / 3)
		fmt.Fprint(out, logs.View())
	}
}

func indent(out io.Writer, depth int) {
	fmt.Fprint(out, strings.Repeat("  ", depth))
}

const (
	kwColor     = termenv.ANSICyan
	parentColor = termenv.ANSIWhite
	moduleColor = termenv.ANSIMagenta
)

func (fe *Frontend) renderIDBase(out *termenv.Output, id *idproto.ID) error {
	typeName := id.Type.ToAST().Name()
	parent := out.String(typeName)
	if id.Module != nil {
		parent = parent.Foreground(moduleColor)
	}
	fmt.Fprint(out, parent.String())
	return nil
}

func (fe *Frontend) renderID(out *termenv.Output, vtx *progrock.Vertex, id *idproto.ID, depth int, inline bool) error {
	if !inline {
		indent(out, depth)
	}

	if vtx != nil {
		fe.renderStatus(out, vtx)
	}

	if id.Base != nil {
		if err := fe.renderIDBase(out, id.Base); err != nil {
			return err
		}
		fmt.Fprint(out, ".")
	}

	fmt.Fprint(out, out.String(id.Field).Bold())

	if len(id.Args) > 0 {
		fmt.Fprint(out, "(")
		var needIndent bool
		for _, arg := range id.Args {
			if _, ok := arg.Value.ToInput().(*idproto.ID); ok {
				needIndent = true
				break
			}
		}
		if needIndent {
			fmt.Fprintln(out)
			depth++
			depth++
			for _, arg := range id.Args {
				indent(out, depth)
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String(), arg.Name)
				val := arg.Value.GetValue()
				fmt.Fprint(out, " ")
				switch x := val.(type) {
				case *idproto.Literal_Id:
					argVertexID, err := x.Id.Digest()
					if err != nil {
						return err
					}
					argVtx := fe.db.Vertices[argVertexID.String()]
					base := x.Id
					if baseStep, ok := fe.db.HighLevelStep(x.Id); ok {
						base = baseStep.ID()
					}
					if err := fe.renderID(out, argVtx, base, depth-1, true); err != nil {
						return err
					}
				default:
					fe.renderLiteral(out, arg.Value)
					fmt.Fprintln(out)
				}
			}
			depth--
			indent(out, depth)
			depth-- //nolint:ineffassign
		} else {
			for i, arg := range id.Args {
				if i > 0 {
					fmt.Fprint(out, ", ")
				}
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String()+" ", arg.Name)
				fe.renderLiteral(out, arg.Value)
			}
		}
		fmt.Fprint(out, ")")
	}

	typeStr := out.String(": " + id.Type.ToAST().String()).Faint()
	fmt.Fprint(out, typeStr)

	if vtx != nil {
		fe.renderDuration(out, vtx)
	}

	fmt.Fprintln(out)

	return nil
}

func (fe *Frontend) renderVertex(out *termenv.Output, vtx *progrock.Vertex, depth int) error {
	indent(out, depth)
	fe.renderStatus(out, vtx)
	fmt.Fprint(out, vtx.Name)
	fe.renderVertexTasks(out, vtx, depth)
	fe.renderDuration(out, vtx)
	fmt.Fprintln(out)
	return nil
}

func (fe *Frontend) renderLiteral(out *termenv.Output, lit *idproto.Literal) {
	var color termenv.Color
	switch val := lit.GetValue().(type) {
	case *idproto.Literal_Bool:
		color = termenv.ANSIRed
	case *idproto.Literal_Int:
		color = termenv.ANSIRed
	case *idproto.Literal_Float:
		color = termenv.ANSIRed
	case *idproto.Literal_String_:
		color = termenv.ANSIYellow
		if fe.window.Width != -1 && len(val.String_) > fe.window.Width {
			display := string(digest.FromString(val.String_))
			fmt.Fprint(out, out.String("ETOOBIG:"+display).Foreground(color))
			return
		}
	case *idproto.Literal_Id:
		color = termenv.ANSIMagenta
	case *idproto.Literal_Enum:
		color = termenv.ANSIYellow
	case *idproto.Literal_Null:
		color = termenv.ANSIBrightBlack
	case *idproto.Literal_List:
		fmt.Fprint(out, "[")
		for i, item := range lit.GetList().Values {
			if i > 0 {
				fmt.Fprint(out, ", ")
			}
			fe.renderLiteral(out, item)
		}
		fmt.Fprint(out, "]")
		return
	case *idproto.Literal_Object:
		fmt.Fprint(out, "{")
		for i, item := range lit.GetObject().Values {
			if i > 0 {
				fmt.Fprint(out, ", ")
			}
			fmt.Fprintf(out, "%s: ", item.GetName())
			fe.renderLiteral(out, item.Value)
		}
		fmt.Fprint(out, "}")
		return
	}
	fmt.Fprint(out, out.String(lit.ToAST().String()).Foreground(color))
}

func (fe *Frontend) renderStatus(out *termenv.Output, vtx *progrock.Vertex) {
	var symbol string
	var color termenv.Color
	if vtx.Completed != nil {
		switch {
		case vtx.Error != nil:
			symbol = ui.IconFailure
			color = termenv.ANSIRed
		case vtx.Canceled:
			symbol = ui.IconSkipped
			color = termenv.ANSIBrightBlack
		default:
			symbol = ui.IconSuccess
			color = termenv.ANSIGreen
		}
	} else {
		symbol = ui.DotFilled
		color = termenv.ANSIYellow
	}

	symbol = out.String(symbol).Foreground(color).String()

	fmt.Fprintf(out, "%s ", symbol)
}

func (fe *Frontend) renderDuration(out *termenv.Output, vtx *progrock.Vertex) {
	fmt.Fprint(out, " ")
	duration := out.String(fmtDuration(vtx.Duration()))
	if vtx.Completed != nil {
		duration = duration.Faint()
	} else {
		duration = duration.Foreground(termenv.ANSIYellow)
	}
	fmt.Fprint(out, duration)
}

var (
	progChars = []string{"⠀", "⡀", "⣀", "⣄", "⣤", "⣦", "⣶", "⣷", "⣿"}
)

func (fe *Frontend) renderVertexTasks(out *termenv.Output, vtx *progrock.Vertex, depth int) error {
	tasks := fe.db.Tasks[vtx.Id]
	if len(tasks) == 0 {
		return nil
	}
	var spaced bool
	for _, t := range tasks {
		var sym termenv.Style
		if t.GetTotal() != 0 {
			percent := int(100 * (float64(t.GetCurrent()) / float64(t.GetTotal())))
			idx := (len(progChars) - 1) * percent / 100
			chr := progChars[idx]
			sym = out.String(chr)
		} else {
			// TODO: don't bother printing non-progress-bar tasks for now
			// else if t.Completed != nil {
			// sym = out.String(ui.IconSuccess)
			// } else if t.Started != nil {
			// sym = out.String(ui.DotFilled)
			// }
			continue
		}
		if t.Completed != nil {
			sym = sym.Foreground(termenv.ANSIGreen)
		} else if t.Started != nil {
			sym = sym.Foreground(termenv.ANSIYellow)
		}
		if !spaced {
			fmt.Fprint(out, " ")
			spaced = true
		}
		fmt.Fprint(out, sym)
	}
	return nil
}
