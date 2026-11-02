package clientdb

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/engine/archive"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"
)

// Multi-span display text reads (archive.LogQuery), the display-free push,
// and display text search, over the same per-row metadata index as the
// selected reads in selection.go. None of them decodes a body it does not
// return or search.

// LogQueryScope is a resolved archive.LogQuery: the spans whose own rows
// match, and the dag.digest values whose rows match wherever they were
// emitted. A live scope also follows spans that arrive after it was resolved.
// A scope belongs to one reader, which keeps what it decided about each span
// and digest across its reads; it is not safe for concurrent use.
type LogQueryScope struct {
	// everything matches every span (an unscoped search).
	everything bool
	// own holds spans selected without descendants; tree holds every span
	// of a descendant selection.
	own     map[string]bool
	tree    map[string]bool
	digests map[string]bool

	// live scopes resolve against a growing store.
	live *spanLookup

	// What the scope decided, by the store's interned span and digest IDs
	// (logLookup.spanIndex, digestIndex). Rows are matched by these, so a
	// span or digest is looked up by name once, when its first row is read,
	// not once per row. A span not placed yet is decided again only once the
	// span index has changed (spanAt holds the generation it was decided at).
	spans     []scopeMatch
	spanAt    []uint64
	digestsBy []scopeMatch
	placement placementMemo

	// oneShot holds the rows of the scope's current one-shot read (see
	// ReadDisplayLogs).
	oneShot *displayCandidates
}

// scopeMatch is a LogQueryScope's decision about one span or digest.
type scopeMatch uint8

const (
	matchUnresolved scopeMatch = iota
	// matchIn and matchOut are final: the tree only grows, and a fixed-cut
	// or descendant-free scope never changes.
	matchIn
	matchOut
	// matchOutAt: a live span not beneath the tree as of its generation.
	matchOutAt
	// matchUnplacedAt: a live span the store has no snapshot of as of its
	// generation, so its place is unknown and it is left to the reader.
	matchUnplacedAt
)

func newLogQueryScope(spans []archive.LogSpan) *LogQueryScope {
	scope := &LogQueryScope{own: map[string]bool{}, tree: map[string]bool{}, digests: map[string]bool{}}
	for _, s := range spans {
		scope.own[s.SpanID] = true
		for _, d := range s.Digests {
			scope.digests[d] = true
		}
	}
	return scope
}

// LiveLogScope resolves spans against the store's current span index. Spans
// placed later are found as their rows are read: a row of a span outside the
// resolved tree matches when the span is now beneath a descendant selection
// (it, its parent or its cause link arrived later), or when the store has no
// snapshot of it yet (its position is unknown, so the reader decides).
func (s *DB) LiveLogScope(spans []archive.LogSpan) *LogQueryScope {
	scope := newLogQueryScope(spans)
	scope.live = s.lookup
	for _, sel := range spans {
		if !sel.Descendants {
			continue
		}
		for id := range s.lookup.logScope(sel.SpanID) {
			scope.tree[id] = true
		}
	}
	return scope
}

// LogQueryScope resolves spans at the view's fixed cut.
func (v *SpanView) LogQueryScope(spans []archive.LogSpan) *LogQueryScope {
	scope := newLogQueryScope(spans)
	for _, sel := range spans {
		if sel.Descendants {
			for id := range v.Scope(sel.SpanID, true) {
				scope.tree[id] = true
			}
		}
	}
	return scope
}

// match reports whether a row (its metadata, under idx's read lock) is in
// scope. Spans outside the tree are placed as their rows are read, since
// later spans and links may place them.
func (q *LogQueryScope) match(idx *logLookup, m logMeta) bool {
	if q.everything {
		return true
	}
	if m.digest != 0 && len(q.digests) > 0 && q.digestIn(idx, m.digest) {
		return true
	}
	return q.spanIn(idx, m.span)
}

func (q *LogQueryScope) digestIn(idx *logLookup, digest uint32) bool {
	if int(digest) >= len(q.digestsBy) {
		q.digestsBy = append(q.digestsBy, make([]scopeMatch, len(idx.digests)-len(q.digestsBy))...)
	}
	switch q.digestsBy[digest] {
	case matchIn:
		return true
	case matchOut:
		return false
	case matchUnresolved:
	}
	ok := q.digests[idx.digests[digest]]
	q.digestsBy[digest] = matchOut
	if ok {
		q.digestsBy[digest] = matchIn
	}
	return ok
}

