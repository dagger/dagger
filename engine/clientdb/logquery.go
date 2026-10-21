package clientdb

import (
	"context"
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
// emitted. A live scope also follows spans that arrive after it was resolved;
// it belongs to one reader and is not safe for concurrent use.
type LogQueryScope struct {
	// everything matches every span (an unscoped search).
	everything bool
	// own holds spans selected without descendants; tree holds every span
	// of a descendant selection.
	own     map[string]bool
	tree    map[string]bool
	digests map[string]bool

	// live scopes resolve against a growing store. spanCut is the span
	// high-water the tree was resolved at.
	live    *spanLookup
	spanCut int64
}

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
// that arrive later are placed as their rows are read: a row of a span the
// scope has not placed matches when the span turns out to be beneath a
// descendant selection, or when the store has no snapshot of it yet (its
// position is unknown, so the reader decides).
func (s *DB) LiveLogScope(spans []archive.LogSpan) *LogQueryScope {
	scope := newLogQueryScope(spans)
	// Read the cut first: a span indexed while the tree is walked is then
	// at worst checked twice, never missed.
	scope.spanCut = s.spans.highWater()
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

// matcher returns the per-scan row predicate. Undecided live spans are
// memoized for one scan only, since later spans and links may place them.
func (q *LogQueryScope) matcher() func(idx *logLookup, m logMeta) bool {
	undecided := map[uint32]bool{}
	return func(idx *logLookup, m logMeta) bool {
		if q.everything {
			return true
		}
		if m.digest != 0 && q.digests[idx.digests[m.digest]] {
			return true
		}
		span := idx.spanIDs[m.span]
		if q.own[span] || q.tree[span] {
			return true
		}
		if q.live == nil || len(q.tree) == 0 {
			return false
		}
		if ok, memo := undecided[m.span]; memo {
			return ok
		}
		first, known := q.live.first(idx.traceIDs[m.trace], span)
		var ok bool
		switch {
		case !known:
			ok = true
		case first > q.spanCut && q.live.beneath(span, q.tree):
			// The tree only grows, so placing the span is permanent.
			q.tree[span] = true
			return true
		}
		undecided[m.span] = ok
		return ok
	}
}

// SelectDisplayLogs reads the display text rows (see logFlags.display) of
// scope in (p.AfterID, p.ThroughID], scanning at most p.Limit rows. traceID
// "" matches every trace. next advances across unmatched rows too.
func (s *DB) SelectDisplayLogs(ctx context.Context, p SelectLogsRangeParams, traceID string, scope *LogQueryScope) ([]Log, int64, error) {
	match := scope.matcher()
	trace, traceKnown, traceResolved := uint32(0), false, traceID == ""
	return s.scanLogs(ctx, p, func(idx *logLookup, _ int64, m logMeta) (bool, error) {
		if !traceResolved {
			trace, traceKnown = idx.traceIndex[traceID]
			traceResolved = true
		}
		if traceID != "" && (!traceKnown || m.trace != trace) {
			return false, nil
		}
		return m.flags.display() && match(idx, m), nil
	})
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
}

// LogPresenceSet remembers the (trace, span, digest) triples a push stream
// has announced. It belongs to one stream.
type LogPresenceSet struct {
	seen map[[3]uint32]struct{}
}

func NewLogPresenceSet() *LogPresenceSet {
	return &LogPresenceSet{seen: map[[3]uint32]struct{}{}}
}

// SelectLogsWithoutDisplay reads every row in (p.AfterID, p.ThroughID],
// scanning at most p.Limit rows, except display text, which it reports as
// presence instead: once per (trace, span, digest) not yet in seen. It is the
// live push for a frontend that reads display text on demand.
func (s *DB) SelectLogsWithoutDisplay(ctx context.Context, p SelectLogsRangeParams, seen *LogPresenceSet) ([]Log, []LogPresence, int64, error) {
	var presence []LogPresence
	var keys [][3]uint32
	rows, next, err := s.scanLogs(ctx, p, func(idx *logLookup, i int64, m logMeta) (bool, error) {
		if !m.flags.display() {
			return true, nil
		}
		key := [3]uint32{m.trace, m.span, m.digest}
		if _, ok := seen.seen[key]; !ok {
			seen.seen[key] = struct{}{}
			keys = append(keys, key)
			presence = append(presence, LogPresence{
				Row:       i + 1,
				Timestamp: m.timestamp,
				TraceID:   idx.traceIDs[m.trace],
				SpanID:    idx.spanIDs[m.span],
				Digest:    idx.digests[m.digest],
			})
		}
		return false, nil
	})
	if err != nil {
		for _, key := range keys {
			delete(seen.seen, key)
		}
		return nil, nil, p.AfterID, err
	}
	// A byte-bounded scan may stop early; presence past it is reported again
	// by the next scan, so forget it here.
	for len(presence) > 0 && presence[len(presence)-1].Row > next {
		delete(seen.seen, keys[len(keys)-1])
		presence, keys = presence[:len(presence)-1], keys[:len(keys)-1]
	}
	return rows, presence, next, nil
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
		rows, next, err := s.SelectDisplayLogs(ctx, SelectLogsRangeParams{AfterID: result.Next, ThroughID: throughID, Limit: 4096}, traceID, scope)
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
