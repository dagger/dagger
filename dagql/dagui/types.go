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
	RevealedChildren        bool

	// Children holds the trees of the spans placed beneath the span. A tree
	// RowsView shows collapsed doesn't have them built: ChildTrees builds them.
	Children []*TraceTree

	// Revealed holds the trees of the span's revealed spans, built beneath
	// it, when it shows them in place of its children (see
	// ShouldShowRevealedSpans). They're copies: a revealed span also has a
	// tree of its own wherever its home is, usually among Children. Like
	// Children, they may not be built yet: RevealedTrees builds them.
	Revealed []*TraceTree

	// lazy is the walk that built the tree, if it built only the trees its
	// view shows; it builds Children and Revealed on demand.
	lazy                         *spanWalker
	childrenBuilt, revealedBuilt bool
	// revealedCopy marks a copy of a revealed span, or a tree beneath one.
	revealedCopy bool
	// hasChildren memoizes whether unbuilt Children would be empty: 0 when
	// unknown, 1 if they would, 2 if not.
	hasChildren uint8
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
	Depth                   int
	IsRunningOrChildRunning bool
	HasChildren             bool
	ShowingChildren         bool
	Expanded                bool
}

type RowsView struct {
	Zoomed *Span
	Body   []*TraceTree
	// BySpan maps each span to its tree; for a revealed span, the tree at its
	// home, if it has one, rather than its copy beneath a revealing span.
	//
	// A view built lazily (see rowsView) only builds the trees its rows show,
	// so BySpan only holds those, plus whatever has been built on demand
	// since. A span whose home tree isn't built maps to a revealed copy of
	// it, if one is. HomeTree finds or builds any span's home tree.
	BySpan map[SpanID]*TraceTree

	// walker built the view, and builds more of it on demand.
	walker *spanWalker
}

func (db *DB) AllSpans() iter.Seq[*Span] {
	return db.Spans.Iter()
}

func (db *DB) HasChecks() bool {
	return db.HasChecksForSpan(nil)
}

// HasChecksForSpan reports whether the root-relative surfaced check view is
// non-empty. A nil root means the live trace root, matching SurfacedChecks.
func (db *DB) HasChecksForSpan(root *Span) bool {
	return len(db.SurfacedChecksForSpan(root)) > 0
}

func (db *DB) HasGenerateReport() bool {
	for span := range db.kindSpanIter() {
		if span.GenerateSkipped {
			return true
		}
	}
	return false
}

// SkippedModuleSpans returns the spans reporting workspace modules that
// best-effort generate skipped because they could not be loaded, in encounter
// order. The final report renders these as a persisted "SKIPPED MODULES"
// section so they survive the live tree collapsing on a successful run.
func (db *DB) SkippedModuleSpans() []*Span {
	var out []*Span
	for span := range db.kindSpanIter() {
		if span.GenerateSkipped {
			out = append(out, span)
		}
	}
	return out
}

// RegeneratedModuleSpans returns the spans `dagger generate` emitted for
// skipped modules whose directory its changes touched, keyed by the module
// name they share with the skipped-module span. Each records the outcome of
// loading the module again with the changes applied: OK (it loads) or failed
// (the post-generation error). The report shows that outcome instead of the
// pre-generation load error it supersedes.
func (db *DB) RegeneratedModuleSpans() map[string]*Span {
	out := map[string]*Span{}
	for span := range db.kindSpanIter() {
		if span.GenerateRegenerated {
			out[span.Name] = span
		}
	}
	return out
}

// RowsView builds the trees of the view opts selects that its rows show: the
// top level, and the trees beneath every tree Rows(opts) shows expanded. See
// rowsView.
func (db *DB) RowsView(opts FrontendOpts) *RowsView {
	return db.rowsView(opts, true)
}

// rowsView builds a RowsView: every tree in it, or lazily, only the trees
// its rows show. A lazy view doesn't build the Children (or Revealed) of a
// tree Rows(opts) shows collapsed, so a rebuild costs what the view shows,
// not what it holds; they're built on demand (see ChildTrees), by Rows too,
// when another opts expands it.
//
// Where each span goes depends only on local rules (see spanWalker), so the
// trees built on demand, and the running state and children a collapsed tree
// reports, are what building every tree would give.
func (db *DB) rowsView(opts FrontendOpts, lazy bool) *RowsView {
	view := &RowsView{
		BySpan: make(map[SpanID]*TraceTree),
	}
	w, zoomed, ok := db.newSpanWalker(opts)
	if !ok {
		// we haven't received the zoomed span yet, so don't render anything
		//
		// this happens when we create a span and immediately zoom to it
		return view
	}
	w.lazy = lazy
	view.Zoomed = zoomed
	view.walker = w
	w.emitted = func(tree *TraceTree, revealedCopy bool) {
		// A span's home tree takes the place of a revealed copy of it, which
		// takes the place of nothing else.
		if _, ok := view.BySpan[tree.Span.ID]; revealedCopy && ok {
			return
		}
		view.BySpan[tree.Span.ID] = tree
	}
	view.Body = w.walk()
	return view
}

