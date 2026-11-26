package dagql

import (
	"cmp"
	"fmt"
	"hash/maphash"
	"slices"
	"time"

	persistdb "github.com/dagger/dagger/dagql/persistdb"
)

// A save reads the store holding egraphMu for reading in pieces, letting it
// go between them (readInPiecesFor), so that a writer that comes during the
// read waits for one piece and not the whole store, and so does every reader
// queued behind that writer. While a save reads, the writers of what it saves
// note the entries, terms and classes they change (persistNotes). After its
// walk of the whole store, the save reads again what was noted, in pieces
// too, until what is left fits in one piece; then, in one last hold, it reads
// again what was noted since and nothing else. A unit nobody noted since the
// save read it is as it was then, so what the save has read is the store as
// it was in that last hold. Which entries it keeps, the closure of the stored
// roots, it then works out on its own copy with no lock held (assemble).
//
// What changes under egraphMu held for reading is not noted, and a save reads
// it as a save under one hold did, at some moment of its read: an entry's
// record, whose write count is its mark (recordLock), and its last use, which
// its results row carries and every save compares.

// persistPieceHold is how long a save holds egraphMu for reading in one
// piece of its read. A writer waits for at most one piece behind a save.
// When writers always wait, as they do at a fill's peak, each piece costs the
// save one writer's hold: the save then reads for about this long between two
// of them, and its read takes about 1 + h/persistPieceHold times its reading
// time, h the writers' mean hold.
const persistPieceHold = 10 * time.Millisecond

// persistCatchUpRounds bounds the rounds in which a save reads again in
// pieces what writers noted during its read, before its last hold.
const persistCatchUpRounds = 3

// persistNotes are the entries, terms and classes whose saved rows a write
// changed while a save read the store, for the save to read again. Writers
// note under egraphMu held for writing, and a save takes the notes under it
// held for reading, which no writer can hold meanwhile. Guarded by egraphMu.
type persistNotes struct {
	// reading is set while a save reads the store.
	reading bool
	entries []sharedResultID
	terms   []egraphTermID
	classes []eqClassID
}

// testPersistNotesAlways, set in tests, has writers note what they change at
// every write, not only while a save reads: each save then checks that every
// unit it finds changed since the save before was noted (checkPersistNotes).
var testPersistNotesAlways bool

func (n *persistNotes) taking() bool {
	return n.reading || testPersistNotesAlways
}

func (n *persistNotes) count() int {
	return len(n.entries) + len(n.terms) + len(n.classes)
}

// take returns the notes made so far, and starts afresh.
func (n *persistNotes) take() persistNotes {
	taken := persistNotes{entries: n.entries, terms: n.terms, classes: n.classes}
	n.entries, n.terms, n.classes = nil, nil, nil
	return taken
}

func (n *persistNotes) add(o persistNotes) {
	n.entries = append(n.entries, o.entries...)
	n.terms = append(n.terms, o.terms...)
	n.classes = append(n.classes, o.classes...)
}

// noteSavedEntryLocked notes that a write changed what a save writes of the
// entry numbered id, or that the entry came or went. Requires egraphMu for
// writing.
func (c *Cache) noteSavedEntryLocked(id sharedResultID) {
	if id == 0 || !c.persistNotes.taking() {
		return
	}
	c.persistNotes.entries = append(c.persistNotes.entries, id)
}

// noteSavedTermLocked notes that a write changed what a save writes of the
// term numbered id, or that the term came or went. Requires egraphMu for
// writing.
func (c *Cache) noteSavedTermLocked(id egraphTermID) {
	if id == 0 || !c.persistNotes.taking() {
		return
	}
	c.persistNotes.terms = append(c.persistNotes.terms, id)
}

// noteSavedClassLocked notes that a write changed what a save writes of the
// class numbered id, or of the rows that name it, resolved to their roots:
// its digests, its root, or whether it lives. Requires egraphMu for writing.
func (c *Cache) noteSavedClassLocked(id eqClassID) {
	if id == 0 || !c.persistNotes.taking() {
		return
	}
	c.persistNotes.classes = append(c.persistNotes.classes, id)
}

// savingLocked reports whether a save is reading the store: no class number
// is taken again meanwhile, and classes are not compacted, so that a number
// a save has read names one class for the whole read. Requires egraphMu.
func (c *Cache) savingLocked() bool {
	return c.persistNotes.reading
}

