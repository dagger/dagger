package dagql

import (
	"cmp"
	"slices"
	"strings"

	"github.com/opencontainers/go-digest"
)

// A run is one execution of an entry's operation: the process, pull or copy
// that wrote its value. The Cloud names, for each record it stores, the run
// that made it, and keeps whether that run ran the step: a record the Cloud
// learns from an engine's call row declares its parts pending, since the call
// returns its lazy result before the step runs, and is a recipe until the
// engine runs it. Engines keep no runs and send none: the Cloud names a
// record's run from what a merge already carries (mergeRunLocked), and learns
// that it ran from the run's own holding (noteHoldingRanLocked). Nothing in
// this cache decides by either; ownRunRanLocked and EntryRun read them.
//
// RunName names a run by the entry that made it, in the engine cache that ran
// the work: that cache, the entry's number there, and the entry's replacement
// count there.
type RunName struct {
	Cache        CacheID `json:"cache"`
	Number       uint64  `json:"number"`
	Replacements uint64  `json:"replacements,omitempty"`
}

// runRanBy is how the Cloud learned that a stored record's own run ran its
// step (sharedResult.runRan), or runNotRan.
type runRanBy uint8

const (
	runNotRan runRanBy = iota
	// runRanAtMerge: the run's holding had already run its value when the
	// merge installed the record.
	runRanAtMerge
	// runRanByProof: the record proves a part besides its metadata complete
	// itself, and no holding had said the run ran when the merge installed
	// it.
	runRanByProof
	// runRanByCall: a call row of the run's holding said it executed or
	// joined the call and reported a part besides its metadata complete,
	// after the merge installed the record.
	runRanByCall
	// runRanByEvaluation: a lazy row of the run's holding reported a part
	// besides its metadata complete, and the row didn't install it from a
	// Cloud offer, after the merge installed the record.
	runRanByEvaluation
)

// savedRunLocked returns a copy of the run res's stored record names, for a
// save: nil on an engine, which keeps none. Requires egraphMu.
func savedRunLocked(res *sharedResult) *RunName {
	if res.run == nil {
		return nil
	}
	run := *res.run
	return &run
}

// mergeRunLocked names, on the Cloud, the run of the record row installs
// from a merge of prep, and how the Cloud knows that run ran its step. A
// record the engine computed is its own entry's run. So is one it imported
// (its envelope's mark, valueMergePrep.senderImported) and then ran itself,
// for want of the Cloud's content: its holding ran its value at the row's
// replacement count (holdingRanLocked). Another imported record is the run
// of the Cloud entry the engine's holding of it is on, the entry the Cloud
// sent it from (ApplyMergedReply, or a restore's hand-over), or, when the
// Cloud holds no such holding or its entry names no run, the engine's own.
// The run ran when its holding had run its value, or else when the record
// proves a part besides its metadata complete (valueMergePrep.completeParts).
// Requires egraphMu.
func (c *Cache) mergeRunLocked(prep *valueMergePrep, row *mergeRow) (RunName, runRanBy) {
	key := HolderKey{Cache: prep.from, Number: row.value.SenderNumber}
	run := RunName{Cache: key.Cache, Number: key.Number, Replacements: row.value.SenderReplacements}
	if prep.senderImported[row.ordinal] && !c.holdingRanLocked(key, run.Replacements) {
		if entry, _ := c.holdingLocked(key); entry != nil && entry.run != nil {
			run = *entry.run
		}
	}
	switch {
	case c.holdingRanLocked(HolderKey{Cache: run.Cache, Number: run.Number}, run.Replacements):
		return run, runRanAtMerge
	case ranPartsReported(prep.completeParts[row.ordinal]):
		return run, runRanByProof
	}
	return run, runNotRan
}

// holdingRanLocked reports whether the holding key ran its value at
// replacement count replacements (holding.ran): its own run of the entry.
// Another engine's holding of the same entry says nothing of it. Requires
// egraphMu.
func (c *Cache) holdingRanLocked(key HolderKey, replacements uint64) bool {
	_, h := c.holdingLocked(key)
	return h != nil && h.ran && h.replacements == replacements
}

// noteHoldingRanLocked notes that h, the holding key of its cache's entry,
// ran its value at its replacement count, by how (holding.ran), and marks
// the stored record of entry, which the holding sits on, as having run when
// it names that holding's run and didn't yet (sharedResult.runRan). The entry
// is then marked changed, for readers that keep their answers. Requires
// egraphMu for writing.
func (c *Cache) noteHoldingRanLocked(key HolderKey, entry *sharedResult, h *holding, by runRanBy) {
	h.ran = true
	if !c.blobBacked || entry == nil || entry.run == nil || entry.runRan != runNotRan {
		return
	}
	if run := *entry.run; key.Cache != run.Cache || key.Number != run.Number || h.replacements != run.Replacements {
		return
	}
	entry.runRan = by
	c.markEntryLocked(entry.id)
}

