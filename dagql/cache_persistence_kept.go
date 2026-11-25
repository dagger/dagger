package dagql

import (
	"cmp"
	"encoding/json"
	"fmt"
	"hash/maphash"
	"slices"
	"sync"
	"sync/atomic"

	persistdb "github.com/dagger/dagger/dagql/persistdb"
)

// A save writes only what differs from what the persistence database
// already holds. The database holds the cache as its last committed save
// wrote it, or as the open read it, and the cache keeps that as fingerprints
// (persistKept): one for each kind of row of each entry, term and class the
// database holds. A save takes, under the same read hold of egraphMu, the
// fingerprints of the rows it would write now, and writes the units whose
// fingerprints moved, deletes those the store no longer holds, and leaves
// the rest (snapshotPersistStateLocked, applyPersistStateSnapshot). It
// replaces the kept fingerprints only once its transaction commits, so a save
// that fails leaves them describing the database as it still is. After any
// successful save, the database holds exactly what a save of the whole store
// at that save's hold would have written.
//
// An entry's record is most of its row's bytes and most of what a save
// encodes, so a save doesn't encode it to see whether it moved. A record the
// cache holds encoded, as a blob-backed cache holds all of them, changes only
// by being written: under the entry's payload lock, which counts each write
// (recordLock), or by its frame or stored parts changing, which count too, or
// by the entry's own fields its envelope takes (recordInputs), which the
// save fingerprints. While the count and those fields are what they were when
// the record row was last written, the row is as the save would write it. A
// live value, as an engine holds, can change inside itself, so a save encodes
// it and compares its bytes.
//
// The fingerprints are seeded (Cache.persistSeed), per cache and per open, and
// never leave memory.

// recordLock is an entry's payload lock (sharedResult.payloadMu). It counts
// the writes to what the entry's saved record holds: each unlock for writing
// counts one, as do a frame stored or dropped (storeResultCall,
// dropResultCall) and a change of the entry's stored parts
// (changeStoredPartsLocked), each after the write. A save reads the count
// before what it saves of the record, so a write it misses moves the count
// past the one it keeps, and the next save writes the record.
type recordLock struct {
	sync.RWMutex
	writes atomic.Uint32
}

// Unlock counts a write to the entry's record and unlocks l for writing.
func (l *recordLock) Unlock() {
	l.writes.Add(1)
	l.RWMutex.Unlock()
}

// noteWrite counts a write to the entry's record made under another lock.
func (l *recordLock) noteWrite() {
	l.writes.Add(1)
}

// persistKept is what the persistence database holds of the cache: each
// entry, term and class it holds, sorted by number, with the fingerprints of
// their rows.
type persistKept struct {
	entries []keptEntry
	terms   []keptUnit
	classes []keptUnit
}

// keptEntry is what the database holds of one entry: a fingerprint of its
// results row, of its record row with its snapshot links, and of each other
// kind of its rows. recordWrites and recordInputs are the entry's count of
// writes to its record and the fingerprint of its fields the record takes,
// as they were when its record row was last written or read.
type keptEntry struct {
	id           sharedResultID
	row          uint64
	record       uint64
	recordInputs uint64
	deps         uint64
	classes      uint64
	calls        uint64
	taught       uint64
	edge         uint64
	recordWrites uint32
}

// keptUnit is what the database holds of a term or a class: its number and
// the fingerprint of its rows.
type keptUnit struct {
	id uint64
	fp uint64
}

// rowsPrint fingerprints a set of rows of one kind: their count, and the sum
// of each row's seeded hash. A row's position, where it has one, is one of
// its columns, so the sum fingerprints an ordered list as well as a set, and
// rows can be added in any order.
type rowsPrint struct {
	n   uint64
	sum uint64
}

func (p *rowsPrint) add(h uint64) {
	p.n++
	p.sum += h
}

func (p rowsPrint) value(seed maphash.Seed) uint64 {
	return maphash.Comparable(seed, [2]uint64{p.n, p.sum})
}

// rowPrint is a hash of one row: of its columns, as a comparable value.
func rowPrint[T comparable](seed maphash.Seed, row T) uint64 {
	return maphash.Comparable(seed, row)
}

// keptResultRow is the columns of an entry's results row, all but its
// record's.
type keptResultRow struct {
	OutputEffectIDs           string
	ExpiresAtUnix             int64
	Replacements              int64
	Indexed                   bool
	CloudHoldingNumber        int64
	CloudHoldingStored        bool
	CloudHoldingExpiresAtUnix int64
	CreatedAtUnixNano         int64
	LastUsedAtUnixNano        int64
	RecordType                string
	Description               string
	ChangeStamp               int64
}

