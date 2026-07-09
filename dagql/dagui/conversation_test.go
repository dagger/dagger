package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
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
// Boundary, or on a severed chain that never reaches the root, stay hidden --
// the same containment SurfacedChecks applies to fixture checks.
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
		// parent never imported -> severed chain, can't be proven boundary-free.
		messageSnapshot(severedID, "severed", spanID(missingParentID), "user"),
	})

	got := surfacedMessageNames(db.SurfacedConversation())
	if !got["real"] {
		t.Errorf("expected \"real\" to surface (surfaced: %v)", got)
	}
	for _, hidden := range []string{"contained", "severed"} {
		if got[hidden] {
			t.Errorf("expected %q to stay hidden, but it surfaced (surfaced: %v)", hidden, got)
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
