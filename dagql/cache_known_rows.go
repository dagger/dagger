package dagql

import "sync"

// KnownRows are the entries of an engine cache whose records one recipient,
// the Cloud over one service connection, has merged: an export carried them
// in full, and the recipient said it merged that export. A records-only
// export sends a known row that hasn't changed since as a held row, with no
// record and nothing below it, so that a record the closures of many exports
// share travels once. Whether a held row holds is the recipient's merge's to
// decide: it commits nothing unless every held row's recipe has a live value
// there. KnownRows only chooses what to leave out.
type KnownRows struct {
	mu   sync.Mutex
	rows map[uint64]RowRevision
}

// RowRevision is an entry's revisions as an export captured it: its
// replacement count, and the revisions of its transfer state and of its
// dependencies. A known row is held only while its entry still has them.
type RowRevision struct {
	Replacements uint64
	Transfer     uint64
	Dependencies uint64
}

// ExportedRows are the rows one export carried: in full, by number, with the
// revisions it captured, and as held rows, by number.
type ExportedRows struct {
	Full map[uint64]RowRevision
	Held []uint64
}

func NewKnownRows() *KnownRows {
	return &KnownRows{rows: map[uint64]RowRevision{}}
}

// Merged records that the recipient merged the export that carried rows: its
// full rows are known, at the revisions it captured. Its held rows were known
// already, and stay as they were.
func (k *KnownRows) Merged(rows ExportedRows) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for number, rev := range rows.Full {
		k.rows[number] = rev
	}
}

// NotMerged records that the recipient didn't merge the export that carried
// rows: its held rows are forgotten, so that another export of the same roots
// carries them in full. Its full rows were never marked.
func (k *KnownRows) NotMerged(rows ExportedRows) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, number := range rows.Held {
		delete(k.rows, number)
	}
}

// knowsLocked reports whether the recipient merged res as it stands: an
// export it merged carried res in full, and res's revisions haven't moved
// since. A nil KnownRows knows nothing. Requires egraphMu.
func (k *KnownRows) knowsLocked(res *sharedResult) bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	rev, ok := k.rows[uint64(res.id)]
	k.mu.Unlock()
	return ok && rev == RowRevision{Replacements: res.replacements, Transfer: res.transferRevision, Dependencies: res.dependencyOwnershipRevision}
}

// Len is the number of known rows.
func (k *KnownRows) Len() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.rows)
}