func (q *LogQueryScope) spanIn(idx *logLookup, span uint32) bool {
	if int(span) >= len(q.spans) {
		grow := len(idx.spanIDs) - len(q.spans)
		q.spans = append(q.spans, make([]scopeMatch, grow)...)
		q.spanAt = append(q.spanAt, make([]uint64, grow)...)
	}
	switch state := q.spans[span]; state {
	case matchIn:
		return true
	case matchOut:
		return false
	case matchOutAt, matchUnplacedAt:
		if q.spanAt[span] == q.live.generation() {
			return state == matchUnplacedAt
		}
	}
	return q.resolveSpan(span, idx.spanIDs[span])
}

// resolveSpan decides a span the scope has not decided yet, or whose place
// may have changed since.
func (q *LogQueryScope) resolveSpan(span uint32, id string) bool {
	if q.own[id] || q.tree[id] {
		q.spans[span] = matchIn
		return true
	}
	if q.live == nil || len(q.tree) == 0 {
		q.spans[span] = matchOut
		return false
	}
	known, beneath, gen := q.live.place(id, q.tree, &q.placement)
	switch {
	case !known:
		// Its place is unknown yet: leave it to the reader.
		q.spans[span], q.spanAt[span] = matchUnplacedAt, gen
		return true
	case beneath:
		// A span stored after the tree was resolved, or one whose parent
		// or cause link arrived later. The tree only grows, so placing the
		// span is permanent.
		q.tree[id] = true
		q.spans[span] = matchIn
		return true
	default:
		q.spans[span], q.spanAt[span] = matchOutAt, gen
		return false
	}
}

// SelectDisplayLogs reads the display text rows (see logFlags.display) of
// scope in (p.AfterID, p.ThroughID], scanning at most p.Limit rows. traceID
// "" matches every trace. next advances across unmatched rows too.
func (s *DB) SelectDisplayLogs(ctx context.Context, p SelectLogsRangeParams, traceID string, scope *LogQueryScope) ([]Log, int64, error) {
	trace, traceKnown, traceResolved := uint32(0), false, traceID == ""
	return s.scanLogs(ctx, p, func(idx *logLookup, _ int64, m logMeta) (bool, error) {
		if !traceResolved {
			trace, traceKnown = idx.traceIndex[traceID]
			traceResolved = true
		}
		if traceID != "" && (!traceKnown || m.trace != trace) {
			return false, nil
		}
		return m.flags.display() && scope.match(idx, m), nil
	})
}

// displayIndexMaxShare bounds how much of a one-shot read's range
// ReadDisplayLogs gathers from the row indexes: when the candidate rows are
// more than 1/displayIndexMaxShare of the range (a roll-up of the root, a
// digest that streamed all session), scanning the range costs less than
// gathering, filtering and sorting them. 0 never scans. A variable for
// tests.
var displayIndexMaxShare int64 = 4

// displayCandidates are the display rows of one one-shot read of a scope:
// in (after, through], of one trace, ascending; or scan, when the read
// scans instead.
type displayCandidates struct {
	after, through int64
	traceID        string
	scan           bool
	rows           []int64
}

// ReadDisplayLogs is SelectDisplayLogs for a one-shot read of (p.AfterID,
// p.ThroughID], one call per batch with p.ThroughID fixed: it reads the
// same rows. Its first call gathers the rows from the row indexes (the
// scope's spans and every span now beneath its tree, spans with no snapshot
// yet when it has a tree, and its digests), and later calls page through
// them, so a read costs what it returns rather than the size of the store,
// and a batch holds at most p.Limit rows instead of covering p.Limit rows.
// next is the last row the batch covers, matched or not: the end of the
// read once no row is left. n is the batch's size in the units of p.Limit:
// the rows returned, or the rows scanned when the read fell back to a scan
// (see displayIndexMaxShare).
func (s *DB) ReadDisplayLogs(ctx context.Context, p SelectLogsRangeParams, traceID string, scope *LogQueryScope) (rows []Log, next int64, n int, err error) {
	if err := ctx.Err(); err != nil {
		return nil, p.AfterID, 0, err
	}
	if p.AfterID < 0 || p.ThroughID < p.AfterID || p.Limit <= 0 {
		return nil, p.AfterID, 0, fmt.Errorf("invalid log range")
	}
	c := scope.oneShot
	if c == nil || c.through != p.ThroughID || c.traceID != traceID || p.AfterID < c.after {
		c, err = s.gatherDisplayCandidates(p.AfterID, p.ThroughID, traceID, scope)
		if err != nil {
			return nil, p.AfterID, 0, err
		}
		scope.oneShot = c
	}
	if c.scan {
		rows, next, err := s.SelectDisplayLogs(ctx, p, traceID, scope)
		return rows, next, int(next - p.AfterID), err
	}
	start, _ := slices.BinarySearch(c.rows, p.AfterID+1)
	next = p.ThroughID
	var ids []int64
	var bytes int64
	idx := s.logIdx
	idx.mu.RLock()
	for _, id := range c.rows[start:] {
		size := idx.meta[id-1].size
		// Bound retained bodies by bytes as well as rows, as scanLogs does.
		if int64(len(ids)) == p.Limit || (len(ids) > 0 && size > selectedLogBatchBytes-bytes) {
			next = id - 1
			break
		}
		bytes += size
		ids = append(ids, id)
	}
	idx.mu.RUnlock()
	rows, err = s.SelectLogRows(ctx, ids)
	if err != nil {
		return nil, p.AfterID, 0, err
	}
	return rows, next, len(ids), nil
}

