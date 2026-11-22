package dagql

import "github.com/opencontainers/go-digest"

// A run is one execution of an entry's operation: the process, pull or copy
// that wrote its value. The Cloud names, for each record it stores, the run
// that made it, and keeps whether that run ran the step.
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
	runRanAtMerge
	runRanByProof
	runRanByCall
	runRanByEvaluation
)

// EntryRun is what the Cloud's stored record of an entry says of its run.
type EntryRun struct {
	// Run is the run that made the record.
	Run RunName
	// Ran reports that the run ran its step.
	Ran bool
}

// EntryRun returns, on the Cloud, what the stored record of the entry
// numbered number says of its run, or false when the cache has no such entry
// or the entry stores no record that names one. Not built yet: it names none.
func (c *Cache) EntryRun(number uint64) (EntryRun, bool) {
	return EntryRun{}, false
}

// RunCounts counts the Cloud's stored records by what they say of their
// runs (StoredRuns).
type RunCounts struct {
	Records         int            `json:"records"`
	Unnamed         int            `json:"unnamed"`
	NotRan          int            `json:"notRan"`
	RanAtMerge      int            `json:"ranAtMerge"`
	RanByProof      int            `json:"ranByProof"`
	RanByCall       int            `json:"ranByCall"`
	RanByEvaluation int            `json:"ranByEvaluation"`
	NotRanRecords   []NotRanRecord `json:"notRanRecords"`
}

// NotRanRecord is a stored record whose named run hasn't run its step, as
// far as the Cloud knows.
type NotRanRecord struct {
	Number uint64        `json:"number"`
	Recipe digest.Digest `json:"recipe"`
	Run    RunName       `json:"run"`
}

// StoredRuns counts the cache's stored records by what they say of their
// runs. Not built yet: it counts none.
func (c *Cache) StoredRuns() RunCounts {
	return RunCounts{}
}
