package dagui

import (
	"sort"
)

// MessageNode is a surfaced LLM conversation message span, with any nested
// child messages beneath it (a tool call's sub-agent turns roll up under the
// tool-call node).
type MessageNode struct {
	Span     *Span // the message span (prompt, response, thinking, or tool call)
	Children []*MessageNode
}

// SurfacedConversation returns the whole trace's LLM conversation as a tree:
// every message span in the DB that no Boundary or Encapsulate contains.
//
// "The whole trace" is deliberately not "the root span's subtree". A resuming
// client holds TWO traces in one DB (hack/designs/resume-from-trace.md §5.1) —
// the live session's, rooted at db.RootSpan, and an imported one hanging off a
// second parentless span — and the restored conversation has to surface
// whichever root it hangs off. Resolving nil to db.RootSpan filed every
// imported message as contained and dropped it, which is also how this
// disagreed with HasConversationForSpan(nil): that one has always meant the
// whole DB (see underSurfaceRoot). Scoped surfacing passes an explicit root
// and is untouched, keeping the fixture containment it exists for.
func (db *DB) SurfacedConversation() []*MessageNode {
	return db.SurfacedConversationForSpan(nil)
}

// SurfacedConversationForSpan returns the LLM conversation beneath root as a
// tree, independent of the `reveal` mechanism -- the message analog of
// DB.SurfacedChecksForSpan. A nil root means every message span in the DB, not
// the trace root's subtree (see SurfacedConversation).
//
// Internal messages (the system prompt) are skipped, matching the live tree.
//
// A span with an LLMRole is surfaced only if its ancestor chain reaches root
// with no Boundary or Encapsulate span in between. That drops LLM runs a test
// intentionally drives as a fixture (wrapped in a boundary), the same
// containment the reveal bubbling applies, and -- since the walk stops AT root
// -- it also drops the transcript of the run that merely *contains* root when
// the caller is zoomed into a subtree. A chain that is severed before reaching
// an EXPLICIT root (an unreceived placeholder, or a reparenting seam the
// incremental fetch never loaded) can't be proven to be inside it, so it's
// treated as contained too -- exactly as for checks. With no root there is
// nothing for a chain to fail to reach: what remains is the containment
// question, and a severed chain that passed no boundary is uncontained.
//
// Unlike checks there is no dedup: each message span is its own node, nested
// under the nearest surfaced ancestor message (so a sub-agent's turns roll up
// beneath the tool call that spawned them). Roots and children are ordered by
// start time, because a conversation is a sequence, not a failed-first set.
//
// The result is cached per DB mutation and per root, like SurfacedChecks;
// callers must treat the returned nodes as read-only.
func (db *DB) SurfacedConversationForSpan(root *Span) []*MessageNode {
	// Deliberately NOT db.surfaceRoot(root): that resolves nil to the live
	// trace root, which is right for checks/generators/services and wrong here
	// (see SurfacedConversation).
	key := surfaceRootID(root)
	if db.surfacedConversationInit && db.surfacedConversationAt == db.mutations && db.surfacedConversationRoot == key {
		return db.surfacedConversation
	}
	db.surfacedConversation = db.buildSurfacedConversation(root)
	db.surfacedConversationAt = db.mutations
	db.surfacedConversationRoot = key
	db.surfacedConversationInit = true
	return db.surfacedConversation
}

