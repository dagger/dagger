package dagui

import (
	"iter"
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
	RevealedChildren        bool

	Children []*TraceTree
}

// TraceRow is the flattened representation of the tree so we can easily walk
// it backwards and render only the parts that will fit on screen. Otherwise
// large traces get giga slow.
type TraceRow struct {
	Index int

	Span *Span

	Parent         *TraceRow `json:"-"`
	Previous       *TraceRow `json:"-"`
	PreviousVisual *TraceRow `json:"-"`
	Next           *TraceRow `json:"-"`
	NextVisual     *TraceRow `json:"-"`

	Chained                 bool
	Final                   bool
	Depth                   int
	IsRunningOrChildRunning bool
	HasChildren             bool
	ShowingChildren         bool
	Expanded                bool
}

type RowsView struct {
	Zoomed *Span
	Body   []*TraceTree
	BySpan map[SpanID]*TraceTree
}

func (db *DB) AllSpans() iter.Seq[*Span] {
	return db.Spans.Iter()
}

func (db *DB) RowsView(opts FrontendOpts) *RowsView {
	view := &RowsView{
		BySpan: make(map[SpanID]*TraceTree),
	}
	if opts.ZoomedSpan.IsValid() {
		if zoomed, ok := db.Spans.Map[opts.ZoomedSpan]; ok {
			view.Zoomed = zoomed
		} else {
			// we haven't received the zoomed span yet, so don't render anything
			//
			// this happens when we create a span and immediately zoom to it
			return view
		}
	}
	var spans iter.Seq[*Span]
	if view.Zoomed != nil {
		if len(view.Zoomed.RevealedSpans.Order) > 0 &&
			// Revealed spans bubble up all the way to the root span. By default, we
			// want to preserve the top-level context (i.e. spans immediately beneath
			// root). So, we only prioritize revealed spans if the zoomed span is also
			// marked Passthrough. That's how shell mode is able to take over the
			// top-level UI: it creates a `shell` span with `passthrough: true` and
			// zooms it.
			//
			// We could consider making this default later even for the root span.
			// Maybe it's slick to see only the intentionally revealed stuff? But you
			// probably wouldn't that for Errored spans which are auto-revealed.
			view.Zoomed.Passthrough {
			spans = view.Zoomed.RevealedSpans.Iter()
		} else {
			spans = view.Zoomed.ChildSpans.Iter()
		}
	} else {
		spans = db.AllSpans()
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

func (db *DB) WalkSpans(opts FrontendOpts, spans iter.Seq[*Span], f func(*TraceTree)) { //nolint:gocyclo
	var lastTree *TraceTree
	var lastCall *TraceTree
	seen := make(map[SpanID]bool)
	var walk func(*Span, *TraceTree) bool
	walk = func(span *Span, parent *TraceTree) bool {
		spanID := span.ID
		if seen[spanID] {
			return false
		}
		seen[spanID] = true

		// If the span should be hidden, don't even collect it into the tree so we
		// can track relationships between rows accurately (e.g. chaining pipeline
		// calls).
		if !opts.ShouldShow(db, span) {
			return false
		}

		if (span.Passthrough && !opts.Debug) ||
			// We inserted a stub for this span, but never received data for it. This
			// can happen if we're within a larger trace - we'll allocate our parent,
			// but not actually see it, so just move along to its children.
			!span.Received {
			for _, child := range span.ChildSpans.Order {
				walk(child, parent)
			}
			return false
		}

		if opts.Filter != nil {
			switch opts.Filter(span) {
			case WalkContinue:
			case WalkSkip, WalkStop:
				if lastTree != nil {
					lastTree.Final = true
				}
				return false
			case WalkPassthrough:
				// TODO: this Final field is a bit tedious...
				if lastTree != nil {
					lastTree.Final = true
				}
				for _, child := range span.ChildSpans.Order {
					walk(child, parent)
				}
				return false
			}
		}

		// display causal spans inline (always only one, but the data is many:many)
		reparent := false
		for cause := range span.CausalSpans {
			if !span.HasParent(cause) {
				reparent = walk(cause, parent)
			}
		}

		// reparent
		if reparent {
			parent = lastTree
		}

		tree := &TraceTree{
			Span:   span,
			Parent: parent,
		}
		if lastTree != nil {
			if lastTree.Span.Call() != nil && span.Call() == nil {
				lastTree.Final = true
			}
		}
		if lastCall != nil {
			if base := span.Base(); base != nil {
				tree.Chained =
					lastCall.Parent == tree.Parent &&
						(base.Digest == lastCall.Span.CallDigest ||
							base.Digest == lastCall.Span.Output)
				lastCall.Final = !tree.Chained
			}
		}
		if span.IsRunningOrEffectsRunning() {
			tree.setRunning()
		}

		f(tree)
		lastTree = tree
		if tree.Span.CallDigest != "" {
			lastCall = tree
		}

		tree.RevealedChildren = len(span.RevealedSpans.Order) > 0

		for _, child := range span.ChildSpans.Order {
			walk(child, tree)
		}

		if lastTree != nil {
			lastTree.Final = true
		}
		lastTree = tree
		if tree.Span.CallDigest != "" {
			lastCall = tree
		}
		return true
	}
	for span := range spans {
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
	var walk func(*TraceTree, *TraceRow, int) *TraceRow
	walk = func(tree *TraceTree, parent *TraceRow, depth int) *TraceRow {
		row := &TraceRow{
			Index: len(rows.Order),
			Span:  tree.Span,

			Parent: parent,

			Chained:                 tree.Chained,
			Final:                   tree.Final,
			Depth:                   depth,
			IsRunningOrChildRunning: tree.IsRunningOrChildRunning,

			HasChildren: tree.hasVisibleChildren(opts),
			Expanded:    tree.IsExpanded(opts),
		}
		if len(rows.Order) > 0 {
			prev := rows.Order[len(rows.Order)-1]
			row.PreviousVisual = prev
			prev.NextVisual = row
		}
		rows.Order = append(rows.Order, row)
		rows.BySpan[tree.Span.ID] = row
		if row.Expanded {
			var lastChild *TraceRow

			if tree.shouldShowRevealedSpans(opts) {
				// Show revealed spans directly, finding their TraceTrees
				for _, revealedSpan := range tree.Span.RevealedSpans.Order {
					if revealedTree, ok := lv.BySpan[revealedSpan.ID]; ok {
						childRow := walk(revealedTree, row, depth+1)
						if lastChild != nil {
							childRow.Previous = lastChild
							lastChild.Next = childRow
						}
						lastChild = childRow
					}
				}
			} else {
				// Show direct children
				for _, child := range tree.Children {
					childRow := walk(child, row, depth+1)
					if lastChild != nil {
						childRow.Previous = lastChild
						lastChild.Next = childRow
					}
					lastChild = childRow
				}
			}
			row.ShowingChildren = row.HasChildren
		}
		return row
	}
	var lastChild *TraceRow
	for _, tree := range lv.Body {
		childRow := walk(tree, nil, 0)
		if lastChild != nil {
			childRow.Previous = lastChild
			lastChild.Next = childRow
		}
		lastChild = childRow
	}
	return rows
}

func (row *TraceTree) shouldShowRevealedSpans(opts FrontendOpts) bool {
	verbosity := opts.Verbosity
	if v, ok := opts.SpanVerbosity[row.Span.ID]; ok {
		verbosity = v
	}
	return row.RevealedChildren && !opts.RevealNoisySpans && verbosity < ShowSpammyVerbosity
}

func (row *TraceTree) hasVisibleChildren(opts FrontendOpts) bool {
	if row.shouldShowRevealedSpans(opts) {
		return len(row.Span.RevealedSpans.Order) > 0
	} else {
		return len(row.Children) > 0
	}
}

func (row *TraceTree) IsExpanded(opts FrontendOpts) bool {
	expanded, toggled := opts.SpanExpanded[row.Span.ID]
	if toggled {
		return expanded
	}

	autoExpand := row.Depth() < 1 && row.IsRunningOrChildRunning

	alwaysExpand := row.Span.IsCanceled() ||
		opts.Verbosity >= ExpandCompletedVerbosity ||
		opts.ExpandCompleted

	// never expand tool calls by default, tends to show a bunch of guts that
	// distracts from the overall history
	neverExpand := row.Span.LLMTool != "" || row.Span.RollUpLogs || row.Span.RollUpSpans

	return (autoExpand || alwaysExpand) && !neverExpand
}

func (row *TraceTree) Depth() int {
	if row.Parent == nil {
		return 0
	}
	return row.Parent.Depth() + 1
}

func (row *TraceTree) Rows(opts FrontendOpts) []*TraceRow {
	view := &RowsView{Body: []*TraceTree{row}}
	return view.Rows(opts).Order
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

func (row *TraceRow) Root() *TraceRow {
	if row.Parent == nil {
		return row
	}
	return row.Parent.Root()
}
