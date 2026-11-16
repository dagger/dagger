package clientdb

import (
	"database/sql"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// readDisplayIndexed reads (after, through] as a one-shot read does, with
// ReadDisplayLogs, limit rows per batch. A batch never covers more rows than
// it returns, unless it is the read's last.
func readDisplayIndexed(t *testing.T, db *DB, traceID string, scope *LogQueryScope, after, through, limit int64) []int64 {
	t.Helper()
	var ids []int64
	for after < through {
		rows, next, n, err := db.ReadDisplayLogs(t.Context(), SelectLogsRangeParams{AfterID: after, ThroughID: through, Limit: limit}, traceID, scope)
		require.NoError(t, err)
		require.Greater(t, next, after)
		require.LessOrEqual(t, next, through)
		require.LessOrEqual(t, int64(n), limit)
		for _, row := range rows {
			require.Greater(t, row.ID, after)
			require.LessOrEqual(t, row.ID, next)
			ids = append(ids, row.ID)
		}
		after = next
	}
	return ids
}

// readDisplayScanned is readDisplay with a trace and a batch size.
func readDisplayScanned(t *testing.T, db *DB, traceID string, scope *LogQueryScope, after, through, limit int64) []int64 {
	t.Helper()
	var ids []int64
	for after < through {
		rows, next, err := db.SelectDisplayLogs(t.Context(), SelectLogsRangeParams{AfterID: after, ThroughID: through, Limit: limit}, traceID, scope)
		require.NoError(t, err)
		after = next
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
	}
	return ids
}

// indexEquivStore builds a random store for TestReadDisplayLogsMatchesScan:
// a span forest whose snapshots arrive out of order (children before their
// parents, rows before their spans, parents and cause links added by later
// snapshots), with rows of two traces, rows with no span ID, digest-routed
// rows and rows that are not display text.
type indexEquivStore struct {
	t       *testing.T
	rng     *rand.Rand
	db      *DB
	ids     []string
	digests []string
	// stored are the spans with a snapshot so far.
	stored map[string]bool
}

const indexEquivOtherTrace = "02000000000000000000000000000000"

func newIndexEquivStore(t *testing.T, seed uint64) *indexEquivStore {
	s := &indexEquivStore{
		t:      t,
		rng:    rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)),
		db:     logQueryStore(t),
		stored: map[string]bool{},
	}
	for i := range 24 {
		s.ids = append(s.ids, fmt.Sprintf("%016x", 0xa0+i))
	}
	s.digests = []string{"xxh3:a", "xxh3:b", "xxh3:c"}
	return s
}

func (s *indexEquivStore) span() string { return s.ids[s.rng.IntN(len(s.ids))] }

// appendSpans stores a batch of snapshots: new spans with or without a
// parent, and later snapshots of stored spans that add a parent or links.
func (s *indexEquivStore) appendSpans() {
	var rows []Span
	for range 1 + s.rng.IntN(4) {
		id := s.span()
		row := Span{TraceID: selectionTrace, SpanID: id}
		if s.rng.IntN(3) == 0 {
			row.TraceID = indexEquivOtherTrace
		}
		if s.rng.IntN(4) != 0 {
			if parent := s.span(); parent != id {
				row.ParentSpanID = validString(parent)
			}
		}
		if s.rng.IntN(4) == 0 {
			var links []sdktrace.Link
			for range 1 + s.rng.IntN(2) {
				purpose := telemetry.LinkPurposeCause
				if s.rng.IntN(4) == 0 {
					purpose = ""
				}
				links = append(links, spanLink(s.t, selectionTrace, s.span(), purpose))
			}
			row.Links = linksJSON(s.t, links...)
		}
		s.stored[id] = true
		rows = append(rows, row)
	}
	_, err := s.db.AppendSpans(rows)
	require.NoError(s.t, err)
}

