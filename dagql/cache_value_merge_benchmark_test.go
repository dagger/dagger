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

// mergeBenchBody returns a body of n fields, distinct for seed, each about
// 230 bytes encoded: 44 give about 10 KB, the size of a module session's
// records, which averaged 10 KB in the end-to-end run's merges (design 11.5).
func mergeBenchBody(seed string, n int) []transferTestField {
	fields := make([]transferTestField, n)
	for i := range fields {
		fields[i] = transferTestField{
			Name:        fmt.Sprintf("%s-field-%02d", seed, i),
			Description: fmt.Sprintf("Field %d of %s, described as a module's function would be.", i, seed),
			Type:        "String!",
			Args: []transferTestArg{
				{Name: "path", Type: "String!", Default: `"/src"`},
				{Name: "exclude", Type: "[String!]", Default: "[]"},
			},
		}
	}
	return fields
}

// mergeBenchShape is a bundle the large-record merge benchmark sends.
type mergeBenchShape struct {
	// records is the number of records in the bundle.
	records int
	// chain makes the bundle one root over a chain of records, each
	// depending on the one before, as a module's runtime container is.
	// Otherwise half the records are roots, each with a dependency of its
	// own.
	chain bool
	// fields is the number of fields in each record's body.
	fields int
}

// BenchmarkMergeValuesLargeRecords merges bundles of records of 10 KB or more
// into an engine cache that is empty, so every record creates an entry, and
// into one that already has every entry, so every record lands on one. Each
// record references its dependency in its payload, and about half carry the
// Cloud's offer of their part, as in the end-to-end run's merges. The shapes:
//   - roots of their own, 100 to 2,000 records of about 10 KB;
//   - the end-to-end run's largest single root, a module's runtime container:
//     one root over 63 records of about 16 KB, 1 MB in all, which a byte cap
//     on merges can't split.
//
// Each operation merges into a new cache. It reports the reply's commit hold
// of egraphMu, mean and longest, and the longest a reader looping on egraphMu
// waited during a merge.
func BenchmarkMergeValuesLargeRecords(b *testing.B) {
	shapes := map[string]mergeBenchShape{
		"runtime-root/records=63": {records: 63, chain: true, fields: 70},
	}
	names := []string{"runtime-root/records=63"}
	for _, records := range []int{100, 500, 2_000} {
		name := fmt.Sprintf("records=%d", records)
		shapes[name] = mergeBenchShape{records: records, fields: 44}
		names = append(names, name)
	}
	for _, name := range names {
		for _, into := range []string{"empty", "existing"} {
			b.Run(name+"/into="+into, func(b *testing.B) {
				benchmarkMergeLargeRecords(b, shapes[name], into == "existing")
			})
		}
	}
}

func benchmarkMergeLargeRecords(b *testing.B, shape mergeBenchShape, existing bool) {
	newCache := func(name string) (context.Context, *Cache, *Server) {
		ctx := cacheTestContext(context.Background())
		c, err := NewCache(ctx, filepath.Join(b.TempDir(), name+".db"), nil, nil)
		require.NoError(b, err)
		srv := newDagqlServerForTest(b, &persistCodecRoot{})
		srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
		return srvToContext(ContextWithCache(ctx, c), srv), c, srv
	}
	publish := func(ctx context.Context, c *Cache, srv *Server, field string, child AnyResult) AnyResult {
		value := &transferTestValue{Text: field, Fields: mergeBenchBody(field, shape.fields)}
		if child != nil {
			value.Child = uint64(child.cacheSharedResult().id)
		}
		frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
		res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(value, frame)
		})
		require.NoError(b, err)
		if child != nil {
			transferTestDependency(c, ctx, res, child)
		}
		return res
	}
	// populate publishes the shape's values in c, each referencing and
	// depending on its dependency. It returns the roots, and the records
	// that carry an offer.
	populate := func(ctx context.Context, c *Cache, srv *Server) (roots, offered []AnyResult) {
		if shape.chain {
			var prev AnyResult
			for i := range shape.records {
				prev = publish(ctx, c, srv, fmt.Sprintf("step-%d", i), prev)
				if i%2 == 1 {
					offered = append(offered, prev)
				}
			}
			return []AnyResult{prev}, offered
		}
		for i := range shape.records / 2 {
			dep := publish(ctx, c, srv, fmt.Sprintf("dep-%d", i), nil)
			roots = append(roots, publish(ctx, c, srv, fmt.Sprintf("root-%d", i), dep))
		}
		return roots, roots
	}

	actx, a, asrv := newCache("a")
	b.Cleanup(func() { require.NoError(b, a.CloseDiscardingPersistence()) })
	roots, offered := populate(actx, a, asrv)
	for _, res := range offered {
		out, err := a.testOfferParts(actx, res, []PersistedPartOffer{testLiveOffer()})
		require.NoError(b, err)
		require.Equal(b, OfferAccepted, out[0].Outcome)
	}
	var bundle ValueBundle
	require.NoError(b, a.WithExportedValues(actx, ValueSelection{Roots: roots}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	require.Len(b, bundle.Values, shape.records)
	encoded, err := json.Marshal(bundle)
	require.NoError(b, err)

	var merging, holding, longestHold, longestWait time.Duration
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		bctx, c, bsrv := newCache("b")
		if existing {
			populate(bctx, c, bsrv)
		}
		c.egraphMu.RLock()
		before := len(c.resultsByID)
		c.egraphMu.RUnlock()
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
		if wait := <-waited; wait > longestWait {
			longestWait = wait
		}
		require.NoError(b, err)
		require.True(b, reply.Committed)
		merging += elapsed
		holding += reply.CommitHold
		if reply.CommitHold > longestHold {
			longestHold = reply.CommitHold
		}
		c.egraphMu.RLock()
		after := len(c.resultsByID)
		c.egraphMu.RUnlock()
		if existing {
			require.Equal(b, before, after, "every record lands on an entry")
		} else {
			require.Equal(b, shape.records, after, "every record creates an entry")
		}
		require.NoError(b, c.CloseDiscardingPersistence())
		b.StartTimer()
	}
	b.ReportMetric(float64(len(encoded))/float64(shape.records), "bytes/record")
	b.ReportMetric(float64(merging.Microseconds())/1000/float64(b.N), "merge-ms")
	b.ReportMetric(float64(holding.Microseconds())/1000/float64(b.N), "hold-ms")
	b.ReportMetric(float64(longestHold.Microseconds())/1000, "longest-hold-ms")
	b.ReportMetric(float64(longestWait.Microseconds())/1000, "reader-wait-ms")
}
