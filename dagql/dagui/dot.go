package dagui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

func (db *DB) WriteDot(
	outputFilePath string,
	focusField string,
	showInternal bool,
) {
	if outputFilePath == "" {
		return
	}
	out, err := os.Create(outputFilePath)
	if err != nil {
		panic(err)
	}

	dag := db.getDotDag(focusField, showInternal)
	dag.writeTo(out)
}

type dotDag struct {
	vtxByCallDgst map[string]*dotVtx
}

type dotVtx struct {
	dgst string
	call *callpbv1.Call
	span *Span

	children map[string]*dotEdge
	parents  map[string]*dotEdge

	// whether to include in dot output
	show bool
}

type dotEdge struct {
	kind edgeKind
	// only for kind arg
	argName string
}

func (db *DB) getDotDag(focusField string, showInternal bool) *dotDag {
	dag := &dotDag{
		vtxByCallDgst: make(map[string]*dotVtx),
	}

	for _, span := range db.Spans.Order {
		call := span.Call
		if call == nil {
			continue
		}
		callDgst := call.Digest

		vtx, ok := dag.getOrInitVtx(callDgst)
		if ok && vtx.call != nil {
			// already handled
			continue
		}
		vtx.call = call
		vtx.span = span

		if vtx.call.ReceiverDigest != "" {
			parentVtx, _ := dag.getOrInitVtx(vtx.call.ReceiverDigest)
			edge := dag.getOrInitEdge(parentVtx, vtx)
			edge.kind = edgeKindReceiver
		} else if parentSpan := findParentSpanWithCall(vtx.span); parentSpan != nil {
			// see if we can connect to a parent span (i.e. one module calling to another or to core, etc.)
			parentVtx, _ := dag.getOrInitVtx(parentSpan.Call.Digest)
			edge := dag.getOrInitEdge(parentVtx, vtx)
			if edge.kind == edgeKindUnset {
				edge.kind = edgeKindSpan
			}
		}

		for _, arg := range vtx.call.Args {
			argCallDgstLit, ok := arg.Value.Value.(*callpbv1.Literal_CallDigest)
			if !ok || argCallDgstLit == nil {
				continue
			}
			argCallDgst := argCallDgstLit.CallDigest

			parentVtx, _ := dag.getOrInitVtx(argCallDgst)
			edge := dag.getOrInitEdge(parentVtx, vtx)
			if edge.kind == edgeKindUnset {
				edge.kind = edgeKindArg
				edge.argName = arg.Name
			}
		}
	}

	focusedVtxs := make(map[*dotVtx]struct{})
	for _, vtx := range dag.vtxByCallDgst {
		// if asked to focus on vertexes with a given field name, find those
		if focusField != "" {
			if vtx.call == nil {
				continue
			}
			if vtx.call.Field == focusField {
				focusedVtxs[vtx] = struct{}{}
			}
			continue
		}

		// otherwise, "focus" on the roots (vtxs with no parents) so we show everything
		if len(vtx.parents) == 0 {
			focusedVtxs[vtx] = struct{}{}
		}
	}

	visited := make(map[*dotVtx]struct{})
	for vtx := range focusedVtxs {
		dag.setShow(vtx, visited, showInternal)
	}

	return dag
}

func (dag *dotDag) getOrInitVtx(callDgst string) (*dotVtx, bool) {
	vtx, ok := dag.vtxByCallDgst[callDgst]
	if !ok {
		vtx = &dotVtx{
			dgst:     callDgst,
			children: make(map[string]*dotEdge),
			parents:  make(map[string]*dotEdge),
		}
		dag.vtxByCallDgst[callDgst] = vtx
	}
	return vtx, ok
}

func (dag *dotDag) getOrInitEdge(parent, child *dotVtx) *dotEdge {
	_, parentToChildOk := parent.children[child.dgst]
	edge, childToParentOk := child.parents[parent.dgst]
	switch {
	case parentToChildOk && childToParentOk:
		return edge
	case parentToChildOk:
		panic(fmt.Sprintf(
			"parent-to-child edge exists, but child-to-parent does not: child (%q) -> parent (%q)",
			child.dgst,
			parent.dgst,
		))
	case childToParentOk:
		panic(fmt.Sprintf(
			"child-to-parent edge exists, but parent-to-child does not: parent (%q) -> child (%q)",
			parent.dgst,
			child.dgst,
		))
	}

	edge = &dotEdge{}
	parent.children[child.dgst] = edge
	child.parents[parent.dgst] = edge
	return edge
}

