package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	logapi "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

const (
	lqRoot  = "00000000000000a1"
	lqKid   = "00000000000000a2"
	lqOther = "00000000000000b1"
)

func logQuerySpanRow(id, parent string) clientdb.Span {
	s := clientdb.Span{TraceID: archiveTestTrace, SpanID: id}
	if parent != "" {
		s.ParentSpanID.String, s.ParentSpanID.Valid = parent, true
	}
	return s
}

func logQueryRow(t *testing.T, span, body string, attrs ...logapi.KeyValue) clientdb.Log {
	t.Helper()
	rec := scopedLogRecord(t, "test", logapi.StringValue(body), attrs...)
	row, err := logRecordRow(&rec)
	require.NoError(t, err)
	row.SpanID.String = span
	return row
}

type logQueryFixture struct {
	ps       *PubSub
	srv      *Server
	records  map[string]*clientRecord
	shutdown map[string]chan struct{}
}

func newLogQueryFixture(t *testing.T, clientIDs ...string) *logQueryFixture {
	dbs := clientdb.NewDBs(t.TempDir())
	t.Cleanup(func() { _ = dbs.Close() })
	f := &logQueryFixture{srv: &Server{clientDBs: dbs}, records: map[string]*clientRecord{}, shutdown: map[string]chan struct{}{}}
	f.ps = &PubSub{srv: f.srv}
	sess := &daggerSession{telemetryPubSub: f.ps}
	for _, id := range clientIDs {
		ch := make(chan struct{})
		f.shutdown[id] = ch
		f.records[id] = &clientRecord{daggerSession: sess, clientID: id, shutdownCh: ch}
	}
	return f
}

func (f *logQueryFixture) append(t *testing.T, clientID string, spans []clientdb.Span, logs ...clientdb.Log) {
	t.Helper()
	db, err := f.srv.clientDBs.Open(t.Context(), clientID)
	require.NoError(t, err)
	defer db.Close()
	if len(spans) > 0 {
		_, err = db.AppendSpans(spans)
		require.NoError(t, err)
	}
	if len(logs) > 0 {
		_, err = db.AppendLogs(logs)
		require.NoError(t, err)
	}
}

// serve exposes one client's live telemetry endpoints over a real HTTP
// server, so a follow can be read while it is still being written.
func (f *logQueryFixture) serve(t *testing.T, clientID string) *httptest.Server {
	record := f.records[clientID]
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/logs", httpHandlerFunc(f.ps.LogsSubscribeHandler, record))
	mux.HandleFunc("POST /v1/logs/query", httpHandlerFunc(f.ps.LogsQueryHandler, record))
	mux.HandleFunc("POST /v1/logs/search", httpHandlerFunc(f.ps.LogsSearchHandler, record))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func postLogQuery(t *testing.T, ts *httptest.Server, q any) *http.Response {
	t.Helper()
	body, err := json.Marshal(q)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/v1/logs/query", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Accept", enginetel.LiveContentType)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

type logFrame struct {
	kind   enginetel.LiveFrameKind
	cursor int64
	bodies []string
	attrs  []map[string]string
}

func readLogFrame(t *testing.T, r io.Reader) logFrame {
	t.Helper()
	kind, cursor, payload, err := enginetel.ReadLiveFrame(r)
	require.NoError(t, err)
	f := logFrame{kind: kind, cursor: cursor}
	if kind != enginetel.LiveFrameData {
		return f
	}
	var batch collogspb.ExportLogsServiceRequest
	require.NoError(t, proto.Unmarshal(payload, &batch))
	for _, rl := range batch.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				f.bodies = append(f.bodies, lr.GetBody().GetStringValue())
				attrs := map[string]string{"span": fmt.Sprintf("%x", lr.SpanId)}
				for _, kv := range lr.Attributes {
					attrs[kv.Key] = kv.GetValue().GetStringValue()
				}
				f.attrs = append(f.attrs, attrs)
			}
		}
	}
	return f
}

// readUntil reads frames until want bodies arrived (or a terminal frame),
// returning the bodies and the last frame.
func readLogBodies(t *testing.T, r io.Reader, want int) ([]string, logFrame) {
	t.Helper()
	var bodies []string
	for {
		f := readLogFrame(t, r)
		bodies = append(bodies, f.bodies...)
		if f.kind == enginetel.LiveFrameTerminal || (want > 0 && len(bodies) >= want) {
			return bodies, f
		}
	}
}

