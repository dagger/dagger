package dagql

import (
	"fmt"
	"testing"
)

// benchmarkRerunRow is the call row a rerun's engine reports for its i-th
// result: a recipe every engine of the pull request computes, named by its
// own request digest too, over the result before it, with a term, a content
// digest and two parts, as a check's calls report them.
func benchmarkRerunRow(i int) RemoteHolding {
	var deps []uint64
	if i > 0 {
		deps = []uint64{uint64(i)}
	}
	return RemoteHolding{
		Recipe:        testDigest(fmt.Sprintf("rerun-recipe-%d", i)),
		Request:       testDigest(fmt.Sprintf("rerun-request-%d", i)),
		Field:         "withExec",
		Term:          &RemoteTerm{Self: testDigest(fmt.Sprintf("rerun-self-%d", i))},
		ContentDigest: testDigest(fmt.Sprintf("rerun-content-%d", i)),
		TypeName:      "Container",
		Deps:          deps,
		Parts:         []PersistedPartAddress{{Part: "fs"}, {Part: "metadata"}},
	}
}

// BenchmarkAttachRerunRows reports what dagql's AttachRemoteHolding costs a
// call row, with the row's session hold, as a rerun's fresh engine reports
// its calls: rows of recipes the Cloud cache already holds from earlier
// engines (held=N), each a holding of the new engine on an entry and class
// that exist. held=0 is the same rows on an empty cache, as a fill's first
// engine reports them.
func BenchmarkAttachRerunRows(b *testing.B) {
	const rows = 20_000
	for _, held := range []int{0, 30} {
		b.Run(fmt.Sprintf("held=%d", held), func(b *testing.B) {
			ctx := b.Context()
			// Each iteration starts from a cache of its own, seeded outside
			// the timer: one fresh engine's rows on held earlier engines'.
			for range b.N {
				b.StopTimer()
				cloud, err := NewCache(ctx, "", nil, nil)
				if err != nil {
					b.Fatal(err)
				}
				for e := range held {
					engine := CacheID(fmt.Sprintf("earlier-%02d", e))
					for i := range rows {
						key := HolderKey{Cache: engine, Number: uint64(i + 1)}
						if _, err := cloud.AttachRemoteHolding(ctx, key, benchmarkRerunRow(i)); err != nil {
							b.Fatal(err)
						}
					}
				}
				b.StartTimer()
				for i := range rows {
					key := HolderKey{Cache: "fresh", Number: uint64(i + 1)}
					if _, err := cloud.AttachRemoteHolding(ctx, key, benchmarkRerunRow(i)); err != nil {
						b.Fatal(err)
					}
					if _, err := cloud.AddRemoteHold(ctx, key, fmt.Sprintf("s%d", i/49)); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				_ = cloud.CloseDiscardingPersistence()
				b.StartTimer()
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*rows), "ns/row")
		})
	}
}
