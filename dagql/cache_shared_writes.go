package dagql

import "strings"

// SharedWrites are the kinds of state engines share that a write of one
// engine cache's holdings changed, on a blob-backed cache, the Cloud's.
// Everything else such a write changes is that engine cache's own: its
// holdings, their dependencies, sessions and retention, and its index of
// them. The state engines share is the e-graph and the entries: whether an
// entry exists, and its own facts. A write that changes none of them
// changes only what no other engine's write touches. Each write still takes
// the cache's lock exclusively, and reports the kinds it changed in its
// RemoteChange, so that its caller can count how much of the cache's writes
// need the whole cache to themselves.
type SharedWrites uint16

const (
	// SharedNewEntry: an entry for a recipe the cache had no entry of.
	SharedNewEntry SharedWrites = 1 << iota
	// SharedNewEngineCache: an engine cache's state, created for its first
	// holding (remoteCacheLocked).
	SharedNewEngineCache
	// SharedIdentity: an identity the e-graph didn't hold, taught to it: a
	// digest, a class, a term or a class's content digest
	// (writeRemoteIdentityLocked).
	SharedIdentity
	// SharedTaughtContent: the content digests spans taught an entry, which
	// its frame takes (teachRemoteIdentityLocked).
	SharedTaughtContent
	// SharedKeptCall: a call kept on an entry (keepCallLocked).
	SharedKeptCall
	// SharedExpiry: an entry's expiry, from a call that returned it
	// (keepCallLocked).
	SharedExpiry
	// SharedRunRan: an entry's stored record's run noted as having run
	// (noteHoldingRanLocked).
	SharedRunRan
	// SharedEntryRemoved: an entry removed once nothing owned it.
	SharedEntryRemoved
	// SharedEngineCacheGone: an engine cache's state removed with its last
	// holding.
	SharedEngineCacheGone
	// SharedClassesCompacted: a collection's compaction of the classes,
	// which frees the slots of the classes no term or entry uses
	// (compactEqClassesAfterCollectionLocked). A check that frees nothing
	// changes no shared state.
	SharedClassesCompacted
)

// SharedWriteKinds is how many kinds SharedWrites has: each is a bit below
// 1 << SharedWriteKinds.
const SharedWriteKinds = 10

var sharedWriteNames = [SharedWriteKinds]string{
	"new_entry", "new_engine_cache", "identity", "taught_content", "kept_call",
	"expiry", "run_ran", "entry_removed", "engine_cache_gone", "classes_compacted",
}

// Each calls f with the index and name of each kind w holds, in the order
// of their bits.
func (w SharedWrites) Each(f func(i int, name string)) {
	for i, name := range sharedWriteNames {
		if w&(1<<i) != 0 {
			f(i, name)
		}
	}
}

func (w SharedWrites) String() string {
	var names []string
	w.Each(func(_ int, name string) { names = append(names, name) })
	return strings.Join(names, "|")
}

// noteSharedWriteLocked records in change, the record of the write in
// progress, that the write changed state engines share, of the kinds in w.
// Only a blob-backed cache records. Requires egraphMu for writing.
func (c *Cache) noteSharedWriteLocked(change *RemoteChange, w SharedWrites) {
	if c.changes != nil {
		change.Shared |= w
	}
}
