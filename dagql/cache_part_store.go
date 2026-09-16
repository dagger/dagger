package dagql

import (
	"context"
	"github.com/dagger/dagger/engine/snapshots"
)

// PersistedPartInstaller prepares a complete root representation, including
// receiver-relative roles. Inline storage is attached at prepareInlineReadyPart.
type PersistedPartInstaller interface {
	PreparePartRecord(receiver PersistedRecord, source PersistedRecord, descriptor PartDescriptor, target PersistedPartAddress) (PersistedRecord, error)
}

// PreparedPartStore contains only precomputed assignments. TryLock must not
// block or evaluate, and Publish cannot fail, allocate storage or run cleanup.
type PreparedPartStore interface {
	TryLock() bool
	Publish()
	Unlock()
}
type PartStorePreparer interface {
	PreparePartStore(context.Context, *PersistDecodeContext, PersistedRecord, PartDescriptor, snapshots.ImmutableRef) (PreparedPartStore, error)
}

// A producer publishes its complete missing write set in one transaction.
type PartBatchStorePreparer interface {
	PreparePartStores(context.Context, *PersistDecodeContext, PersistedRecord, []PartDescriptor, []snapshots.ImmutableRef) (PreparedPartStore, error)
}
