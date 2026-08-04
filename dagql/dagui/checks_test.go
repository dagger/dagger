package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func checkSnapshot(id byte, name string, parent SpanID, checkName string) SpanSnapshot {
	start := time.Unix(int64(id), 0)
	return SpanSnapshot{
		ID:        SpanID{SpanID: trace.SpanID{id}},
		TraceID:   TraceID{TraceID: trace.TraceID{1}},
		Name:      name,
		StartTime: start,
		EndTime:   start.Add(time.Second),
		ParentID:  parent,
		CheckName: checkName,
		Status:    sdktrace.Status{},
	}
}

func surfacedNames(roots []*CheckNode) map[string]bool {
	names := map[string]bool{}
	var walk func(ns []*CheckNode)
	walk = func(ns []*CheckNode) {
		for _, n := range ns {
			names[n.Name] = true
			walk(n.Children)
		}
	}
	walk(roots)
	return names
}

// TestSurfacedChecksHidesSeveredFixtureChecks covers the boundary-containment
// rule when the Boundary span itself isn't loaded. A check a test runs as a
// fixture reaches the outer trace through a nested `dagger check` invocation, so
// its ancestor chain dead-ends at the reparenting seam (the spawning withExec)
// or at an unreceived placeholder -- below the test's Boundary span, which the
// incremental fetch never pulls in. Such a severed chain can't be proven
// boundary-free, so the fixture check must stay hidden; a real trace-level check
// (and its legitimately-nested sub-checks) always reaches the root and surfaces.
func TestSurfacedChecksHidesSeveredFixtureChecks(t *testing.T) {
	const (
		rootID byte = iota + 1
		realCheckID
		realSubCheckID
		seamID
		fixtureSeamCheckID
		fixturePlaceholderCheckID
		boundaryID
		fixtureBoundaryCheckID
	)
	// id never imported -> stays an unreceived placeholder.
	const missingParentID byte = 99

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		// The trace root command (not itself a check), imported first so it wins
		// db.RootSpan.
		checkSnapshot(rootID, "test-base", SpanID{}, ""),
		// A real trace-level check and a sub-check nested under it -- both reach
		// the root, so both surface.
		checkSnapshot(realCheckID, "real:check", SpanID{SpanID: trace.SpanID{rootID}}, "real:check"),
		checkSnapshot(realSubCheckID, "real:subcheck", SpanID{SpanID: trace.SpanID{realCheckID}}, "real:subcheck"),
		// The reparenting seam: a received, parentless withExec that isn't the
		// trace root (it spawned a nested `dagger check`).
		checkSnapshot(seamID, "Container.withExec", SpanID{}, ""),
		// A fixture check whose chain dead-ends at that seam -- must stay hidden.
		checkSnapshot(fixtureSeamCheckID, "fixture-seam:check", SpanID{SpanID: trace.SpanID{seamID}}, "fixture-seam:check"),
		// A fixture check whose parent was never received (placeholder) -- hidden.
		checkSnapshot(fixturePlaceholderCheckID, "fixture-placeholder:check", SpanID{SpanID: trace.SpanID{missingParentID}}, "fixture-placeholder:check"),
		// The existing rule still applies: a fixture check under a *loaded*
		// Boundary span stays hidden.
		boundarySnapshot(boundaryID, rootID),
		checkSnapshot(fixtureBoundaryCheckID, "fixture-boundary:check", SpanID{SpanID: trace.SpanID{boundaryID}}, "fixture-boundary:check"),
	})

	got := surfacedNames(db.SurfacedChecks())
	want := map[string]bool{"real:check": true, "real:subcheck": true}
	for name := range want {
		if !got[name] {
			t.Errorf("expected %q to surface, but it didn't (surfaced: %v)", name, got)
		}
	}
	for _, hidden := range []string{
		"fixture-seam:check",
		"fixture-placeholder:check",
		"fixture-boundary:check",
	} {
		if got[hidden] {
			t.Errorf("expected %q to stay hidden, but it surfaced (surfaced: %v)", hidden, got)
		}
	}

	// real:subcheck nests under real:check, not at the top level.
	roots := db.SurfacedChecks()
	if len(roots) != 1 || roots[0].Name != "real:check" {
		t.Fatalf("roots = %v, want a single real:check root", roots)
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].Name != "real:subcheck" {
		t.Fatalf("real:check children = %v, want [real:subcheck]", roots[0].Children)
	}
}