func TestLiveLogQueryOneShotAndFollow(t *testing.T) {
	f := newLogQueryFixture(t, "client")
	f.append(t, "client",
		[]clientdb.Span{logQuerySpanRow(lqRoot, ""), logQuerySpanRow(lqKid, lqRoot), logQuerySpanRow(lqOther, "")},
		logQueryRow(t, lqRoot, "root 1"),
		logQueryRow(t, lqOther, "other"),
		logQueryRow(t, lqKid, "kid 1", logapi.Bool(telemetry.LogsVerboseAttr, true)),
		logQueryRow(t, lqRoot, "name", logapi.String(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName)),
	)
	ts := f.serve(t, "client")
	query := archive.LogQuery{Spans: []archive.LogSpan{{SpanID: lqRoot, Descendants: true}}}

	resp := postLogQuery(t, ts, query)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get(enginetel.LiveFeaturesHeader), enginetel.LiveFeatureLogQuery)
	require.Equal(t, enginetel.LiveFrameHello, readLogFrame(t, resp.Body).kind)
	bodies, last := readLogBodies(t, resp.Body, 0)
	require.Equal(t, []string{"root 1", "kid 1"}, bodies, "display text of the subtree, verbose included")
	require.Equal(t, enginetel.LiveFrameTerminal, last.kind)
	require.EqualValues(t, 4, last.cursor, "a one-shot read ends at the store's end")

	// Follow: rows appended later arrive on the open stream, including those
	// of a span that joins the subtree after the query started.
	query.Follow = true
	follow := postLogQuery(t, ts, query)
	defer follow.Body.Close()
	resp = follow
	require.Equal(t, enginetel.LiveFrameHello, readLogFrame(t, resp.Body).kind)
	bodies, _ = readLogBodies(t, resp.Body, 2)
	require.Equal(t, []string{"root 1", "kid 1"}, bodies)
	const late = "00000000000000a3"
	f.append(t, "client", []clientdb.Span{logQuerySpanRow(late, lqKid)},
		logQueryRow(t, lqOther, "other 2"),
		logQueryRow(t, late, "late child"),
	)
	bodies, _ = readLogBodies(t, resp.Body, 1)
	require.Equal(t, []string{"late child"}, bodies)

	// The client's telemetry drains: the follow ends with a terminal frame
	// at the store's end.
	f.append(t, "client", nil, logQueryRow(t, lqRoot, "last words"))
	close(f.shutdown["client"])
	bodies, last = readLogBodies(t, resp.Body, 0)
	require.Equal(t, []string{"last words"}, bodies)
	require.Equal(t, enginetel.LiveFrameTerminal, last.kind)
	require.EqualValues(t, 7, last.cursor)
}

func TestLiveLogQueryThrough(t *testing.T) {
	f := newLogQueryFixture(t, "client")
	f.append(t, "client",
		[]clientdb.Span{logQuerySpanRow(lqRoot, ""), logQuerySpanRow(lqKid, lqRoot)},
		logQueryRow(t, lqRoot, "root 1"),
		logQueryRow(t, lqKid, "kid 1"),
		logQueryRow(t, lqRoot, "root 2"),
	)
	ts := f.serve(t, "client")
	read := func(q archive.LogQuery) ([]string, logFrame) {
		resp := postLogQuery(t, ts, q)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, enginetel.LiveFrameHello, readLogFrame(t, resp.Body).kind)
		return readLogBodies(t, resp.Body, 0)
	}
	spans := []archive.LogSpan{{SpanID: lqRoot, Descendants: true}}

	bodies, last := read(archive.LogQuery{Spans: spans, Through: 2})
	require.Equal(t, []string{"root 1", "kid 1"}, bodies, "a bounded one-shot stops at its bound")
	require.Equal(t, enginetel.LiveFrameTerminal, last.kind)
	require.EqualValues(t, 2, last.cursor)

	bodies, last = read(archive.LogQuery{Spans: spans, Through: 100})
	require.Equal(t, []string{"root 1", "kid 1", "root 2"}, bodies, "a bound past the end stops at the end")
	require.EqualValues(t, 3, last.cursor)
}