// persistRead is what a save has read of the store: each entry, term and
// class as it last read it, from which assemble works out the save with no
// lock held. The walk reads each unit once; a unit read again after it
// (again) replaces its walk's read.
type persistRead struct {
	kept    *persistKept
	seed    maphash.Seed
	entries []persistEntryRead
	terms   []persistTermRead
	classes []persistClassRead
	// again holds the reads made after the walk, in the order made, and
	// rereading says that reads go there.
	again struct {
		entries []persistEntryRead
		terms   []persistTermRead
		classes []persistClassRead
	}
	rereading bool
	// The scratch rows of the unit being read: a unit whose rows moved
	// keeps a copy of them.
	deps      []persistdb.MirrorResultDep
	classRows []persistdb.MirrorResultOutputEqClass
	calls     []persistdb.MirrorResultCall
	taught    []persistdb.MirrorResultTaught
	inputs    []persistdb.MirrorTermInput
	inputIDs  []eqClassID
	digests   []persistdb.MirrorEqClassDigest
	// cursor is where the walk, which reads each kind of unit in number
	// order, is in kept's list of that kind. A unit read again after the
	// walk is found in kept by a search.
	cursor struct {
		entries, terms, classes int
	}
	// edges holds each entry read's ownership edges, and ids each entry
	// read's output classes and each term read's input classes, all
	// resolved to their roots.
	edges []sharedResultID
	ids   []eqClassID
	// units counts the units read.
	units int
	// prints are, in tests that note always, each unit's fingerprint of
	// what changes under egraphMu held for writing (checkPersistNotes).
	prints *persistPrints
}

// persistEntryRead is what a save read of one entry.
type persistEntryRead struct {
	id sharedResultID
	// present says the cache held the entry when it was read; nothing else
	// is read of one it didn't.
	present bool
	// valid says the entry is attached cleanly with its payload prepared,
	// root that it is a stored root.
	valid, root bool
	// edges and classes are where its ownership edges and its output
	// classes are, in the read's edges and ids.
	edgeAt, edgeN   int32
	classAt, classN int32
	// next is its fingerprints, for the save's next kept table, and result
	// what the save writes of it when any moved from what the database
	// holds.
	next   keptEntry
	result *persistResultSnapshot
}

// persistTermRead is what a save read of one term.
type persistTermRead struct {
	id      egraphTermID
	present bool
	output  eqClassID
	inAt    int32
	inN     int32
	fp      uint64
	term    *persistTermSnapshot
}

// persistClassRead is what a save read of one class: present only for a
// class that is its own root.
type persistClassRead struct {
	id      eqClassID
	present bool
	fp      uint64
	class   *persistClassSnapshot
}

func newPersistRead(kept *persistKept, seed maphash.Seed) *persistRead {
	rd := &persistRead{kept: kept, seed: seed}
	if testPersistNotesAlways {
		rd.prints = newPersistPrints()
	}
	return rd
}

// sizeFor makes room for a walk of the store as it is: one read of each
// entry, term and live class, and of as many edges as the last save read,
// with room for a little growth, so that no slice grows by copying. Requires
// egraphMu for reading.
func (rd *persistRead) sizeFor(c *Cache) {
	room := func(n int) int { return n + n/16 }
	rd.entries = slices.Grow(rd.entries, room(len(c.resultsByID)))
	rd.terms = slices.Grow(rd.terms, room(len(c.egraphTerms)))
	rd.classes = slices.Grow(rd.classes, room(c.liveEqClasses))
	rd.edges = slices.Grow(rd.edges, room(rd.kept.edges))
}

func (rd *persistRead) addEntry(e persistEntryRead) {
	if rd.rereading {
		rd.again.entries = append(rd.again.entries, e)
	} else {
		rd.entries = append(rd.entries, e)
	}
}

func (rd *persistRead) addTerm(t persistTermRead) {
	if rd.rereading {
		rd.again.terms = append(rd.again.terms, t)
	} else {
		rd.terms = append(rd.terms, t)
	}
}

func (rd *persistRead) addClass(k persistClassRead) {
	if rd.rereading {
		rd.again.classes = append(rd.again.classes, k)
	} else {
		rd.classes = append(rd.classes, k)
	}
}

// keptAt finds the unit numbered id in kept's sorted list: from the walk's
// cursor, which only moves forward, or, for a unit read again, by a search.
func keptAt[T any](list []T, cursor *int, rereading bool, id uint64, idOf func(*T) uint64) (T, bool) {
	var zero T
	if rereading {
		i, found := slices.BinarySearchFunc(list, id, func(u T, id uint64) int { return cmp.Compare(idOf(&u), id) })
		if !found {
			return zero, false
		}
		return list[i], true
	}
	for *cursor < len(list) && idOf(&list[*cursor]) < id {
		*cursor++
	}
	if *cursor < len(list) && idOf(&list[*cursor]) == id {
		return list[*cursor], true
	}
	return zero, false
}

func keptEntryID(e *keptEntry) uint64 { return uint64(e.id) }
func keptUnitID(u *keptUnit) uint64   { return u.id }

func (rd *persistRead) keptEntry(id sharedResultID) (keptEntry, bool) {
	return keptAt(rd.kept.entries, &rd.cursor.entries, rd.rereading, uint64(id), keptEntryID)
}

