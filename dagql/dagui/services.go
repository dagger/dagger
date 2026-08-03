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

// Origin returns the API span that produced the Service value (e.g.
// Container.asService), reached via the service span's cause links — the
// engine links the exec span to every install span. Returns nil when no named
// install span is loaded (a replayed or partially fetched trace).
func (node *ServiceNode) Origin() *Span {
	for _, cause := range node.Span.causesViaLinks.Order {
		if cause.Name != "" {
			return cause
		}
	}
	return nil
}

// SurfacedServices returns the whole trace's service instances as a tree. It
// is SurfacedServicesForSpan relative to the trace root.
func (db *DB) SurfacedServices() []*ServiceNode {
	return db.SurfacedServicesForSpan(nil)
}

// SurfacedServicesForSpan returns the service instances started beneath root
// as a tree, independent of the `reveal` mechanism — the service analog of
// DB.SurfacedChecksForSpan / DB.SurfacedConversationForSpan. A nil root means
// the trace root.
//
// A span marked as a service (telemetryattrs.ServiceAttr) is surfaced only if
// its ancestor chain reaches root with no Boundary or Encapsulate span in
// between — the same zoom-relative containment the other surfaced kinds apply,
// so a service a test drives as a fixture stays hidden, and a zoom sees only
// the services its own subtree started. A chain severed before root (an
// unreceived placeholder, or a reparenting seam the incremental fetch never
// loaded) can't be proven boundary-free and is treated as contained too.
//
// Like the conversation there is no dedup: each service span is its own node,
// nested under the nearest surfaced ancestor service. Roots and children are
// ordered by start time. The result is cached per DB mutation and per root;
// callers must treat the returned nodes as read-only.
func (db *DB) SurfacedServicesForSpan(root *Span) []*ServiceNode {
	r := db.surfaceRoot(root)
	key := surfaceRootID(r)
	if db.surfacedServicesInit && db.surfacedServicesAt == db.mutations && db.surfacedServicesRoot == key {
		return db.surfacedServices
	}
	db.surfacedServices = db.buildSurfacedServices(r)
	db.surfacedServicesAt = db.mutations
	db.surfacedServicesRoot = key
	db.surfacedServicesInit = true
	return db.surfacedServices
}

func (db *DB) buildSurfacedServices(root *Span) []*ServiceNode {
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
		reachedRoot := span == root
		for p := span.ParentSpan; p != nil; p = p.ParentSpan {
			atRoot := p == root
			if !atRoot && (p.Boundary || p.Encapsulate) {
				contained = true
				break
			}
			if !parentID.IsValid() && p.Service {
				parentID = p.ID
			}
			if atRoot {
				// Stop at root: its own flags are outside the question, but it
				// still nests (see SurfacedChecksForSpan).
				reachedRoot = true
				break
			}
		}
		if !contained && root != nil && !reachedRoot {
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

// HasServices reports whether the trace contains any service-instance spans,
// for live affordances that want a cheap existence check (mirrors HasChecks).
func (db *DB) HasServices() bool {
	return db.HasServicesForSpan(nil)
}

// HasServicesForSpan is HasServices restricted to root's subtree; a nil root
// means the whole trace.
func (db *DB) HasServicesForSpan(root *Span) bool {
	for _, span := range db.Spans.Order {
		if span.Service && underSurfaceRoot(span, root) {
			return true
		}
	}
	return false
}
