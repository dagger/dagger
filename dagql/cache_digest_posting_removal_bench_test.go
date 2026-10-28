package dagql

import (
	"fmt"
	"testing"
)

// Exercise normal persisted-root selection and the ownership cascade, including
// an import, rather than measuring only the digest index helper.
func BenchmarkMetadataPruneRestoredWideOutput(b *testing.B) {
	for _, width := range []int{256, 1024, 4096} {
		b.Run(fmt.Sprint(width), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				f := cacheDerivedIndexWideOutputFixture(b, width, true)
				before := len(f.cache.resultsByID)
				maximum := cacheMetadataResultEstimatedBytes * int64(before) / 2
				b.StartTimer()
				report, err := f.cache.PruneMetadataEstimate(f.ctx, maximum, 1)
				b.StopTimer()
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(report.RemovedPersistedRootCount), "roots/op")
				if !report.Triggered || report.AfterPrune.EstimatedBytes >= maximum || report.RemovedPersistedRootCount != before-len(f.cache.resultsByID) {
					b.Fatalf("prune removed %d/%d roots; %d results remain", report.RemovedPersistedRootCount, before, len(f.cache.resultsByID))
				}
				f.close(b)
			}
		})
	}
}