// gatherDisplayCandidates collects the display rows of scope in (after,
// through] from the row indexes, or settles on a scan.
func (s *DB) gatherDisplayCandidates(after, through int64, traceID string, q *LogQueryScope) (*displayCandidates, error) {
	c := &displayCandidates{after: after, through: through, traceID: traceID}
	if q.everything {
		c.scan = true
		return c, nil
	}
	var unplaced []string
	if q.live != nil && len(q.tree) > 0 {
		// Place every span beneath the tree now, as a scan would place each
		// as it reads its rows, and keep the spans whose place is unknown.
		unplaced = q.live.closeBeneath(q.tree, s.logIdx.unplacedSpanIDs())
	}
	idx := s.logIdx
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	if through > int64(len(idx.meta)) {
		return nil, errors.New("log stream truncated before cut")
	}
	var lists [][]int64
	for id := range q.own {
		if !q.tree[id] {
			lists = idx.spanRowsLocked(id, lists)
		}
	}
	for id := range q.tree {
		lists = idx.spanRowsLocked(id, lists)
	}
	for _, id := range unplaced {
		lists = idx.spanRowsLocked(id, lists)
	}
	for d := range q.digests {
		if digest, ok := idx.digestIndex[d]; ok && digest != 0 {
			lists = append(lists, idx.displayByDigest[digest])
		}
	}
	// Narrow each list to the range, and count.
	var total int64
	for i, rows := range lists {
		lo, _ := slices.BinarySearch(rows, after+1)
		hi, _ := slices.BinarySearch(rows, through+1)
		lists[i] = rows[lo:hi]
		total += int64(hi - lo)
	}
	if displayIndexMaxShare > 0 && total*displayIndexMaxShare > through-after {
		c.scan = true
		return c, nil
	}
	trace, traceKnown := uint32(0), traceID == ""
	if traceID != "" {
		trace, traceKnown = idx.traceIndex[traceID]
	}
	if !traceKnown {
		return c, nil
	}
	c.rows = make([]int64, 0, total)
	for _, rows := range lists {
		for _, id := range rows {
			m := idx.meta[id-1]
			if m.flags.display() && (traceID == "" || m.trace == trace) {
				c.rows = append(c.rows, id)
			}
		}
	}
	if len(lists) > 1 {
		slices.Sort(c.rows)
		c.rows = slices.Compact(c.rows)
	}
	return c, nil
}

// LogPresence announces that a span has display text without carrying it:
// the first display row of each (trace, span, digest) in a push stream.
type LogPresence struct {
	// Row is the first such row's ID.
	Row       int64
	Timestamp int64
	TraceID   string
	SpanID    string
	Digest    string

	key [3]uint32
}

// LogPresenceSet remembers the (trace, span, digest) triples a push stream
// has announced. It belongs to one stream.
type LogPresenceSet struct {
	seen map[[3]uint32]struct{}
}

func NewLogPresenceSet() *LogPresenceSet {
	return &LogPresenceSet{seen: map[[3]uint32]struct{}{}}
}

// Forget unannounces presence a stream did not deliver after all (e.g. a
// batch it discarded to refetch a smaller one), so it is reported again.
func (s *LogPresenceSet) Forget(presence []LogPresence) {
	for _, p := range presence {
		delete(s.seen, p.key)
	}
}