func (db *DB) buildSurfacedConversation(root *Span) []*MessageNode {
	type info struct {
		span     *Span
		parentID SpanID
	}
	byID := map[SpanID]*info{}
	for span := range db.Spans.Iter() {
		if span.LLMRole == "" || span.Internal {
			continue
		}
		if span == root {
			// The root is the FRAME of the question ("what was said beneath
			// this span"), not content within it. A scoped tool result zooms
			// to the tool-call display span, which is itself a message span --
			// surfacing it would render the tool call's own row in place of
			// the subtree the report is about.
			continue
		}

		anchoredToMessageBoundary := false
		var parentID SpanID
		mayRollUp := spanMayRollUp(span, root, func(parent *Span) {
			if parent == root {
				return
			}
			if !parentID.IsValid() && parent.LLMRole != "" {
				parentID = parent.ID
			}
			if (parent.Boundary || parent.Encapsulate) && parent.LLMRole != "" {
				anchoredToMessageBoundary = true
			}
		})
		if !mayRollUp && !anchoredToMessageBoundary {
			continue
		}
		byID[span.ID] = &info{span: span, parentID: parentID}
	}

	nodes := make(map[SpanID]*MessageNode, len(byID))
	for id, in := range byID {
		nodes[id] = &MessageNode{Span: in.span}
	}
	var roots []*MessageNode
	for id, in := range byID {
		node := nodes[id]
		if in.parentID.IsValid() {
			if parent, ok := nodes[in.parentID]; ok {
				parent.Children = append(parent.Children, node)
			}
			// A message anchored to a contained/missing tool call must not escape
			// that boundary as a new conversation root.
			continue
		}
		roots = append(roots, node)
	}

	var sortNodes func(ns []*MessageNode)
	sortNodes = func(ns []*MessageNode) {
		sort.SliceStable(ns, func(i, j int) bool {
			return ns[i].Span.Before(ns[j].Span)
		})
		for _, n := range ns {
			sortNodes(n.Children)
		}
	}
	sortNodes(roots)
	return roots
}

// HasConversation reports whether the surfaced conversation is non-empty.
func (db *DB) HasConversation() bool {
	return db.HasConversationForSpan(nil)
}

// HasConversationForSpan reports whether the root-relative surfaced
// conversation is non-empty, including message-boundary anchoring.
func (db *DB) HasConversationForSpan(root *Span) bool {
	return len(db.SurfacedConversationForSpan(root)) > 0
}

// PromoteConversationTo wires the surfaced conversation into host.RevealedSpans
// (and each nested message into its parent message's RevealedSpans) so the live
// tree can surface the transcript at the top level without relying on the
// deprecated `reveal` attribute. It mirrors what reveal bubbling used to
// populate, but derives it from the reveal-independent SurfacedConversation tree
// -- so a top-level turn lands under the host and a sub-agent's turns nest under
// the tool call that spawned them. Idempotent: re-adds are no-ops on the set.
//
// It is the live-tree analog of conversationReport, which renders the same tree
// for the final report. Callers mark host Passthrough (see
// promoteConversationLocked) so RowsView iterates these revealed spans instead
// of host's raw children.
func (db *DB) PromoteConversationTo(host *Span) {
	db.PromoteConversationNodesTo(host, db.SurfacedConversation())
}

// PromoteConversationNodesTo is PromoteConversationTo for an explicit subset of
// the surfaced conversation -- the roster's focused-agent view, which promotes
// one agent's turns rather than every turn in the trace.
func (db *DB) PromoteConversationNodesTo(host *Span, nodes []*MessageNode) {
	if host == nil {
		return
	}
	var wire func(parent *Span, nodes []*MessageNode)
	wire = func(parent *Span, nodes []*MessageNode) {
		for _, node := range nodes {
			parent.RevealedSpans.Add(node.Span)
			wire(node.Span, node.Children)
		}
	}
	wire(host, nodes)
}

// DemoteConversationNodesFrom withdraws a promotion, removing each node's span
// from the RevealedSpans of whatever it was wired under.
//
// Promotion is an ADD into a set that outlives the render -- it mutates the
// cached, reused DB's spans, which recalculateViewLocked already warns about --
// so it is idempotent for a fixed scope but cannot express a CHANGE of scope.
// Focusing another agent therefore has to withdraw the previous scope
// explicitly; without this the host accumulates every transcript it was ever
// pointed at and the switcher only ever adds.
func (db *DB) DemoteConversationNodesFrom(host *Span, nodes []*MessageNode) {
	if host == nil {
		return
	}
	var unwire func(parent *Span, nodes []*MessageNode)
	unwire = func(parent *Span, nodes []*MessageNode) {
		for _, node := range nodes {
			parent.RevealedSpans.Remove(node.Span)
			unwire(node.Span, node.Children)
		}
	}
	unwire(host, nodes)
}
