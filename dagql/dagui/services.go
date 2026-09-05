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

// Origin returns the API span that produced the Service value, reached through
// the service span's cause links. It returns nil when a partial trace does not
// contain a named origin span.
func (node *ServiceNode) Origin() *Span {
	for _, cause := range node.Span.causesViaLinks.Order {
		if cause.Name != "" {
			return cause
		}
	}
	return nil
}

// DisplaySpan returns the row that best represents this service in the live
// tree: the nearest displayable (received, non-passthrough, non-internal)
// ancestor of the service exec span, stopping at host. The exec span itself
// is deliberately passthrough (its logs are routed away and its failure
// propagates to its origin), so revealing it directly would render nothing
// useful; for `dagger up` the walk lands on the per-service display span
// core/modtree.go's RunUp starts — the row carrying the port-suffixed name,
// the rolled-up health-check and service logs, and the `ready <url>` child.
// When no ancestor below host qualifies, fall back to the origin API span,
// then to the exec span itself.
func (node *ServiceNode) DisplaySpan(host *Span) *Span {
	for s := node.Span; s != nil && s != host; s = s.ParentSpan {
		if s.Received && !s.Passthrough && !s.Internal {
			return s
		}
	}
	if origin := node.Origin(); origin != nil {
		return origin
	}
	return node.Span
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

		var parentID SpanID
		if !spanMayRollUp(span, root, func(parent *Span) {
			if !parentID.IsValid() && parent.Service {
				parentID = parent.ID
			}
		}) {
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

// HasServices reports whether the trace's surfaced service view is non-empty.
func (db *DB) HasServices() bool {
	return db.HasServicesForSpan(nil)
}

// HasServicesForSpan reports whether the root-relative surfaced service view
// is non-empty. A nil root means the live trace root.
func (db *DB) HasServicesForSpan(root *Span) bool {
	return len(db.SurfacedServicesForSpan(root)) > 0
}

// PromoteServicesTo wires each surfaced service's display span into
// host.RevealedSpans (and each nested service's into its parent's display
// span) so the live tree can lead with a run's services — the service analog
// of PromoteConversationTo. Unlike checks or the conversation, service spans
// are ambient (any run that binds a service has one), so this is NOT applied
// to every trace: only a command that declares itself to be about its
// services (`dagger up`, see idtui's promoteServicesLocked) promotes them.
// Idempotent: re-adds are no-ops on the set.
//
// A service's readiness markers — the `ready <url>` spans `dagger up` starts
// beneath the display span once a health check passes, recognized by
// ServiceURLs — are wired into the display span's own RevealedSpans. The
// display span rolls up its logs, so its expansion shows exactly its
// revealed spans (see TraceTree.ShouldShowRevealedSpans) — the readiness
// URL beneath the rolled-up health-check and service logs, with the
// evaluation machinery staying hidden — and IsExpanded auto-expands a
// service display span with revealed children to make that the default.
//
// Callers mark host Passthrough so RowsView iterates these revealed spans
// instead of host's raw children.
func (db *DB) PromoteServicesTo(host *Span) {
	if host == nil {
		return
	}
	var wire func(parent *Span, nodes []*ServiceNode)
	wire = func(parent *Span, nodes []*ServiceNode) {
		for _, node := range nodes {
			display := node.DisplaySpan(host)
			if display != parent {
				parent.RevealedSpans.Add(display)
			}
			for _, child := range display.ChildSpans.Order {
				if len(child.ServiceURLs) > 0 {
					display.RevealedSpans.Add(child)
				}
			}
			wire(display, node.Children)
		}
	}
	wire(host, db.SurfacedServicesForSpan(host))
}