func TestLiveLogQueryResumesAndValidates(t *testing.T) {
	f := newLogQueryFixture(t, "client")
	f.append(t, "client", []clientdb.Span{logQuerySpanRow(lqRoot, "")},
		logQueryRow(t, lqRoot, "one"), logQueryRow(t, lqRoot, "two"))
	ts := f.serve(t, "client")

	body, err := json.Marshal(archive.LogQuery{Spans: []archive.LogSpan{{SpanID: lqRoot}}})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/v1/logs/query", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Accept", enginetel.LiveContentType)
	req.Header.Set(enginetel.LiveCursorHeader, "1")
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, enginetel.LiveFrameHello, readLogFrame(t, resp.Body).kind)
	bodies, _ := readLogBodies(t, resp.Body, 0)
	require.Equal(t, []string{"two"}, bodies, "the cursor is the store's log row ID")

	for name, q := range map[string]any{
		"no spans":   archive.LogQuery{},
		"bad span":   archive.LogQuery{Spans: []archive.LogSpan{{SpanID: "nope"}}},
		"not json":   "spans",
		"too many":   archive.LogQuery{Spans: make([]archive.LogSpan, archive.MaxLogQuerySpans+1)},
		"empty dig":  archive.LogQuery{Spans: []archive.LogSpan{{SpanID: lqRoot, Digests: []string{""}}}},
		"wrong type": map[string]any{"spans": 1},
	} {
		resp := postLogQuery(t, ts, q)
		resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, name)
	}
}

// Thousands of spans travel in the body, far beyond what a URL could carry.
func TestLiveLogQueryLargeSpanSet(t *testing.T) {
	f := newLogQueryFixture(t, "client")
	f.append(t, "client", []clientdb.Span{logQuerySpanRow(lqRoot, ""), logQuerySpanRow(lqOther, "")},
		logQueryRow(t, lqOther, "unselected"),
		logQueryRow(t, lqRoot, "selected"),
		logQueryRow(t, lqOther, "routed", logapi.String(telemetry.DagDigestAttr, "xxh3:svc")),
	)
	ts := f.serve(t, "client")
	spans := make([]archive.LogSpan, 0, archive.MaxLogQuerySpans)
	for i := range archive.MaxLogQuerySpans - 1 {
		spans = append(spans, archive.LogSpan{SpanID: fmt.Sprintf("%016x", 0x10000+i), Descendants: i%2 == 0})
	}
	spans = append(spans, archive.LogSpan{SpanID: lqRoot, Digests: []string{"xxh3:svc"}})
	body, err := json.Marshal(archive.LogQuery{Spans: spans})
	require.NoError(t, err)
	require.Greater(t, len(body), 64<<10)
	resp := postLogQuery(t, ts, archive.LogQuery{Spans: spans})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, enginetel.LiveFrameHello, readLogFrame(t, resp.Body).kind)
	bodies, _ := readLogBodies(t, resp.Body, 0)
	require.Equal(t, []string{"selected", "routed"}, bodies)
}

// A nested client reads its own store, never its parent's.
func TestLiveLogQueryNestedClientStore(t *testing.T) {
	f := newLogQueryFixture(t, "main", "nested")
	f.append(t, "main", []clientdb.Span{logQuerySpanRow(lqRoot, "")}, logQueryRow(t, lqRoot, "main's"))
	f.append(t, "nested", []clientdb.Span{logQuerySpanRow(lqRoot, "")}, logQueryRow(t, lqRoot, "nested's"))
	resp := postLogQuery(t, f.serve(t, "nested"), archive.LogQuery{Spans: []archive.LogSpan{{SpanID: lqRoot}}})
	defer resp.Body.Close()
	require.Equal(t, enginetel.LiveFrameHello, readLogFrame(t, resp.Body).kind)
	bodies, _ := readLogBodies(t, resp.Body, 0)
	require.Equal(t, []string{"nested's"}, bodies)
}

