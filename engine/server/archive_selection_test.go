package server

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dagger/dagger/engine/archive"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/stretchr/testify/require"
	logapi "go.opentelemetry.io/otel/log"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

func TestArchiveLazySelectionHTTP(t *testing.T) {
	srv, sess, db, _ := archiveFixture(t)
	for i := 1; i <= 8; i++ {
		row := clientdb.Span{TraceID: archiveTestTrace, SpanID: fmt.Sprintf("%016x", i), Name: fmt.Sprint(i), Resource: []byte("{}"), InstrumentationScope: []byte("{}"), Attributes: []byte("[]"), Links: []byte("[]"), Events: []byte("[]")}
		row.Attributes = []byte(`[{"key":"dagger.io/ui.has_logs","value":{"boolValue":false}},{"key":"dagger.io/ui.child_count","value":{"intValue":"900"}},{"key":"dagger.io/ui.partial","value":{"boolValue":false}}]`)
		if i > 1 {
			row.ParentSpanID = sql.NullString{String: fmt.Sprintf("%016x", i-1), Valid: true}
		}
		if i == 6 {
			row.StatusCode = 1
		}
		_, err := db.AppendSpans([]clientdb.Span{row})
		require.NoError(t, err)
		rec := scopedLogRecord(t, "test", logapi.StringValue(fmt.Sprint(i)))
		log, err := logRecordRow(&rec)
		require.NoError(t, err)
		log.SpanID = sql.NullString{String: row.SpanID, Valid: true}
		_, err = db.AppendLogs([]clientdb.Log{log})
		require.NoError(t, err)
	}
	require.NoError(t, srv.finalizeSessionArchive(t.Context(), sess, nil))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, srv.serveArchiveHTTP(w, r, sess.clientRecords["main"]))
	}))
	defer server.Close()
	c, err := archive.NewClientWithURL(server.Client(), server.URL)
	require.NoError(t, err)
	m, err := c.Inspect(t.Context(), archiveTestTrace, "")
	require.NoError(t, err)
	require.Equal(t, sess.archiveManifest.Generation, m.Generation)
	release, err := c.AcquireGeneration(t.Context(), archiveTestTrace, m.Generation)
	require.NoError(t, err)
	defer release()
	_, err = c.WithSourceSession("wrong").Inspect(t.Context(), archiveTestTrace, m.Generation)
	require.ErrorIs(t, err, archive.ErrCorrupt)
	readSpans := func(sel *archive.SpanSelection) map[string]bool {
		t.Helper()
		ids := map[string]bool{}
		cursor, err := c.Traces(t.Context(), archiveTestTrace, archive.StreamOptions{Generation: m.Generation, HighWater: m.HighWater.Spans, Spans: sel}, func(_ int64, b *coltracepb.ExportTraceServiceRequest) error {
			for _, rs := range b.ResourceSpans {
				for _, ss := range rs.ScopeSpans {
					for _, s := range ss.Spans {
						id := hex.EncodeToString(s.SpanId)
						ids[id] = true
						if sel != nil && sel.DagUIView {
							attrs := map[string]bool{}
							for _, a := range s.Attributes {
								require.False(t, attrs[a.Key], "duplicate annotation %s", a.Key)
								attrs[a.Key] = true
								switch a.Key {
								case telemetryattrs.UIHasLogsAttr:
									require.True(t, a.Value.GetBoolValue())
								case telemetryattrs.UIChildCountAttr:
									require.LessOrEqual(t, a.Value.GetIntValue(), int64(1))
								case telemetryattrs.UIPartialAttr:
									require.Equal(t, !sel.Full && len(sel.Listen) == 0, a.Value.GetBoolValue())
								}
							}
							require.True(t, attrs[telemetryattrs.UIChildCountAttr])
							require.True(t, attrs[telemetryattrs.UIHasLogsAttr])
							require.True(t, attrs[telemetryattrs.UIPartialAttr])
						}
					}
				}
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, m.HighWater.Spans, cursor)
		return ids
	}
	initial := readSpans(&archive.SpanSelection{DagUIView: true})
	require.True(t, initial[fmt.Sprintf("%016x", 6)], "deep errors are priority")
	require.False(t, initial[fmt.Sprintf("%016x", 8)], "initial view is not the whole trace")
	require.Len(t, readSpans(&archive.SpanSelection{Full: true, DagUIView: true}), 8)
	require.Len(t, readSpans(&archive.SpanSelection{NoRoot: true, Listen: []string{fmt.Sprintf("%016x", 3)}, DagUIView: true}), 8)
	for _, descendants := range []bool{false, true} {
		var bodies []string
		cursor, err := c.Logs(t.Context(), archiveTestTrace, archive.StreamOptions{Generation: m.Generation, HighWater: m.HighWater.Logs, Logs: &archive.LogSelection{SpanID: fmt.Sprintf("%016x", 3), Descendants: descendants, Records: archive.LogRecordsLogs}}, func(_ int64, b *collogspb.ExportLogsServiceRequest) error {
			for _, rl := range b.ResourceLogs {
				for _, sl := range rl.ScopeLogs {
					for _, log := range sl.LogRecords {
						bodies = append(bodies, log.Body.GetStringValue())
					}
				}
			}
			return nil
		})
		require.NoError(t, err)
		require.Equal(t, m.HighWater.Logs, cursor)
		if descendants {
			require.Equal(t, []string{"3", "4", "5", "6", "7", "8"}, bodies)
		} else {
			require.Equal(t, []string{"3"}, bodies)
		}
	}
}