// resultRowPrint fingerprints an entry's results row.
func resultRowPrint(seed maphash.Seed, row persistdb.MirrorResult) uint64 {
	return rowPrint(seed, keptResultRow{
		OutputEffectIDs:           row.OutputEffectIDs,
		ExpiresAtUnix:             row.ExpiresAtUnix,
		Replacements:              row.Replacements,
		Indexed:                   row.Indexed,
		CloudHoldingNumber:        row.CloudHoldingNumber,
		CloudHoldingStored:        row.CloudHoldingStored,
		CloudHoldingExpiresAtUnix: row.CloudHoldingExpiresAtUnix,
		CreatedAtUnixNano:         row.CreatedAtUnixNano,
		LastUsedAtUnixNano:        row.LastUsedAtUnixNano,
		RecordType:                row.RecordType,
		Description:               row.Description,
		ChangeStamp:               row.ChangeStamp,
	})
}

// recordBytesPrint fingerprints an entry's record row: its call frame and
// its self payload.
func recordBytesPrint(seed maphash.Seed, callFrameJSON string, selfPayload []byte) uint64 {
	return maphash.Comparable(seed, [2]uint64{maphash.String(seed, callFrameJSON), maphash.Bytes(seed, selfPayload)})
}

// recordPrint fingerprints an entry's record row, from recordBytesPrint, with
// its snapshot links, which the record's encoding names.
func recordPrint(seed maphash.Seed, recordBytes uint64, links rowsPrint) uint64 {
	return maphash.Comparable(seed, [2]uint64{recordBytes, links.value(seed)})
}

// keptRecordInputs is what of an entry's own fields its record's envelope
// takes besides those its payload lock counts (persistResultEnvelope).
type keptRecordInputs struct {
	PersistDeclined bool
	HasRun          bool
	Run             RunName
	RunRan          runRanBy
	Offers          uint64
}

// recordInputsPrint fingerprints keptRecordInputs. Offers are rare, and
// fingerprinted by their JSON when there are any.
func recordInputsPrint(seed maphash.Seed, persistDeclined bool, run *RunName, runRan runRanBy, offers []PersistedPartOffer) (uint64, error) {
	in := keptRecordInputs{PersistDeclined: persistDeclined, RunRan: runRan}
	if run != nil {
		in.HasRun, in.Run = true, *run
	}
	if len(offers) > 0 {
		raw, err := json.Marshal(offers)
		if err != nil {
			return 0, err
		}
		in.Offers = maphash.Bytes(seed, raw)
	}
	return rowPrint(seed, in), nil
}

// keptTermRow is a term's row, all but its term digest, which the save
// derives from the rest (calcEgraphTermDigest).
type keptTermRow struct {
	SelfDigest      string
	OutputEqClassID int64
}

// termPrint fingerprints a term's rows: its row and its inputs.
func termPrint(seed maphash.Seed, selfDigest string, outputEqClassID int64, inputs rowsPrint) uint64 {
	return maphash.Comparable(seed, [2]uint64{rowPrint(seed, keptTermRow{SelfDigest: selfDigest, OutputEqClassID: outputEqClassID}), inputs.value(seed)})
}

// persistKeptCursor walks a sorted kept list beside the save's own walk in
// number order, telling for each number whether the database holds it, and
// collecting the numbers it holds that the walk passed by: those the store
// no longer holds.
type persistKeptCursor[T any] struct {
	list []T
	id   func(T) uint64
	next int
	gone []int64
}

// at returns the kept unit numbered id, if the database holds one, after
// collecting every kept number below id as gone. Calls come in increasing id
// order.
func (k *persistKeptCursor[T]) at(id uint64) (T, bool) {
	for k.next < len(k.list) && k.id(k.list[k.next]) < id {
		k.gone = append(k.gone, int64(k.id(k.list[k.next])))
		k.next++
	}
	if k.next < len(k.list) && k.id(k.list[k.next]) == id {
		unit := k.list[k.next]
		k.next++
		return unit, true
	}
	var zero T
	return zero, false
}

// finish collects every kept number the walk didn't reach as gone, and
// returns all the gone numbers.
func (k *persistKeptCursor[T]) finish() []int64 {
	for ; k.next < len(k.list); k.next++ {
		k.gone = append(k.gone, int64(k.id(k.list[k.next])))
	}
	return k.gone
}

