package dagql

import (
	"encoding/json"

	"github.com/opencontainers/go-digest"
)

// These indexes describe ordinary cached scalar rows. They neither retain rows
// nor add a persistence representation. Every membership is a cache-local row
// ID, so deleting or replacing a row releases all of its index storage.
type callMetadataIndex struct {
	records     map[sharedResultID]indexedCallMetadata
	byOrigin    map[callMetadataLookupKey]map[sharedResultID]struct{}
	byCandidate map[callMetadataLookupKey]map[sharedResultID]struct{}
	byResult    map[sharedResultID]map[sharedResultID]struct{}
}

type callMetadataLookupKey struct{ namespace, key string }

type indexedCallMetadata struct {
	record   CallMetadataRecord
	outputID sharedResultID
}

// indexCallMetadataLocked runs when an ordinary row's value is installed,
// including persistence restore and value transfer. It never decodes objects
// or forces a lazy part. Requires egraphMu for writing.
func (c *Cache) indexCallMetadataLocked(row *sharedResult) {
	c.unindexCallMetadataLocked(row.id)
	frame := row.loadResultCall()
	if frame == nil || frame.Kind != ResultCallKindSynthetic || frame.SyntheticOp != callMetadataOp {
		return
	}
	state := row.loadPayloadState()
	var raw string
	if value, ok := state.self.(String); ok {
		raw = value.String()
	} else if env := state.persistedEnvelope; env != nil && env.Kind == persistedResultKindScalar && env.TypeName == "String" {
		if json.Unmarshal(env.ScalarJSON, &raw) != nil {
			return
		}
	} else {
		return
	}
	var record CallMetadataRecord
	if json.Unmarshal([]byte(raw), &record) != nil || record.Namespace == "" || record.Origin == "" || !json.Valid(record.Payload) {
		return
	}
	if namespace, ok := callMetadataFrameArg(frame, "namespace"); ok && namespace != record.Namespace {
		return
	}
	if c.callMetadata.records == nil {
		c.callMetadata.records = map[sharedResultID]indexedCallMetadata{}
		c.callMetadata.byOrigin = map[callMetadataLookupKey]map[sharedResultID]struct{}{}
		c.callMetadata.byCandidate = map[callMetadataLookupKey]map[sharedResultID]struct{}{}
		c.callMetadata.byResult = map[sharedResultID]map[sharedResultID]struct{}{}
	}
	entry := indexedCallMetadata{record: record}
	if frame.Receiver != nil {
		entry.outputID = sharedResultID(frame.Receiver.ResultID)
	}
	c.callMetadata.records[row.id] = entry
	addCallMetadataRow(c.callMetadata.byOrigin, callMetadataLookupKey{record.Namespace, record.Origin.String()}, row.id)
	addCallMetadataRow(c.callMetadata.byCandidate, callMetadataLookupKey{record.Namespace, record.CandidateKey}, row.id)
	if entry.outputID != 0 {
		addCallMetadataRow(c.callMetadata.byResult, entry.outputID, row.id)
	}
}

// unindexCallMetadataLocked requires egraphMu for writing.
func (c *Cache) unindexCallMetadataLocked(id sharedResultID) {
	entry, ok := c.callMetadata.records[id]
	if !ok {
		return
	}
	delete(c.callMetadata.records, id)
	removeCallMetadataRow(c.callMetadata.byOrigin, callMetadataLookupKey{entry.record.Namespace, entry.record.Origin.String()}, id)
	removeCallMetadataRow(c.callMetadata.byCandidate, callMetadataLookupKey{entry.record.Namespace, entry.record.CandidateKey}, id)
	if entry.outputID != 0 {
		removeCallMetadataRow(c.callMetadata.byResult, entry.outputID, id)
	}
	if len(c.callMetadata.records) == 0 {
		c.callMetadata = callMetadataIndex{}
	}
}

func addCallMetadataRow[K comparable](index map[K]map[sharedResultID]struct{}, key K, id sharedResultID) {
	if index[key] == nil {
		index[key] = map[sharedResultID]struct{}{}
	}
	index[key][id] = struct{}{}
}

func removeCallMetadataRow[K comparable](index map[K]map[sharedResultID]struct{}, key K, id sharedResultID) {
	delete(index[key], id)
	if len(index[key]) == 0 {
		delete(index, key)
	}
}

// callMetadataOriginsLocked expands query candidates to all observations of
// their origins. A different candidate key must not hide a conflicting report.
func (c *Cache) callMetadataOriginsLocked(selected map[sharedResultID]struct{}, namespace string) map[sharedResultID]struct{} {
	all := map[sharedResultID]struct{}{}
	seen := map[digest.Digest]bool{}
	for id := range selected {
		entry, ok := c.callMetadata.records[id]
		if !ok || entry.record.Namespace != namespace || seen[entry.record.Origin] {
			continue
		}
		seen[entry.record.Origin] = true
		for sibling := range c.callMetadata.byOrigin[callMetadataLookupKey{namespace, entry.record.Origin.String()}] {
			all[sibling] = struct{}{}
		}
	}
	return all
}