// ranPartsReported reports whether parts, an observation's complete part
// keys, name a part besides the metadata part.
func ranPartsReported(parts []string) bool {
	for _, key := range parts {
		if !strings.Contains(key, metadataPartField) {
			return true
		}
	}
	return false
}

// metadataPartField is the metadata part's field in an address key
// (partAddressKey), at any output path.
const metadataPartField = `"part":"metadata"`

// ownRunRanLocked reports whether res's stored record's own run ran its
// step: the run's holding reported a part besides its metadata complete,
// from a call it executed or joined or from an evaluation that didn't
// install a Cloud offer, or the record proves such a part complete itself
// (sharedResult.runRan). Another engine's run of the same entry says nothing
// of it. Requires egraphMu.
func (c *Cache) ownRunRanLocked(res *sharedResult) bool {
	return res.run != nil && res.runRan != runNotRan
}

// EntryRun is what the Cloud's stored record of an entry says of its run.
type EntryRun struct {
	// Run is the run that made the record.
	Run RunName
	// Ran reports that the run ran its step (ownRunRanLocked).
	Ran bool
}

// EntryRun returns, on the Cloud, what the stored record of the entry
// numbered number says of its run, or false when the cache has no such entry
// or the entry stores no record that names one.
func (c *Cache) EntryRun(number uint64) (EntryRun, bool) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	res := c.resultsByID[sharedResultID(number)]
	if res == nil || res.run == nil || res.noValueLocked() {
		return EntryRun{}, false
	}
	return EntryRun{Run: *res.run, Ran: c.ownRunRanLocked(res)}, true
}

// RunCounts counts the Cloud's stored records by what they say of their
// runs (StoredRuns), for a run's reader to check the runs by.
type RunCounts struct {
	// Records are the entries that store a record.
	Records int `json:"records"`
	// Unnamed are those whose record names no run.
	Unnamed int `json:"unnamed"`
	// NotRan are the named ones whose run hasn't run its step, as far as
	// the Cloud knows: recipes.
	NotRan int `json:"notRan"`
	// RanAtMerge, RanByProof, RanByCall and RanByEvaluation are the named
	// ones that ran, by how the Cloud learned it (runRanBy).
	RanAtMerge      int `json:"ranAtMerge"`
	RanByProof      int `json:"ranByProof"`
	RanByCall       int `json:"ranByCall"`
	RanByEvaluation int `json:"ranByEvaluation"`
	// NotRanRecords name the NotRan records, by number: a run among them
	// that an engine's telemetry shows ran is a fault in what the Cloud
	// learned.
	NotRanRecords []NotRanRecord `json:"notRanRecords"`
}

// NotRanRecord is a stored record whose named run hasn't run its step, as
// far as the Cloud knows.
type NotRanRecord struct {
	Number uint64        `json:"number"`
	Recipe digest.Digest `json:"recipe"`
	Run    RunName       `json:"run"`
}

// StoredRuns counts the cache's stored records by what they say of their
// runs (RunCounts). It reads every entry under one hold of the lock for
// reading, so a caller asks it once at a push's end, not on a hot path.
func (c *Cache) StoredRuns() RunCounts {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	counts := RunCounts{NotRanRecords: []NotRanRecord{}}
	for id, res := range c.resultsByID {
		res.payloadMu.RLock()
		stored := res.persistedEnvelope != nil || res.stored != nil
		res.payloadMu.RUnlock()
		if !stored {
			continue
		}
		counts.Records++
		if res.run == nil {
			counts.Unnamed++
			continue
		}
		switch res.runRan {
		case runRanAtMerge:
			counts.RanAtMerge++
		case runRanByProof:
			counts.RanByProof++
		case runRanByCall:
			counts.RanByCall++
		case runRanByEvaluation:
			counts.RanByEvaluation++
		default:
			counts.NotRan++
			recipe, _ := c.entryRecipeLocked(uint64(id))
			counts.NotRanRecords = append(counts.NotRanRecords, NotRanRecord{Number: uint64(id), Recipe: recipe, Run: *res.run})
		}
	}
	slices.SortFunc(counts.NotRanRecords, func(a, b NotRanRecord) int { return cmp.Compare(a.Number, b.Number) })
	return counts
}