// keptFromImportedRows is what the database holds, as an open read it: the
// fingerprints of every entry's, term's and class's rows as they are there,
// before the restore changed anything. A row the restore dropped or changed
// is kept as it was read, so the next save writes the unit again.
// resultRows are sorted by entry number (listMirrorResults); the other
// rows may come in any order. The fields of an entry's record are kept once
// the restore's own work is done (keepRestoredRecords).
func keptFromImportedRows(
	seed maphash.Seed,
	resultRows []importedResultRow,
	eqClassRows []persistdb.MirrorEqClass,
	eqClassDigestRows []persistdb.MirrorEqClassDigest,
	termRows []persistdb.MirrorTerm,
	termInputRows []persistdb.MirrorTermInput,
	resultOutputEqClassRows []persistdb.MirrorResultOutputEqClass,
	persistedEdgeRows []persistdb.MirrorPersistedEdge,
	resultCallRows []persistdb.MirrorResultCall,
	resultTaughtRows []persistdb.MirrorResultTaught,
	resultDepRows []persistdb.MirrorResultDep,
	resultSnapshotRows []persistdb.MirrorResultSnapshotLink,
) persistKept {
	var kept persistKept
	kept.entries = make([]keptEntry, len(resultRows))
	type entryPrints struct {
		deps, classes, calls, taught, edge, links rowsPrint
	}
	prints := make([]entryPrints, len(resultRows))
	at := func(id int64) *entryPrints {
		i, found := slices.BinarySearchFunc(kept.entries, sharedResultID(id), func(e keptEntry, id sharedResultID) int {
			return cmp.Compare(e.id, id)
		})
		if !found {
			return nil
		}
		return &prints[i]
	}
	for i := range resultRows {
		kept.entries[i].id = sharedResultID(resultRows[i].ID)
	}
	for _, row := range resultDepRows {
		if p := at(row.ParentResultID); p != nil {
			p.deps.add(rowPrint(seed, row))
		}
	}
	for _, row := range resultOutputEqClassRows {
		if p := at(row.ResultID); p != nil {
			p.classes.add(rowPrint(seed, row))
		}
	}
	for _, row := range resultCallRows {
		if p := at(row.ResultID); p != nil {
			p.calls.add(rowPrint(seed, row))
		}
	}
	for _, row := range resultTaughtRows {
		if p := at(row.ResultID); p != nil {
			p.taught.add(rowPrint(seed, row))
		}
	}
	for _, row := range persistedEdgeRows {
		if p := at(row.ResultID); p != nil {
			p.edge.add(rowPrint(seed, row))
		}
	}
	for _, row := range resultSnapshotRows {
		if p := at(row.ResultID); p != nil {
			p.links.add(rowPrint(seed, row))
		}
	}
	for i := range resultRows {
		e, p := &kept.entries[i], &prints[i]
		e.row = resultRowPrint(seed, resultRows[i].MirrorResult)
		e.record = recordPrint(seed, resultRows[i].recordBytes, p.links)
		e.deps = p.deps.value(seed)
		e.classes = p.classes.value(seed)
		e.calls = p.calls.value(seed)
		e.taught = p.taught.value(seed)
		e.edge = p.edge.value(seed)
	}

	kept.terms = make([]keptUnit, len(termRows))
	termInputs := make([]rowsPrint, len(termRows))
	for i, row := range termRows {
		kept.terms[i].id = uint64(row.ID)
	}
	slices.SortFunc(kept.terms, func(a, b keptUnit) int { return cmp.Compare(a.id, b.id) })
	termAt := func(id int64) int {
		i, found := slices.BinarySearchFunc(kept.terms, uint64(id), func(u keptUnit, id uint64) int { return cmp.Compare(u.id, id) })
		if !found {
			return -1
		}
		return i
	}
	for _, row := range termInputRows {
		if i := termAt(row.TermID); i >= 0 {
			termInputs[i].add(rowPrint(seed, row))
		}
	}
	for _, row := range termRows {
		i := termAt(row.ID)
		kept.terms[i].fp = termPrint(seed, row.SelfDigest, row.OutputEqClassID, termInputs[i])
	}

	kept.classes = make([]keptUnit, len(eqClassRows))
	classDigests := make([]rowsPrint, len(eqClassRows))
	for i, row := range eqClassRows {
		kept.classes[i].id = uint64(row.ID)
	}
	slices.SortFunc(kept.classes, func(a, b keptUnit) int { return cmp.Compare(a.id, b.id) })
	for _, row := range eqClassDigestRows {
		i, found := slices.BinarySearchFunc(kept.classes, uint64(row.EqClassID), func(u keptUnit, id uint64) int { return cmp.Compare(u.id, id) })
		if found {
			classDigests[i].add(rowPrint(seed, row))
		}
	}
	for i := range kept.classes {
		kept.classes[i].fp = classDigests[i].value(seed)
	}
	return kept
}

// keepRestoredRecords sets, in what the database holds, each restored
// entry's count of writes to its record and the fingerprint of the fields its
// record takes, as the restore left them: the record row the database holds
// is the record as it was restored. It then keeps kept as what the database
// holds (Cache.persistKept).
func (c *Cache) keepRestoredRecords(kept *persistKept) error {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for i := range kept.entries {
		e := &kept.entries[i]
		res := c.resultsByID[e.id]
		if res == nil {
			continue
		}
		e.recordWrites = res.loadPayloadState().recordWrites
		offers, err := res.pendingOffersLocked()
		if err != nil {
			return err
		}
		if e.recordInputs, err = recordInputsPrint(c.persistSeed, res.persistDeclined, savedRunLocked(res), res.runRan, offers); err != nil {
			return fmt.Errorf("restored result %d: %w", e.id, err)
		}
	}
	c.persistKept = *kept
	return nil
}