// readEntryLocked reads the entry numbered id, res or nil if the cache
// doesn't hold it: what the closure of the stored roots needs, its
// fingerprints, and, when any moved from what the database holds, what the
// save writes of it. Requires egraphMu for reading.
//
//nolint:gocyclo // one entry's every kind of row, in the order the save writes them
func (c *Cache) readEntryLocked(rd *persistRead, id sharedResultID, res *sharedResult) error {
	rd.units++
	if c.testPersistRead != nil {
		c.testPersistRead()
	}
	if res == nil {
		rd.addEntry(persistEntryRead{id: id})
		if rd.prints != nil {
			delete(rd.prints.entries, id)
		}
		return nil
	}
	seed := rd.seed
	e := persistEntryRead{id: id, present: true}
	// A record whose payload is still as the Cloud sent it names rows by the
	// Cloud's numbers: one refused at its preparation
	// (prepareMergedPayloadsForSave) is left out, with all that owns it.
	e.valid = res.attachmentState() == resultAttachmentClean && !res.holdsUnpreparedPayload()
	e.edgeAt = int32(len(rd.edges))
	for depID := range c.ownedResultIDsLocked(res) {
		rd.edges = append(rd.edges, depID)
	}
	e.edgeN = int32(len(rd.edges)) - e.edgeAt

	prev, held := rd.keptEntry(id)
	next := keptEntry{id: id}

	deps := rd.deps[:0]
	var depsPrint rowsPrint
	for _, depID := range res.deps {
		row := persistdb.MirrorResultDep{ParentResultID: int64(id), DepResultID: int64(depID)}
		depsPrint.add(rowPrint(seed, row))
		deps = append(deps, row)
	}
	next.deps = depsPrint.value(seed)
	rd.deps = deps

	classes := rd.classRows[:0]
	var classesPrint rowsPrint
	e.classAt = int32(len(rd.ids))
	for outputEqID := range c.resultOutputEqClasses[id].All() {
		outputEqID = c.eqClassRootLocked(outputEqID)
		// Two of the entry's classes joined since name one root once.
		if outputEqID == 0 || slices.Contains(rd.ids[e.classAt:], outputEqID) {
			continue
		}
		rd.ids = append(rd.ids, outputEqID)
		row := persistdb.MirrorResultOutputEqClass{ResultID: int64(id), EqClassID: int64(outputEqID)}
		classesPrint.add(rowPrint(seed, row))
		classes = append(classes, row)
	}
	e.classN = int32(len(rd.ids)) - e.classAt
	next.classes = classesPrint.value(seed)
	rd.classRows = classes

	calls := rd.calls[:0]
	var callsPrint rowsPrint
	for request, call := range res.keptCallsLocked() {
		row := persistdb.MirrorResultCall{ResultID: int64(id), RequestDigest: request.String(), Call: call.encoded}
		callsPrint.add(rowPrint(seed, row))
		calls = append(calls, row)
	}
	next.calls = callsPrint.value(seed)
	rd.calls = calls

	taught := rd.taught[:0]
	var taughtPrint rowsPrint
	if tc := res.taughtContent; tc != nil {
		for i, digest := range tc.digests {
			row := persistdb.MirrorResultTaught{ResultID: int64(id), Position: int64(i), Digest: digest.String(), Given: i < tc.given}
			taughtPrint.add(rowPrint(seed, row))
			taught = append(taught, row)
		}
	}
	next.taught = taughtPrint.value(seed)
	rd.taught = taught

	var edge *persistdb.MirrorPersistedEdge
	var edgePrint rowsPrint
	if pe, root := c.persistedEdgesByResult[id]; root {
		e.root = true
		edge = &persistdb.MirrorPersistedEdge{
			ResultID:          int64(id),
			CreatedAtUnixNano: pe.createdAtUnixNano,
			ExpiresAtUnix:     pe.expiresAtUnix,
			Unpruneable:       pe.unpruneable,
		}
		edgePrint.add(rowPrint(seed, *edge))
	}
	next.edge = edgePrint.value(seed)

	// The record's write count is read before the record itself
	// (recordLock): a write the save misses moves it past what the save
	// keeps.
	payload := res.loadPayloadState()
	offers, err := res.pendingOffersLocked()
	if err != nil {
		return err
	}
	cloudKey, cloud := res.cloudHoldingLocked()
	var cloudStored bool
	var cloudExpiresAtUnix int64
	if cloud != nil {
		cloudStored = !cloud.unstored
		cloudExpiresAtUnix = cloud.expiresAtUnix
	}
	row := persistdb.MirrorResult{
		ID:                        int64(id),
		ExpiresAtUnix:             res.expiresAtUnix,
		Replacements:              int64(res.replacements),
		Indexed:                   len(res.recipeKeys) > 0,
		CloudHoldingNumber:        int64(cloudKey.Number),
		CloudHoldingStored:        cloudStored,
		CloudHoldingExpiresAtUnix: cloudExpiresAtUnix,
		CreatedAtUnixNano:         payload.createdAtUnixNano,
		LastUsedAtUnixNano:        payload.lastUsedAtUnixNano,
		RecordType:                res.recordType,
		Description:               res.description,
		ChangeStamp:               int64(res.changedAt),
	}
	next.row = resultRowPrint(seed, row)
	run := savedRunLocked(res)
	if next.recordInputs, err = recordInputsPrint(seed, res.persistDeclined, run, res.runRan, offers); err != nil {
		return fmt.Errorf("persist result %d: %w", id, err)
	}
	next.recordWrites = payload.recordWrites
	// A record held encoded is the one the database holds while its write
	// count and the fields it takes haven't moved. A live value can change
	// inside itself: it is encoded and compared.
	live := payload.persistedEnvelope == nil && payload.hasValue && payload.self != nil
	next.record = prev.record
	e.next = next
	if rd.prints != nil {
		rd.prints.noteEntry(seed, &e, row)
	}

	var writes persistEntryWrites
	if !held || next.row != prev.row {
		writes |= persistWriteRow
	}
	if !held || live || next.recordWrites != prev.recordWrites || next.recordInputs != prev.recordInputs {
		writes |= persistWriteRecord
	}
	if !held || next.deps != prev.deps {
		writes |= persistWriteDeps
	}
	if !held || next.classes != prev.classes {
		writes |= persistWriteClasses
	}
	if !held || next.calls != prev.calls {
		writes |= persistWriteCalls
	}
	if !held || next.taught != prev.taught {
		writes |= persistWriteTaught
	}
	if !held || next.edge != prev.edge {
		writes |= persistWriteEdge
	}
	if writes != 0 {
		result := &persistResultSnapshot{
			resultID:   id,
			row:        row,
			write:      writes,
			held:       held,
			keptRecord: prev.record,
		}
		if writes&persistWriteDeps != 0 {
			result.resultDeps = slices.Clone(deps)
		}
		if writes&persistWriteClasses != 0 {
			result.outputEqClasses = slices.Clone(classes)
		}
		if writes&persistWriteCalls != 0 {
			result.calls = slices.Clone(calls)
		}
		if writes&persistWriteTaught != 0 {
			result.taught = slices.Clone(taught)
		}
		if writes&persistWriteEdge != 0 {
			result.edge = edge
		}
		if writes&persistWriteRecord != 0 {
			if payload.snapshotLinkIntent != nil {
				payload.snapshotOwnerLinks = cloneSnapshotRefLinks(payload.snapshotLinkIntent.Links)
			}
			result.imported = res.imported
			result.persistDeclined = res.persistDeclined
			result.boundSession = res.boundSession
			result.run = run
			result.runRan = res.runRan
			result.pendingOffers = offers
			result.storedParts = res.storedPartsLocked()
			result.frame = res.loadResultCall().clone()
			result.self = payload.self
			result.isObject = payload.isObject
			result.hasValue = payload.hasValue
			result.sessionResourceHandle = res.sessionResourceHandle
			result.persistedEnvelope = payload.persistedEnvelope
			result.stored = payload.stored
			result.partFacts = payload.partFacts
			result.snapshotOwnerLinks = payload.snapshotOwnerLinks
		}
		e.result = result
	}
	rd.addEntry(e)
	return nil
}

