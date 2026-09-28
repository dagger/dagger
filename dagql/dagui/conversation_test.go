package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// messageSnapshot builds an LLM message span snapshot. StartTime is derived from
// id so that importing in id order matches conversation (start-time) order.
func messageSnapshot(id byte, name string, parent SpanID, llmRole string) SpanSnapshot {
	start := time.Unix(int64(id), 0)
	return SpanSnapshot{
		ID:        SpanID{SpanID: trace.SpanID{id}},
		TraceID:   TraceID{TraceID: trace.TraceID{1}},
		Name:      name,
		StartTime: start,
		EndTime:   start.Add(time.Second),
		ParentID:  parent,
		LLMRole:   llmRole,
		Status:    sdktrace.Status{},
	}
}

func spanID(id byte) SpanID {
	return SpanID{SpanID: trace.SpanID{id}}
}

func surfacedMessageNames(roots []*MessageNode) map[string]bool {
	names := map[string]bool{}
	var walk func(ns []*MessageNode)
	walk = func(ns []*MessageNode) {
		for _, n := range ns {
			names[n.Span.Name] = true
			walk(n.Children)
		}
	}
	walk(roots)
	return names
}

// TestSurfacedConversationOrdersByStartTime asserts roots render in
// conversation order (start time), not failed-first like checks.
func TestSurfacedConversationOrdersByStartTime(t *testing.T) {
	const (
		rootID byte = iota + 1
		firstID
		secondID
		thirdID
	)
	db := NewDB()
	// Import out of chronological order to prove the sort, not the import order.
	db.ImportSnapshots([]SpanSnapshot{
		messageSnapshot(rootID, "root", SpanID{}, ""),
		messageSnapshot(thirdID, "third", spanID(rootID), "user"),
		messageSnapshot(firstID, "first", spanID(rootID), "user"),
		messageSnapshot(secondID, "second", spanID(rootID), "assistant"),
	})

	roots := db.SurfacedConversation()
	got := make([]string, len(roots))
	for i, n := range roots {
		got[i] = n.Span.Name
	}
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("roots = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("roots = %v, want %v", got, want)
		}
	}
}

// TestSurfacedConversationNestsSubAgentUnderToolCall asserts a sub-agent's turns
// (message spans nested under a tool-call span) roll up beneath the tool call.
func TestSurfacedConversationNestsSubAgentUnderToolCall(t *testing.T) {
	const (
		rootID byte = iota + 1
		promptID
		toolCallID
		subPromptID
		subResponseID
	)
	db := NewDB()
	toolCall := messageSnapshot(toolCallID, "spawn", spanID(rootID), "assistant")
	toolCall.LLMTool = "spawn"
	db.ImportSnapshots([]SpanSnapshot{
		messageSnapshot(rootID, "root", SpanID{}, ""),
		messageSnapshot(promptID, "prompt", spanID(rootID), "user"),
		toolCall,
		messageSnapshot(subPromptID, "sub-prompt", spanID(toolCallID), "user"),
		messageSnapshot(subResponseID, "sub-response", spanID(toolCallID), "assistant"),
	})

	roots := db.SurfacedConversation()
	if len(roots) != 2 || roots[0].Span.Name != "prompt" || roots[1].Span.Name != "spawn" {
		t.Fatalf("roots = %v, want [prompt spawn]", roots)
	}
	spawn := roots[1]
	if len(spawn.Children) != 2 ||
		spawn.Children[0].Span.Name != "sub-prompt" ||
		spawn.Children[1].Span.Name != "sub-response" {
		t.Fatalf("spawn children = %v, want [sub-prompt sub-response]", spawn.Children)
	}
}

