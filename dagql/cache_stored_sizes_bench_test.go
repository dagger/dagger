package dagql

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// The stored sizes benchmark's pool has the size of a production-scale pool
// measured in a simulator run: about 15,700 stored values and 1,400 stored roots.
// Every root sits on 4 records of its own over one of 16 shared chains of
// 540 records, as a module's calls sit on a runtime; every fourth root's
// own records also depend on a shared chain of 6 records of about 155 KB, the
// ~930 KB dependency that run resent hundreds of times. The other records
// are about 4 KB. Every root stores a part. 15 engine caches hold about 100k
// of the entries between them, so HeldBy's walk over an entry's holdings
// sees as many as the service's Cloud cache does.
const (
	sizesBenchChains    = 16
	sizesBenchChain     = 540
	sizesBenchRoots     = 1_400
	sizesBenchOwn       = 4
	sizesBenchBig       = 6
	sizesBenchBigFields = 620
	sizesBenchBigEvery  = 4
	sizesBenchCaches    = 15
)

// storedSizesBenchCloud returns a blob-backed cache holding the pool, with
// the engine caches' holdings, and its roots by number. The pool is exported
// from an engine's cache and merged, as the service receives it.
func storedSizesBenchCloud(b *testing.B) (context.Context, *Cache, []uint64) {
	b.Helper()
	actx := cacheTestContext(context.Background())
	a, err := NewCache(actx, filepath.Join(b.TempDir(), "a.db"), nil, nil)
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, a.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(b, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*storedBenchValue]{}))
	actx = srvToContext(ContextWithCache(actx, a), srv)
	publish := func(field string, fields int, child AnyResult, also AnyResult) AnyResult {
		value := &storedBenchValue{Text: field, Fields: mergeBenchBody(field, fields)}
		if child != nil {
			value.Child = uint64(child.cacheSharedResult().id)
		}
		frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
		res, err := a.GetOrInitCall(actx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame)
		})
		require.NoError(b, err)
		for _, dep := range []AnyResult{child, also} {
			if dep != nil {
				transferTestDependency(a, actx, res, dep)
			}
		}
		return res
	}
	var big AnyResult
	for i := range sizesBenchBig {
		big = publish(fmt.Sprintf("big-%d", i), sizesBenchBigFields, big, nil)
	}
	tops := make([]AnyResult, sizesBenchChains)
	for c := range sizesBenchChains {
		for i := range sizesBenchChain {
			tops[c] = publish(fmt.Sprintf("chain-%d-%d", c, i), storedBenchFields, tops[c], nil)
		}
	}
	var roots []AnyResult
	for i := range sizesBenchRoots {
		prev := tops[i%sizesBenchChains]
		for j := range sizesBenchOwn {
			var also AnyResult
			if j == 0 && i%sizesBenchBigEvery == 0 {
				also = big
			}
			prev = publish(fmt.Sprintf("own-%d-%d", i, j), storedBenchFields, prev, also)
		}
		roots = append(roots, publish(fmt.Sprintf("root-%d", i), storedBenchFields, prev, nil))
	}
	var bundle ValueBundle
	require.NoError(b, a.WithExportedValues(actx, ValueSelection{Roots: roots}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(b, bundle.Values, sizesBenchBig+sizesBenchChains*sizesBenchChain+sizesBenchRoots*(sizesBenchOwn+1))

	ctx := cacheTestContext(context.Background())
	cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, cloud.CloseDiscardingPersistence()) })
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(b, err)
	require.True(b, reply.Committed)
	numbers := map[TransferOrdinal]uint64{}
	for _, value := range reply.Values {
		numbers[value.Ordinal] = value.Number
	}
	var batch []uint64
	for _, root := range bundle.Roots {
		number := numbers[root.Ordinal]
		ok, err := cloud.SetStoredPart(ctx, number, storedBenchPart(number), 0)
		require.NoError(b, err)
		require.True(b, ok)
		batch = append(batch, number)
	}

	type entry struct {
		number uint64
		recipe RemoteHolding
	}
	var entries []entry
	cloud.egraphMu.RLock()
	for _, value := range reply.Values {
		res := cloud.resultsByID[sharedResultID(value.Number)]
		recipe := res.recipeKeys[0]
		entries = append(entries, entry{number: value.Number, recipe: RemoteHolding{Recipe: recipe, Request: recipe, Field: res.description}})
	}
	cloud.egraphMu.RUnlock()
	holdings := 0
	for k := range sizesBenchCaches {
		cache := CacheID(fmt.Sprintf("cache-%02d", k))
		for i, e := range entries {
			if (i+k)%7 >= 3 {
				continue
			}
			_, err := cloud.AttachRemoteHolding(ctx, HolderKey{Cache: cache, Number: e.number}, e.recipe)
			require.NoError(b, err)
			holdings++
		}
	}
	b.ReportMetric(float64(len(entries)), "values")
	b.ReportMetric(float64(holdings), "holdings")
	return ctx, cloud, batch
}

