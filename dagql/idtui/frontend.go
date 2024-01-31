package idtui

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dagger/dagger/dagql/idproto"
	"github.com/muesli/termenv"
	"github.com/vito/progrock"
	"github.com/vito/progrock/ui"
)

type Frontend struct {
	allIDs  map[string]*idproto.ID
	leafIDs map[string]*idproto.ID
}

func New() progrock.Frontend {
	return &Frontend{
		allIDs: make(map[string]*idproto.ID),
	}
}

var _ progrock.Writer = (*Frontend)(nil)

func (f *Frontend) WriteStatus(status *progrock.StatusUpdate) error {
	for _, v := range status.Metas {
		var id idproto.ID
		if err := v.Data.UnmarshalTo(&id); err != nil {
			return fmt.Errorf("unmarshal payload: %w", err)
		}
		dig, err := id.Digest()
		if err != nil {
			return fmt.Errorf("digest payload: %w", err)
		}
		f.allIDs[dig.String()] = &id
	}
	if len(status.Metas) > 0 {
		f.leafIDs = make(map[string]*idproto.ID)
		for vid, id := range f.allIDs {
			f.leafIDs[vid] = id
		}

		for _, idp := range f.allIDs {
			for parent := idp.Parent; parent != nil; parent = parent.Parent {
				pdig, err := parent.Digest()
				if err != nil {
					return fmt.Errorf("digest parent: %w", err)
				}
				delete(f.leafIDs, pdig.String())
			}
			for _, arg := range idp.Args {
				switch x := arg.Value.GetValue().(type) {
				case *idproto.Literal_Id:
					adig, err := x.Id.Digest()
					if err != nil {
						return fmt.Errorf("digest parent: %w", err)
					}
					delete(f.leafIDs, adig.String())
				default:
				}
			}
		}
	}
	return nil
}

func (*Frontend) Close() error {
	return nil
}

var _ progrock.Frontend = (*Frontend)(nil)

