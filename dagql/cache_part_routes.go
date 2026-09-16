package dagql

// PartProducerRoute is data-only. NeedsMetadata means a caller must finish
// metadata before asking for the concrete positional write set.
type PartProducerRoute struct {
	Group         ProducerAddress
	WriteSet      []PersistedPartAddress
	HasProducer   bool
	NeedsMetadata bool
}

type PersistedPartRouter interface {
	RouteParts(PersistedPayloadVisit, PartKey) (PartProducerRoute, error)
}
