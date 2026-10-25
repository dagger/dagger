package clientdb

import (
	"fmt"
	"testing"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// BenchmarkLogMetaIndexReopen measures reopening a log-heavy store, whose
// recovery rebuilds the log metadata index row by row.
func BenchmarkLogMetaIndexReopen(b *testing.B) {
	const rows = 200_000
	root := b.TempDir()
	db, err := openStore(b.Context(), root, "client", telemetryTailBudget)
	require.NoError(b, err)
	stdout := selectionLog(b, "", selectionText("a line of ordinary exec output"), &commonpb.KeyValue{Key: "stdio.stream", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 1}}})
	progress := selectionLog(b, "", selectionText("progress"), selectionAttr(telemetryattrs.ProgressItemAttr, "item"))
	payload := selectionLog(b, "", selectionText("markdown"))
	batch := make([]Log, 0, 1000)
	for i := range rows {
		var row Log
		switch {
		case i%100 == 0:
			// Rows with unique attributes defeat any per-blob memoization.
			row = payload
			row.Attributes, err = MarshalProtoJSONs([]*commonpb.KeyValue{selectionAttr(telemetry.ContentTypeAttr, "text/markdown"), selectionAttr("log.uid", fmt.Sprint(i))})
			require.NoError(b, err)
		case i%50 == 0:
			row = progress
		default:
			row = stdout
		}
		row.SpanID.String = fmt.Sprintf("%016x", i/4)
		row.Timestamp = int64(i)
		batch = append(batch, row)
		if len(batch) == cap(batch) {
			_, err = db.AppendLogs(batch)
			require.NoError(b, err)
			batch = batch[:0]
		}
	}
	require.NoError(b, db.Close())
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		db, err := openStore(b.Context(), root, "client", telemetryTailBudget)
		require.NoError(b, err)
		require.Len(b, db.logIdx.meta, rows)
		require.NoError(b, db.Close())
	}
}
