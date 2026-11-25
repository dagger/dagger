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
	resultSnapshotLinks   []persistdb.MirrorResultSnapshotLink
	// changedAt is, on a blob-backed cache, the entry's change stamp
	// (sharedResult.changedAt), which its stored row saves.
	changedAt uint64
}

type persistStateSnapshot struct {
	nextResultID          sharedResultID
	persistedEdges        []persistdb.MirrorPersistedEdge
	resultCalls           []persistdb.MirrorResultCall
	resultTaught          []persistdb.MirrorResultTaught
	eqClasses             []persistdb.MirrorEqClass
	eqClassDigests        []persistdb.MirrorEqClassDigest
	terms                 []persistdb.MirrorTerm
	termInputs            []persistdb.MirrorTermInput
	resultOutputEqClasses []persistdb.MirrorResultOutputEqClass
	results               []persistResultSnapshot
	snapshotContentLinks  []persistdb.MirrorSnapshotContentLink
	snapshotContentHashes []persistdb.MirrorSnapshotContentHash
	importedLayerByBlob   []persistdb.MirrorImportedLayerBlobIndex
	importedLayerByDiff   []persistdb.MirrorImportedLayerDiffIndex
	// held is how long taking the snapshot held egraphMu for reading.
	held time.Duration
	// writes is, on a blob-backed cache, its write count (Cache.writes),
	// read under the same hold as every row's change stamp.
	writes uint64
}
