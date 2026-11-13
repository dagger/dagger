package dagql

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// BenchmarkChangeMarks measures what marking costs a blob-backed cache's
// writes, each against the same writes with marking off (changes nil, as on
// an engine's own cache):
//   - rows: 8 engine caches each attach, hold and retain 2,000 holdings of
//     the same 2,000 recipes, with dependencies and a completed part, then
//     end their sessions; the recipes' entries share a content digest in
//     classes of 4 or 64 entries, so each mark widens over its class; then
//     every cache is released and collected, which removes every entry,
//     whole classes at once;
//   - merge: an engine's export of 1,000 roots with a dependency each
//     merged into an empty Cloud cache, and its commit's hold;
//   - prune: a metadata prune that drops every one of those values, after
//     a second engine cache's holdings of them.
//
// It reports each arm's time per row and per collected holding, merge-ms
// and commit-hold-ms, and prune-ms.
func BenchmarkChangeMarks(b *testing.B) {
	for _, class := range []int{4, 64} {
		for _, marks := range []bool{true, false} {
			b.Run(fmt.Sprintf("rows/class=%d/marks=%t", class, marks), func(b *testing.B) {
				benchmarkChangeMarksRows(b, class, marks)
			})
		}
	}
	bundle := changeMarksBenchBundle(b, 1_000)
	for _, marks := range []bool{true, false} {
		b.Run(fmt.Sprintf("merge-prune/marks=%t", marks), func(b *testing.B) {
			benchmarkChangeMarksMergePrune(b, bundle, marks)
		})
	}
}

func changeMarksBenchCloud(b *testing.B, marks bool) (context.Context, *Cache) {
	ctx := cacheTestContext(context.Background())
	c, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(b, err)
	if !marks {
		c.changes = nil
	}
	return ctx, c
}

func benchmarkChangeMarksRows(b *testing.B, class int, marks bool) {
	const engines, holdings = 8, 2_000
	var applying, collecting time.Duration
	for range b.N {
		b.StopTimer()
		ctx, cloud := changeMarksBenchCloud(b, marks)
		b.StartTimer()
		start := time.Now()
		for e := range engines {
			cache := CacheID(fmt.Sprintf("cache-%d", e))
			session := "s-" + string(cache)
			for i := range holdings {
				key := HolderKey{Cache: cache, Number: uint64(i + 1)}
				recipe := testDigest(fmt.Sprintf("recipe-%d", i))
				desc := RemoteHolding{
					Recipe: recipe, Request: recipe, Field: "field", TypeName: "Directory",
					ContentDigest: testDigest(fmt.Sprintf("content-%d", i/class)),
					Parts:         []PersistedPartAddress{{Part: "snapshot"}},
				}
				if i > 0 {
					desc.Deps = []uint64{uint64(i)}
				}
				_, err := cloud.AttachRemoteHolding(ctx, key, desc)
				require.NoError(b, err)
				_, err = cloud.AddRemoteHold(ctx, key, session)
				require.NoError(b, err)
				_, err = cloud.ObserveRemoteRetention(ctx, key, *retainedAt(1, int64(i), 0))
				require.NoError(b, err)
			}
			cloud.ReleaseRemoteSession(ctx, cache, session)
		}
		applying += time.Since(start)
		start = time.Now()
		var candidates []HolderKey
		for e := range engines {
			candidates = append(candidates, cloud.ReleaseRemoteCache(ctx, CacheID(fmt.Sprintf("cache-%d", e))).Candidates...)
		}
		collected, _, err := cloud.CollectRemoteHoldings(ctx, candidates)
		require.NoError(b, err)
		collecting += time.Since(start)
		require.Len(b, collected, engines*holdings)
		b.StopTimer()
		require.NoError(b, cloud.CloseDiscardingPersistence())
		b.StartTimer()
	}
	b.ReportMetric(float64(applying.Nanoseconds())/float64(b.N*engines*holdings), "ns/row")
	b.ReportMetric(float64(collecting.Nanoseconds())/float64(b.N*engines*holdings), "collect-ns/holding")
}

// changeMarksBenchBundle exports roots with a dependency each from an
// engine cache.
func changeMarksBenchBundle(b *testing.B, roots int) ValueBundle {
	ctx := cacheTestContext(context.Background())
	a, err := NewCache(ctx, "", nil, nil)
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, a.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(b, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	actx := srvToContext(ContextWithCache(ctx, a), srv)
	publish := func(field string) AnyResult {
		value := &transferTestValue{Text: field, Fields: mergeBenchBody(field, 16)}
		frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
		res, err := a.GetOrInitCall(actx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame)
		})
		require.NoError(b, err)
		return res
	}
	var sent []AnyResult
	for i := range roots {
		root := publish(fmt.Sprintf("root-%d", i))
		transferTestDependency(a, actx, root, publish(fmt.Sprintf("dep-%d", i)))
		sent = append(sent, root)
	}
	var bundle ValueBundle
	require.NoError(b, a.WithExportedValues(actx, ValueSelection{Roots: sent}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(b, bundle.Values, 2*roots)
	return bundle
}

func benchmarkChangeMarksMergePrune(b *testing.B, bundle ValueBundle, marks bool) {
	var merging, holding, pruning time.Duration
	for range b.N {
		b.StopTimer()
		ctx, cloud := changeMarksBenchCloud(b, marks)
		b.StartTimer()
		start := time.Now()
		reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
		merging += time.Since(start)
		require.NoError(b, err)
		holding += reply.CommitHold

		b.StopTimer()
		for _, value := range reply.Values {
			recipe, field := changeTestRecipe(cloud, value.Number)
			_, err := cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-b", Number: value.Number}, RemoteHolding{Recipe: recipe, Request: recipe, Field: field, Executed: true})
			require.NoError(b, err)
		}
		b.StartTimer()
		start = time.Now()
		report, err := cloud.PruneMetadataEstimate(ctx, 2, 1)
		pruning += time.Since(start)
		require.NoError(b, err)
		require.Positive(b, report.DroppedValues)
		b.StopTimer()
		require.NoError(b, cloud.CloseDiscardingPersistence())
		b.StartTimer()
	}
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 / float64(b.N) }
	b.ReportMetric(ms(merging), "merge-ms")
	b.ReportMetric(ms(holding), "commit-hold-ms")
	b.ReportMetric(ms(pruning), "prune-ms")
}
