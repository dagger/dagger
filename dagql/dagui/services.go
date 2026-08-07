package dagui

import (
	"sort"
)

// ServiceNode is a surfaced service-instance span — the long-lived exec span
// the engine marks with telemetryattrs.ServiceAttr, alive exactly while the
// service runs — with any services started within its subtree nested beneath
// it (e.g. a dev engine service hosting further services).
type ServiceNode struct {
	Span     *Span
	Children []*ServiceNode
}

// Name returns the service's display name: its network hostname
// (ServiceNameAttr), falling back to the span name.
func (node *ServiceNode) Name() string {
	if node.Span.ServiceName != "" {
		return node.Span.ServiceName
	}
	return node.Span.Name
}

// SurfacedServices returns the trace's service instances as a tree,
// independent of the `reveal` mechanism — the service analog of
// DB.SurfacedChecks / DB.SurfacedConversation.
//
// A span marked as a service (telemetryattrs.ServiceAttr) is surfaced only if
// its ancestor chain reaches the trace root with no Boundary or Encapsulate
// span in between — the same containment the other surfaced kinds apply, so a
// service a test drives as a fixture stays hidden. A chain severed before the
// root (an unreceived placeholder, or a reparenting seam the incremental fetch
// never loaded) can't be proven boundary-free and is treated as contained too.
//
// Like the conversation there is no dedup: each service span is its own node,
// nested under the nearest surfaced ancestor service. Roots and children are
// ordered by start time. The result is cached per DB mutation; callers must
// treat the returned nodes as read-only.
func (db *DB) SurfacedServices() []*ServiceNode {
	if db.surfacedServicesInit && db.surfacedServicesAt == db.mutations {
		return db.surfacedServices
	}
	db.surfacedServices = db.buildSurfacedServices()
	db.surfacedServicesAt = db.mutations
	db.surfacedServicesInit = true
	return db.surfacedServices
}

func (db *DB) buildSurfacedServices() []*ServiceNode {
	type info struct {
		span     *Span
		parentID SpanID
	}
	byID := map[SpanID]*info{}
	for span := range db.Spans.Iter() {
		if !span.Service || span.Internal {
			continue
		}

		contained := false
		var parentID SpanID
		reachedRoot := span == db.RootSpan
		for p := span.ParentSpan; p != nil; p = p.ParentSpan {
			if p.Boundary || p.Encapsulate {
				contained = true
				break
			}
			if !parentID.IsValid() && p.Service {
				parentID = p.ID
			}
			if p == db.RootSpan {
				reachedRoot = true
				break
			}
		}
		if !contained && db.RootSpan != nil && !reachedRoot {
			contained = true
		}
		if contained {
			continue
		}
		byID[span.ID] = &info{span: span, parentID: parentID}
	}

	nodes := make(map[SpanID]*ServiceNode, len(byID))
	for id, in := range byID {
		nodes[id] = &ServiceNode{Span: in.span}
	}
	var roots []*ServiceNode
	for id, in := range byID {
		node := nodes[id]
		if in.parentID.IsValid() {
			if parent, ok := nodes[in.parentID]; ok {
				parent.Children = append(parent.Children, node)
			}
			// A service anchored to a missing ancestor service must not escape
			// as a new root (mirrors the conversation's containment handling).
			continue
		}
		roots = append(roots, node)
	}

	var sortNodes func(ns []*ServiceNode)
	sortNodes = func(ns []*ServiceNode) {
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