// TestSurfacedConversationHidesContainedMessages asserts messages behind a
// Boundary stay hidden -- the same containment SurfacedChecks applies to
// fixture checks -- and pins what "contained" means for a chain that never
// reaches the root, which the two questions answer differently.
//
// Relative to an EXPLICIT root, a severed chain cannot be shown to be inside
// it, so it is contained. For the WHOLE DB there is no root to reach: a
// resuming client holds a second trace hanging off a second parentless span
// (hack/designs/resume-from-trace.md §5.1.3), and every message in it is
// severed from db.RootSpan by construction. Requiring the live root there
// dropped the entire restored transcript, so what remains is the containment
// question alone -- which is what HasConversationForSpan(nil) has always
// asked.
func TestSurfacedConversationHidesContainedMessages(t *testing.T) {
	const (
		rootID byte = iota + 1
		realID
		boundaryID
		containedID
		severedID
	)
	const missingParentID byte = 99

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		messageSnapshot(rootID, "root", SpanID{}, ""),
		messageSnapshot(realID, "real", spanID(rootID), "user"),
		boundarySnapshot(boundaryID, rootID),
		messageSnapshot(containedID, "contained", spanID(boundaryID), "user"),
		// parent never imported -> severed chain, unreachable from the root.
		messageSnapshot(severedID, "severed", spanID(missingParentID), "user"),
	})

	got := surfacedMessageNames(db.SurfacedConversation())
	if !got["real"] {
		t.Errorf("expected \"real\" to surface (surfaced: %v)", got)
	}
	if got["contained"] {
		t.Errorf("expected \"contained\" to stay hidden, but it surfaced (surfaced: %v)", got)
	}
	if !got["severed"] {
		t.Errorf("expected \"severed\" to surface for the whole-DB question (surfaced: %v)", got)
	}
	// The whole-DB answer must agree with the whole-DB predicate; they
	// disagreeing is the defect §5.1.3 names.
	if !db.HasConversation() {
		t.Error("HasConversation and SurfacedConversation disagree about the same trace")
	}

	// Scoped to the root, both stay hidden: one behind its boundary, the other
	// because nothing shows it is beneath the root at all.
	root := db.Spans.Map[spanID(rootID)]
	if root == nil {
		t.Fatal("root span not loaded")
	}
	scoped := surfacedMessageNames(db.SurfacedConversationForSpan(root))
	if !scoped["real"] {
		t.Errorf("expected \"real\" to surface when scoped (surfaced: %v)", scoped)
	}
	for _, hidden := range []string{"contained", "severed"} {
		if scoped[hidden] {
			t.Errorf("expected %q to stay hidden when scoped, but it surfaced (surfaced: %v)", hidden, scoped)
		}
	}
}

// TestSurfacedConversationNoDedup asserts two same-named message spans surface as
// two distinct nodes (a conversation is a sequence, not a deduped set).
func TestSurfacedConversationNoDedup(t *testing.T) {
	const (
		rootID byte = iota + 1
		firstID
		secondID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		messageSnapshot(rootID, "root", SpanID{}, ""),
		messageSnapshot(firstID, "LLM response", spanID(rootID), "assistant"),
		messageSnapshot(secondID, "LLM response", spanID(rootID), "assistant"),
	})
	roots := db.SurfacedConversation()
	if len(roots) != 2 {
		t.Fatalf("expected 2 distinct nodes, got %d", len(roots))
	}
}

// TestSurfacedConversationSkipsInternalMessages asserts the system prompt
// (marked Internal) is not surfaced, matching the live tree.
func TestSurfacedConversationSkipsInternalMessages(t *testing.T) {
	const (
		rootID byte = iota + 1
		systemID
		userID
	)
	db := NewDB()
	system := messageSnapshot(systemID, "system prompt", spanID(rootID), "system")
	system.Internal = true
	db.ImportSnapshots([]SpanSnapshot{
		messageSnapshot(rootID, "root", SpanID{}, ""),
		system,
		messageSnapshot(userID, "LLM prompt", spanID(rootID), "user"),
	})

	got := surfacedMessageNames(db.SurfacedConversation())
	if got["system prompt"] {
		t.Errorf("expected the internal system prompt to be skipped (surfaced: %v)", got)
	}
	if !got["LLM prompt"] {
		t.Errorf("expected the user prompt to surface (surfaced: %v)", got)
	}
}

func TestSurfacedConversationMemoizedPerFrame(t *testing.T) {
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		{
			ID:        testID(1),
			TraceID:   TraceID{TraceID: trace.TraceID{1}},
			Name:      "root",
			StartTime: time.Unix(1, 0),
			EndTime:   time.Unix(5, 0),
			Status:    sdktrace.Status{Code: codes.Ok},
		},
		{
			ID:        testID(2),
			TraceID:   TraceID{TraceID: trace.TraceID{1}},
			Name:      "LLM prompt",
			LLMRole:   "user",
			ParentID:  testID(1),
			StartTime: time.Unix(2, 0),
			EndTime:   time.Unix(3, 0),
			Status:    sdktrace.Status{Code: codes.Ok},
		},
	})

	first := db.SurfacedConversation()
	if len(first) != 1 || first[0].Span.Name != "LLM prompt" {
		t.Fatalf("expected the prompt message, got %+v", first)
	}
	if again := db.SurfacedConversation(); &again[0] != &first[0] {
		t.Fatal("repeated same-frame reads must hit the cache")
	}

	// New span data (a second message) must invalidate the cache.
	db.ImportSnapshots([]SpanSnapshot{{
		ID:        testID(3),
		TraceID:   TraceID{TraceID: trace.TraceID{1}},
		Name:      "LLM response",
		LLMRole:   "assistant",
		ParentID:  testID(1),
		StartTime: time.Unix(3, 0),
		EndTime:   time.Unix(4, 0),
		Status:    sdktrace.Status{Code: codes.Ok},
	}})
	fresh := db.SurfacedConversation()
	if len(fresh) != 2 {
		t.Fatalf("cache must be invalidated by new span data, got %d messages", len(fresh))
	}
}

