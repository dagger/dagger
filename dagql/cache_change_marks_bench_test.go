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
		collected, _, err := cloud.CollectRemoteHoldings(ctx, candidates, nil)
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

// BenchmarkClassJoin times a join of a large class on a blob-backed cache: an
// entry alone in its class joins, by a content digest another engine's
// holding of its recipe gives it, a class of size entries that hold no part,
// as a commit's or a row's entry joins the class of results equal to it once
// its identity is taught. It reports join-us, that holding's attach with its
// join and its marks handed back, and named/join, the entries its change
// names for readers.
func BenchmarkClassJoin(b *testing.B) {
	for _, size := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("class=%d", size), func(b *testing.B) {
			benchmarkClassJoin(b, size)
		})
	}
}

func benchmarkClassJoin(b *testing.B, size int) {
	ctx, cloud := changeMarksBenchCloud(b, true)
	content := testDigest("class-join-content")
	holding := func(cache CacheID, i int) (HolderKey, RemoteHolding) {
		recipe := testDigest(fmt.Sprintf("class-join-%s-%d", cache, i))
		return HolderKey{Cache: cache, Number: uint64(i + 1)}, RemoteHolding{
			Recipe: recipe, Request: recipe, Field: "field", TypeName: "Directory", ContentDigest: content,
		}
	}
	for i := range size {
		key, desc := holding("cache-class", i)
		_, err := cloud.AttachRemoteHolding(ctx, key, desc)
		require.NoError(b, err)
	}
	class, ok := cloud.EntryClassEntries(changeMarksBenchNumber(b, cloud, HolderKey{Cache: "cache-class", Number: 1}))
	require.True(b, ok)
	require.Len(b, class.Entries, size, "one class of size entries")
	var took time.Duration
	i, named := 0, 0
	for b.Loop() {
		// A new entry, alone in its class, that another engine's holding
		// of its recipe then gives the class's content digest: its class
		// joins the large one.
		b.StopTimer()
		key, desc := holding("cache-new", i)
		desc.ContentDigest = ""
		_, err := cloud.AttachRemoteHolding(ctx, key, desc)
		require.NoError(b, err)
		_, desc = holding("cache-new", i)
		key = HolderKey{Cache: "cache-join", Number: uint64(i + 1)}
		i++
		b.StartTimer()
		start := time.Now()
		change, err := cloud.AttachRemoteHolding(ctx, key, desc)
		took += time.Since(start)
		require.NoError(b, err)
		named += len(change.Entries)
	}
	b.ReportMetric(float64(took.Microseconds())/float64(b.N), "join-us")
	b.ReportMetric(float64(named)/float64(b.N), "named/join")
}

// BenchmarkClassJoinOfTwoClasses times a join of two classes of size
// entries each, none holding a part, on a blob-backed cache: an entry of
// one class, given the other's content digest by a holding of its recipe,
// joins them, as a kept record's identity can join two classes of entries
// the cache had. It reports join-us and named/join, as BenchmarkClassJoin
// does.
func BenchmarkClassJoinOfTwoClasses(b *testing.B) {
	for _, size := range []int{100, 1_000} {
		b.Run(fmt.Sprintf("classes=%d+%d", size, size), func(b *testing.B) {
			benchmarkClassJoinOfTwoClasses(b, size)
		})
	}
}

func benchmarkClassJoinOfTwoClasses(b *testing.B, size int) {
	holding := func(class string, i int) (HolderKey, RemoteHolding) {
		recipe := testDigest(fmt.Sprintf("two-classes-%s-%d", class, i))
		return HolderKey{Cache: CacheID("cache-" + class), Number: uint64(i + 1)}, RemoteHolding{
			Recipe: recipe, Request: recipe, Field: "field", TypeName: "Directory", ContentDigest: testDigest("two-classes-content-" + class),
		}
	}
	var took time.Duration
	named := 0
	for b.Loop() {
		b.StopTimer()
		ctx, cloud := changeMarksBenchCloud(b, true)
		for _, class := range []string{"a", "b"} {
			for i := range size {
				key, desc := holding(class, i)
				_, err := cloud.AttachRemoteHolding(ctx, key, desc)
				require.NoError(b, err)
			}
		}
		// Another engine's holding of class a's first recipe, with class b's
		// content: its entry joins the two classes.
		_, desc := holding("a", 0)
		desc.ContentDigest = testDigest("two-classes-content-b")
		b.StartTimer()
		start := time.Now()
		change, err := cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-join", Number: 1}, desc)
		took += time.Since(start)
		require.NoError(b, err)
		named += len(change.Entries)
		b.StopTimer()
		class, ok := cloud.EntryClassEntries(changeMarksBenchNumber(b, cloud, HolderKey{Cache: "cache-b", Number: 1}))
		require.True(b, ok)
		require.Len(b, class.Entries, 2*size, "the two classes joined")
		b.StartTimer()
	}
	b.ReportMetric(float64(took.Microseconds())/float64(b.N), "join-us")
	b.ReportMetric(float64(named)/float64(b.N), "named/join")
}

// changeMarksBenchNumber is the entry number of a holding the cache has.
func changeMarksBenchNumber(b *testing.B, c *Cache, key HolderKey) uint64 {
	b.Helper()
	held, ok := c.RemoteHoldingInfo(key)
	require.True(b, ok)
	return held.Number
}
