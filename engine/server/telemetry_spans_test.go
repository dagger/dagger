package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
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

func TestSpanQueryFrames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		count    int
		perFrame int // byte budget fits this many ordinary spans exactly
		bad      int // 1-based index of an oversized span, if any
	}{
		{"exact fit", 5, 5, 0},
		{"multiple frames", 5, 2, 0},
		{"partial answer", 5, 2, 3},
		{"oversized singleton", 1, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := clientdb.SpanQueryResult{Through: 42}
			var want []string
			for i := 1; i <= tc.count; i++ {
				row := logQuerySpanRow(fmt.Sprintf("%016x", i), "")
				res := fmt.Sprintf("res-%d", (i-1)/4)
				scope := fmt.Sprintf("scope-%d", (i-1)/2)
				var err error
				row.Resource, err = proto.Marshal(&otlpresourcev1.Resource{Attributes: []*otlpcommonv1.KeyValue{{
					Key: "service.name", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: res}},
				}}})
				require.NoError(t, err)
				row.InstrumentationScope, err = proto.Marshal(&otlpcommonv1.InstrumentationScope{Name: scope})
				require.NoError(t, err)
				result.Spans = append(result.Spans, row)
				if i != tc.bad {
					want = append(want, res+"/"+scope+"/"+row.SpanID)
				}
			}
			maxBytes := proto.Size(spanQueryBatch(result.Spans[:tc.perFrame], result))
			if tc.bad != 0 {
				result.Spans[tc.bad-1].Name = strings.Repeat("x", maxBytes)
			}
			var stream bytes.Buffer
			require.NoError(t, writeSpanQueryFrames(&stream, result, maxBytes))
			var got []string
			var frames int
			for {
				kind, cursor, payload, err := enginetel.ReadLiveFrame(&stream)
				require.Equal(t, result.Through, cursor)
				if err != nil || kind == enginetel.LiveFrameTerminal {
					if tc.bad != 0 {
						require.ErrorIs(t, err, enginetel.ErrLiveStream)
						require.ErrorContains(t, err, result.Spans[tc.bad-1].SpanID)
					} else {
						require.NoError(t, err)
					}
					break
				}
				require.Equal(t, enginetel.LiveFrameData, kind)
				require.LessOrEqual(t, len(payload), maxBytes)
				var batch coltracepb.ExportTraceServiceRequest
				require.NoError(t, proto.Unmarshal(payload, &batch))
				for _, rs := range batch.ResourceSpans {
					for _, ss := range rs.ScopeSpans {
						for _, span := range ss.Spans {
							res := rs.Resource.Attributes[0].GetValue().GetStringValue()
							got = append(got, res+"/"+ss.Scope.Name+"/"+hex.EncodeToString(span.SpanId))
						}
					}
				}
				frames++
			}
			require.Zero(t, stream.Len(), "nothing after the terminal or error frame")
			require.Equal(t, want, got, "deliver every span that fits, once")
			if tc.count == tc.perFrame && tc.bad == 0 {
				require.Equal(t, 1, frames)
			} else if len(want) > tc.perFrame {
				require.Greater(t, frames, 1)
			}
		})
	}
}
