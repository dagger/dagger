package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func generatorSnapshot(id byte, name string, parent SpanID, generatorName string) SpanSnapshot {
	start := time.Unix(int64(id), 0)
	return SpanSnapshot{
		ID:            SpanID{SpanID: trace.SpanID{id}},
		TraceID:       TraceID{TraceID: trace.TraceID{1}},
		Name:          name,
		StartTime:     start,
		EndTime:       start.Add(time.Second),
		ParentID:      parent,
		GeneratorName: generatorName,
		Status:        sdktrace.Status{},
	}
}

// TestSurfacedGeneratorsContainment covers the same containment rules as
// checks: a generator surfaces only when its ancestor chain reaches the trace
// root boundary-free; a chain under a Boundary span, or severed at a
// reparenting seam / unreceived placeholder, stays hidden (a generator run a
// test drives as a fixture).
func TestSurfacedGeneratorsContainment(t *testing.T) {
	const (
		rootID byte = iota + 1
		realGenID
		seamID
		fixtureSeamGenID
		fixturePlaceholderGenID
		boundaryID
		fixtureBoundaryGenID
	)
	// id never imported -> stays an unreceived placeholder.
	const missingParentID byte = 99

	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		// The trace root command (not itself a generator), imported first so it
		// wins db.RootSpan.
		generatorSnapshot(rootID, "generate", SpanID{}, ""),
		// A real trace-level generator -- reaches the root, so it surfaces.
		generatorSnapshot(realGenID, "viztest:generate-fail", SpanID{SpanID: trace.SpanID{rootID}}, "viztest:generate-fail"),
		// The reparenting seam: a received, parentless withExec that isn't the
		// trace root (it spawned a nested `dagger generate`).
		generatorSnapshot(seamID, "Container.withExec", SpanID{}, ""),
		// A fixture generator whose chain dead-ends at that seam -- hidden.
		generatorSnapshot(fixtureSeamGenID, "fixture-seam:gen", SpanID{SpanID: trace.SpanID{seamID}}, "fixture-seam:gen"),
		// A fixture generator whose parent was never received -- hidden.
		generatorSnapshot(fixturePlaceholderGenID, "fixture-placeholder:gen", SpanID{SpanID: trace.SpanID{missingParentID}}, "fixture-placeholder:gen"),
		// A fixture generator under a loaded Boundary span -- hidden.
		boundarySnapshot(boundaryID, rootID),
		generatorSnapshot(fixtureBoundaryGenID, "fixture-boundary:gen", SpanID{SpanID: trace.SpanID{boundaryID}}, "fixture-boundary:gen"),
	})

	roots := db.SurfacedGenerators()
	if len(roots) != 1 || roots[0].Name != "viztest:generate-fail" {
		t.Fatalf("roots = %+v, want a single viztest:generate-fail root", roots)
	}
	if !db.HasGenerators() {
		t.Fatal("HasGenerators must report true when generator spans exist")
	}
}

func TestSurfacedGeneratorsMemoizedAndOrdered(t *testing.T) {
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
			ID:            testID(2),
			TraceID:       TraceID{TraceID: trace.TraceID{1}},
			Name:          "gen docs",
			GeneratorName: "docs",
			ParentID:      testID(1),
			StartTime:     time.Unix(2, 0),
			EndTime:       time.Unix(3, 0),
			Status:        sdktrace.Status{Code: codes.Ok},
		},
	})

	first := db.SurfacedGenerators()
	if len(first) != 1 || first[0].Name != "docs" {
		t.Fatalf("expected the docs generator, got %+v", first)
	}
	if again := db.SurfacedGenerators(); &again[0] != &first[0] {
		t.Fatal("repeated same-frame reads must hit the cache")
	}

	// New span data (a failed generator) must invalidate the cache, and the
	// failed generator must sort first.
	db.ImportSnapshots([]SpanSnapshot{{
		ID:            testID(3),
		TraceID:       TraceID{TraceID: trace.TraceID{1}},
		Name:          "gen sdk",
		GeneratorName: "sdk",
		ParentID:      testID(1),
		StartTime:     time.Unix(3, 0),
		EndTime:       time.Unix(4, 0),
		Status:        sdktrace.Status{Code: codes.Error},
	}})
	fresh := db.SurfacedGenerators()
	if len(fresh) != 2 {
		t.Fatalf("cache must be invalidated by new span data, got %d generators", len(fresh))
	}
	if !fresh[0].Failed || fresh[0].Name != "sdk" {
		t.Fatalf("failed generator must sort first, got %+v", fresh[0])
	}
}

// TestPromoteGeneratorsTo verifies the live-tree wiring: the surfaced
// generators land in the host's RevealedSpans so a Passthrough host surfaces
// them as the top-level rows (the analog of PromoteConversationTo).
func TestPromoteGeneratorsTo(t *testing.T) {
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		generatorSnapshot(1, "generate", SpanID{}, ""),
		generatorSnapshot(2, "viztest:gen", SpanID{SpanID: trace.SpanID{1}}, "viztest:gen"),
	})

	host := db.RootSpan
	if host == nil {
		t.Fatal("expected a root span")
	}
	db.PromoteGeneratorsTo(host)
	if len(host.RevealedSpans.Order) != 1 {
		t.Fatalf("RevealedSpans = %d spans, want 1", len(host.RevealedSpans.Order))
	}
	if host.RevealedSpans.Order[0].GeneratorName != "viztest:gen" {
		t.Fatalf("revealed span = %+v, want the generator", host.RevealedSpans.Order[0])
	}
	// Idempotent: re-promotion must not duplicate.
	db.PromoteGeneratorsTo(host)
	if len(host.RevealedSpans.Order) != 1 {
		t.Fatalf("re-promotion duplicated revealed spans: %d", len(host.RevealedSpans.Order))
	}
}