// readTermLocked reads the term numbered id, term or nil if the cache doesn't
// hold it. Requires egraphMu for reading.
func (c *Cache) readTermLocked(rd *persistRead, id egraphTermID, term *egraphTerm) error {
	rd.units++
	if term == nil {
		rd.addTerm(persistTermRead{id: id})
		if rd.prints != nil {
			delete(rd.prints.terms, id)
		}
		return nil
	}
	seed := rd.seed
	t := persistTermRead{id: id, present: true, output: c.eqClassRootLocked(term.outputEqID)}
	inputProvenance := c.termInputProvenance[id]
	if len(inputProvenance) != len(term.inputEqIDs) {
		return fmt.Errorf("persist term %d: input provenance len %d does not match input eq IDs len %d", id, len(inputProvenance), len(term.inputEqIDs))
	}
	inputs := rd.inputs[:0]
	var inputsPrint rowsPrint
	t.inAt = int32(len(rd.ids))
	inputEqIDs := rd.inputIDs[:0]
	for i, inputEqID := range term.inputEqIDs {
		inputEqID = c.eqClassRootLocked(inputEqID)
		inputEqIDs = append(inputEqIDs, inputEqID)
		rd.ids = append(rd.ids, inputEqID)
		row := persistdb.MirrorTermInput{
			TermID:         int64(id),
			Position:       int64(i),
			InputEqClassID: int64(inputEqID),
			ProvenanceKind: inputProvenance[i].String(),
		}
		inputsPrint.add(rowPrint(seed, row))
		inputs = append(inputs, row)
	}
	t.inN = int32(len(rd.ids)) - t.inAt
	rd.inputs, rd.inputIDs = inputs, inputEqIDs
	selfDigest := term.selfDigest.String()
	t.fp = termPrint(seed, selfDigest, int64(t.output), inputsPrint)
	if rd.prints != nil {
		rd.prints.terms[id] = t.fp
	}
	if prev, held := keptAt(rd.kept.terms, &rd.cursor.terms, rd.rereading, uint64(id), keptUnitID); !held || prev.fp != t.fp {
		t.term = &persistTermSnapshot{
			row: persistdb.MirrorTerm{
				ID:              int64(id),
				SelfDigest:      selfDigest,
				TermDigest:      calcEgraphTermDigest(term.selfDigest, inputEqIDs),
				OutputEqClassID: int64(t.output),
			},
			inputs: slices.Clone(inputs),
			held:   held,
		}
	}
	rd.addTerm(t)
	return nil
}