func TestLivePresencePush(t *testing.T) {
	f := newLogQueryFixture(t, "client")
	f.append(t, "client", nil,
		logQueryRow(t, lqRoot, "text 1"),
		logQueryRow(t, lqRoot, "text 2"),
		logQueryRow(t, lqRoot, "renamed", logapi.String(telemetryattrs.LogRoleAttr, telemetryattrs.LogRoleSpanName)),
		logQueryRow(t, lqKid, "svc", logapi.String(telemetry.DagDigestAttr, "xxh3:svc")),
	)
	close(f.shutdown["client"])
	ts := f.serve(t, "client")
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/v1/logs?display=presence", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", enginetel.LiveContentType)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Contains(t, strings.Split(resp.Header.Get(enginetel.LiveFeaturesHeader), ","), enginetel.LiveFeatureLogPresence)
	require.Equal(t, enginetel.LiveFrameHello, readLogFrame(t, resp.Body).kind)
	var bodies []string
	var attrs []map[string]string
	for {
		frame := readLogFrame(t, resp.Body)
		if frame.kind == enginetel.LiveFrameTerminal {
			require.EqualValues(t, 4, frame.cursor)
			break
		}
		bodies = append(bodies, frame.bodies...)
		attrs = append(attrs, frame.attrs...)
	}
	require.Len(t, bodies, 3)
	require.Contains(t, bodies, "renamed", "non-display records are pushed whole")
	require.NotContains(t, bodies, "text 1")
	var presence []string
	for _, a := range attrs {
		if a[telemetryattrs.LogRoleAttr] == telemetryattrs.LogRolePresence {
			presence = append(presence, a["span"]+"/"+a[telemetry.DagDigestAttr])
		}
	}
	require.ElementsMatch(t, []string{lqRoot + "/", lqKid + "/xxh3:svc"}, presence, "one presence record per span and digest")
}

// A batch the stream discards to refetch a smaller one (too large to frame)
// must not swallow its presence.
func TestPresenceSurvivesRefetch(t *testing.T) {
	f := newLogQueryFixture(t, "client")
	f.append(t, "client", nil, logQueryRow(t, lqRoot, "text"), logQueryRow(t, lqKid, "text"))
	db, err := f.srv.clientDBs.Open(t.Context(), "client")
	require.NoError(t, err)
	defer db.Close()
	count := func(msg proto.Message) int {
		n := 0
		for _, rl := range msg.(*collogspb.ExportLogsServiceRequest).ResourceLogs {
			for _, sl := range rl.ScopeLogs {
				n += len(sl.LogRecords)
			}
		}
		return n
	}
	fetch := presenceLogFetcher(nil)
	_, msg, _, err := fetch(t.Context(), db, 0, 10)
	require.NoError(t, err)
	require.Equal(t, 2, count(msg))
	next, msg, rows, err := fetch(t.Context(), db, 0, 1)
	require.NoError(t, err)
	require.Equal(t, 1, rows)
	require.Equal(t, 1, count(msg), "the refetch reports the presence again")
	_, msg, _, err = fetch(t.Context(), db, next, 10)
	require.NoError(t, err)
	require.Equal(t, 1, count(msg))
	_, msg, rows, err = fetch(t.Context(), db, 2, 10)
	require.NoError(t, err)
	require.Zero(t, rows, "idle")
	require.Nil(t, msg)
}