func (f *Frontend) Render(tape *progrock.Tape, w io.Writer, u *progrock.UI) error {
	var ids []vertexAndID //nolint:prealloc
	for vid, id := range f.leafIDs {
		vtx := tape.Vertices[vid]
		if vtx == nil {
			continue
		}
		vtxAndID := vertexAndID{
			vtx: vtx,
			id:  id,
		}
		if id.Field == "id" {
			// skip selecting ID since that means it'll show up somewhere else that's
			// more interesting
			continue
		}
		if vtxAndID.totalDuration(tape) < 100*time.Millisecond {
			continue
		}
		ids = append(ids, vtxAndID)
	}
	sort.Slice(ids, func(i, j int) bool {
		vi := ids[i].vtx
		vj := ids[j].vtx
		return vi.Started.AsTime().Before(vj.Started.AsTime())
	})
	out := ui.NewOutput(w, termenv.WithProfile(tape.ColorProfile))
	for _, id := range ids {
		if err := renderID(tape, out, u, id.vtx, id.id, 0); err != nil {
			return err
		}
		fmt.Fprintln(out)
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

func renderID(tape *progrock.Tape, out *termenv.Output, u *progrock.UI, vtx *progrock.Vertex, id *idproto.ID, depth int) error {
	if id.Parent != nil {
		parentVtxID, err := id.Parent.Digest()
		if err != nil {
			return err
		}
		parentVtx := tape.Vertices[parentVtxID.String()]
		if err := renderID(tape, out, u, parentVtx, id.Parent, depth); err != nil {
			return err
		}
	}

	indent := func() {
		fmt.Fprint(out, strings.Repeat("  ", depth))
	}

	indent()

	dig, err := id.Digest()
	if err != nil {
		return err
	}

	// if id.Parent == nil {
	// 	fmt.Fprintln(out, out.String(dot+" id: "+dig.String()).Bold())
	// 	indent()
	// }

	if vtx != nil {
		renderStatus(out, u, vtx)
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
					argVtx := tape.Vertices[argVertexID.String()]
					if err := renderID(tape, out, u, argVtx, x.Id, depth); err != nil {
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

	if vtx != nil && (tape.IsClosed || tape.ShowCompletedLogs || vtx.Completed == nil || vtx.Error != nil) {
		renderVertexTasksAndLogs(out, u, tape, vtx, depth)
	}

	if os.Getenv("SHOW_VERTICES") == "" {
		return nil
	}

	children := collectTransitiveChildren(tape, dig.String())
	sort.Slice(children, func(i, j int) bool {
		vi := children[i]
		vj := children[j]
		if vi.Cached && vj.Cached {
			return vi.Name < vj.Name
		}
		return vi.Started.AsTime().Before(vj.Started.AsTime())
	})

	depth++

	for _, vtx := range children {
		if vtx.Completed != nil && vtx.Error == nil {
			continue
		}

		indent()
		renderStatus(out, u, vtx)
		if err := u.RenderVertexTree(out, vtx); err != nil {
			return err
		}
		renderVertexTasksAndLogs(out, u, tape, vtx, depth)
	}

	return nil
}

func collectTransitiveChildren(tape *progrock.Tape, groupID string) []*progrock.Vertex {
	var children []*progrock.Vertex //nolint:prealloc
	for id := range tape.GroupVertices[groupID] {
		child := tape.Vertices[id]
		if child == nil {
			continue
		}
		children = append(children, child)
	}
	for sub := range tape.GroupChildren[groupID] {
		children = append(children, collectTransitiveChildren(tape, sub)...)
	}
	return children
}

func rootOf(id *idproto.ID) *idproto.ID {
	if id.Parent == nil {
		return id
	}
	return rootOf(id.Parent)
}

type vertexAndID struct {
	vtx *progrock.Vertex
	id  *idproto.ID
}

func (vid vertexAndID) totalDuration(tape *progrock.Tape) time.Duration {
	root := rootOf(vid.id)
	rootVtxID, err := root.Digest()
	if err != nil {
		return 0
	}
	rootVtx := tape.Vertices[rootVtxID.String()]
	if rootVtx == nil {
		return 0
	}
	return progrock.Duration(rootVtx.Started, vid.vtx.Completed)
}

func renderLiteral(out *termenv.Output, lit *idproto.Literal) {
	var color termenv.Color
	switch lit.GetValue().(type) {
	case *idproto.Literal_Bool:
		color = termenv.ANSIBrightRed
	case *idproto.Literal_Int:
		color = termenv.ANSIRed
	case *idproto.Literal_Float:
		color = termenv.ANSIRed
	case *idproto.Literal_String_:
		color = termenv.ANSIYellow
	case *idproto.Literal_Id:
		color = termenv.ANSIYellow
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

func renderStatus(out *termenv.Output, u *progrock.UI, vtx *progrock.Vertex) {
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
		symbol, _, _ = u.Spinner.ViewFrame(ui.DotFrames)
		color = termenv.ANSIYellow
	}

	symbol = out.String(symbol).Foreground(color).String()

	fmt.Fprintf(out, "%s ", symbol) // NB: has to match indent level
}

func renderVertexTasksAndLogs(out *termenv.Output, u *progrock.UI, tape *progrock.Tape, vtx *progrock.Vertex, depth int) error {
	indent := func() {
		fmt.Fprint(out, strings.Repeat("  ", depth))
	}

	tasks := tape.VertexTasks[vtx.Id]
	for _, t := range tasks {
		if t.Completed != nil {
			continue
		}
		indent()
		fmt.Fprint(out, ui.VertRightBar, " ")
		if err := u.RenderTask(out, t); err != nil {
			return err
		}
	}

	term := tape.VertexLogs(vtx.Id)

	if vtx.Error != nil {
		term.SetHeight(term.UsedHeight())
	} else {
		term.SetHeight(tape.ReasonableTermHeight)
	}

	term.SetPrefix(strings.Repeat("  ", depth) + ui.VertBoldBar + " ")

	if tape.IsClosed {
		term.SetHeight(term.UsedHeight())
	}

	return u.RenderTerm(out, term)
}