// readClassLocked reads the class numbered id: present only if it is its own
// root, with the digests a save writes for it. Requires egraphMu for reading.
func (c *Cache) readClassLocked(rd *persistRead, id eqClassID) {
	rd.units++
	if int(id) >= len(c.egraphParents) || c.egraphParents[id] != id {
		rd.addClass(persistClassRead{id: id})
		return
	}
	seed := rd.seed
	rows := rd.digests[:0]
	for dig := range c.eqClassToDigests[id].All() {
		if dig != "" {
			rows = append(rows, persistdb.MirrorEqClassDigest{EqClassID: int64(id), Digest: dig})
		}
	}
	for extra := range c.eqClassExtraDigests[id] {
		if extra.Digest == "" {
			continue
		}
		dig := extra.Digest.String()
		rows = append(rows,
			persistdb.MirrorEqClassDigest{EqClassID: int64(id), Digest: dig},
			persistdb.MirrorEqClassDigest{EqClassID: int64(id), Digest: dig, Label: extra.Label})
	}
	// A digest the class holds and an extra digest of it name one row once.
	slices.SortFunc(rows, func(a, b persistdb.MirrorEqClassDigest) int {
		return cmp.Or(cmp.Compare(a.Digest, b.Digest), cmp.Compare(a.Label, b.Label))
	})
	rows = slices.Compact(rows)
	rd.digests = rows
	var digestsPrint rowsPrint
	for _, row := range rows {
		digestsPrint.add(rowPrint(seed, row))
	}
	k := persistClassRead{id: id, present: true, fp: digestsPrint.value(seed)}
	if prev, held := keptAt(rd.kept.classes, &rd.cursor.classes, rd.rereading, uint64(id), keptUnitID); !held || prev.fp != k.fp {
		k.class = &persistClassSnapshot{id: int64(id), held: held, digests: slices.Clone(rows)}
	}
	rd.addClass(k)
}

// walkStoreLocked reads every entry, term and class of the store, each kind
// in number order, taking a step of r after each, which lets egraphMu go
// between two pieces; a nil r reads it all in the hold the caller has.
// Requires egraphMu for reading.
//
// It first collects the numbers of the entries and of the terms by ranging
// over the cache's maps across the holds it lets go, and sorts them with no
// lock held. A map may change between two steps of a range: every key there
// at the start and still there is visited once, and one added or deleted
// meanwhile may or may not be. A writer notes each of those, and the save
// reads it again. Classes are read by number, through the union-find's
// slots.
func (c *Cache) walkStoreLocked(rd *persistRead, r *readPieces) error {
	rd.sizeFor(c)
	step := func() {
		if r != nil {
			r.step()
		}
	}
	sorted := func(sortIDs func()) {
		if r == nil {
			sortIDs()
			return
		}
		r.without(sortIDs)
	}
	resultIDs := make([]sharedResultID, 0, len(c.resultsByID))
	for id := range c.resultsByID {
		resultIDs = append(resultIDs, id)
		step()
	}
	sorted(func() { slices.Sort(resultIDs) })
	for _, id := range resultIDs {
		if err := c.readEntryLocked(rd, id, c.resultsByID[id]); err != nil {
			return err
		}
		step()
	}
	termIDs := make([]egraphTermID, 0, len(c.egraphTerms))
	for id := range c.egraphTerms {
		termIDs = append(termIDs, id)
		step()
	}
	sorted(func() { slices.Sort(termIDs) })
	for _, id := range termIDs {
		if err := c.readTermLocked(rd, id, c.egraphTerms[id]); err != nil {
			return err
		}
		step()
	}
	for id := eqClassID(1); int(id) < len(c.egraphParents); id++ {
		if c.egraphParents[id] != id {
			continue
		}
		c.readClassLocked(rd, id)
		step()
	}
	return nil
}