// SelectLogsWithoutDisplay reads every row in (p.AfterID, p.ThroughID],
// scanning at most p.Limit rows, except display text, which it reports as
// presence instead: once per (trace, span, digest) not yet in seen. It is the
// live push for a frontend that reads display text on demand.
func (s *DB) SelectLogsWithoutDisplay(ctx context.Context, p SelectLogsRangeParams, seen *LogPresenceSet) ([]Log, []LogPresence, int64, error) {
	var presence []LogPresence
	rows, next, err := s.scanLogs(ctx, p, func(idx *logLookup, i int64, m logMeta) (bool, error) {
		if !m.flags.display() {
			return true, nil
		}
		key := [3]uint32{m.trace, m.span, m.digest}
		if _, ok := seen.seen[key]; !ok {
			seen.seen[key] = struct{}{}
			presence = append(presence, LogPresence{
				Row:       i + 1,
				Timestamp: m.timestamp,
				TraceID:   idx.traceIDs[m.trace],
				SpanID:    idx.spanIDs[m.span],
				Digest:    idx.digests[m.digest],
				key:       key,
			})
		}
		return false, nil
	})
	if err != nil {
		seen.Forget(presence)
		return nil, nil, p.AfterID, err
	}
	// A byte-bounded scan may stop early; presence past it is reported again
	// by the next scan, so forget it here.
	keep := len(presence)
	for keep > 0 && presence[keep-1].Row > next {
		keep--
	}
	seen.Forget(presence[keep:])
	return rows, presence[:keep], next, nil
}

// logSearchScanBytes bounds the bodies one SearchLogs call reads, so a search
// over a huge store returns a page (More) instead of reading it all at once.
const logSearchScanBytes = 64 << 20

// SearchLogs matches display text in (search.After, throughID] line by line.
// scope nil searches every span; traceID "" every trace. It stops at the
// search's match limit or its scan budget, reporting More.
func (s *DB) SearchLogs(ctx context.Context, search archive.LogSearch, throughID int64, traceID string, scope *LogQueryScope) (archive.LogSearchResult, error) {
	matchLine, err := search.Matcher()
	if err != nil {
		return archive.LogSearchResult{}, err
	}
	limit := search.Limit
	if limit == 0 {
		limit = archive.DefaultLogSearchLimit
	}
	if scope == nil {
		scope = &LogQueryScope{everything: true}
	}
	result := archive.LogSearchResult{Matches: []archive.LogMatch{}, Next: search.After}
	var scanned int64
	for result.Next < throughID {
		rows, next, _, err := s.ReadDisplayLogs(ctx, SelectLogsRangeParams{AfterID: result.Next, ThroughID: throughID, Limit: 4096}, traceID, scope)
		if err != nil {
			return result, err
		}
		for _, row := range rows {
			scanned += int64(len(row.Body))
			matches := searchLogRow(row, matchLine)
			if len(result.Matches)+len(matches) > limit {
				result.More = true
				if len(result.Matches) == 0 {
					// One row has more matches than a page: return the
					// first page of them and move past the row.
					result.Matches = matches[:limit]
					result.Next = row.ID
				} else {
					// Resume at this row; a page never splits one.
					result.Next = row.ID - 1
				}
				return result, nil
			}
			result.Matches = append(result.Matches, matches...)
		}
		result.Next = next
		if scanned >= logSearchScanBytes && result.Next < throughID {
			result.More = true
			break
		}
	}
	return result, nil
}

func searchLogRow(row Log, matchLine func(string) bool) []archive.LogMatch {
	var body commonpb.AnyValue
	if err := proto.Unmarshal(row.Body, &body); err != nil {
		return nil
	}
	var matches []archive.LogMatch
	for i, line := range strings.Split(body.GetStringValue(), "\n") {
		if !matchLine(line) {
			continue
		}
		if len(line) > archive.MaxLogSearchSnippet {
			line = strings.ToValidUTF8(line[:archive.MaxLogSearchSnippet], "")
		}
		matches = append(matches, archive.LogMatch{
			TraceID: row.TraceID.String,
			SpanID:  row.SpanID.String,
			Digest:  logRowDigest(row),
			Row:     row.ID,
			Line:    i,
			Text:    line,
		})
	}
	return matches
}

func logRowDigest(row Log) string {
	_, digest, _ := logAttrClass(row.Attributes)
	return digest
}
