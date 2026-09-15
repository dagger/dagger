package clientdb

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDump(t *testing.T) {
	root := t.TempDir()
	store, err := openStore(t.Context(), root, "client", telemetryTailBudget)
	require.NoError(t, err)
	_, err = store.AppendSpans([]Span{{TraceID: "trace", SpanID: "span"}})
	require.NoError(t, err)
	_, err = store.AppendLogs([]Log{{Body: []byte("log")}})
	require.NoError(t, err)
	_, err = store.AppendMetrics([]Metric{{Data: []byte("metric")}})
	require.NoError(t, err)
	require.NoError(t, store.Close())

	var out bytes.Buffer
	require.NoError(t, Dump(t.Context(), root, "client", DumpAll, &out))
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.Len(t, lines, 3)
	var streams []string
	for _, line := range lines {
		var record struct {
			Stream string          `json:"stream"`
			Row    json.RawMessage `json:"row"`
		}
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		require.NotEmpty(t, record.Row)
		streams = append(streams, record.Stream)
	}
	require.Equal(t, []string{DumpSpans, DumpLogs, DumpMetrics}, streams)

	out.Reset()
	require.NoError(t, Dump(t.Context(), root, "client", DumpLogs, &out))
	require.Contains(t, out.String(), `"stream":"logs"`)
	require.NotContains(t, out.String(), `"stream":"spans"`)
	require.Error(t, Dump(t.Context(), root, "client", "unknown", &out))
}