// TestPromoteConversationToWiresRevealedSpans asserts PromoteConversationTo
// populates the host's RevealedSpans with the top-level turns and nests a
// sub-agent's turns under the tool-call span that spawned them -- what the live
// tree consumes now that LLM messages no longer set `reveal`.
func TestPromoteConversationToWiresRevealedSpans(t *testing.T) {
	const (
		rootID byte = iota + 1
		promptID
		toolCallID
		subPromptID
	)
	db := NewDB()
	toolCall := messageSnapshot(toolCallID, "spawn", spanID(rootID), "assistant")
	toolCall.LLMTool = "spawn"
	db.ImportSnapshots([]SpanSnapshot{
		messageSnapshot(rootID, "root", SpanID{}, ""),
		messageSnapshot(promptID, "prompt", spanID(rootID), "user"),
		toolCall,
		messageSnapshot(subPromptID, "sub-prompt", spanID(toolCallID), "user"),
	})

	root := db.Spans.Map[spanID(rootID)]
	db.PromoteConversationTo(root)

	// The host surfaces both top-level turns.
	var topNames []string
	for _, s := range root.RevealedSpans.Order {
		topNames = append(topNames, s.Name)
	}
	if len(topNames) != 2 || topNames[0] != "prompt" || topNames[1] != "spawn" {
		t.Fatalf("host RevealedSpans = %v, want [prompt spawn]", topNames)
	}

	// The sub-agent turn nests under the tool call, not the host.
	toolSpan := db.Spans.Map[spanID(toolCallID)]
	if len(toolSpan.RevealedSpans.Order) != 1 || toolSpan.RevealedSpans.Order[0].Name != "sub-prompt" {
		t.Fatalf("tool call RevealedSpans = %v, want [sub-prompt]", toolSpan.RevealedSpans.Order)
	}

	// Idempotent: a second promotion (e.g. a later render frame) doesn't
	// duplicate entries.
	db.PromoteConversationTo(root)
	if len(root.RevealedSpans.Order) != 2 {
		t.Fatalf("host RevealedSpans after re-promote = %d, want 2", len(root.RevealedSpans.Order))
	}
}

// llmCall registers a call payload for one link of an LLM recipe chain.
func llmCall(db *DB, digest, field, receiver string) {
	db.Calls[digest] = &callpbv1.Call{
		Digest:         digest,
		Field:          field,
		Type:           &callpbv1.Type{NamedType: "LLM"},
		ReceiverDigest: receiver,
	}
}

