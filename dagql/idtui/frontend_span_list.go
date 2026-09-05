package idtui

import (
	"slices"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/vito/tuist"
)

// SpanListView renders selected direct children of a trace span as normal,
// interactive span trees. It shares the frontend's DB, rendering policy, and
// span components; only the choice and ordering of roots belongs to the
// command screen.
type SpanListView struct {
	tuist.Compo

	fe      *frontendPretty
	root    func() dagui.SpanID
	include func() []dagui.SpanID

	// hoist lifts each included span to the top level of the list even when
	// it sits deep beneath root: the walk passes through everything that is
	// not an included span or inside one, so the rows render as if the
	// included spans were root's direct children (used by ServiceList, whose
	// per-service display spans live under the call machinery that opened
	// them). While include() is still empty the list falls back to root's
	// own children instead of rendering nothing, so setup progress shows.
	hoist bool

	scope     spanTreeScope
	container *tuist.Container
	visible   []dagui.SpanID
}

var (
	_ tuist.Component   = (*SpanListView)(nil)
	_ tuist.Mounter     = (*SpanListView)(nil)
	_ tuist.Dismounter  = (*SpanListView)(nil)
	_ tuist.Interactive = (*SpanListView)(nil)
)

func newSpanListView(fe *frontendPretty, root func() dagui.SpanID, include func() []dagui.SpanID) *SpanListView {
	return &SpanListView{
		fe:        fe,
		root:      root,
		include:   include,
		container: &tuist.Container{},
		scope: spanTreeScope{
			spanTrees: make(map[dagui.SpanID]*SpanTreeView),
		},
	}
}

func (v *SpanListView) OnMount(tuist.Context) {
	if v.fe.spanLists == nil {
		v.fe.spanLists = make(map[*SpanListView]struct{})
	}
	v.fe.spanLists[v] = struct{}{}
}

func (v *SpanListView) OnDismount() {
	delete(v.fe.spanLists, v)
}

func (v *SpanListView) UpdateAll() {
	v.Update()
	v.container.Update()
	for _, tree := range v.scope.spanTrees {
		tree.Update()
	}
}

func (v *SpanListView) Render(ctx tuist.Context) {
	if !v.sync() {
		return
	}
	v.RenderChild(ctx, v.container)
}

func (v *SpanListView) sync() bool {
	if v.root == nil {
		return v.clear()
	}
	rootID := v.root()
	root := v.fe.db.Spans.Map[rootID]
	if root == nil {
		return v.clear()
	}

	ids := []dagui.SpanID(nil)
	if v.include != nil {
		ids = v.include()
	}

	opts := v.fe.FrontendOpts
	opts.ZoomedSpan = rootID
	if v.hoist && len(ids) > 0 {
		// Hoist: pass through every span that is neither an included span nor
		// beneath one, so the included spans surface as the walk's top-level
		// trees no matter how deep their real parents sit.
		included := make(map[dagui.SpanID]bool, len(ids))
		for _, id := range ids {
			included[id] = true
		}
		opts.Filter = func(span *dagui.Span) dagui.WalkDecision {
			for s := span; s != nil; s = s.ParentSpan {
				if included[s.ID] {
					return dagui.WalkContinue
				}
				if s.ID == rootID {
					break
				}
			}
			return dagui.WalkPassthrough
		}
	}
	rowsView := v.fe.db.RowsView(opts)
	if len(rowsView.Body) == 0 {
		return v.clear()
	}
	v.scope.rowsView = rowsView
	v.scope.rows = rowsView.Rows(opts)
	v.scope.opts = opts

	children := make([]tuist.Component, 0, len(rowsView.Body))
	if v.include == nil || (v.hoist && len(ids) == 0) {
		ids = make([]dagui.SpanID, 0, len(rowsView.Body))
		for _, tree := range rowsView.Body {
			if tree != nil && tree.Span != nil {
				ids = append(ids, tree.Span.ID)
			}
		}
	}
	for _, id := range ids {
		i := slices.IndexFunc(rowsView.Body, func(tree *dagui.TraceTree) bool {
			return tree != nil && tree.Span != nil && tree.Span.ID == id
		})
		if i < 0 {
			continue
		}
		tree := rowsView.Body[i]
		spanTree := v.fe.getOrCreateSpanTreeInScope(tree.Span.ID, &v.scope)
		spanTree.parent = nil
		spanTree.indexInParent = len(children)
		v.fe.syncTreeNodeInScope(spanTree, treePrefix{}, &v.scope)
		children = append(children, spanTree)
	}

	if !sameComponents(v.container.Children, children) {
		v.container.Children = children
		v.container.Update()
	}
	v.visible = v.visible[:0]
	selected := make(map[dagui.SpanID]struct{}, len(ids))
	for _, id := range ids {
		selected[id] = struct{}{}
	}
	for _, row := range v.scope.rows.Order {
		top := row
		for top.Parent != nil {
			top = top.Parent
		}
		if _, ok := selected[top.Span.ID]; ok {
			v.visible = append(v.visible, row.Span.ID)
		}
	}
	return len(children) > 0
}

func (v *SpanListView) clear() bool {
	v.scope.rowsView = nil
	v.scope.rows = nil
	v.visible = nil
	if len(v.container.Children) > 0 {
		v.container.Children = nil
		v.container.Update()
	}
	return false
}

// Focused reports whether this list currently owns the frontend's focused
// span. It lets an outer command screen compose navigation across lists.
func (v *SpanListView) Focused() bool {
	return slices.Contains(v.visible, v.fe.FocusedSpan)
}

func (v *SpanListView) FocusFirst() bool {
	if !v.sync() || len(v.visible) == 0 {
		return false
	}
	return v.focus(v.visible[0])
}

func (v *SpanListView) FocusLast() bool {
	if !v.sync() || len(v.visible) == 0 {
		return false
	}
	return v.focus(v.visible[len(v.visible)-1])
}

func (v *SpanListView) focus(id dagui.SpanID) bool {
	tree := v.scope.spanTrees[id]
	if tree == nil {
		return false
	}
	v.fe.autoFocus = false
	v.fe.FocusedSpan = id
	v.fe.tui.SetFocus(tree)
	tree.Update()
	v.Update()
	return true
}

func (v *SpanListView) move(delta int) bool {
	if !v.sync() || len(v.visible) == 0 {
		return false
	}
	i := slices.Index(v.visible, v.fe.FocusedSpan)
	if i < 0 {
		if delta > 0 {
			return v.FocusFirst()
		}
		return v.FocusLast()
	}
	i += delta
	if i < 0 || i >= len(v.visible) {
		return false
	}
	return v.focus(v.visible[i])
}

// HandleKeyPress provides list-local navigation. Returning false at either
// edge lets the command screen move focus into an adjacent span list.
func (v *SpanListView) HandleKeyPress(_ tuist.Context, ev uv.KeyPressEvent) bool {
	switch uv.Key(ev).String() {
	case "down", "j":
		return v.move(1)
	case "up", "k":
		return v.move(-1)
	case "home":
		return v.FocusFirst()
	case "end", "G":
		return v.FocusLast()
	case "right", "l", "enter":
		if !v.Focused() {
			return v.FocusFirst()
		}
		v.fe.setExpanded(v.fe.FocusedSpan, true)
		return true
	case "left", "h":
		if !v.Focused() {
			return false
		}
		row := v.scope.rows.BySpan[v.fe.FocusedSpan]
		if row != nil && row.Expanded {
			v.fe.setExpanded(v.fe.FocusedSpan, false)
			return true
		}
	}
	return false
}
