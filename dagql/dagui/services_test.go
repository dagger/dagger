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

// TestServiceDisplaySpan covers the live-tree anchor walk: the display span
// is the nearest displayable (received, non-passthrough, non-internal)
// ancestor of the service exec span below host -- for `dagger up`, the
// per-service span RunUp starts -- falling back to the exec span itself when
// nothing below host qualifies.
func TestServiceDisplaySpan(t *testing.T) {
	const (
		rootID byte = iota + 1
		displayID
		startID
		execID
		bareSvcID
		orphanSvcID
	)
	db := NewDB()
	display := serviceTestSnapshot(displayID, "web :80", spanID(rootID))
	starter := serviceTestSnapshot(startID, "service.start", spanID(displayID))
	starter.Passthrough = true
	svc := markService(serviceTestSnapshot(execID, "exec nginx", spanID(startID)), "web.dagger.local")
	svc.Passthrough = true
	// A displayable service span is its own display span.
	bare := markService(serviceTestSnapshot(bareSvcID, "exec bare", spanID(rootID)), "bare.dagger.local")
	// A hidden service span with no displayable ancestor below host falls
	// back to itself (no origin links in this synthetic trace).
	orphan := markService(serviceTestSnapshot(orphanSvcID, "exec orphan", spanID(rootID)), "orphan.dagger.local")
	orphan.Passthrough = true
	db.ImportSnapshots([]SpanSnapshot{
		serviceTestSnapshot(rootID, "root", SpanID{}),
		display,
		starter,
		svc,
		bare,
		orphan,
	})

	host := db.RootSpan
	roots := db.SurfacedServices()
	if len(roots) != 3 {
		t.Fatalf("roots = %d, want 3 services", len(roots))
	}
	if got := roots[0].DisplaySpan(host); got == nil || got.Name != "web :80" {
		t.Fatalf("DisplaySpan = %+v, want the visible display span above the passthrough chain", got)
	}
	if got := roots[1].DisplaySpan(host); got == nil || got.Name != "exec bare" {
		t.Fatalf("bare DisplaySpan = %+v, want the exec span itself", got)
	}
	if got := roots[2].DisplaySpan(host); got == nil || got.Name != "exec orphan" {
		t.Fatalf("orphan DisplaySpan = %+v, want the exec-span fallback", got)
	}
}

// TestPromoteServicesToWiresDisplayAndReadySpans covers the live-tree wiring
// for runs that lead with their services (`dagger up`): the display span
// lands in host.RevealedSpans, and the service's readiness marker (the
// `ready <url>` span, recognized by ServiceURLs) lands in the display span's
// own RevealedSpans -- so the display row's expansion shows exactly the
// readiness state, not the evaluation machinery.
func TestPromoteServicesToWiresDisplayAndReadySpans(t *testing.T) {
	const (
		rootID byte = iota + 1
		displayID
		evalID
		startID
		execID
		readyID
	)
	db := NewDB()
	display := serviceTestSnapshot(displayID, "web :80", spanID(rootID))
	// evaluation machinery beneath the display span: must NOT be revealed
	eval := serviceTestSnapshot(evalID, "HelloWithServices.web", spanID(displayID))
	starter := serviceTestSnapshot(startID, "service.start", spanID(displayID))
	starter.Passthrough = true
	svc := markService(serviceTestSnapshot(execID, "exec nginx", spanID(startID)), "web.dagger.local")
	svc.Passthrough = true
	ready := serviceTestSnapshot(readyID, "ready http://localhost:80", spanID(displayID))
	ready.ServiceURLs = []string{"http://localhost:80"}
	db.ImportSnapshots([]SpanSnapshot{
		serviceTestSnapshot(rootID, "root", SpanID{}),
		display,
		eval,
		starter,
		svc,
		ready,
	})

	host := db.RootSpan
	if !db.HasServicesForSpan(host) {
		t.Fatal("HasServicesForSpan must report true when a service surfaces")
	}
	db.PromoteServicesTo(host)
	if got := host.RevealedSpans.Order; len(got) != 1 || got[0].Name != "web :80" {
		t.Fatalf("host revealed = %+v, want just the display span", got)
	}
	displaySpan := host.RevealedSpans.Order[0]
	if got := displaySpan.RevealedSpans.Order; len(got) != 1 || got[0].Name != "ready http://localhost:80" {
		t.Fatalf("display revealed = %+v, want just the readiness marker", got)
	}
	// Idempotent: re-promotion must not duplicate.
	db.PromoteServicesTo(host)
	if len(host.RevealedSpans.Order) != 1 || len(displaySpan.RevealedSpans.Order) != 1 {
		t.Fatalf("re-promotion duplicated revealed spans: host=%d display=%d",
			len(host.RevealedSpans.Order), len(displaySpan.RevealedSpans.Order))
	}
}
