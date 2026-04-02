package dagql

import (
	"fmt"

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
	resultID              sharedResultID
	frame                 *ResultCall
	self                  Typed
	hasValue              bool
	sessionResourceHandle SessionResourceHandle
	persistedEnvelope     *PersistedResultEnvelope
	row                   persistdb.MirrorResult
	resultDeps            []persistdb.MirrorResultDep
	resultSnapshotLinks   []persistdb.MirrorResultSnapshotLink
}

type persistStateSnapshot struct {
	persistedEdges        []persistdb.MirrorPersistedEdge
	eqClasses             []persistdb.MirrorEqClass
	eqClassDigests        []persistdb.MirrorEqClassDigest
	terms                 []persistdb.MirrorTerm
	termInputs            []persistdb.MirrorTermInput
	resultOutputEqClasses []persistdb.MirrorResultOutputEqClass
	results               []persistResultSnapshot
	snapshotContentLinks  []persistdb.MirrorSnapshotContentLink
	importedLayerByBlob   []persistdb.MirrorImportedLayerBlobIndex
	importedLayerByDiff   []persistdb.MirrorImportedLayerDiffIndex
}