func TestLiveLogSearch(t *testing.T) {
	f := newLogQueryFixture(t, "client")
	f.append(t, "client", []clientdb.Span{logQuerySpanRow(lqRoot, ""), logQuerySpanRow(lqKid, lqRoot), logQuerySpanRow(lqOther, "")},
		logQueryRow(t, lqKid, "compiling\npanic: oops"),
		logQueryRow(t, lqOther, "panic elsewhere"),
	)
	ts := f.serve(t, "client")
	search := func(s archive.LogSearch) (archive.LogSearchResult, int) {
		body, err := json.Marshal(s)
		require.NoError(t, err)
		resp, err := ts.Client().Post(ts.URL+"/v1/logs/search", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		var result archive.LogSearchResult
		if resp.StatusCode == http.StatusOK {
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		}
		return result, resp.StatusCode
	}
	result, code := search(archive.LogSearch{Pattern: "panic"})
	require.Equal(t, http.StatusOK, code)
	require.Len(t, result.Matches, 2)
	require.Equal(t, archive.LogMatch{TraceID: archiveTestTrace, SpanID: lqKid, Row: 1, Line: 1, Text: "panic: oops"}, result.Matches[0])

	result, _ = search(archive.LogSearch{Pattern: "PANIC", IgnoreCase: true, Spans: []archive.LogSpan{{SpanID: lqRoot, Descendants: true}}})
	require.Len(t, result.Matches, 1)
	require.Equal(t, lqKid, result.Matches[0].SpanID)

	_, code = search(archive.LogSearch{Pattern: "[", Regexp: true})
	require.Equal(t, http.StatusBadRequest, code)
}

func TestArchiveLogQueryAndSearch(t *testing.T) {
	srv := &Server{clientDBs: clientdb.NewDBs(t.TempDir())}
	db, err := srv.clientDBs.Open(t.Context(), "main")
	require.NoError(t, err)
	defer db.Close()
	_, err = db.AppendSpans([]clientdb.Span{logQuerySpanRow(lqRoot, ""), logQuerySpanRow(lqKid, lqRoot), logQuerySpanRow(lqOther, "")})
	require.NoError(t, err)
	_, err = db.AppendLogs([]clientdb.Log{
		logQueryRow(t, lqKid, "kid"),
		logQueryRow(t, lqOther, "other"),
		logQueryRow(t, lqOther, "routed", logapi.String(telemetry.DagDigestAttr, "xxh3:svc")),
	})
	require.NoError(t, err)
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	_, err = db.AppendLogs([]clientdb.Log{logQueryRow(t, lqKid, "after the cut")})
	require.NoError(t, err)
	manifest := archive.Manifest{TraceID: archiveTestTrace, MainClientID: "main", HighWater: archive.HighWater{Spans: cut.Spans, Logs: cut.Logs, Metrics: cut.Metrics}}

	body, err := json.Marshal(archive.LogQuery{Spans: []archive.LogSpan{{SpanID: lqRoot, Descendants: true, Digests: []string{"xxh3:svc"}}}})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	require.NoError(t, srv.serveArchiveSignalWithPayloadLimit(resp, req, manifest, manifest.HighWater, false, "logs", enginetel.MaxLivePayloadSize))
	bodies, last := readLogBodies(t, resp.Body, 0)
	require.Equal(t, []string{"kid", "routed"}, bodies, "the same multi-span body reads an archive at its cut")
	require.EqualValues(t, cut.Logs, last.cursor)

	// A body excludes query-string selections.
	req = httptest.NewRequest(http.MethodPost, "/?span_id="+lqRoot, bytes.NewReader(body))
	err = srv.serveArchiveSignalWithPayloadLimit(httptest.NewRecorder(), req, manifest, manifest.HighWater, false, "logs", enginetel.MaxLivePayloadSize)
	require.ErrorContains(t, err, "excludes")

	body, err = json.Marshal(archive.LogSearch{Pattern: "kid"})
	require.NoError(t, err)
	resp = httptest.NewRecorder()
	require.NoError(t, srv.serveArchiveSearch(resp, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)), manifest, manifest.HighWater))
	var result archive.LogSearchResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Len(t, result.Matches, 1, "the search stops at the cut")
	require.Equal(t, strconv.FormatInt(cut.Logs, 10), strconv.FormatInt(result.Next, 10))
}

// TestFlushHandler: the barrier flushes what the session's providers still
// hold into the client's store before answering its high-water marks.
func TestFlushHandler(t *testing.T) {
	f := newLogQueryFixture(t, "client")
	f.append(t, "client", []clientdb.Span{logQuerySpanRow(lqRoot, ""), logQuerySpanRow(lqKid, lqRoot)},
		logQueryRow(t, lqKid, "stored"),
	)
	sess := f.records["client"].daggerSession
	sess.loggerProvider = sdklog.NewLoggerProvider(sdklog.WithProcessor(
		sdklog.NewBatchProcessor(f.ps.Logs("client"), sdklog.WithExportInterval(time.Hour)),
	))
	t.Cleanup(func() { _ = sess.loggerProvider.Shutdown(context.Background()) })
	var rec logapi.Record
	rec.SetBody(logapi.StringValue("batched"))
	sess.loggerProvider.Logger("test").Emit(t.Context(), rec)

	mux := http.NewServeMux()
	mux.HandleFunc("POST "+enginetel.LiveFlushPath, httpHandlerFunc(f.ps.FlushHandler, f.records["client"]))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	resp, err := ts.Client().Post(ts.URL+enginetel.LiveFlushPath, "", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var marks enginetel.LiveFlushResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&marks))
	require.Equal(t, enginetel.LiveFlushResult{Spans: 2, Logs: 2}, marks, "the batched record is stored and counted")
}
