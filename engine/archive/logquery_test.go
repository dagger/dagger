package archive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestClientLogQueryAndSearchPostBodies(t *testing.T) {
	traceID := strings.Repeat("c", 32)
	span := strings.Repeat("a", 16)
	payload, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*otlplogsv1.ResourceLogs{{}}})
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != LogQueryContentType {
			http.Error(w, "want a JSON POST", http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case archiveResourcePath(traceID, "logs"):
			var q LogQuery
			if err := DecodeLogBody(r.Body, &q); err != nil || q.Validate() != nil || q.Spans[0].SpanID != span || !q.Spans[0].Descendants {
				http.Error(w, "bad query", http.StatusBadRequest)
				return
			}
			if r.URL.Query().Get("unsealed") != "1" {
				http.Error(w, "want unsealed", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", enginetel.LiveContentType)
			_ = enginetel.WriteLiveFrame(w, 2, payload)
			_ = enginetel.WriteLiveTerminal(w, 4)
		case archiveResourcePath(traceID, "search"):
			var s LogSearch
			if err := DecodeLogBody(r.Body, &s); err != nil || s.Pattern != "boom" {
				http.Error(w, "bad search", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(LogSearchResult{Matches: []LogMatch{{SpanID: span, Row: 3, Text: "boom"}}, Next: 4})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testArchiveClient(t, server)

	var batches int
	cursor, err := client.LogQuery(context.Background(), traceID, StreamOptions{HighWater: 4, Unsealed: true},
		LogQuery{Spans: []LogSpan{{SpanID: span, Descendants: true}}},
		func(int64, *collogspb.ExportLogsServiceRequest) error { batches++; return nil })
	require.NoError(t, err)
	require.EqualValues(t, 4, cursor)
	require.Equal(t, 1, batches)

	_, err = client.LogQuery(context.Background(), traceID, StreamOptions{HighWater: 4, Logs: &LogSelection{SpanID: span}}, LogQuery{Spans: []LogSpan{{SpanID: span}}}, nil)
	require.ErrorContains(t, err, "excludes")
	_, err = client.LogQuery(context.Background(), traceID, StreamOptions{HighWater: 4}, LogQuery{}, nil)
	require.ErrorContains(t, err, "no spans")

	result, err := client.SearchLogs(context.Background(), traceID, false, LogSearch{Pattern: "boom"})
	require.NoError(t, err)
	require.Equal(t, LogSearchResult{Matches: []LogMatch{{SpanID: span, Row: 3, Text: "boom"}}, Next: 4}, result)
}
