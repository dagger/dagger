package idtui

import (
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type Trace struct {
	ID         trace.TraceID
	Epoch, End time.Time
	IsRunning  bool
	db         *DB
}

func (trace *Trace) HexID() string {
	return trace.ID.String()
}

func (trace *Trace) Name() string {
	if span := trace.db.PrimarySpanForTrace(trace.ID); span != nil {
		return span.Name()
	}
	return "unknown"
}

func (trace *Trace) PrimarySpan() *Span {
	return trace.db.PrimarySpanForTrace(trace.ID)
}

type Task struct {
	Span      sdktrace.ReadOnlySpan
	Name      string
	Current   int64
	Total     int64
	Started   time.Time
	Completed time.Time
}

func CollectSpans(db *DB, traceID trace.TraceID) []*Span {
	var spans []*Span //nolint:prealloc
	for _, span := range db.SpanOrder {
		if span.Ignore {
			continue
		}
		if traceID.IsValid() && span.SpanContext().TraceID() != traceID {
			continue
		}
		spans = append(spans, span)
	}
	return spans
}

func CollectTree(steps []*Span) []*TraceTree {
	var rows []*TraceTree
	WalkSteps(steps, func(row *TraceTree) {
		if row.Parent != nil {
			row.Parent.Children = append(row.Parent.Children, row)
		} else {
			rows = append(rows, row)
		}
	})
	return rows
}

type TraceTree struct {
	Span *Span

	Parent *TraceTree

	IsRunningOrChildRunning bool
	Chained                 bool

	Children []*TraceTree
}

// TraceRow is the flattened representation of the tree so we can easily walk
// it backwards and render only the parts that will fit on screen. Otherwise
// large traces get giga slow.
type TraceRow struct {
	Index                   int
	Span                    *Span
	Depth                   int
	IsRunningOrChildRunning bool
}

type RowsView struct {
	Primary *Span
	Body    []*TraceTree
}

func CollectRowsView(rows []*TraceTree) *RowsView {
	view := &RowsView{}
	for _, row := range rows {
		if row.Span.Primary {
			// promote children of primary vertex to the top-level
			for _, child := range row.Children {
				child.Parent = nil
			}
			view.Primary = row.Span
			// reveal anything 'extra' below the primary content
			view.Body = append(row.Children, view.Body...)
		} else {
			// reveal anything 'extra' by default (fail open)
			view.Body = append(view.Body, row)
		}
	}
	return view
}

func (lv *RowsView) Rows(opts FrontendOpts) []*TraceRow {
	var rows []*TraceRow
	var walk func(*TraceTree, int)
	walk = func(tree *TraceTree, depth int) {
		if !opts.ShouldShow(tree) {
			return
		}
		if !tree.Span.Passthrough {
			rows = append(rows, &TraceRow{
				Index:                   len(rows),
				Span:                    tree.Span,
				Depth:                   depth,
				IsRunningOrChildRunning: tree.IsRunningOrChildRunning,
			})
			depth++
		}
		if tree.IsRunningOrChildRunning {
			for _, child := range tree.Children {
				walk(child, depth)
			}
		}
	}
	for _, row := range lv.Body {
		walk(row, 0)
	}
	return rows
}

func (row *TraceTree) Depth() int {
	if row.Parent == nil {
		return 0
	}
	return row.Parent.Depth() + 1
}

func (row *TraceTree) setRunning() {
	row.IsRunningOrChildRunning = true
	if row.Parent != nil && !row.Parent.IsRunningOrChildRunning {
		row.Parent.setRunning()
	}
}

func WalkSteps(spans []*Span, f func(*TraceTree)) {
	var lastRow *TraceTree
	seen := map[trace.SpanID]bool{}
	var walk func(*Span, *TraceTree)
	walk = func(span *Span, parent *TraceTree) {
		spanID := span.ID
		if seen[spanID] {
			return
		}
		if span.Passthrough {
			for _, child := range span.Children() {
				walk(child, parent)
			}
			return
		}
		row := &TraceTree{
			Span:   span,
			Parent: parent,
		}
		if span.Base != nil && lastRow != nil {
			row.Chained = span.Base.Digest == lastRow.Span.Digest
		}
		if span.IsRunning {
			row.setRunning()
		}
		f(row)
		lastRow = row
		seen[spanID] = true
		for _, child := range span.Children() {
			walk(child, row)
		}
		lastRow = row
	}
	for _, step := range spans {
		walk(step, nil)
	}
}
