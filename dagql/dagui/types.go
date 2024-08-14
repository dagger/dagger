package dagui

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

type Task struct {
	Span      sdktrace.ReadOnlySpan
	Name      string
	Current   int64
	Total     int64
	Started   time.Time
	Completed time.Time
}

type TraceTree struct {
	Span *Span

	Parent *TraceTree

	IsRunningOrChildRunning bool
	Chained                 bool
	Final                   bool

	Children []*TraceTree
}

// TraceRow is the flattened representation of the tree so we can easily walk
// it backwards and render only the parts that will fit on screen. Otherwise
// large traces get giga slow.
type TraceRow struct {
	Index                   int
	Span                    *Span
	Chained                 bool
	Depth                   int
	IsRunningOrChildRunning bool
	Previous                *TraceRow
}

type RowsView struct {
	Zoomed *Span
	Body   []*TraceTree
	BySpan map[trace.SpanID]*TraceTree
}

func (db *DB) RowsView(zoomedID trace.SpanID) *RowsView {
	view := &RowsView{
		Zoomed: db.Spans[zoomedID],
		BySpan: make(map[trace.SpanID]*TraceTree),
	}
	var spans []*Span
	if view.Zoomed != nil {
		spans = view.Zoomed.ChildrenAndLinkedSpans()
	} else {
		spans = db.SpanOrder
	}
	db.WalkSpans(spans, func(row *TraceTree) {
		if row.Parent != nil {
			row.Parent.Children = append(row.Parent.Children, row)
		} else {
			view.Body = append(view.Body, row)
		}
		view.BySpan[row.Span.ID] = row
	})
	return view
}

func (db *DB) WalkSpans(spans []*Span, f func(*TraceTree)) {
	var lastRow *TraceTree
	seen := make(map[trace.SpanID]bool, len(spans))
	var walk func(*Span, *TraceTree)
	walk = func(span *Span, parent *TraceTree) {
		if span.Ignore {
			return
		}
		spanID := span.ID
		if seen[spanID] {
			return
		}
		if span.Passthrough {
			for _, child := range span.ChildrenAndLinkedSpans() {
				walk(child, parent)
			}
			return
		}
		row := &TraceTree{
			Span:   span,
			Parent: parent,
		}
		if span.Base != nil && lastRow != nil {
			// TODO: sync with Cloud impl.
			row.Chained = span.Base.Digest == lastRow.Span.Digest
			lastRow.Final = !row.Chained
		}
		if span.IsRunning() {
			row.setRunning()
		}
		f(row)
		lastRow = row
		seen[spanID] = true
		for _, child := range span.ChildrenAndLinkedSpans() {
			walk(child, row)
		}
		if lastRow != nil {
			lastRow.Final = true
		}
		lastRow = row
	}
	for _, step := range spans {
		walk(step, nil)
	}
	if lastRow != nil {
		lastRow.Final = true
	}
}

type Rows struct {
	Order  []*TraceRow
	BySpan map[trace.SpanID]*TraceRow
}

func (lv *RowsView) Rows(opts FrontendOpts) *Rows {
	rows := &Rows{
		BySpan: make(map[trace.SpanID]*TraceRow, len(lv.Body)),
	}
	var walk func(*TraceTree, int)
	walk = func(tree *TraceTree, depth int) {
		if !opts.ShouldShow(tree) {
			return
		}
		if !tree.Span.Passthrough {
			row := &TraceRow{
				Index:                   len(rows.Order),
				Span:                    tree.Span,
				Chained:                 tree.Chained,
				Depth:                   depth,
				IsRunningOrChildRunning: tree.IsRunningOrChildRunning,
			}
			if len(rows.Order) > 0 {
				row.Previous = rows.Order[len(rows.Order)-1]
			}
			rows.Order = append(rows.Order, row)
			rows.BySpan[tree.Span.ID] = row
			depth++
		}
		if tree.IsRunningOrChildRunning || tree.Span.IsFailed() || opts.Verbosity >= ExpandCompletedVerbosity {
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
	if row.IsRunningOrChildRunning {
		return
	}
	row.IsRunningOrChildRunning = true
	if row.Parent != nil && !row.Parent.IsRunningOrChildRunning {
		row.Parent.setRunning()
	}
}
