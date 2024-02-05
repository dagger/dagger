package idtui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/idproto"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
	"github.com/vito/progrock/ui"
)

type Frontend struct {
	// Debug tells the frontend to show everything and do one big final render.
	Debug bool

	// updated by Run
	program   *tea.Program
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

	// TUI state/config
	fps    float64           // frames per second
	spin   tea.Model         // da spin zone
	window tea.WindowSizeMsg // set by BubbleTea
	view   *bytes.Buffer     // rendered async

	// held to synchronize tea.Model and progrock.Writer
	mu sync.Mutex
}

func New() *Frontend {
	spin := ui.NewRave()
	spin.Frames = ui.MiniDotFrames
	return &Frontend{
		// TODO need to silence logging so it doesn't break the TUI. would be better
		// to hook into progrock logs.
		db: NewDB(slog.New(slog.NewTextHandler(io.Discard, nil))),

		// sane default, fine-tune if needed
		fps:  30,
		spin: spin,
		view: new(bytes.Buffer),
	}
}

func (f *Frontend) Run(ctx context.Context, run func(context.Context) error) error {
	f.run = run
	f.runCtx, f.interrupt = context.WithCancel(ctx)
	f.program = tea.NewProgram(f, tea.WithOutput(os.Stderr))
	_, err := f.program.Run()
	if err != nil {
		return err
	}
	if f.runCtx.Err() != nil {
		return f.runCtx.Err()
	}
	return f.err
}

var _ progrock.Writer = (*Frontend)(nil)

func (f *Frontend) WriteStatus(update *progrock.StatusUpdate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.db.WriteStatus(update); err != nil {
		return err
	}
	if len(update.Vertexes) > 0 {
		f.steps = CollectSteps(f.db)
		f.rows = CollectRows(f.steps)
	}
	return nil
}

type eofMsg struct{}

func (f *Frontend) Close() error {
	f.program.Send(eofMsg{})
	return nil
}

func (f *Frontend) Render(w io.Writer) error {
	out := ui.NewOutput(w)
	for _, row := range f.rows {
		if f.Debug || row.IsInteresting() {
			if err := f.renderRow(out, row); err != nil {
				return err
			}
		}
	}
	return nil
}

var _ tea.Model = (*Frontend)(nil)

func (f *Frontend) Init() tea.Cmd {
	return tea.Batch(
		f.spin.Init(),
		ui.Frame(f.fps),
		f.spawn,
	)
}

type doneMsg struct {
	err error
}

func (m *Frontend) spawn() tea.Msg {
	return doneMsg{m.run(m.runCtx)}
}

func (m *Frontend) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case doneMsg: // run finished
		m.done = true
		m.err = msg.err
		if m.eof {
			return m, tea.Quit
		}
		return m, nil

	case eofMsg: // received end of updates
		m.eof = true
		if m.done {
			return m, tea.Quit
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.interrupt()
			return m, nil // tea.Quit is deferred until we receive doneMsg
		default:
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.window = msg
		return m, nil

	case ui.FrameMsg:
		// NB: take care not to forward Frame downstream, since that will result
		// in runaway ticks. instead inner components should send a SetFpsMsg to
		// adjust the outermost layer.
		m.render()
		return m, ui.Frame(m.fps)

	default:
		return m, nil
	}
}

func (f *Frontend) render() {
	f.mu.Lock()
	f.view.Reset()
	f.Render(f.view)
	f.mu.Unlock()
}

func (f *Frontend) View() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		if errors.Is(f.runCtx.Err(), context.Canceled) {
			return "canceled\n"
		}
		return fmt.Sprintf("%s\n", f.view)
	}
	if f.done && f.eof {
		return ""
	}
	return fmt.Sprintf("%s\n", f.view)
}