// readNotedLocked reads again each unit noted, taking a step of r after
// each, or none with a nil r, and returns how many it read. Requires
// egraphMu for reading.
func (c *Cache) readNotedLocked(rd *persistRead, notes persistNotes, r *readPieces) (int, error) {
	read := 0
	slices.Sort(notes.entries)
	for _, id := range slices.Compact(notes.entries) {
		if err := c.readEntryLocked(rd, id, c.resultsByID[id]); err != nil {
			return read, err
		}
		read++
		if r != nil {
			r.step()
		}
	}
	slices.Sort(notes.terms)
	for _, id := range slices.Compact(notes.terms) {
		if err := c.readTermLocked(rd, id, c.egraphTerms[id]); err != nil {
			return read, err
		}
		read++
		if r != nil {
			r.step()
		}
	}
	slices.Sort(notes.classes)
	for _, id := range slices.Compact(notes.classes) {
		c.readClassLocked(rd, id)
		read++
		if r != nil {
			r.step()
		}
	}
	return read, nil
}

// persistWalk is how a save's read of the store went, for its log line and
// its tests.
type persistWalk struct {
	// longest is the longest a single hold of egraphMu lasted, held their
	// total, and took the read's whole time, from its first hold to the end
	// of its last.
	longest, held, took time.Duration
	// pieces counts the holds, rounds the rounds that read noted units
	// again in pieces, and last the units the last hold read again.
	pieces, rounds, last int
}

// readStore reads the store for a save: a walk of every unit in pieces,
// rounds that read again in pieces what writers noted meanwhile, and a last
// hold that reads again what was noted since. It returns what it read, how
// the read went, the next result number and the write count as of the last
// hold, and, in tests that check saves (testPersistFullSave), a read of the
// whole store taken in that last hold.
func (c *Cache) readStore() (rd *persistRead, walk persistWalk, nextResultID sharedResultID, writes uint64, full *persistRead, rerr error) {
	rd = newPersistRead(&c.persistKept, c.persistSeed)
	r := c.readInPiecesFor(persistPieceHold)
	r.report = func(d time.Duration) {
		walk.held += d
		if d > walk.longest {
			walk.longest = d
		}
		walk.pieces++
	}
	started := time.Now()
	// What was noted since the last save, in tests that note always, is
	// what testPersistNotes holds this save's changes against.
	since := c.persistNotes.take()
	c.persistNotes.reading = true
	defer func() {
		if rerr != nil {
			// A save that fails leaves the notes for the next one, which
			// reads the whole store again anyway.
			c.persistNotes.reading = false
			c.persistNotes.add(since)
		}
		r.done()
		walk.took = time.Since(started)
	}()
	fail := func(err error) (*persistRead, persistWalk, sharedResultID, uint64, *persistRead, error) {
		return nil, walk, 0, 0, nil, err
	}
	if err := c.walkStoreLocked(rd, r); err != nil {
		return fail(err)
	}
	if c.testPersistWalked != nil {
		r.without(c.testPersistWalked)
	}
	rd.rereading = true
	// What fits in one piece is read in the last hold.
	perPiece := rd.units / (r.yields() + 1)
	notes := c.persistNotes.take()
	since.add(notes)
	for ; walk.rounds < persistCatchUpRounds && notes.count() > perPiece; walk.rounds++ {
		if _, err := c.readNotedLocked(rd, notes, r); err != nil {
			return fail(err)
		}
		notes = c.persistNotes.take()
		since.add(notes)
	}
	// The last hold starts afresh, with what was noted before it: it holds
	// egraphMu for reading only as long as reading those again takes.
	r.yield()
	last := c.persistNotes.take()
	since.add(last)
	notes.add(last)
	var err error
	if walk.last, err = c.readNotedLocked(rd, notes, nil); err != nil {
		return fail(err)
	}
	// Every entry the save keeps was installed under egraphMu, so its number
	// is below this, whatever a merge reserves meanwhile (takeResultIDs). A
	// write applies its stamps before it lets go of egraphMu
	// (stampPendingLocked), so every row's stamp is at most this count.
	nextResultID = sharedResultID(c.nextSharedResultID.Load())
	writes = c.writes
	// A save into a database that holds nothing writes everything, as a full
	// save does: only one that could leave something out is checked.
	if testPersistFullSave != nil && !c.persistKept.empty() {
		full = newPersistRead(&persistKept{}, c.persistSeed)
		full.prints = nil
		if err := c.walkStoreLocked(full, nil); err != nil {
			return fail(err)
		}
	}
	c.persistNotes.reading = false
	if rd.prints != nil {
		rd.prints.noted = since
	}
	return rd, walk, nextResultID, writes, full, nil
}

// persistPrints are, in tests that note always (testPersistNotesAlways),
// what a save read of each unit that changes only under egraphMu held for
// writing: each entry's fingerprints without its last use and its record,
// each term's fingerprint, and each class's that a unit names. noted is what
// was noted since the save before. A test checks each save's prints against
// the save before's (testPersistNotes): a unit that came, went or changed
// without a note is a write that doesn't note.
type persistPrints struct {
	entries map[sharedResultID]persistEntryPrint
	terms   map[egraphTermID]uint64
	classes map[eqClassID]uint64
	noted   persistNotes
}

func newPersistPrints() *persistPrints {
	return &persistPrints{entries: map[sharedResultID]persistEntryPrint{}, terms: map[egraphTermID]uint64{}, classes: map[eqClassID]uint64{}}
}

