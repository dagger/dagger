package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/clientdb"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// querySpans posts a span query to one client's live endpoints and returns
// the spans it answers, with their stamped attributes, and the terminal
// cursor.
func querySpans(t *testing.T, f *logQueryFixture, clientID string, q clientdb.SpanQuery) (map[string]map[string]any, int64) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+enginetel.LiveSpanQueryPath, httpHandlerFunc(f.ps.SpansQueryHandler, f.records[clientID]))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	body, err := json.Marshal(q)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+enginetel.LiveSpanQueryPath, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Accept", enginetel.LiveContentType)
	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	spans := map[string]map[string]any{}
	for {
		kind, cursor, payload, err := enginetel.ReadLiveFrame(resp.Body)
		require.NoError(t, err)
		switch kind {
		case enginetel.LiveFrameHello:
			continue
		case enginetel.LiveFrameTerminal:
			return spans, cursor
		}
		var batch coltracepb.ExportTraceServiceRequest
		require.NoError(t, proto.Unmarshal(payload, &batch))
		for _, rs := range batch.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				for _, span := range ss.Spans {
					attrs := map[string]any{}
					for _, kv := range span.Attributes {
						switch kv.Key {
						case telemetryattrs.UIChildCountAttr:
							attrs[kv.Key] = kv.GetValue().GetIntValue()
						case telemetryattrs.UIHasLogsAttr:
							attrs[kv.Key] = kv.GetValue().GetBoolValue()
						}
					}
					spans[hex.EncodeToString(span.SpanId)] = attrs
				}
			}
		}
	}
}

func keysOf(m map[string]map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// A span query answers a span's children and ancestors from the live
// store, each stamped with its child count and whether it has display text.
func TestLiveSpanQuery(t *testing.T) {
	const (
		root    = "00000000000000c1"
		mid     = "00000000000000c2"
		kidA    = "00000000000000c3"
		kidB    = "00000000000000c4"
		grand   = "00000000000000c5"
		elsewhr = "00000000000000c6"
	)
	f := newLogQueryFixture(t, "client")
	f.append(t, "client",
		[]clientdb.Span{
			logQuerySpanRow(root, ""),
			logQuerySpanRow(mid, root),
			logQuerySpanRow(kidA, mid),
			logQuerySpanRow(kidB, mid),
			logQuerySpanRow(grand, kidA),
			logQuerySpanRow(elsewhr, root),
		},
		logQueryRow(t, kidB, "kid b says hi"),
	)

	spans, cursor := querySpans(t, f, "client", clientdb.SpanQuery{Spans: []string{mid}, Children: true, Ancestors: true})
	require.Equal(t, []string{root, mid, kidA, kidB}, keysOf(spans), "the span, its children and its ancestors; nothing deeper or beside")
	require.EqualValues(t, 6, cursor, "the store's span high-water mark")
	require.EqualValues(t, 2, spans[root][telemetryattrs.UIChildCountAttr])
	require.EqualValues(t, 2, spans[mid][telemetryattrs.UIChildCountAttr])
	require.EqualValues(t, 1, spans[kidA][telemetryattrs.UIChildCountAttr])
	require.EqualValues(t, 0, spans[kidB][telemetryattrs.UIChildCountAttr])
	require.Equal(t, true, spans[kidB][telemetryattrs.UIHasLogsAttr])
	require.NotContains(t, spans[kidA], telemetryattrs.UIHasLogsAttr)

	exact, _ := querySpans(t, f, "client", clientdb.SpanQuery{Spans: []string{grand}})
	require.Equal(t, []string{grand}, keysOf(exact), "without children or ancestors, just the span")
}
