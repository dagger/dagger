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
