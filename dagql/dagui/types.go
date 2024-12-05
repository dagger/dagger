package dagui

import (
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

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
	Parent                  *Span
	HasChildren             bool
}

type RowsView struct {
	Zoomed *Span
	Body   []*TraceTree
	BySpan map[SpanID]*TraceTree
}

func (db *DB) RowsView(opts FrontendOpts) *RowsView {
	view := &RowsView{
		BySpan: make(map[SpanID]*TraceTree),
	}
	if zoomed, ok := db.Spans.Map[opts.ZoomedSpan]; ok {
		view.Zoomed = zoomed
	}
	var spans []*Span
	if view.Zoomed != nil {
		spans = view.Zoomed.ChildSpans.Order
	} else {
		spans = db.Spans.Order
	}
	db.WalkSpans(opts, spans, func(tree *TraceTree) {
		if tree.Parent != nil {
			tree.Parent.Children = append(tree.Parent.Children, tree)
		} else {
			view.Body = append(view.Body, tree)
		}
		view.BySpan[tree.Span.ID] = tree
	})
	return view
}

func (db *DB) WalkSpans(opts FrontendOpts, spans []*Span, f func(*TraceTree)) {
	var lastTree *TraceTree
	seen := make(map[SpanID]bool)
	var walk func(*Span, *TraceTree)
	walk = func(span *Span, parent *TraceTree) {
		spanID := span.ID
		if seen[spanID] {
			return
		}
		seen[spanID] = true

		// If the span should be hidden, don't even collect it into the tree so we
		// can track relationships between rows accurately (e.g. chaining pipeline
		// calls).
		if !opts.ShouldShow(span) {
			return
		}

		if span.Passthrough ||
			// We inserted a stub for this span, but never received data for it. This
			// can happen if we're within a larger trace - we'll allocate our parent,
			// but not actually see it, so just move along to its children.
			!span.Received {
			for _, child := range span.ChildSpans.Order {
				walk(child, parent)
			}
			return
		}

		tree := &TraceTree{
			Span:   span,
			Parent: parent,
		}
		if span.Base != nil && lastTree != nil {
			tree.Chained = span.Base.Digest == lastTree.Span.CallDigest ||
				span.Base.Digest == lastTree.Span.Output
			lastTree.Final = !tree.Chained
		}
		if lastTree != nil && lastTree.Span.Call != nil && tree.Span.Call == nil {
			lastTree.Final = true
		}
		if span.IsRunningOrEffectsRunning() {
			tree.setRunning()
		}
		f(tree)
		lastTree = tree
		for _, child := range span.ChildSpans.Order {
			walk(child, tree)
		}
		if lastTree != nil {
			lastTree.Final = true
		}
		lastTree = tree
	}
	for _, span := range spans {
		walk(span, nil)
	}
	if lastTree != nil {
		lastTree.Final = true
	}
}

type Rows struct {
	Order  []*TraceRow
	BySpan map[SpanID]*TraceRow
}

func (lv *RowsView) Rows(opts FrontendOpts) *Rows {
	rows := &Rows{
		BySpan: make(map[SpanID]*TraceRow, len(lv.Body)),
	}
	var walk func(*TraceTree, *Span, int)
	walk = func(tree *TraceTree, parent *Span, depth int) {
		row := &TraceRow{
			Index:                   len(rows.Order),
			Span:                    tree.Span,
			Chained:                 tree.Chained,
			Depth:                   depth,
			IsRunningOrChildRunning: tree.IsRunningOrChildRunning,
			Parent:                  parent,
			HasChildren:             len(tree.Children) > 0,
		}
		if len(rows.Order) > 0 {
			row.Previous = rows.Order[len(rows.Order)-1]
		}
		rows.Order = append(rows.Order, row)
		rows.BySpan[tree.Span.ID] = row
		if tree.IsRunningOrChildRunning || tree.Span.IsFailedOrCausedFailure() || opts.Verbosity >= ExpandCompletedVerbosity {
			for _, child := range tree.Children {
				walk(child, row.Span, depth+1)
			}
		}
	}
	for _, row := range lv.Body {
		// TODO: parent should be zoomed span?
		walk(row, lv.Zoomed, 0)
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