// persistEntryPrint is an entry's fingerprints but its record's, with its
// results row's taken without its last use.
type persistEntryPrint struct {
	Row, RecordInputs, Deps, Classes, Calls, Taught, Edge uint64
}

// noteEntry keeps an entry read's fingerprints without its last use, which a
// lookup moves under egraphMu held for reading, and its record, which its
// write count covers.
func (p *persistPrints) noteEntry(seed maphash.Seed, e *persistEntryRead, row persistdb.MirrorResult) {
	row.LastUsedAtUnixNano = 0
	p.entries[e.id] = persistEntryPrint{
		Row:          resultRowPrint(seed, row),
		RecordInputs: e.next.recordInputs,
		Deps:         e.next.deps,
		Classes:      e.next.classes,
		Calls:        e.next.calls,
		Taught:       e.next.taught,
		Edge:         e.next.edge,
	}
}

// testPersistNotes, when set, is called after each save's read of the store
// with the prints of the read before and of this one (persistPrints).
var testPersistNotes func(c *Cache, before, after *persistPrints)

// printClasses keeps the fingerprint of each class a present entry or term
// read names, as its root, when it was read as one.
func (rd *persistRead) printClasses(entries []persistEntryRead) {
	named := make(map[eqClassID]struct{})
	for i := range entries {
		e := &entries[i]
		for _, id := range rd.ids[e.classAt : e.classAt+e.classN] {
			named[id] = struct{}{}
		}
	}
	for i := range rd.terms {
		t := &rd.terms[i]
		if !t.present {
			continue
		}
		named[t.output] = struct{}{}
		for _, id := range rd.ids[t.inAt : t.inAt+t.inN] {
			named[id] = struct{}{}
		}
	}
	for i := range rd.classes {
		k := &rd.classes[i]
		if _, ok := named[k.id]; ok && k.present {
			rd.prints.classes[k.id] = k.fp
		}
	}
}

// readAgain lays the reads made after the walk over the walk's, which are in
// number order with each number once: a number read again takes its last
// read, in place, and a number only read again, as a unit made after the walk
// passed its number, is merged in, moving the walk's reads from the end.
func readAgain[T any](walk, again []T, id func(*T) uint64) []T {
	var only []T
	for i := range again {
		n := id(&again[i])
		if at, found := slices.BinarySearchFunc(walk, n, func(u T, n uint64) int { return cmp.Compare(id(&u), n) }); found {
			walk[at] = again[i]
			continue
		}
		if at, found := slices.BinarySearchFunc(only, n, func(u T, n uint64) int { return cmp.Compare(id(&u), n) }); found {
			only[at] = again[i]
		} else {
			only = slices.Insert(only, at, again[i])
		}
	}
	if len(only) == 0 {
		return walk
	}
	n := len(walk)
	walk = slices.Grow(walk, len(only))[:n+len(only)]
	i, j := n-1, len(only)-1
	for k := len(walk) - 1; j >= 0; k-- {
		if i >= 0 && id(&walk[i]) > id(&only[j]) {
			walk[k] = walk[i]
			i--
		} else {
			walk[k] = only[j]
			j--
		}
	}
	return walk
}

// noEntry is an ownership edge to an entry the store doesn't hold, once the
// edges are turned into entries' indexes.
const noEntry = ^sharedResultID(0)

