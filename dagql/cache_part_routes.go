package dagql

// PartProducerRoute is data-only. NeedsMetadata means a caller must finish
// metadata before asking for the concrete positional write set.
type PartProducerRoute struct {
	Group         ProducerAddress
	WriteSet      []PersistedPartAddress
	HasProducer   bool
	NeedsMetadata bool
	Delegation    *PartDelegation
}

// PartDelegation authorizes one unchanged part from the exact recorded parent.
// It is runtime routing data, never a saved producer or an equivalence claim.
type PartDelegation struct {
	ParentResultID uint64
	Address        PersistedPartAddress
}

type PersistedPartRouter interface {
	RouteParts(PersistedPayloadVisit, PartKey) (PartProducerRoute, error)
}
