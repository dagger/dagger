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

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/idproto"
	zone "github.com/lrstanley/bubblezone"
	"github.com/muesli/termenv"
	"github.com/opencontainers/go-digest"
	"github.com/vito/progrock"
	"github.com/vito/progrock/ui"
)

type Frontend struct {
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

	// da spin zone
	spin tea.Model
	// da bubble zone

	// set by BubbleTea
	window tea.WindowSizeMsg

	// view is rendered async into this buffer to avoid rendering and allocating
	// too frequently
	view *bytes.Buffer

	// held to synchronize tea.Model and progrock.Writer
	mu sync.Mutex
}

const FPS = 60

func New() *Frontend {
	zone.NewGlobal()
	spin := ui.NewRave()
	spin.Frames = ui.DotFrames
	return &Frontend{
		// TODO need to silence logging so it doesn't break the TUI. would be better
		// to hook into progrock logs.
		db: NewDB(slog.New(slog.NewTextHandler(io.Discard, nil))),

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

func (row *TraceRow) IsRunning() bool {
	if row.Step.IsRunning() {
		return true
	}
	for _, child := range row.Children {
		if child.IsRunning() {
			return true
		}
	}
	return false
}

func (f *Frontend) Render(w io.Writer) error {
	out := ui.NewOutput(w)
	for _, row := range f.rows {
		if !row.IsRunning() {
			continue
		}

		if err := f.renderRow(out, row); err != nil {
			return err
		}
	}
	return nil
}

var _ tea.Model = (*Frontend)(nil)

func (f *Frontend) Init() tea.Cmd {
	return tea.Batch(
		f.spin.Init(),
		ui.Frame(FPS),
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
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.interrupt()
			return m, nil // tea.Quit is deferred until we receive doneMsg
		default:
			return m, nil
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case doneMsg:
		m.done = true
		m.err = msg.err
		if m.eof {
			return m, tea.Quit
		}
		return m, nil

	case eofMsg:
		m.eof = true
		if m.done {
			return m, tea.Quit
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.window = msg
		return m, nil

	case ui.FrameMsg:
		// NB: take care not to forward Frame downstream, since that will result
		// in runaway ticks. instead inner components should send a SetFpsMsg to
		// adjust the outermost layer.
		m.render()
		return m, tea.Batch(
			ui.Frame(FPS),
		)

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
		return f.err.Error() + "\n"
	}
	if f.done && f.eof {
		return ""
	}
	return f.view.String() + "\n"
}

func (fe *Frontend) renderRow(out *termenv.Output, row *TraceRow) error {
	vtx := row.Step.FirstVertex()

	if row.IsRunning() {
		id := row.Step.ID()
		if id != nil {
			if err := fe.renderID(out, vtx, row.Step.ID(), row.Depth()); err != nil {
				return err
			}
		} else if vtx := row.Step.FirstVertex(); vtx != nil {
			if err := fe.renderVertex(out, vtx, row.Depth()); err != nil {
				return err
			}
		}
		if logs, ok := fe.db.Logs[row.Step.Digest]; ok {
			logs.SetPrefix(strings.Repeat("  ", row.Depth()+1))
			logs.SetHeight(fe.window.Height / 3)
			fmt.Fprint(out, logs.View())
		}
	}

	for _, child := range row.Children {
		// out.SetWindowTitle // TODO  this would be cool
		if err := fe.renderRow(out, child); err != nil {
			return err
		}
	}

	return nil
}

func indent(out io.Writer, depth int) {
	fmt.Fprint(out, strings.Repeat("  ", depth))
}

func (fe *Frontend) renderID(out *termenv.Output, vtx *progrock.Vertex, id *idproto.ID, depth int) error {
	indent(out, depth)

	// if id.Parent == nil {
	// 	fmt.Fprintln(out, out.String(dot+" id: "+dig.String()).Bold())
	// 	indent()
	// }

	if vtx != nil {
		fe.renderStatus(out, vtx)
	}

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
			depth++
			for _, arg := range id.Args {
				indent(out, depth)
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
					argVtx := fe.db.Vertices[argVertexID.String()]
					base := x.Id
					if baseStep, ok := fe.db.HighLevelStep(x.Id); ok {
						base = baseStep.ID()
					}
					if err := fe.renderID(out, argVtx, base, depth); err != nil {
						return err
					}
					depth--
					indent(out, depth)
					fmt.Fprintln(out, "}")
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
	fmt.Fprintln(out, typeStr)

	// if vtx != nil && (tape.IsClosed || tape.ShowCompletedLogs || vtx.Completed == nil || vtx.Error != nil) {
	// 	renderVertexTasksAndLogs(out, u, tape, vtx, depth)
	// }

	return nil
}

func (fe *Frontend) renderVertex(out *termenv.Output, vtx *progrock.Vertex, depth int) error {
	indent(out, depth)
	fe.renderStatus(out, vtx)
	fmt.Fprintln(out, vtx.Name)
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
		symbol = fe.spin.View()
		color = termenv.ANSIYellow
	}

	symbol = out.String(symbol).Foreground(color).String()

	fmt.Fprintf(out, "%s ", symbol) // NB: has to match indent level
}

// func renderVertexTasksAndLogs(out *termenv.Output, u *progrock.UI, tape *progrock.Tape, vtx *progrock.Vertex, depth int) error {
// 	indent := func() {
// 		fmt.Fprint(out, strings.Repeat("  ", depth))
// 	}

// 	tasks := tape.VertexTasks[vtx.Id]
// 	for _, t := range tasks {
// 		if t.Completed != nil {
// 			continue
// 		}
// 		indent()
// 		fmt.Fprint(out, ui.VertRightBar, " ")
// 		if err := u.RenderTask(out, t); err != nil {
// 			return err
// 		}
// 	}

// 	term := tape.VertexLogs(vtx.Id)

// 	if vtx.Error != nil {
// 		term.SetHeight(term.UsedHeight())
// 	} else {
// 		term.SetHeight(tape.ReasonableTermHeight)
// 	}

// 	term.SetPrefix(strings.Repeat("  ", depth) + ui.VertBoldBar + " ")

// 	if tape.IsClosed {
// 		term.SetHeight(term.UsedHeight())
// 	}

// 	return u.RenderTerm(out, term)
// }

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