// HomeTree returns the tree of span's home in the view (see spanWalker),
// building the trees on its way down from the top if they aren't built yet,
// or nil if the view doesn't place span.
func (lv *RowsView) HomeTree(span *Span) *TraceTree {
	if lv == nil {
		return nil
	}
	if tree := lv.BySpan[span.ID]; tree != nil && !tree.revealedCopy {
		return tree
	}
	if lv.walker == nil {
		return nil
	}
	return lv.walker.homeTree(lv.Body, span)
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
			for _, child := range tree.VisibleChildren(opts) {
				childRow := walk(child, row, depth+1)
				if lastChild != nil {
					childRow.Previous = lastChild
					lastChild.Next = childRow
				}
				lastChild = childRow
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

func (row *TraceTree) ShouldShowRevealedSpans(opts FrontendOpts) bool {
	return row.RevealedChildren && showsRevealedSpans(row.Span, opts)
}

// showsRevealedSpans is ShouldShowRevealedSpans for a span with revealed
// spans.
func showsRevealedSpans(span *Span, opts FrontendOpts) bool {
	verbosity := opts.Verbosity
	if v, ok := opts.SpanVerbosity[span.ID]; ok {
		verbosity = v
	}
	return !opts.RevealNoisySpans && verbosity < ShowSpammyVerbosity
}

// ChildTrees returns the trees of the spans placed beneath the tree's span
// (its Children), building them first if they aren't yet.
func (row *TraceTree) ChildTrees() []*TraceTree {
	if row.lazy != nil && !row.childrenBuilt {
		row.lazy.buildOnDemand(row, false)
	}
	return row.Children
}

// RevealedTrees returns the copies of the span's revealed spans beneath the
// tree (its Revealed), building them first if they aren't yet.
func (row *TraceTree) RevealedTrees() []*TraceTree {
	if row.lazy != nil && !row.revealedBuilt {
		row.lazy.buildOnDemand(row, true)
	}
	return row.Revealed
}

// VisibleChildren returns the trees Rows shows beneath the tree when it's
// expanded under opts: its revealed spans' copies or its children.
func (row *TraceTree) VisibleChildren(opts FrontendOpts) []*TraceTree {
	if row.ShouldShowRevealedSpans(opts) {
		return row.RevealedTrees()
	}
	return row.ChildTrees()
}

func (row *TraceTree) hasVisibleChildren(opts FrontendOpts) bool {
	if row.ShouldShowRevealedSpans(opts) {
		return row.Span.RevealedSpans.Len() > 0
	}
	if row.lazy != nil && !row.childrenBuilt {
		if row.hasChildren == 0 {
			row.hasChildren = 1
			if row.lazy.hasChildren(row) {
				row.hasChildren = 2
			}
		}
		return row.hasChildren == 2
	}
	return len(row.Children) > 0
}

func (row *TraceTree) IsExpanded(opts FrontendOpts) bool {
	expanded, toggled := opts.SpanExpanded[row.Span.ID]
	if toggled {
		return expanded
	}

	verbosity := opts.Verbosity
	if v, ok := opts.SpanVerbosity[row.Span.ID]; ok {
		verbosity = v
	}

	autoExpand := row.Depth() < 1 && row.IsRunningOrChildRunning

	alwaysExpand := row.Span.IsCanceled() ||
		(row.Span.LLMRole != "" && row.Span.RevealedSpans.Len() > 0) ||
		verbosity >= ExpandCompletedVerbosity ||
		opts.ExpandCompleted

	// Tool calls and rolled-up spans hide their guts by default -- they tend to
	// show a bunch of internals that distract from the overall history. But at a
	// high enough verbosity (the same threshold that expands completed spans)
	// the user is explicitly asking to see everything, so let it punch through
	// the rollup boundary -- e.g. 'dagger trace --span <toolcall> -vvvvv' to
	// inspect a slow tool call's full call tree. ExpandCompleted alone does not
	// punch through: it keeps completed spans open but still respects rollup
	// boundaries.
	neverExpand := (row.Span.LLMTool != "" || row.Span.RollUpLogs || row.Span.RollUpSpans) &&
		verbosity < ExpandCompletedVerbosity

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

func (row *TraceRow) Root() *TraceRow {
	if row.Parent == nil {
		return row
	}
	return row.Parent.Root()
}
