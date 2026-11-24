package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// markedSpans returns how many of the session's ended spans carry the wcprof
// engine span mark.
func markedSpans(rec *tracetest.SpanRecorder) int {
	marked := 0
	for _, span := range rec.Ended() {
		for _, kv := range span.Attributes() {
			if string(kv.Key) == telemetryattrs.WcprofEngineSpanAttr {
				marked++
			}
		}
	}
	return marked
}

// While wcprof isn't recording, a session's spans get no mark and its trace no
// count. Its carrier still declares its cache spans.
func TestWcprofMarkerOffWhileNotRecording(t *testing.T) {
	t.Parallel()
	srv := &Server{wcprofSpanCount: newWcprofSpanCounter()}
	traceID := trace.TraceID{6}
	sess, rec := cacheSpanTestSession(t, "s", traceID, srv.wcprofSpanCount)
	sess.wcprofEnabled = false
	emitTestSpan(sess, "7")
	emitTestSpan(sess, "")

	require.Zero(t, srv.wcprofSpanCount.Final(traceID))
	srv.stampSessionComplete(context.Background(), sess)

	require.Zero(t, markedSpans(rec))
	attrs, ok := carrierAttrs(rec)
	require.True(t, ok)
	require.Equal(t, "1", attrs[telemetryattrs.CacheSessionSpansAttr])
	require.NotContains(t, attrs, telemetryattrs.WcprofSessionSpanCountAttr)
}

// A session that opted into wcprof marks every span it starts and declares
// exactly that many; the carrier is neither marked nor counted.
func TestWcprofMarkerOnForProfiledSession(t *testing.T) {
	t.Parallel()
	srv := &Server{wcprofSpanCount: newWcprofSpanCounter()}
	sess, rec := cacheSpanTestSession(t, "s", trace.TraceID{7}, srv.wcprofSpanCount)
	emitTestSpan(sess, "7")
	emitTestSpan(sess, "")
	emitTestSpan(sess, "")
	srv.stampSessionComplete(context.Background(), sess)

	require.Equal(t, 3, markedSpans(rec))
	attrs, ok := carrierAttrs(rec)
	require.True(t, ok)
	require.Equal(t, "3", attrs[telemetryattrs.WcprofSessionSpanCountAttr])
}

// Engine-global recording marks and counts a session that didn't opt in.
func TestWcprofMarkerOnWhileRecordingGlobally(t *testing.T) {
	recordGlobally(t)
	srv := &Server{wcprofSpanCount: newWcprofSpanCounter()}
	sess, rec := cacheSpanTestSession(t, "s", trace.TraceID{8}, srv.wcprofSpanCount)
	sess.wcprofEnabled = false
	emitTestSpan(sess, "7")
	emitTestSpan(sess, "")
	srv.stampSessionComplete(context.Background(), sess)

	require.Equal(t, 2, markedSpans(rec))
	attrs, ok := carrierAttrs(rec)
	require.True(t, ok)
	require.Equal(t, "2", attrs[telemetryattrs.WcprofSessionSpanCountAttr])
}
