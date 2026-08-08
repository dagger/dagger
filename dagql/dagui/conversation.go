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

// SurfacedConversation returns the whole trace's LLM conversation as a tree.
// It is SurfacedConversationForSpan relative to the trace root.
func (db *DB) SurfacedConversation() []*MessageNode {
	return db.SurfacedConversationForSpan(nil)
}

// SurfacedConversationForSpan returns the LLM conversation beneath root as a
// tree, independent of the `reveal` mechanism -- the message analog of
// DB.SurfacedChecksForSpan. A nil root means the trace root.
//
// Internal messages (the system prompt) are skipped, matching the live tree.
//
// A span with an LLMRole is surfaced only if its ancestor chain reaches root
// with no Boundary or Encapsulate span in between. That drops LLM runs a test
// intentionally drives as a fixture (wrapped in a boundary), the same
// containment the reveal bubbling applies, and -- since the walk stops AT root
// -- it also drops the transcript of the run that merely *contains* root when
// the caller is zoomed into a subtree. A chain that is severed before reaching
// root (an unreceived placeholder, or a reparenting seam the incremental fetch
// never loaded) can't be proven boundary-free, so it's treated as contained
// too -- exactly as for checks.
//
// Unlike checks there is no dedup: each message span is its own node, nested
// under the nearest surfaced ancestor message (so a sub-agent's turns roll up
// beneath the tool call that spawned them). Roots and children are ordered by
// start time, because a conversation is a sequence, not a failed-first set.
//
// The result is cached per DB mutation and per root, like SurfacedChecks;
// callers must treat the returned nodes as read-only.
func (db *DB) SurfacedConversationForSpan(root *Span) []*MessageNode {
	r := db.surfaceRoot(root)
	key := surfaceRootID(r)
	if db.surfacedConversationInit && db.surfacedConversationAt == db.mutations && db.surfacedConversationRoot == key {
		return db.surfacedConversation
	}
	db.surfacedConversation = db.buildSurfacedConversation(r)
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

		contained := false
		anchoredToMessage := false
		var parentID SpanID
		reachedRoot := false
		for p := span.ParentSpan; p != nil; p = p.ParentSpan {
			atRoot := p == root
			if !atRoot && (p.Boundary || p.Encapsulate) {
				// Tool-call messages are boundaries so nested work remains anchored
				// beneath the call, but the call itself remains in the conversation.
				if p.LLMRole != "" {
					parentID = p.ID
					anchoredToMessage = true
					break
				}
				contained = true
				break
			}
			if !atRoot && !parentID.IsValid() && p.LLMRole != "" {
				// Anchor only to messages strictly BELOW root: root itself is
				// the frame and isn't part of the surfaced tree, so a message
				// anchored to it would have no node to nest under.
				parentID = p.ID
			}
			if atRoot {
				// Stop at root: its own flags are outside the question, but it
				// still anchors the messages beneath it (see
				// SurfacedChecksForSpan).
				reachedRoot = true
				break
			}
		}
		if !contained && !anchoredToMessage && root != nil && !reachedRoot {
			contained = true
		}
		if contained {
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

// SurfacedConversationForAgent returns ONE agent's conversation: the messages
// beneath each of the loop spans the engine published for it, merged in
// start-time order.
//
// Scoping by AGENT rather than by span is what makes it whole. A resume after
// a failure relaunches the loop under a fresh span
// (hack/designs/async-agents.md §9), which is why AgentNode keys on the
// spawn-minted instance ID and keeps a list; a caller that scoped to
// AgentNode.Span() alone would silently drop everything said before the last
// relaunch.
//
// Nil, or an agent with no loop spans, means no conversation -- not the whole
// trace. The distinction matters: callers use the empty result to decide the
// scope is not worth applying, and collapsing it to the whole trace here would
// take that choice away from them.
//
// Cached per DB mutation and per agent, in a memo slot of its own. It
// deliberately does NOT share the whole-trace slot: that one is single-entry
// and keyed by root, so the roster's per-agent view and the report's
// zoom-scoped view would evict each other on every render.
func (db *DB) SurfacedConversationForAgent(node *AgentNode) []*MessageNode {
	if node == nil || len(node.Spans) == 0 {
		return nil
	}
	if db.agentConversationInit && db.agentConversationAt == db.mutations && db.agentConversationID == node.ID {
		return db.agentConversation
	}
	var roots []*MessageNode
	for _, span := range node.Spans {
		// buildSurfacedConversation directly, not SurfacedConversationForSpan:
		// the latter would write each loop span through the shared single-slot
		// memo, leaving it holding whichever span happened to be last.
		roots = append(roots, db.buildSurfacedConversation(span)...)
	}
	sort.SliceStable(roots, func(i, j int) bool {
		return roots[i].Span.Before(roots[j].Span)
	})
	db.agentConversation = roots
	db.agentConversationAt = db.mutations
	db.agentConversationID = node.ID
	db.agentConversationInit = true
	return db.agentConversation
}

// HasConversation reports whether the trace contains any LLM message spans, so
// the live view can promote the conversation to the top level (mirrors
// HasChecks).
func (db *DB) HasConversation() bool {
	return db.HasConversationForSpan(nil)
}

// HasConversationForSpan is HasConversation restricted to root's subtree; a nil
// root means the whole trace.
func (db *DB) HasConversationForSpan(root *Span) bool {
	for _, span := range db.Spans.Order {
		if span.LLMRole != "" && underSurfaceRoot(span, root) {
			return true
		}
	}
	return false
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
