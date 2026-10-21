package clientdb

import (
	"testing"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

func logQueryStore(t *testing.T) *DB {
	t.Helper()
	db, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return db
}

func logQuerySpan(id, parent string) Span {
	s := Span{TraceID: selectionTrace, SpanID: id, Attributes: []byte("[]")}
	if parent != "" {
		s.ParentSpanID = validString(parent)
	}
	return s
}

func readDisplay(t *testing.T, db *DB, scope *LogQueryScope, after, through int64) ([]int64, int64) {
	t.Helper()
	var ids []int64
	for after < through {
		rows, next, err := db.SelectDisplayLogs(t.Context(), SelectLogsRangeParams{AfterID: after, ThroughID: through, Limit: 2}, "", scope)
		require.NoError(t, err)
		require.Greater(t, next, after)
		after = next
		ids = append(ids, logIDs(rows)...)
	}
	return ids, after
}

func TestDisplayLogsClassesSpansAndDigests(t *testing.T) {
	db := logQueryStore(t)
	verbose := &commonpb.KeyValue{Key: telemetry.LogsVerboseAttr, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}}
	_, err := db.AppendSpans([]Span{logQuerySpan("root", ""), logQuerySpan("kid", "root"), logQuerySpan("other", "")})
	require.NoError(t, err)
	_, err = db.AppendLogs([]Log{
		selectionLog(t, "root", selectionText("text")),                                                                            // 1
		selectionLog(t, "root", selectionText("verbose"), verbose),                                                                // 2: display includes verbose
		selectionLog(t, "root", selectionText("name"), selectionAttr(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName)), // 3
		selectionLog(t, "root", selectionText("control"), selectionAttr(agentcontrol.VersionAttr, "1")),                           // 4
		selectionLog(t, "kid", selectionText("kid")),                                                                              // 5
		selectionLog(t, "other", selectionText("other")),                                                                          // 6
		selectionLog(t, "other", selectionText("routed"), selectionAttr(telemetry.DagDigestAttr, "xxh3:svc")),                     // 7
	})
	require.NoError(t, err)
	hw := db.HighWater().Logs

	ids, _ := readDisplay(t, db, db.LiveLogScope([]archive.LogSpan{{SpanID: "root"}}), 0, hw)
	require.Equal(t, []int64{1, 2}, ids, "own rows only, verbose included, metadata and control excluded")

	ids, _ = readDisplay(t, db, db.LiveLogScope([]archive.LogSpan{{SpanID: "root", Descendants: true}}), 0, hw)
	require.Equal(t, []int64{1, 2, 5}, ids)

	ids, _ = readDisplay(t, db, db.LiveLogScope([]archive.LogSpan{{SpanID: "kid"}, {SpanID: "root", Digests: []string{"xxh3:svc"}}}), 0, hw)
	require.Equal(t, []int64{1, 2, 5, 7}, ids, "a set of spans plus digest-routed rows")

	// The archive's fixed-cut scope agrees.
	view, err := db.SpanViewAt(t.Context(), selectionTrace, db.HighWater(), nil)
	require.NoError(t, err)
	ids, _ = readDisplay(t, db, view.LogQueryScope([]archive.LogSpan{{SpanID: "root", Descendants: true}}), 0, hw)
	require.Equal(t, []int64{1, 2, 5}, ids)
}

func TestDisplayLogsLiveScopeFollowsNewSpans(t *testing.T) {
	db := logQueryStore(t)
	_, err := db.AppendSpans([]Span{logQuerySpan("root", ""), logQuerySpan("other", "")})
	require.NoError(t, err)
	scope := db.LiveLogScope([]archive.LogSpan{{SpanID: "root", Descendants: true}})

	// A grandchild arrives after the scope was resolved, and a log row
	// arrives before its span does.
	_, err = db.AppendSpans([]Span{logQuerySpan("kid", "root"), logQuerySpan("grandkid", "kid"), logQuerySpan("stranger", "other")})
	require.NoError(t, err)
	_, err = db.AppendLogs([]Log{
		selectionLog(t, "grandkid", selectionText("placed")), // 1
		selectionLog(t, "stranger", selectionText("no")),     // 2
		selectionLog(t, "early", selectionText("unknown")),   // 3: span not stored yet
		selectionLog(t, "other", selectionText("no")),        // 4
	})
	require.NoError(t, err)
	ids, cursor := readDisplay(t, db, scope, 0, db.HighWater().Logs)
	require.Equal(t, []int64{1, 3}, ids, "new descendants are placed; unknown spans are left to the reader")
	require.True(t, scope.tree["grandkid"])

	// Once known and not beneath the root, the early span no longer matches.
	_, err = db.AppendSpans([]Span{logQuerySpan("early", "other")})
	require.NoError(t, err)
	_, err = db.AppendLogs([]Log{selectionLog(t, "early", selectionText("no")), selectionLog(t, "grandkid", selectionText("yes"))})
	require.NoError(t, err)
	ids, _ = readDisplay(t, db, scope, cursor, db.HighWater().Logs)
	require.Equal(t, []int64{6}, ids)

	// A selection without descendants never over-selects.
	_, err = db.AppendLogs([]Log{selectionLog(t, "late", selectionText("unknown"))})
	require.NoError(t, err)
	ids, _ = readDisplay(t, db, db.LiveLogScope([]archive.LogSpan{{SpanID: "root"}}), 0, db.HighWater().Logs)
	require.Empty(t, ids)
}

