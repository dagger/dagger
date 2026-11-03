package dagql

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Repeated exports revisit stored parts on shared closures. Keep the values
// live and vary dependency shape without timing transfer encoding or storage.
func BenchmarkSetStoredPartGraph(b *testing.B) {
	for _, shape := range []string{"chain", "shared-base"} {
		for _, size := range []int{64, 256} {
			b.Run(fmt.Sprintf("%s/results=%d", shape, size), func(b *testing.B) {
				ctx, c := storedPartTestCache(b, WithBlobStore())
				rows := make([]*sharedResult, size)
				parts := make([]PersistedPartOffer, size)
				for i := range rows {
					res := storedPartTestPublish(b, ctx, c, "bench", fmt.Sprintf("result%d", i), &captureTestValue{text: "value"})
					rows[i] = res.cacheSharedResult()
					parts[i] = testLiveOffer()
					parts[i].Owner.DependencyIDs = nil
				}
				c.egraphMu.Lock()
				for i := 1; i < size; i++ {
					dep := i - 1
					if shape == "shared-base" {
						dep = 0
					}
					c.applyPartDependenciesLocked(ctx, rows[i], []*sharedResult{rows[dep]}, nil)
					parts[i].Owner.DependencyIDs = []uint64{uint64(rows[dep].id)}
				}
				c.egraphMu.Unlock()
				for i, row := range rows {
					stored, err := c.SetStoredPart(ctx, uint64(row.id), parts[i], 0)
					require.NoError(b, err)
					require.True(b, stored)
				}
				b.ReportAllocs()
				for b.Loop() {
					for i, row := range rows {
						stored, err := c.SetStoredPart(ctx, uint64(row.id), parts[i], 0)
						require.NoError(b, err)
						require.True(b, stored)
					}
				}
				b.ReportMetric(float64(size), "parts/op")
			})
		}
	}
}
