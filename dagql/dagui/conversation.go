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

// SurfacedConversation returns the trace's LLM conversation as a tree,
// independent of the `reveal` mechanism -- the message analog of
// DB.SurfacedChecks.
//
// Internal messages (the system prompt) are skipped, matching the live tree.
//
// A span with an LLMRole is surfaced only if its ancestor chain reaches the
// trace root with no Boundary or Encapsulate span in between. That drops LLM
// runs a test intentionally drives as a fixture (wrapped in a boundary), the
// same containment the reveal bubbling applies. A chain that is severed before
// reaching the root (an unreceived placeholder, or a reparenting seam the
// incremental fetch never loaded) can't be proven boundary-free, so it's
// treated as contained too -- exactly as for checks.
//
// Unlike checks there is no dedup: each message span is its own node, nested
// under the nearest surfaced ancestor message (so a sub-agent's turns roll up
// beneath the tool call that spawned them). Roots and children are ordered by
// start time, because a conversation is a sequence, not a failed-first set.
//
// The result is cached per DB mutation, like SurfacedChecks; callers must treat
// the returned nodes as read-only.
func (db *DB) SurfacedConversation() []*MessageNode {
	if db.surfacedConversationInit && db.surfacedConversationAt == db.mutations {
		return db.surfacedConversation
	}
	db.surfacedConversation = db.buildSurfacedConversation()
	db.surfacedConversationAt = db.mutations
	db.surfacedConversationInit = true
	return db.surfacedConversation
}

func (db *DB) buildSurfacedConversation() []*MessageNode {
	type info struct {
		span     *Span
		parentID SpanID
	}
	byID := map[SpanID]*info{}
	for span := range db.Spans.Iter() {
		if span.LLMRole == "" {
			continue
		}
		// Skip internal messages (the system prompt), matching the live tree, which
		// hides them. The transcript should read as the actual conversation --
		// prompts, responses, tool calls -- not the system scaffolding.
		if span.Internal {
			continue
		}
		// Walk ancestors toward the root: a Boundary/Encapsulate between this
		// message and the root contains it (hide it); otherwise remember the
		// nearest ancestor message to nest under, and note whether we reach the
		// root at all.
		contained := false
		var parentID SpanID
		reachedRoot := span == db.RootSpan
		for p := span.ParentSpan; p != nil; p = p.ParentSpan {
			if p.Boundary || p.Encapsulate {
				contained = true
				break
			}
			if !parentID.IsValid() && p.LLMRole != "" {
				parentID = p.ID
			}
			if p == db.RootSpan {
				reachedRoot = true
				break
			}
		}
		// A severed chain (unreceived placeholder, or a reparenting seam below a
		// test's Boundary that the incremental fetch never loaded) can't be
		// proven boundary-free, so treat it as contained -- fixtures stay hidden
		// just like messages under a loaded Boundary ancestor.
		if !contained && db.RootSpan != nil && !reachedRoot {
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
		if parent, ok := nodes[in.parentID]; ok && in.parentID.IsValid() {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
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

// HasConversation reports whether the trace contains any LLM message spans, so
// the live view can promote the conversation to the top level (mirrors
// HasChecks).
func (db *DB) HasConversation() bool {
	for _, span := range db.Spans.Order {
		if span.LLMRole != "" {
			return true
		}
	}
	return false
}