func boundarySnapshot(id, parent byte) SpanSnapshot {
	snap := checkSnapshot(id, "boundary", SpanID{SpanID: trace.SpanID{parent}}, "")
	snap.Boundary = true
	return snap
}

func TestSurfacedChecksMemoizedPerFrame(t *testing.T) {
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
			Name:      "check lint",
			CheckName: "lint",
			ParentID:  testID(1),
			StartTime: time.Unix(2, 0),
			EndTime:   time.Unix(3, 0),
			Status:    sdktrace.Status{Code: codes.Ok},
		},
	})

	first := db.SurfacedChecks()
	if len(first) != 1 || first[0].Name != "lint" {
		t.Fatalf("expected the lint check, got %+v", first)
	}
	if again := db.SurfacedChecks(); &again[0] != &first[0] {
		t.Fatal("repeated same-frame reads must hit the cache")
	}

	// New span data (a second check) must invalidate the cache.
	db.ImportSnapshots([]SpanSnapshot{{
		ID:        testID(3),
		TraceID:   TraceID{TraceID: trace.TraceID{1}},
		Name:      "check unit",
		CheckName: "unit",
		ParentID:  testID(1),
		StartTime: time.Unix(3, 0),
		EndTime:   time.Unix(4, 0),
		Status:    sdktrace.Status{Code: codes.Error},
	}})
	fresh := db.SurfacedChecks()
	if len(fresh) != 2 {
		t.Fatalf("cache must be invalidated by new span data, got %d checks", len(fresh))
	}
	if !fresh[0].Failed || fresh[0].Name != "unit" {
		t.Fatalf("failed check must sort first, got %+v", fresh[0])
	}
}

// toolCallSnapshot mirrors the display span core creates for an LLM tool call
// (displayPhases.StartToolCall): a Boundary that also rolls up its logs and
// spans, and carries the tool name. MCP.Call adopts this span's context, so
// EVERY tool call's work -- including any check it runs -- nests beneath a
// Boundary.
//
// Replay-driven integration tests never create this span (toolCallCtx falls
// back to the shared ctx), which is exactly why the containment bug was
// invisible to them; a regression test has to inject it explicitly.
func toolCallSnapshot(id, parent byte, toolName string) SpanSnapshot {
	snap := checkSnapshot(id, toolName, SpanID{SpanID: trace.SpanID{parent}}, "")
	snap.Boundary = true
	snap.RollUpLogs = true
	snap.RollUpSpans = true
	snap.LLMTool = toolName
	snap.LLMRole = "assistant"
	return snap
}

