package clientdb

import (
	"sort"
	"sync"
)

// refLogLookup is the log index as it was before its span IDs and digests
// were interned by their bytes: string-keyed maps and string intern tables.
// It is kept as the reference the interned index must answer identically to
// (TestLogLookupMatchesReference). It classifies rows with the index's own
// classifier, which this change leaves alone.
type refLogLookup struct {
	mu          sync.RWMutex
	rowsBySpan  map[string][]int64
	meta        []logMeta
	metaErrs    map[int64]error
	traceIDs    []string
	traceIndex  map[string]uint32
	spanIDs     []string
	spanIndex   map[string]uint32
	digests     []string
	digestIndex map[string]uint32

	displayByDigest map[uint32][]int64
	unspanned       []int64
	unplaced        map[uint32]struct{}
	spanKnown       func(spanID string) bool

	classifier *logLookup
}

func newRefLogLookup() *refLogLookup {
	return &refLogLookup{
		rowsBySpan:      make(map[string][]int64),
		metaErrs:        make(map[int64]error),
		traceIndex:      make(map[string]uint32),
		spanIndex:       make(map[string]uint32),
		digests:         []string{""},
		digestIndex:     map[string]uint32{"": 0},
		displayByDigest: make(map[uint32][]int64),
		unplaced:        make(map[uint32]struct{}),
		classifier:      newLogLookup(),
	}
}

func (l *refLogLookup) addAll(rows []Log) {
	flags, digests, errs := l.classifier.classify(rows)
	l.mu.Lock()
	for i, row := range rows {
		if errs != nil && errs[i] != nil {
			l.metaErrs[int64(len(l.meta))] = errs[i]
		}
		spans := len(l.spanIDs)
		m := logMeta{
			timestamp: row.Timestamp,
			size:      logCodec.size(row),
			trace:     internLocked(l.traceIndex, &l.traceIDs, row.TraceID.String),
			span:      internLocked(l.spanIndex, &l.spanIDs, row.SpanID.String),
			digest:    internLocked(l.digestIndex, &l.digests, digests[i]),
			flags:     flags[i],
		}
		l.meta = append(l.meta, m)
		if len(l.spanIDs) > spans && l.spanKnown != nil && !l.spanKnown(row.SpanID.String) {
			l.unplaced[m.span] = struct{}{}
		}
		if m.digest != 0 && m.flags.display() {
			l.displayByDigest[m.digest] = append(l.displayByDigest[m.digest], row.ID)
		}
	}
	for _, row := range rows {
		if !row.SpanID.Valid {
			l.unspanned = append(l.unspanned, row.ID)
			continue
		}
		l.rowsBySpan[row.SpanID.String] = append(l.rowsBySpan[row.SpanID.String], row.ID)
	}
	l.mu.Unlock()
}

func (l *refLogLookup) spansStored(rows []Span) {
	l.mu.Lock()
	for _, row := range rows {
		if span, ok := l.spanIndex[row.SpanID]; ok {
			delete(l.unplaced, span)
		}
	}
	l.mu.Unlock()
}

func (l *refLogLookup) spanRowsLocked(id string, lists [][]int64) [][]int64 {
	if rows := l.rowsBySpan[id]; len(rows) > 0 {
		lists = append(lists, rows)
	}
	if id == "" && len(l.unspanned) > 0 {
		lists = append(lists, l.unspanned)
	}
	return lists
}

func (l *refLogLookup) unplacedSpanIDs() []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	ids := make([]string, 0, len(l.unplaced))
	for span := range l.unplaced {
		ids = append(ids, l.spanIDs[span])
	}
	return ids
}

func (l *refLogLookup) rowIDsForSpans(ids map[string]struct{}, perSpanTail int) []int64 {
	var rowIDs []int64
	l.mu.RLock()
	for id := range ids {
		rows := l.rowsBySpan[id]
		if perSpanTail > 0 && len(rows) > perSpanTail {
			rows = rows[len(rows)-perSpanTail:]
		}
		rowIDs = append(rowIDs, rows...)
	}
	l.mu.RUnlock()
	sort.Slice(rowIDs, func(i, j int) bool { return rowIDs[i] < rowIDs[j] })
	return rowIDs
}