// assemble works out a save from what readStore read, holding no lock: the
// closure of the stored roots, with entries that aren't valid carried up to
// what owns them; the terms whose output class a kept entry names; the
// classes kept entries and terms name; what of each the database doesn't
// hold as it is, and what the database holds that the store no longer does.
//
//nolint:gocyclo // the closure, then each kind of unit in turn
func (rd *persistRead) assemble(nextResultID sharedResultID, writes uint64) (persistStateSnapshot, error) {
	var snapshot persistStateSnapshot
	snapshot.nextResultID = nextResultID
	snapshot.writes = writes

	rd.entries = readAgain(rd.entries, rd.again.entries, func(e *persistEntryRead) uint64 { return uint64(e.id) })
	rd.terms = readAgain(rd.terms, rd.again.terms, func(t *persistTermRead) uint64 { return uint64(t.id) })
	rd.classes = readAgain(rd.classes, rd.again.classes, func(k *persistClassRead) uint64 { return uint64(k.id) })
	rd.again.entries, rd.again.terms, rd.again.classes = nil, nil, nil
	entries := slices.DeleteFunc(rd.entries, func(e persistEntryRead) bool { return !e.present })
	if rd.prints != nil {
		rd.printClasses(entries)
	}

	// The closure: an entry that isn't valid, or owns one the store doesn't
	// hold, is invalid, and so is everything that owns it; the stored roots
	// that are valid, and what they own, are kept. Each entry's ownership
	// edges are turned into indexes of entries in place, and the next save
	// sizes its edges by their count.
	index := make(map[sharedResultID]int32, len(entries))
	for i := range entries {
		index[entries[i].id] = int32(i)
	}
	invalid := make([]bool, len(entries))
	var queue []int32
	for i := range entries {
		e := &entries[i]
		snapshot.next.edges += int(e.edgeN)
		for k := e.edgeAt; k < e.edgeAt+e.edgeN; k++ {
			j, found := index[rd.edges[k]]
			if !found {
				rd.edges[k] = noEntry
				e.valid = false
				continue
			}
			rd.edges[k] = sharedResultID(j)
		}
		if !e.valid {
			invalid[i] = true
			queue = append(queue, int32(i))
		}
	}
	owned := func(i int32) []sharedResultID {
		e := &entries[i]
		return rd.edges[e.edgeAt : e.edgeAt+e.edgeN]
	}
	if len(queue) > 0 {
		owners := make([][]int32, len(entries))
		for i := range entries {
			for _, j := range owned(int32(i)) {
				if j != noEntry {
					owners[j] = append(owners[j], int32(i))
				}
			}
		}
		for len(queue) > 0 {
			i := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			for _, owner := range owners[i] {
				if !invalid[owner] {
					invalid[owner] = true
					queue = append(queue, owner)
				}
			}
		}
	}
	selected := make([]bool, len(entries))
	var stack []int32
	for i := range entries {
		if entries[i].root && !invalid[i] {
			stack = append(stack, int32(i))
		}
	}
	kept := 0
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if selected[i] || invalid[i] {
			continue
		}
		selected[i] = true
		kept++
		for _, j := range owned(i) {
			stack = append(stack, int32(j))
		}
	}

	retainedOutputs := make(map[eqClassID]struct{})
	namedClasses := make(map[eqClassID]struct{})
	for i := range entries {
		if !selected[i] {
			continue
		}
		e := &entries[i]
		for _, j := range owned(int32(i)) {
			if j == noEntry || !selected[j] {
				return persistStateSnapshot{}, fmt.Errorf("persist result %d: a dependency is outside persisted root closure", e.id)
			}
		}
		for _, id := range rd.ids[e.classAt : e.classAt+e.classN] {
			retainedOutputs[id] = struct{}{}
			namedClasses[id] = struct{}{}
		}
	}

	keptTerms := persistKeptCursor[keptUnit]{list: rd.kept.terms, id: func(u keptUnit) uint64 { return u.id }}
	for i := range rd.terms {
		t := &rd.terms[i]
		if !t.present {
			continue
		}
		if _, retained := retainedOutputs[t.output]; !retained || t.output == 0 {
			continue
		}
		namedClasses[t.output] = struct{}{}
		for _, id := range rd.ids[t.inAt : t.inAt+t.inN] {
			if id != 0 {
				namedClasses[id] = struct{}{}
			}
		}
		keptTerms.at(uint64(t.id))
		snapshot.next.terms = append(snapshot.next.terms, keptUnit{id: uint64(t.id), fp: t.fp})
		if t.term != nil {
			snapshot.terms = append(snapshot.terms, *t.term)
		}
	}
	snapshot.goneTerms = keptTerms.finish()

	keptClasses := persistKeptCursor[keptUnit]{list: rd.kept.classes, id: func(u keptUnit) uint64 { return u.id }}
	for i := range rd.classes {
		k := &rd.classes[i]
		if _, named := namedClasses[k.id]; !named {
			continue
		}
		if !k.present {
			return persistStateSnapshot{}, fmt.Errorf("persist eq class %d: a saved unit names it, but it was not read as its own root", k.id)
		}
		delete(namedClasses, k.id)
		keptClasses.at(uint64(k.id))
		snapshot.next.classes = append(snapshot.next.classes, keptUnit{id: uint64(k.id), fp: k.fp})
		if k.class != nil {
			snapshot.classes = append(snapshot.classes, *k.class)
		}
	}
	for id := range namedClasses {
		return persistStateSnapshot{}, fmt.Errorf("persist eq class %d: a saved unit names it, but it was not read", id)
	}
	snapshot.goneClasses = keptClasses.finish()

	// What the read held of edges, classes and terms goes before the next
	// kept table of entries is made.
	rd.edges, rd.ids, rd.terms, rd.classes = nil, nil, nil, nil
	keptEntries := persistKeptCursor[keptEntry]{list: rd.kept.entries, id: func(e keptEntry) uint64 { return uint64(e.id) }}
	snapshot.next.entries = make([]keptEntry, 0, kept)
	for i := range entries {
		if !selected[i] {
			continue
		}
		e := &entries[i]
		keptEntries.at(uint64(e.id))
		snapshot.next.entries = append(snapshot.next.entries, e.next)
		if e.result != nil {
			e.result.kept = len(snapshot.next.entries) - 1
			snapshot.results = append(snapshot.results, *e.result)
		}
	}
	snapshot.goneResults = keptEntries.finish()
	return snapshot, nil
}