func (f *Frontend) renderRow(out *termenv.Output, row *TraceRow) error {
	if f.Debug || row.IsInteresting() {
		f.renderStep(out, row.Step, row.Depth())
		f.renderLogs(out, row)
	}
	for _, child := range row.Children {
		if err := f.renderRow(out, child); err != nil {
			return err
		}
	}
	return nil
}

func (fe *Frontend) renderStep(out *termenv.Output, step *Step, depth int) error {
	id := step.ID()
	vtx := step.FirstVertex()
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
	if logs, ok := fe.db.Logs[row.Step.Digest]; ok {
		bar := out.String(ui.VertBoldBar).Foreground(termenv.ANSIBrightBlack)
		logs.SetPrefix(strings.Repeat("  ", row.Depth()) + bar.String() + " ")
		// logs.SetPrefix(strings.Repeat("  ", row.Depth()+1))
		logs.SetHeight(fe.window.Height / 3)
		fmt.Fprint(out, logs.View())
	}
}

func indent(out io.Writer, depth int) {
	fmt.Fprint(out, strings.Repeat("  ", depth))
}

func (fe *Frontend) renderIDAncestry(out *termenv.Output, id *idproto.ID, depth int) error {
	if baseStep, ok := fe.db.HighLevelStep(id); ok {
		id = baseStep.ID()
	}
	if id.Parent != nil {
		if err := fe.renderIDAncestry(out, id.Parent, depth); err != nil {
			return err
		}
	}
	indent(out, depth)
	dig, err := id.Digest()
	if err != nil {
		return err
	}
	if vtx := fe.db.Vertices[dig.String()]; vtx != nil {
		fe.renderStatus(out, vtx)
	} else {
		fmt.Fprint(out, "  ")
	}
	fmt.Fprint(out, out.String(id.Field).Bold())
	if len(id.Args) > 0 {
		fmt.Fprintf(out, "(%s)", out.String("...").Faint())
	}
	fmt.Fprintln(out)
	return nil
}

func (fe *Frontend) renderIDPath(out *termenv.Output, id *idproto.ID) error {
	if baseStep, ok := fe.db.HighLevelStep(id); ok {
		id = baseStep.ID()
	}
	if id.Parent != nil {
		if err := fe.renderIDPath(out, id.Parent); err != nil {
			return err
		}
	}
	fmt.Fprint(out, out.String(id.Field+"."))
	return nil
}

func (fe *Frontend) renderID(out *termenv.Output, vtx *progrock.Vertex, id *idproto.ID, depth int, inline bool) error {
	// if id.Parent != nil {
	// 	if err := fe.renderIDAncestry(out, id.Parent, depth); err != nil {
	// 		return err
	// 	}
	// }

	if !inline {
		indent(out, depth)
	}

	if vtx != nil {
		fe.renderStatus(out, vtx)
	}

	if id.Parent != nil {
		if err := fe.renderIDPath(out, id.Parent); err != nil {
			return err
		}
	}

	fmt.Fprint(out, out.String(id.Field).Bold())

	kwColor := termenv.ANSIBlue

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
				switch x := val.(type) {
				case *idproto.Literal_Id:
					// fmt.Fprintln(out)
					fmt.Fprint(out, " ")
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
					fmt.Fprint(out, " ")
					renderLiteral(out, arg.Value)
					fmt.Fprintln(out)
				}
			}
			depth--
			indent(out, depth)
			depth--
		} else {
			for i, arg := range id.Args {
				if i > 0 {
					fmt.Fprint(out, ", ")
				}
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String()+" ", arg.Name)
				renderLiteral(out, arg.Value)
			}
		}
		fmt.Fprint(out, ")")
	}

	typeStr := out.String(": " + id.Type.ToAST().String()).Foreground(termenv.ANSIBrightBlack)
	fmt.Fprint(out, typeStr)

	if vtx != nil {
		fe.renderDuration(out, vtx)
	}

	fmt.Fprintln(out)

	// if vtx != nil && (tape.IsClosed || tape.ShowCompletedLogs || vtx.Completed == nil || vtx.Error != nil) {
	// 	renderVertexTasksAndLogs(out, u, tape, vtx, depth)
	// }

	return nil
}