// TestSurfacedChecksForSpan covers zoom-relative surfacing: a check run inside
// an LLM tool call is contained by the tool-call display span's Boundary, so it
// never surfaces for the trace as a whole -- but rolling up relative to that
// tool call is asking what ran INSIDE it, and must see it. Flags strictly below
// the given root keep containing, so a fixture check wrapped in its own nested
// boundary stays hidden either way, and checks outside the root drop out.
func TestSurfacedChecksForSpan(t *testing.T) {
	const (
		rootID byte = iota + 1
		toolID
		toolCheckID
		nestedBoundaryID
		fixtureCheckID
		outsideCheckID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		checkSnapshot(rootID, "shell", SpanID{}, ""),
		toolCallSnapshot(toolID, rootID, "check"),
		checkSnapshot(toolCheckID, "shellcheck:check", SpanID{SpanID: trace.SpanID{toolID}}, "shellcheck:check"),
		// A boundary strictly BELOW the root still contains what's under it.
		boundarySnapshot(nestedBoundaryID, toolID),
		checkSnapshot(fixtureCheckID, "fixture:check", SpanID{SpanID: trace.SpanID{nestedBoundaryID}}, "fixture:check"),
		// A plain trace-level check outside the tool call, for the unzoomed
		// baseline.
		checkSnapshot(outsideCheckID, "outside:check", SpanID{SpanID: trace.SpanID{rootID}}, "outside:check"),
	})

	// At the DB root (every unzoomed frontend): the tool call's Boundary hides
	// both checks beneath it, exactly as before.
	unscoped := surfacedNames(db.SurfacedChecks())
	if !unscoped["outside:check"] {
		t.Errorf("unzoomed surfacing lost the trace-level check: %v", unscoped)
	}
	for _, hidden := range []string{"shellcheck:check", "fixture:check"} {
		if unscoped[hidden] {
			t.Errorf("unzoomed surfacing must not surface %q (got %v)", hidden, unscoped)
		}
	}

	// Relative to the tool-call boundary: its own check surfaces...
	toolSpan := db.Spans.Map[SpanID{SpanID: trace.SpanID{toolID}}]
	if toolSpan == nil {
		t.Fatal("tool call span not loaded")
	}
	scoped := surfacedNames(db.SurfacedChecksForSpan(toolSpan))
	if !scoped["shellcheck:check"] {
		t.Errorf("zoomed surfacing dropped the tool call's own check: %v", scoped)
	}
	// ...while the nested boundary below it still contains its fixture...
	if scoped["fixture:check"] {
		t.Errorf("a boundary below the root must still contain: %v", scoped)
	}
	// ...and a check that ran outside the zoom doesn't leak in.
	if scoped["outside:check"] {
		t.Errorf("zoomed surfacing leaked a check from outside the root: %v", scoped)
	}

	// Going back to the DB root restores the unzoomed result byte for byte --
	// the memo keys on the root, not just on db.mutations.
	again := surfacedNames(db.SurfacedChecks())
	if len(again) != len(unscoped) {
		t.Fatalf("re-rooting changed surfacing: %v vs %v", again, unscoped)
	}
	for name := range unscoped {
		if !again[name] {
			t.Fatalf("re-rooting changed surfacing: %v vs %v", again, unscoped)
		}
	}
}

// TestSurfacedConversationForSpan is the conversation half of the same rule:
// roll-up is uniform across the surfacing family, so a sub-agent's turns
// beneath a tool call surface when rolled up relative to that tool call, and
// the enclosing agent's own turns do not.
func TestSurfacedConversationForSpan(t *testing.T) {
	const (
		rootID byte = iota + 1
		outerPromptID
		toolID
		subPromptID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		checkSnapshot(rootID, "shell", SpanID{}, ""),
		messageSnapshot(outerPromptID, "outer prompt", SpanID{SpanID: trace.SpanID{rootID}}, "user"),
		toolCallSnapshot(toolID, rootID, "sub-agent"),
		messageSnapshot(subPromptID, "sub prompt", SpanID{SpanID: trace.SpanID{toolID}}, "user"),
	})

	// At the DB root: the outer prompt and the tool call are the conversation;
	// the sub-agent's turn nests under the tool call.
	roots := db.SurfacedConversation()
	got := surfacedMessageNames(roots)
	if !got["outer prompt"] || !got["sub-agent"] {
		t.Fatalf("unzoomed conversation lost a top-level message: %v", got)
	}

	// Relative to the tool call: only what ran inside it.
	toolSpan := db.Spans.Map[SpanID{SpanID: trace.SpanID{toolID}}]
	if toolSpan == nil {
		t.Fatal("tool call span not loaded")
	}
	scoped := surfacedMessageNames(db.SurfacedConversationForSpan(toolSpan))
	if !scoped["sub prompt"] {
		t.Fatalf("zoomed conversation dropped the sub-agent's own turn: %v", scoped)
	}
	if scoped["outer prompt"] {
		t.Fatalf("zoomed conversation leaked the enclosing run's transcript: %v", scoped)
	}
}