func (dag *dotDag) setShow(vtx *dotVtx, visited map[*dotVtx]struct{}, showInternal bool) {
	if _, ok := visited[vtx]; ok {
		return
	}
	visited[vtx] = struct{}{}

	// don't show things we never found call data for
	if vtx.call == nil {
		return
	}

	// don't show internal unless asked to
	if !showInternal && vtx.span.Internal {
		return
	}

	// hide noisy "id" unless internal requested (even though id is not technically marked as internal)
	if !showInternal && vtx.call.Field == "id" {
		return
	}

	// show it and check its children
	vtx.show = true
	for childVtxDgst := range vtx.children {
		childVtx, ok := dag.vtxByCallDgst[childVtxDgst]
		if !ok {
			panic(fmt.Sprintf("child vtx %q not found", childVtxDgst))
		}
		dag.setShow(childVtx, visited, showInternal)
	}
}

func (dag *dotDag) writeTo(out io.Writer) {
	fmt.Fprintln(out, "digraph {")
	defer fmt.Fprintln(out, "}")

	for vtxDgst, vtx := range dag.vtxByCallDgst {
		if !vtx.show {
			continue
		}

		buf := new(bytes.Buffer)
		fmt.Fprintf(buf, "%s", vtx.call.Field)
		for ai, arg := range vtx.call.Args {
			if ai == 0 {
				fmt.Fprintf(buf, "(")
			} else {
				fmt.Fprintf(buf, ", ")
			}
			fmt.Fprintf(buf, "%s: %s", arg.Name, displayLit(arg.Value))
			if ai == len(vtx.call.Args)-1 {
				fmt.Fprintf(buf, ")")
			}
		}
		if vtx.call.Nth != 0 {
			fmt.Fprintf(buf, "#%d", vtx.call.Nth)
		}
		label := buf.String()

		duration := vtx.span.Activity.Duration(time.Now())
		label += fmt.Sprintf("\n%s", duration)

		thicc := false
		if s := duration.Seconds(); s > 1.0 {
			thicc = true
		}
		border := 1.0
		color := "black"
		if thicc {
			border = 10.0
			color = "red"
		}

		fmt.Fprintf(out, "  %q [label=%q shape=ellipse penwidth=%f color=%s];\n", vtxDgst, label, border, color)

		for childVtxDgst, edge := range vtx.children {
			childVtx, ok := dag.vtxByCallDgst[childVtxDgst]
			if !ok {
				panic(fmt.Sprintf("child vtx %q not found", childVtxDgst))
			}
			if !childVtx.show {
				continue
			}

			switch edge.kind {
			case edgeKindReceiver:
				fmt.Fprintf(out, "  %q -> %q [color=black];\n", vtx.dgst, childVtx.dgst)
			case edgeKindArg:
				fmt.Fprintf(out, "  %q -> %q [color=blue label=%q];\n",
					vtx.dgst,
					childVtx.dgst,
					edge.argName,
				)
			case edgeKindSpan:
				fmt.Fprintf(out, "  %q -> %q [color=green];\n", vtx.dgst, childVtx.dgst)
			}
		}
	}
}

type edgeKind int

const (
	edgeKindUnset edgeKind = iota
	edgeKindReceiver
	edgeKindArg
	edgeKindSpan
)

func findParentSpanWithCall(span *Span) *Span {
	if span.ParentSpan == nil {
		return nil
	}
	if span.ParentSpan.Call != nil {
		return span.ParentSpan
	}
	return findParentSpanWithCall(span.ParentSpan)
}

func displayLit(lit *callpbv1.Literal) string {
	switch val := lit.GetValue().(type) {
	case *callpbv1.Literal_Bool:
		return fmt.Sprintf("%v", val.Bool)
	case *callpbv1.Literal_Int:
		return fmt.Sprintf("%d", val.Int)
	case *callpbv1.Literal_Float:
		return fmt.Sprintf("%f", val.Float)
	case *callpbv1.Literal_String_:
		if len(val.String_) > 256 {
			return "ETOOBIG"
		}
		return fmt.Sprintf("%q", val.String_)
	case *callpbv1.Literal_CallDigest:
		return "<input>"
	case *callpbv1.Literal_Enum:
		return val.Enum
	case *callpbv1.Literal_Null:
		return "null"
	case *callpbv1.Literal_List:
		s := "["
		for i, item := range val.List.GetValues() {
			if i > 0 {
				s += ", "
			}
			s += displayLit(item)
		}
		s += "]"
		return s
	case *callpbv1.Literal_Object:
		s := "{"
		for i, item := range val.Object.GetValues() {
			if i > 0 {
				s += ", "
			}
			s += fmt.Sprintf("%s: %s", item.GetName(), displayLit(item.GetValue()))
		}
		s += "}"
		return s
	default:
		panic(fmt.Sprintf("unknown literal type %T", val))
	}
}
