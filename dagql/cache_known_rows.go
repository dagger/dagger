package dagql

import "sync"

// KnownRows are the entries of an engine cache whose records one recipient,
// the Cloud over one service connection, holds: an export carried them in
// full and the recipient said it merged that export, or a merge of the
// recipient's own records landed on them. An export sends a known row that
// hasn't changed since as a held row, with no record, so that a record the
// closures of many exports share travels once: with nothing below it in a
// records-only export, and with its links to the rows below it and its parts
// in an export that selects parts. Whether a held row holds is the
// recipient's merge's to decide: it commits nothing unless every held row's
// recipe has a live value there, with every part the export puts on the
// row. KnownRows only chooses what to leave out.
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

// EntryStamp is what a reader keeps of an entry as it read it, to tell
// later whether the entry changed since: its revisions, its own expiry, and
// how many calls it keeps (Calls), which are only ever added.
type EntryStamp struct {
	Revision      RowRevision
	ExpiresAtUnix int64
	Calls         int
}

// stampLocked is the entry's stamp as it stands. Requires egraphMu.
func (res *sharedResult) stampLocked() EntryStamp {
	return EntryStamp{
		Revision:      RowRevision{Replacements: res.replacements, Transfer: res.transferRevision, Dependencies: res.dependencyOwnershipRevision},
		ExpiresAtUnix: res.expiresAtUnix,
		Calls:         len(res.calls),
	}
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

// Stale records that the recipient refused the export that carried rows
// because the held rows stale named no longer hold there: those of its held
// rows are forgotten, so that another export carries them in full, and the
// others stay known, since the recipient still holds them. Its full rows
// were never marked.
func (k *KnownRows) Stale(rows ExportedRows, stale []uint64) {
	named := make(map[uint64]bool, len(stale))
	for _, number := range stale {
		named[number] = true
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, number := range rows.Held {
		if named[number] {
			delete(k.rows, number)
		}
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

// knows reports whether the recipient merged entry number at rev: an export
// it merged carried the entry in full with those revisions. A nil KnownRows
// knows nothing.
func (k *KnownRows) knows(number uint64, rev RowRevision) bool {
	if k == nil {
		return false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	known, ok := k.rows[number]
	return ok && known == rev
}

// Len is the number of known rows.
func (k *KnownRows) Len() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.rows)
}
