package dagql

// LazyOperationRoute is data-only. NeedsMetadata means a caller must finish
// metadata before asking for the concrete positional write set.
type LazyOperationRoute struct {
	Group            LazyGroupAddress
	WriteSet         []PersistedPartAddress
	HasLazyOperation bool
	NeedsMetadata    bool
	Delegation       *PartDelegation
}

// PartDelegation authorizes one unchanged part from the exact recorded parent.
// It is runtime routing data, never a saved operation or an equivalence claim.
type PartDelegation struct {
	ParentResultID uint64
	Address        PersistedPartAddress
}

type PersistedPartRouter interface {
	RouteParts(PersistedPayloadVisit, PartKey) (LazyOperationRoute, error)
}

// PersistedPartsRouter routes several parts of one payload, decoding the
// payload once: what RouteParts gives for each part, in order, with each
// part's error, but without its write set. A family routing a payload's
// parts one RouteParts at a time decodes the payload once per part.
type PersistedPartsRouter interface {
	RouteEachPart(PersistedPayloadVisit, []PartKey) ([]LazyOperationRoute, []error)
}

// routeEachPart routes parts of the payload v with router, without their
// write sets, decoding the payload once when the router can.
func routeEachPart(router PersistedPartRouter, v PersistedPayloadVisit, parts []PartKey) ([]LazyOperationRoute, []error) {
	if batch, ok := router.(PersistedPartsRouter); ok {
		return batch.RouteEachPart(v, parts)
	}
	routes, errs := make([]LazyOperationRoute, len(parts)), make([]error, len(parts))
	for i, part := range parts {
		routes[i], errs[i] = router.RouteParts(v, part)
		routes[i].WriteSet = nil
	}
	return routes, errs
}