func (fe *Frontend) renderVertex(out *termenv.Output, vtx *progrock.Vertex, depth int) error {
	indent(out, depth)
	fe.renderStatus(out, vtx)
	fmt.Fprint(out, vtx.Name)
	fe.renderVertexTasks(out, vtx, depth)
	fe.renderDuration(out, vtx)
	fmt.Fprintln(out)
	// if err := u.RenderVertexTree(out, vtx); err != nil {
	// 	return err
	// }
	// return renderVertexTasksAndLogs(out, vtx, depth)
	return nil
}

var maxLen = len("ETOOBIG:") + len(digest.FromString(""))

func renderLiteral(out *termenv.Output, lit *idproto.Literal) {
	var color termenv.Color
	switch val := lit.GetValue().(type) {
	case *idproto.Literal_Bool:
		color = termenv.ANSIBrightRed
	case *idproto.Literal_Int:
		color = termenv.ANSIRed
	case *idproto.Literal_Float:
		color = termenv.ANSIRed
	case *idproto.Literal_String_:
		color = termenv.ANSIYellow
		if len(val.String_) > maxLen {
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
			renderLiteral(out, item)
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
			renderLiteral(out, item.Value)
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
		symbol = ui.DotFilled // fe.spin.View()
		color = termenv.ANSIYellow
	}

	symbol = out.String(symbol).Foreground(color).String()

	fmt.Fprintf(out, "%s ", symbol)
}

func (fe *Frontend) renderDuration(out *termenv.Output, vtx *progrock.Vertex) {
	fmt.Fprint(out, " ")
	duration := out.String(fmtDuration(vtx.Duration()))
	if vtx.Completed != nil {
		duration = duration.Foreground(termenv.ANSIBrightBlack)
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
			// chr = fmt.Sprintf("(%d/%d=%d=%d:%q)", t.GetCurrent(), t.GetTotal(), percent, idx, chr)
			sym = out.String(chr)
		} else {
			// don't bother printing non-progress-bar tasks for now
			//else if t.Completed != nil {
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

func DebugRenderID(out *termenv.Output, u *progrock.UI, id *idproto.ID, depth int) error {
	if id.Parent != nil {
		if err := DebugRenderID(out, u, id.Parent, depth); err != nil {
			return err
		}
	}

	indent := func() {
		fmt.Fprint(out, strings.Repeat("  ", depth))
	}

	indent()

	fmt.Fprint(out, id.Field)

	kwColor := termenv.ANSIBlue

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
			for _, arg := range id.Args {
				indent()
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String(), arg.Name)
				val := arg.Value.GetValue()
				switch x := val.(type) {
				case *idproto.Literal_Id:
					argVertexID, err := x.Id.Digest()
					if err != nil {
						return err
					}
					fmt.Fprintln(out, " "+x.Id.Type.ToAST().Name()+"@"+argVertexID.String()+"{")
					depth++
					if err := DebugRenderID(out, u, x.Id, depth); err != nil {
						return err
					}
					depth--
					indent()
					fmt.Fprintln(out, "}")
				default:
					fmt.Fprint(out, " ")
					renderLiteral(out, arg.Value)
					fmt.Fprintln(out)
				}
			}
			depth--
			indent()
		} else {
			for i, arg := range id.Args {
				if i > 0 {
					fmt.Fprint(out, ", ")
				}
				fmt.Fprintf(out, out.String("%s:").Foreground(kwColor).String()+" ", arg.Name)
				renderLiteral(out, arg.Value)
			}
		}
		fmt.Fprint(out, ")")
	}

	typeStr := out.String(": " + id.Type.ToAST().String()).Foreground(termenv.ANSIBrightBlack)
	fmt.Fprintln(out, typeStr)

	return nil
}
