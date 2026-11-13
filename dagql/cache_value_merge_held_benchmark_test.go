package dagql

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// BenchmarkCloudMergeHeldRows merges into a Cloud an engine's export that is
// almost all held rows, the shape of a stopping engine's last export on a
// whole pull request (48,801 held rows and 1 record took 2.39 s under the
// remote cache service's work lock): one root over n leaves the Cloud
// already holds from the engine, each leaf sent as a held row. It reports
// the prepare's and the commit's time a merge, and of the commit, its
// warming and its hold of the graph lock (MergeReply.Warm, CommitHold),
// each per held row.
func BenchmarkCloudMergeHeldRows(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 48_800} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			ctx := cacheTestContext(context.Background())
			engine, err := NewCache(ctx, "", nil, nil)
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, engine.CloseDiscardingPersistence()) })
			srv := newDagqlServerForTest(b, &persistCodecRoot{})
			srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
			ectx := srvToContext(ContextWithCache(ctx, engine), srv)
			publish := func(field string) AnyResult {
				value := &transferTestValue{Text: field}
				frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
				res, err := engine.GetOrInitCall(ectx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
					return NewResultForCall(value, frame)
				})
				require.NoError(b, err)
				return res
			}
			root := publish("root")
			held := map[uint64]digest.Digest{}
			var leaves []AnyResult
			for i := range n {
				leaf := publish(fmt.Sprintf("leaf-%d", i))
				transferTestDependency(engine, ectx, root, leaf)
				recipe, err := leaf.cacheSharedResult().loadResultCall().deriveRecipeDigest(engine)
				require.NoError(b, err)
				held[uint64(leaf.cacheSharedResult().id)] = recipe
				leaves = append(leaves, leaf)
			}
			full := benchExportBundle(b, ectx, engine, root)
			leavesOnly := benchExportBundle(b, ectx, engine, leaves...)
			heldBundle := full
			heldBundle.Values = append([]TransferredValue(nil), full.Values...)
			for i, row := range heldBundle.Values {
				if recipe, ok := held[row.SenderNumber]; ok {
					heldBundle.Values[i] = TransferredValue{Ordinal: row.Ordinal, SenderNumber: row.SenderNumber, SenderReplacements: row.SenderReplacements, Record: PersistedRecord{ResultID: row.SenderNumber}, ExpiresAtUnix: row.ExpiresAtUnix, Held: recipe}
				}
			}

			cctx := cacheTestContext(context.Background())
			cloud, err := NewCache(cctx, "", nil, nil, WithBlobStore())
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, cloud.CloseDiscardingPersistence()) })
			_, err = cloud.MergeValues(cctx, "engine", leavesOnly)
			require.NoError(b, err)
			first, err := cloud.MergeValues(cctx, "engine", heldBundle)
			require.NoError(b, err)
			require.Equal(b, n, first.Rows.Held)

			var prepared, committed, warm, hold time.Duration
			b.ResetTimer()
			for range b.N {
				start := time.Now()
				p, err := cloud.PrepareValueMerge(cctx, "engine", heldBundle)
				require.NoError(b, err)
				mid := time.Now()
				reply, err := cloud.CommitValueMerge(cctx, p)
				require.NoError(b, err)
				committed += time.Since(mid)
				prepared += mid.Sub(start)
				warm += reply.Warm
				hold += reply.CommitHold
			}
			per := func(d time.Duration) float64 { return float64(d) / float64(b.N) }
			b.ReportMetric(per(prepared)/1e6, "prepare-ms/op")
			b.ReportMetric(per(committed)/1e6, "commit-ms/op")
			b.ReportMetric(per(warm)/1e6, "warm-ms/op")
			b.ReportMetric(per(hold)/1e6, "hold-ms/op")
			b.ReportMetric(per(committed)/float64(n)/1e3, "commit-us/held-row")
		})
	}
}

// benchExportBundle is the bundle an export of roots carries.
func benchExportBundle(b *testing.B, ctx context.Context, c *Cache, roots ...AnyResult) ValueBundle {
	b.Helper()
	var bundle ValueBundle
	require.NoError(b, c.WithExportedValues(ctx, ValueSelection{Roots: roots}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	return bundle
}