func TestLogsWithoutDisplayReportsPresence(t *testing.T) {
	db := logQueryStore(t)
	_, err := db.AppendLogs([]Log{
		selectionLog(t, "a", selectionText("one")), // 1: presence a
		selectionLog(t, "a", selectionText("two")), // 2
		selectionLog(t, "a", selectionText("name"), selectionAttr(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName)), // 3: row
		selectionLog(t, "b", selectionText("svc"), selectionAttr(telemetry.DagDigestAttr, "xxh3:svc")),                         // 4: presence b+digest
		selectionLog(t, "b", selectionText("plain")),                                                                           // 5: presence b
		selectionLog(t, "b", selectionText("control"), selectionAttr(agentcontrol.VersionAttr, "1")),                           // 6: row
	})
	require.NoError(t, err)
	seen := NewLogPresenceSet()
	var rows []int64
	var presence []LogPresence
	for cursor := int64(0); cursor < db.HighWater().Logs; {
		batch, p, next, err := db.SelectLogsWithoutDisplay(t.Context(), SelectLogsRangeParams{AfterID: cursor, ThroughID: db.HighWater().Logs, Limit: 4}, seen)
		require.NoError(t, err)
		cursor = next
		rows = append(rows, logIDs(batch)...)
		presence = append(presence, p...)
	}
	require.Equal(t, []int64{3, 6}, rows)
	require.Equal(t, []LogPresence{
		{Row: 1, TraceID: selectionTrace, SpanID: "a"},
		{Row: 4, TraceID: selectionTrace, SpanID: "b", Digest: "xxh3:svc"},
		{Row: 5, TraceID: selectionTrace, SpanID: "b"},
	}, presence)
}

func TestSearchLogs(t *testing.T) {
	db := logQueryStore(t)
	_, err := db.AppendSpans([]Span{logQuerySpan("root", ""), logQuerySpan("kid", "root"), logQuerySpan("x", "")})
	require.NoError(t, err)
	_, err = db.AppendLogs([]Log{
		selectionLog(t, "root", selectionText("build ok\nERROR: boom\n")),                                                          // 1
		selectionLog(t, "kid", selectionText("error: lower"), selectionAttr(telemetry.DagDigestAttr, "xxh3:d")),                    // 2
		selectionLog(t, "x", selectionText("ERROR elsewhere")),                                                                     // 3
		selectionLog(t, "root", selectionText("ERROR"), selectionAttr(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName)), // 4: not display
	})
	require.NoError(t, err)
	hw := db.HighWater().Logs

	res, err := db.SearchLogs(t.Context(), archive.LogSearch{Pattern: "ERROR"}, hw, "", nil)
	require.NoError(t, err)
	require.Equal(t, []archive.LogMatch{
		{TraceID: selectionTrace, SpanID: "root", Row: 1, Line: 1, Text: "ERROR: boom"},
		{TraceID: selectionTrace, SpanID: "x", Row: 3, Line: 0, Text: "ERROR elsewhere"},
	}, res.Matches)
	require.Equal(t, hw, res.Next)
	require.False(t, res.More)

	res, err = db.SearchLogs(t.Context(), archive.LogSearch{Pattern: "error", IgnoreCase: true}, hw, "", db.LiveLogScope([]archive.LogSpan{{SpanID: "root", Descendants: true}}))
	require.NoError(t, err)
	require.Len(t, res.Matches, 2)
	require.Equal(t, "xxh3:d", res.Matches[1].Digest)

	res, err = db.SearchLogs(t.Context(), archive.LogSearch{Pattern: `^E\w+`, Regexp: true, Limit: 1}, hw, "", nil)
	require.NoError(t, err)
	require.Len(t, res.Matches, 1)
	require.True(t, res.More)
	require.EqualValues(t, 2, res.Next, "resumes at the row that did not fit")
	res, err = db.SearchLogs(t.Context(), archive.LogSearch{Pattern: `^E\w+`, Regexp: true, Limit: 1, After: res.Next}, hw, "", nil)
	require.NoError(t, err)
	require.Len(t, res.Matches, 1)
	require.EqualValues(t, 3, res.Matches[0].Row)
	require.False(t, res.More)

	_, err = db.SearchLogs(t.Context(), archive.LogSearch{Pattern: "(", Regexp: true}, hw, "", nil)
	require.ErrorContains(t, err, "invalid log search pattern")
}