func (s *indexEquivStore) appendLogs() {
	var rows []Log
	for range 1 + s.rng.IntN(12) {
		text := selectionText(fmt.Sprintf("line %d", s.rng.IntN(1000)))
		var row Log
		switch k := s.rng.IntN(20); {
		case k < 2:
			row = selectionLog(s.t, s.span(), selectionText("name"), selectionAttr(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName))
		case k < 3:
			row = selectionLog(s.t, s.span(), selectionText("control"), selectionAttr(agentcontrol.VersionAttr, "1"))
		case k < 7:
			row = selectionLog(s.t, s.span(), text, selectionAttr(telemetry.DagDigestAttr, s.digests[s.rng.IntN(len(s.digests))]))
		case k < 8:
			// A digest-routed row that is not display text.
			row = selectionLog(s.t, s.span(), text, selectionAttr(telemetry.DagDigestAttr, s.digests[s.rng.IntN(len(s.digests))]), selectionAttr(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName))
		case k < 9:
			row = selectionLog(s.t, "", text)
			row.SpanID = sql.NullString{}
		default:
			row = selectionLog(s.t, s.span(), text)
		}
		if s.rng.IntN(4) == 0 {
			row.TraceID = validString(indexEquivOtherTrace)
		}
		rows = append(rows, row)
	}
	_, err := s.db.AppendLogs(rows)
	require.NoError(s.t, err)
}

func (s *indexEquivStore) grow(steps int) {
	for range steps {
		if s.rng.IntN(3) == 0 {
			s.appendSpans()
		} else {
			s.appendLogs()
		}
	}
}

func (s *indexEquivStore) query() []archive.LogSpan {
	var q []archive.LogSpan
	for range 1 + s.rng.IntN(3) {
		sel := archive.LogSpan{SpanID: s.span(), Descendants: s.rng.IntN(2) == 0}
		if s.rng.IntN(3) == 0 {
			sel.Digests = []string{s.digests[s.rng.IntN(len(s.digests))]}
		}
		if s.rng.IntN(10) == 0 {
			sel.Digests = append(sel.Digests, "xxh3:never")
		}
		q = append(q, sel)
	}
	return q
}

// The one-shot read by index returns exactly the rows the scan does, for
// random stores and queries: own spans, trees (with spans placed after the
// scope was resolved, and spans with rows but no snapshot), digests, rows
// with no span ID, ranges ending at Through, resumed cursors, and the
// archive's fixed-cut scopes filtered to a trace.
func TestReadDisplayLogsMatchesScan(t *testing.T) {
	for seed := range uint64(150) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			s := newIndexEquivStore(t, seed)
			s.grow(5 + s.rng.IntN(20))
			q := s.query()
			describe := func() string {
				var parts []string
				for _, sel := range q {
					parts = append(parts, fmt.Sprintf("%+v", sel))
				}
				return strings.Join(parts, " ")
			}
			// Scopes resolved now, read after the store has grown.
			scanScope := s.db.LiveLogScope(q)
			indexScope := s.db.LiveLogScope(q)
			defaultScope := s.db.LiveLogScope(q)
			s.grow(s.rng.IntN(20))

			hw := s.db.HighWater().Logs
			through := hw - s.rng.Int64N(hw/3+1)
			after := s.rng.Int64N(through/3 + 1)
			limit := 1 + s.rng.Int64N(6)
			want := readDisplayScanned(t, s.db, "", scanScope, after, through, limit)
			withIndexShare(0, func() {
				got := readDisplayIndexed(t, s.db, "", indexScope, after, through, limit)
				require.Equal(t, want, got, "live scope %s over (%d, %d]", describe(), after, through)
			})
			got := readDisplayIndexed(t, s.db, "", defaultScope, after, through, limit)
			require.Equal(t, want, got, "live scope %s with the scan fallback", describe())
			// The tree the index read placed agrees with the scan's.
			for id := range scanScope.tree {
				require.True(t, indexScope.tree[id], "span %s placed by the scan only", id)
			}

			// Resumed: a scope resolved for each request, resuming at the
			// previous request's cursor (as a reconnecting client does).
			want = readDisplayScanned(t, s.db, "", s.db.LiveLogScope(q), 0, hw, 4)
			withIndexShare(0, func() {
				var got []int64
				cursor := int64(0)
				for cursor < hw {
					rows, next, _, err := s.db.ReadDisplayLogs(t.Context(), SelectLogsRangeParams{AfterID: cursor, ThroughID: hw, Limit: 1 + s.rng.Int64N(3)}, "", s.db.LiveLogScope(q))
					require.NoError(t, err)
					got = append(got, logIDs(rows)...)
					cursor = next
				}
				if len(got) == 0 {
					got = nil
				}
				require.Equal(t, want, got, "resumed live scope %s", describe())
			})

			// The archive's fixed-cut scope, filtered to a trace.
			view, err := s.db.SpanViewAt(t.Context(), selectionTrace, s.db.HighWater(), nil)
			require.NoError(t, err)
			want = readDisplayScanned(t, s.db, selectionTrace, view.LogQueryScope(q), after, through, limit)
			withIndexShare(0, func() {
				got := readDisplayIndexed(t, s.db, selectionTrace, view.LogQueryScope(q), after, through, limit)
				require.Equal(t, want, got, "archive scope %s", describe())
			})
		})
	}
}

