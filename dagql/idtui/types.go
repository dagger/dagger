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
	Zoomed *Span
	Body   []*TraceTree
}

func (db *DB) RowsView(zoomedID trace.SpanID) *RowsView {
	view := &RowsView{
		Zoomed: db.Spans[zoomedID],
	}
	var spans []*Span
	if view.Zoomed != nil {
		spans = view.Zoomed.ChildrenAndEffects()
	} else {
		spans = db.SpanOrder
	}
	view.Body = db.CollectTree(spans)
	return view
}

func (db *DB) CollectTree(spans []*Span) []*TraceTree {
	var rows []*TraceTree
	db.WalkSpans(spans, func(row *TraceTree) {
		if row.Parent != nil {
			row.Parent.Children = append(row.Parent.Children, row)
		} else {
			rows = append(rows, row)
		}
	})
	return rows
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
			for _, child := range span.ChildSpans {
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
		if span.IsRunning || span.Failed() {
			row.setRunning()
		}
		f(row)
		lastRow = row
		seen[spanID] = true
		for _, child := range span.ChildSpans {
			if child.EffectID != "" && len(db.EffectSites[child.EffectID]) > 0 {
				// let it show up at the call sites instead
				continue
			}
			walk(child, row)
		}
		lastRow = row
		for _, effectID := range span.Effects {
			if effect, ok := db.Effects[effectID]; ok {
				effect.ParentSpan = row.Span
				walk(effect, row)
				if effect.IsRunning || effect.Failed() {
					row.setRunning()
				}
			}
		}
		lastRow = row
	}
	for _, step := range spans {
		walk(step, nil)
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
				Depth:                   depth,
				IsRunningOrChildRunning: tree.IsRunningOrChildRunning,
			}
			rows.Order = append(rows.Order, row)
			rows.BySpan[tree.Span.ID] = row
			depth++
		}
		if tree.IsRunningOrChildRunning || tree.Span.Failed() {
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
