package dagql

import (
	"fmt"
	"time"

	persistdb "github.com/dagger/dagger/dagql/persistdb"
)

type cachePersistInputProvenanceKind string

const (
	cachePersistInputProvenanceKindResult cachePersistInputProvenanceKind = "result"
	cachePersistInputProvenanceKindDigest cachePersistInputProvenanceKind = "digest"
)

type cachePersistInputProvenance struct {
	Kind   cachePersistInputProvenanceKind `json:"kind"`
	Digest string                          `json:"digest"`
}

func (prov cachePersistInputProvenance) validate() error {
	if prov.Kind != cachePersistInputProvenanceKindResult && prov.Kind != cachePersistInputProvenanceKindDigest {
		return fmt.Errorf("unknown input provenance kind %q", prov.Kind)
	}
	if prov.Digest == "" {
		return fmt.Errorf("input provenance %q missing digest", prov.Kind)
	}
	return nil
}

// persistResultSnapshot is what a save writes of one entry: the kinds of its
// rows that changed, or all of them for an entry the database doesn't hold
// (write). Its record is checked when its write count or the fields it takes
// moved, or it is a live value: the fields from imported to
// snapshotOwnerLinks are then the record's, as encodePersistedResultRow
// encodes it.
type persistResultSnapshot struct {
	imported              bool
	persistDeclined       bool
	boundSession          SessionResourceHandle
	run                   *RunName
	runRan                runRanBy
	pendingOffers         []PersistedPartOffer
	storedParts           []storedPart
	resultID              sharedResultID
	frame                 *ResultCall
	self                  Typed
	isObject              bool
	hasValue              bool
	sessionResourceHandle SessionResourceHandle
	persistedEnvelope     *PersistedResultEnvelope
	stored                *storedBody
	partFacts             *partFacts
	snapshotOwnerLinks    []PersistedSnapshotRefLink
	row                   persistdb.MirrorResult
	resultDeps            []persistdb.MirrorResultDep
	outputEqClasses       []persistdb.MirrorResultOutputEqClass
	calls                 []persistdb.MirrorResultCall
	taught                []persistdb.MirrorResultTaught
	edge                  *persistdb.MirrorPersistedEdge
	resultSnapshotLinks   []persistdb.MirrorResultSnapshotLink
	write                 persistEntryWrites
	// kept is the entry's place in the save's next kept table, whose
	// record fingerprint the write fills once it encodes the record. held
	// says whether the database holds the entry, and keptRecord is then the
	// fingerprint of its record row there.
	kept       int
	held       bool
	keptRecord uint64
	// recordWritten says the write wrote the record: its bytes differed.
	recordWritten bool
}

// persistEntryWrites is what a save writes of an entry.
type persistEntryWrites uint8

const (
	// persistWriteRow writes its results row.
	persistWriteRow persistEntryWrites = 1 << iota
	// persistWriteRecord encodes its record, and writes it with its
	// snapshot links if the bytes differ from what the database holds.
	persistWriteRecord
	persistWriteDeps
	persistWriteClasses
	persistWriteCalls
	persistWriteTaught
	persistWriteEdge
)

// persistTermSnapshot is a term a save writes, with its inputs, and whether
// the database holds it already.
type persistTermSnapshot struct {
	row    persistdb.MirrorTerm
	inputs []persistdb.MirrorTermInput
	held   bool
}

// persistClassSnapshot is a class a save writes, with its digests, and
// whether the database holds it already.
type persistClassSnapshot struct {
	id      int64
	digests []persistdb.MirrorEqClassDigest
	held    bool
}

// persistStateSnapshot is what a save writes: the units that changed or are
// new, the numbers of those the store no longer holds, and the fingerprints
// of everything the store holds, which the database holds once the save
// commits (next).
type persistStateSnapshot struct {
	nextResultID          sharedResultID
	results               []persistResultSnapshot
	terms                 []persistTermSnapshot
	classes               []persistClassSnapshot
	goneResults           []int64
	goneTerms             []int64
	goneClasses           []int64
	snapshotContentLinks  []persistdb.MirrorSnapshotContentLink
	snapshotContentHashes []persistdb.MirrorSnapshotContentHash
	importedLayerByBlob   []persistdb.MirrorImportedLayerBlobIndex
	importedLayerByDiff   []persistdb.MirrorImportedLayerDiffIndex
	next                  persistKept
	// held is how long taking the snapshot held egraphMu for reading.
	held time.Duration
	// writes is, on a blob-backed cache, its write count (Cache.writes),
	// read under the same hold as every row's change stamp.
	writes uint64
	// written is how many entries the save wrote, once it has committed.
	written int
	// full is, while a test checks saves (testPersistFullSave), the
	// snapshot of a full save taken under the same hold.
	full *persistStateSnapshot
}