// TestRewindsAbandonMessagesBetweenDigests covers the client half of the
// rewind marker: the messages whose LLM call lies strictly between the
// adopted and abandoned recipes are superseded, a tool call's execution
// subtree goes with it, and the resumed conversation after the marker — even
// one that reuses the abandoned prompt's digest by resubmitting the same
// text — is untouched. Messages of another agent that happen to share the
// chain are not this rewind's to abandon.
func TestRewindsAbandonMessagesBetweenDigests(t *testing.T) {
	const (
		loopID byte = iota + 1
		keptPromptID
		keptReplyID
		oldPromptID
		oldToolID
		oldExecID
		oldReplyID
		markerID
		newPromptID
		newReplyID
		otherLoopID
		otherReplyID
	)
	// llm -> withPrompt(kept) -> withResponse -> withPrompt(old) ->
	// withResponse -> withToolResult -> withResponse(tip)
	db := NewDB()
	llmCall(db, "xxh3:llm", "llm", "")
	llmCall(db, "xxh3:kept", "withPrompt", "xxh3:llm")
	llmCall(db, "xxh3:kept-reply", "withResponse", "xxh3:kept")
	llmCall(db, "xxh3:old", "withPrompt", "xxh3:kept-reply")
	llmCall(db, "xxh3:old-tool", "withResponse", "xxh3:old")
	llmCall(db, "xxh3:old-result", "withToolResult", "xxh3:old-tool")
	llmCall(db, "xxh3:tip", "withResponse", "xxh3:old-result")

	message := func(id byte, name string, parent SpanID, role, digest string) SpanSnapshot {
		snap := messageSnapshot(id, name, parent, role)
		snap.LLMCallDigest = digest
		return snap
	}
	loop := messageSnapshot(loopID, "agent loop", SpanID{}, "")
	loop.Agent, loop.AgentID = true, "chief"
	otherLoop := messageSnapshot(otherLoopID, "other loop", SpanID{}, "")
	otherLoop.Agent, otherLoop.AgentID = true, "worker"
	oldTool := message(oldToolID, "Bash", spanID(loopID), "assistant", "xxh3:old")
	oldTool.LLMTool = "Bash"
	oldExec := messageSnapshot(oldExecID, "exec", spanID(oldToolID), "")
	oldExec.LLMTool = "Bash"
	marker := messageSnapshot(markerID, "conversation rewound", spanID(loopID), "user")
	marker.AgentRewindFrom, marker.AgentRewindTo = "xxh3:tip", "xxh3:kept-reply"
	db.ImportSnapshots([]SpanSnapshot{
		loop,
		message(keptPromptID, "kept prompt", spanID(loopID), "user", "xxh3:kept"),
		message(keptReplyID, "kept reply", spanID(loopID), "assistant", "xxh3:kept"),
		message(oldPromptID, "old prompt", spanID(loopID), "user", "xxh3:old"),
		oldTool,
		oldExec,
		message(oldReplyID, "old reply", spanID(loopID), "assistant", "xxh3:old-result"),
		marker,
		// The user resubmitted the prompt verbatim: same digest, after the
		// marker.
		message(newPromptID, "new prompt", spanID(loopID), "user", "xxh3:old"),
		message(newReplyID, "new reply", spanID(loopID), "assistant", "xxh3:old"),
		otherLoop,
		message(otherReplyID, "other reply", spanID(otherLoopID), "assistant", "xxh3:old"),
	})

	rewinds := db.Rewinds()
	if len(rewinds) != 1 || rewinds[0].Span.ID != spanID(markerID) {
		t.Fatalf("Rewinds() = %+v, want the one marker", rewinds)
	}
	var abandoned []string
	for _, span := range rewinds[0].Abandoned {
		abandoned = append(abandoned, span.Name)
	}
	want := []string{"old prompt", "Bash", "old reply"}
	if len(abandoned) != len(want) {
		t.Fatalf("Abandoned = %v, want %v", abandoned, want)
	}
	for i := range want {
		if abandoned[i] != want[i] {
			t.Fatalf("Abandoned = %v, want %v", abandoned, want)
		}
	}

	superseded := func(id byte) bool {
		return db.SupersededBy(db.Spans.Map[spanID(id)]) == rewinds[0]
	}
	for _, id := range []byte{oldPromptID, oldToolID, oldReplyID, oldExecID} {
		if !superseded(id) {
			t.Errorf("span %d should be superseded by the rewind", id)
		}
	}
	for _, id := range []byte{loopID, keptPromptID, keptReplyID, markerID, newPromptID, newReplyID, otherLoopID, otherReplyID} {
		if superseded(id) {
			t.Errorf("span %d must remain part of the conversation", id)
		}
	}

	// Repeated reads hit the memo until the DB changes.
	if again := db.Rewinds(); &again[0] != &rewinds[0] {
		t.Fatal("repeated same-frame reads must hit the cache")
	}
}

// TestRewindsWithoutPayloadsMarkNothing asserts the honest degradation: a
// marker whose chain this client cannot walk still surfaces as a rewind, but
// abandons no messages rather than guessing.
func TestRewindsWithoutPayloadsMarkNothing(t *testing.T) {
	const (
		loopID byte = iota + 1
		promptID
		markerID
	)
	db := NewDB()
	loop := messageSnapshot(loopID, "agent loop", SpanID{}, "")
	loop.Agent, loop.AgentID = true, "chief"
	prompt := messageSnapshot(promptID, "prompt", spanID(loopID), "user")
	prompt.LLMCallDigest = "xxh3:tip"
	marker := messageSnapshot(markerID, "conversation rewound", spanID(loopID), "user")
	marker.AgentRewindFrom, marker.AgentRewindTo = "xxh3:tip", "xxh3:base"
	db.ImportSnapshots([]SpanSnapshot{loop, prompt, marker})

	rewinds := db.Rewinds()
	if len(rewinds) != 1 || len(rewinds[0].Abandoned) != 0 {
		t.Fatalf("Rewinds() = %+v, want one marker abandoning nothing", rewinds)
	}
	if db.SupersededBy(db.Spans.Map[spanID(promptID)]) != nil {
		t.Fatal("an unwalkable chain must not supersede on a guess")
	}

	// The payloads arriving later (logs and spans are batched independently)
	// complete the walk.
	llmCall(db, "xxh3:base", "llm", "")
	llmCall(db, "xxh3:tip", "withPrompt", "xxh3:base")
	db.mutations++
	if got := db.SupersededBy(db.Spans.Map[spanID(promptID)]); got == nil {
		t.Fatal("payload arrival must invalidate the memo and abandon the prompt")
	}
}