// A one-shot read's batch is bounded by bytes as well as rows, and its
// cursor ends at the read's end even past its last row.
func TestReadDisplayLogsBatches(t *testing.T) {
	db := logQueryStore(t)
	big := strings.Repeat("x", 400<<10)
	_, err := db.AppendSpans([]Span{logQuerySpan("a", ""), logQuerySpan("b", "")})
	require.NoError(t, err)
	_, err = db.AppendLogs([]Log{
		selectionLog(t, "a", selectionText(big)), // 1
		selectionLog(t, "b", selectionText("b")), // 2
		selectionLog(t, "a", selectionText(big)), // 3
		selectionLog(t, "a", selectionText(big)), // 4
		selectionLog(t, "b", selectionText("b")), // 5
		selectionLog(t, "a", selectionText("a")), // 6
		selectionLog(t, "b", selectionText("b")), // 7
	})
	require.NoError(t, err)
	withIndexShare(0, func() {
		scope := db.LiveLogScope([]archive.LogSpan{{SpanID: "a"}})
		rows, next, n, err := db.ReadDisplayLogs(t.Context(), SelectLogsRangeParams{AfterID: 0, ThroughID: 7, Limit: 10}, "", scope)
		require.NoError(t, err)
		require.Equal(t, []int64{1, 3}, logIDs(rows), "the third big row would pass the byte bound")
		require.EqualValues(t, 3, next)
		require.Equal(t, 2, n)

		rows, next, n, err = db.ReadDisplayLogs(t.Context(), SelectLogsRangeParams{AfterID: next, ThroughID: 7, Limit: 1}, "", scope)
		require.NoError(t, err)
		require.Equal(t, []int64{4}, logIDs(rows))
		require.EqualValues(t, 5, next, "a batch at its row limit ends before the next row")
		require.Equal(t, 1, n)

		rows, next, n, err = db.ReadDisplayLogs(t.Context(), SelectLogsRangeParams{AfterID: next, ThroughID: 7, Limit: 1}, "", scope)
		require.NoError(t, err)
		require.Equal(t, []int64{6}, logIDs(rows))
		require.EqualValues(t, 7, next, "the last batch ends at the read's end")
		require.Equal(t, 1, n)

		rows, next, n, err = db.ReadDisplayLogs(t.Context(), SelectLogsRangeParams{AfterID: 0, ThroughID: 7, Limit: 10}, "", db.LiveLogScope([]archive.LogSpan{{SpanID: "nothing"}}))
		require.NoError(t, err)
		require.Empty(t, rows)
		require.EqualValues(t, 7, next, "a read with no rows ends at once")
		require.Zero(t, n)
	})

	// A read whose rows are most of the range scans it instead, counting
	// the rows it covers.
	rows, next, n, err := db.ReadDisplayLogs(t.Context(), SelectLogsRangeParams{AfterID: 0, ThroughID: 7, Limit: 4}, "", db.LiveLogScope([]archive.LogSpan{{SpanID: "a"}, {SpanID: "b"}}))
	require.NoError(t, err)
	require.Equal(t, []int64{1, 2, 3}, logIDs(rows))
	require.EqualValues(t, 3, next)
	require.Equal(t, 3, n)
}
