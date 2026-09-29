package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// BenchmarkMergeValues merges a bundle of 2,000 records into an engine cache
// of 10,000 entries (design 11.1): 1,000 roots, each with a dependency of its
// own. B already has the recipes of half the roots and their dependencies, so
// 1,000 records land on B's entries and 1,000 create new ones. Each operation
// merges into a new B. It reports records merged per second, and the longest
// time a reader looping on B's egraphMu waited during a merge: the longest
// graph-lock hold another operation sees.
func BenchmarkMergeValues(b *testing.B) {
	const roots, entries = 1_000, 10_000
	newCache := func(name string) (context.Context, *Cache, *Server) {
		ctx := cacheTestContext(context.Background())
		c, err := NewCache(ctx, filepath.Join(b.TempDir(), name+".db"), nil, nil)
		require.NoError(b, err)
		srv := newDagqlServerForTest(b, &persistCodecRoot{})
		srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
		return srvToContext(ContextWithCache(ctx, c), srv), c, srv
	}
	publish := func(ctx context.Context, c *Cache, srv *Server, field string) AnyResult {
		value := &transferTestValue{Text: field}
		frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
		res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame)
		})
		require.NoError(b, err)
		return res
	}

	actx, a, asrv := newCache("a")
	b.Cleanup(func() { require.NoError(b, a.CloseDiscardingPersistence()) })
	var sent []AnyResult
	for i := range roots {
		root := publish(actx, a, asrv, fmt.Sprintf("root-%d", i))
		transferTestDependency(a, actx, root, publish(actx, a, asrv, fmt.Sprintf("dep-%d", i)))
		sent = append(sent, root)
	}
	var bundle ValueBundle
	require.NoError(b, a.WithExportedValues(actx, ValueSelection{Roots: sent}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(b, bundle.Values, 2*roots)

	var merging, longest time.Duration
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		bctx, c, bsrv := newCache("b")
		for i := range roots / 2 {
			transferTestDependency(c, bctx, publish(bctx, c, bsrv, fmt.Sprintf("root-%d", i)), publish(bctx, c, bsrv, fmt.Sprintf("dep-%d", i)))
		}
		for i := range entries - roots {
			publish(bctx, c, bsrv, fmt.Sprintf("other-%d", i))
		}
		c.egraphMu.RLock()
		before := len(c.resultsByID)
		c.egraphMu.RUnlock()
		require.Equal(b, entries, before)
		stop, waited := make(chan struct{}), make(chan time.Duration)
		go func() {
			var most time.Duration
			for {
				select {
				case <-stop:
					waited <- most
					return
				default:
				}
				start := time.Now()
				c.egraphMu.RLock()
				if wait := time.Since(start); wait > most {
					most = wait
				}
				c.egraphMu.RUnlock()
			}
		}()
		b.StartTimer()
		start := time.Now()
		reply, err := c.MergeValues(bctx, cloudCacheID, bundle)
		elapsed := time.Since(start)
		b.StopTimer()
		close(stop)
		if wait := <-waited; wait > longest {
			longest = wait
		}
		merging += elapsed
		require.NoError(b, err)
		require.Len(b, reply.Values, 2*roots)
		c.egraphMu.RLock()
		after := len(c.resultsByID)
		c.egraphMu.RUnlock()
		require.Equal(b, before+roots, after, "half the records create entries")
		require.NoError(b, c.CloseDiscardingPersistence())
		b.StartTimer()
	}
	b.ReportMetric(float64(2*roots*b.N)/merging.Seconds(), "records/s")
	b.ReportMetric(float64(longest.Microseconds())/1000, "longest-hold-ms")
}

// BenchmarkWithExportedValuesSharedClosure exports 32 roots that share one
// closure in one call (design 11.1, 7.4 step 1): each root depends on the top
// of a shared chain of 500 entries, so the bundle carries the chain's records
// once. It reports the records and the bundle's JSON bytes per export.
func BenchmarkWithExportedValuesSharedClosure(b *testing.B) {
	const roots, chain = 32, 500
	ctx := cacheTestContext(context.Background())
	c, err := NewCache(ctx, filepath.Join(b.TempDir(), "a.db"), nil, nil)
	require.NoError(b, err)
	b.Cleanup(func() { require.NoError(b, c.CloseDiscardingPersistence()) })
	srv := newDagqlServerForTest(b, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
	ctx = srvToContext(ContextWithCache(ctx, c), srv)
	publish := func(field string) AnyResult {
		value := &transferTestValue{Text: field}
		frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
		res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame)
		})
		require.NoError(b, err)
		return res
	}
	var top AnyResult
	for i := range chain {
		next := publish(fmt.Sprintf("chain-%d", i))
		if top != nil {
			transferTestDependency(c, ctx, next, top)
		}
		top = next
	}
	selection := ValueSelection{}
	for i := range roots {
		root := publish(fmt.Sprintf("root-%d", i))
		transferTestDependency(c, ctx, root, top)
		selection.Roots = append(selection.Roots, root)
	}
	var records, bytes int
	b.ResetTimer()
	for range b.N {
		require.NoError(b, c.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
			records = len(values.Bundle.Values)
			b.StopTimer()
			encoded, err := json.Marshal(values.Bundle)
			require.NoError(b, err)
			bytes = len(encoded)
			b.StartTimer()
			return nil
		}))
	}
	require.Equal(b, roots+chain, records, "the shared closure travels once")
	b.ReportMetric(float64(records), "records/export")
	b.ReportMetric(float64(bytes), "bundle-bytes")
}
