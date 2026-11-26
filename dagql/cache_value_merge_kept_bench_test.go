package dagql

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/snapshots/config"
)

// BenchmarkCloudMergeKeptRecords times a blob-backed cache's merge of an
// engine's records-only bundle of 2,000 records, a chain, every one of
// which the cache already holds: the merge keeps them all. It reports the
// merge's time a record.
func BenchmarkCloudMergeKeptRecords(b *testing.B) {
	ctx, c, srv := transferTestCache(b)
	var prev AnyResult
	for i := range 2000 {
		res := persistedListTestResult(b, ctx, c, srv, fmt.Sprintf("kept-bench-%d", i), String(fmt.Sprintf("kept %d", i)))
		if prev != nil {
			transferTestDependency(c, ctx, res, prev)
		}
		prev = res
	}
	var bundle ValueBundle
	require.NoError(b, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{prev}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	cctx, cloud := storedPartTestCache(b, WithBlobStore())
	_, err := cloud.MergeValues(cctx, "engine-a", bundle)
	require.NoError(b, err)
	_, err = cloud.MergeValues(cctx, "engine-b", bundle)
	require.NoError(b, err)
	b.ResetTimer()
	for b.Loop() {
		reply, err := cloud.MergeValues(cctx, "engine-b", bundle)
		if err != nil || reply.Rows.Kept != len(bundle.Values) {
			b.Fatalf("merge: %v, %d kept of %d", err, reply.Rows.Kept, len(bundle.Values))
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(bundle.Values))/1000, "us/record")
}