// storedSizesBenchForget drops every entry's normalized record and measured
// size, as a restart starts without them.
func storedSizesBenchForget(c *Cache) {
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	for _, res := range c.resultsByID {
		res.storedTransfer.Store(nil)
	}
}

// storedSizesBenchLongestHold measures each root alone, in order, and returns
// the longest: StoredSizes holds egraphMu.RLock for the whole of each root's
// walk, and a root measured alone walks all of its closure.
func storedSizesBenchLongestHold(b *testing.B, ctx context.Context, c *Cache, roots []uint64, held *HeldBy) time.Duration {
	b.Helper()
	var longest time.Duration
	for _, root := range roots {
		start := time.Now()
		_, err := c.StoredSizes(ctx, []uint64{root}, held)
		require.NoError(b, err)
		if d := time.Since(start); d > longest {
			longest = d
		}
	}
	return longest
}

// BenchmarkStoredSizesAtScale measures StoredSizes over the pool: a first
// call over every root after a restart, with no recipient (cold-all), and the
// service's preflight of 64 and 9 consecutive roots for one engine cache,
// once the sizes are measured, over the roots that cache doesn't hold
// (warm-64, warm-9). Each reports the longest
// single root's measure, which bounds a hold of the graph lock.
func BenchmarkStoredSizesAtScale(b *testing.B) {
	ctx, cloud, roots := storedSizesBenchCloud(b)

	b.Run("cold-all", func(b *testing.B) {
		var records int
		for range b.N {
			b.StopTimer()
			storedSizesBenchForget(cloud)
			runtime.GC()
			b.StartTimer()
			sizes, err := cloud.StoredSizes(ctx, roots, nil)
			require.NoError(b, err)
			records = 0
			for _, size := range sizes {
				records += size.Records
			}
		}
		b.StopTimer()
		storedSizesBenchForget(cloud)
		b.ReportMetric(float64(records), "records")
		b.ReportMetric(float64(storedSizesBenchLongestHold(b, ctx, cloud, roots, nil).Nanoseconds()), "longest-hold-ns")
	})

	for _, n := range []int{64, 9} {
		b.Run(fmt.Sprintf("warm-%d", n), func(b *testing.B) {
			_, err := cloud.StoredSizes(ctx, roots, nil)
			require.NoError(b, err)
			held := &HeldBy{Cache: "cache-03", Now: time.Now().Unix()}
			// The service places only the roots the engine doesn't hold.
			var candidates []uint64
			cloud.egraphMu.RLock()
			for _, root := range roots {
				if !held.heldLocked(cloud.resultsByID[sharedResultID(root)]) {
					candidates = append(candidates, root)
				}
			}
			cloud.egraphMu.RUnlock()
			var records int
			b.ResetTimer()
			for i := range b.N {
				start := (i * n) % (len(candidates) - n)
				sizes, err := cloud.StoredSizes(ctx, candidates[start:start+n], held)
				require.NoError(b, err)
				records = 0
				for _, size := range sizes {
					records += size.Records
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(records), "records")
			b.ReportMetric(float64(len(candidates)), "candidates")
			b.ReportMetric(float64(storedSizesBenchLongestHold(b, ctx, cloud, candidates[:n], held).Nanoseconds()), "longest-hold-ns")
		})
	}
}
