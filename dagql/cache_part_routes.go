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

// WorkloadPartRouter identifies groups that only copy an unchanged parent
// part. Accounting follows that part's producer instead of pricing the copy.
type WorkloadPartRouter interface {
	WorkloadPartDelegation(PersistedPayloadVisit, LazyGroupKey) (*PartDelegation, error)
}

type PersistedPartRouter interface {
	RouteParts(PersistedPayloadVisit, PartKey) (LazyOperationRoute, error)
}
