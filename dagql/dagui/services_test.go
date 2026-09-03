package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// serviceTestSnapshot builds a plain span snapshot. StartTime is derived from
// id so that importing in id order matches start-time order (mirrors
// messageSnapshot).
func serviceTestSnapshot(id byte, name string, parent SpanID) SpanSnapshot {
	start := time.Unix(int64(id), 0)
	return SpanSnapshot{
		ID:        SpanID{SpanID: trace.SpanID{id}},
		TraceID:   TraceID{TraceID: trace.TraceID{1}},
		Name:      name,
		StartTime: start,
		EndTime:   start.Add(time.Second),
		ParentID:  parent,
	}
}

// markService marks a snapshot as a service-instance span with the given
// hostname (telemetryattrs.ServiceAttr / ServiceNameAttr, post-ProcessAttribute).
func markService(snap SpanSnapshot, hostname string) SpanSnapshot {
	snap.Service = true
	snap.ServiceName = hostname
	return snap
}

// TestSurfacedServicesOrdersNestsAndContains covers the reveal-independent
// service tree: services surface in start-time order, a service started within
// another service's subtree nests beneath it, and a boundary-contained service
// (a test fixture) stays hidden.
func TestSurfacedServicesOrdersNestsAndContains(t *testing.T) {
	const (
		rootID byte = iota + 1
		boundaryID
		hiddenSvcID
		svcAID
		svcBID
		middleID
		innerSvcID
	)
	db := NewDB()
	boundary := serviceTestSnapshot(boundaryID, "fixture", spanID(rootID))
	boundary.Boundary = true
	// Import the later-starting service first to prove the sort.
	db.ImportSnapshots([]SpanSnapshot{
		serviceTestSnapshot(rootID, "root", SpanID{}),
		boundary,
		markService(serviceTestSnapshot(hiddenSvcID, "hidden", spanID(boundaryID)), "hidden.dagger.local"),
		markService(serviceTestSnapshot(svcBID, "exec svc-b", spanID(rootID)), "b.dagger.local"),
		markService(serviceTestSnapshot(svcAID, "exec svc-a", spanID(rootID)), "a.dagger.local"),
		// a service started within svc-a's subtree, one non-service span down
		serviceTestSnapshot(middleID, "start inner", spanID(svcAID)),
		markService(serviceTestSnapshot(innerSvcID, "exec inner", spanID(middleID)), "inner.dagger.local"),
	})

	roots := db.SurfacedServices()
	if len(roots) != 2 || roots[0].Name() != "a.dagger.local" || roots[1].Name() != "b.dagger.local" {
		names := make([]string, len(roots))
		for i, n := range roots {
			names[i] = n.Name()
		}
		t.Fatalf("roots = %v, want [a.dagger.local b.dagger.local]", names)
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].Name() != "inner.dagger.local" {
		t.Fatalf("svc-a children = %v, want [inner.dagger.local]", roots[0].Children)
	}
	if len(roots[1].Children) != 0 {
		t.Fatalf("svc-b children = %v, want none", roots[1].Children)
	}
}

// TestSurfacedServicesName covers the display accessor: the node names
// itself by hostname, falling back to the exec span's own name.
func TestSurfacedServicesName(t *testing.T) {
	const (
		rootID byte = iota + 1
		svcID
		bareSvcID
	)
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		serviceTestSnapshot(rootID, "root", SpanID{}),
		markService(serviceTestSnapshot(svcID, "exec entrypoint.sh", spanID(rootID)), "web.dagger.local"),
		// no hostname: Name() falls back to the span name
		markService(serviceTestSnapshot(bareSvcID, "exec bare", spanID(rootID)), ""),
	})

	roots := db.SurfacedServices()
	if len(roots) != 2 {
		t.Fatalf("roots = %v, want 2 services", roots)
	}
	web, bare := roots[0], roots[1]
	if web.Name() != "web.dagger.local" {
		t.Fatalf("web.Name() = %q, want web.dagger.local", web.Name())
	}
	if bare.Name() != "exec bare" {
		t.Fatalf("bare.Name() = %q, want span-name fallback", bare.Name())
	}
}
