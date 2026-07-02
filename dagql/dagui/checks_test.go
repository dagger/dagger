package dagui

import (
	"testing"
	"time"

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
