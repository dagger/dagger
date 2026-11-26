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
// almost all held rows, the shape of a stopping engine's last export: one
// root over n leaves the Cloud already holds from the engine, each leaf sent
// as a held row. With outputs, each held row carries an output at its
// pending part, which the commit checks against the Cloud's compressed copy
// of the row, as exports that select parts do; the leaves' records are
// about the size of a module's. It reports the prepare's and the commit's
// time a merge, and of the commit, its warming and its hold of the graph
// lock (MergeReply.Warm, CommitHold), each per held row.
func BenchmarkCloudMergeHeldRows(b *testing.B) {
	for _, outputs := range []bool{false, true} {
		for _, n := range []int{1_000, 10_000, 48_800} {
			name := fmt.Sprint(n)
			if outputs {
				name += "/outputs"
			}
			b.Run(name, func(b *testing.B) {
				ctx := cacheTestContext(context.Background())
				engine, err := NewCache(ctx, "", nil, nil)
				require.NoError(b, err)
				b.Cleanup(func() { require.NoError(b, engine.CloseDiscardingPersistence()) })
				srv := newDagqlServerForTest(b, &persistCodecRoot{})
				srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
				ectx := srvToContext(ContextWithCache(ctx, engine), srv)
				publishValue := func(field string, value *transferTestValue) AnyResult {
					frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
					res, err := engine.GetOrInitCall(ectx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
						return NewResultForCall(value, frame)
					})
					require.NoError(b, err)
					return res
				}
				publish := func(field string) AnyResult {
					return publishValue(field, &transferTestValue{Text: field})
				}
				root := publish("root")
				held := map[uint64]digest.Digest{}
				var leaves []AnyResult
				for i := range n {
					var leaf AnyResult
					if outputs {
						leaf = publishValue(fmt.Sprintf("leaf-%d", i), &transferTestValue{Text: "pending", Fields: mergeBenchBody(fmt.Sprintf("leaf-%d", i), 10)})
					} else {
						leaf = publish(fmt.Sprintf("leaf-%d", i))
					}
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
						if outputs {
							heldBundle.Outputs = append(heldBundle.Outputs, TransferredOutput{Ordinal: row.Ordinal, Address: PersistedPartAddress{Part: "snapshot"}, State: "pending"})
						}
					}
				}

				cctx := cacheTestContext(context.Background())
				cloud, err := NewCache(cctx, "", nil, nil, WithBlobStore())
				require.NoError(b, err)
				b.Cleanup(func() { require.NoError(b, cloud.CloseDiscardingPersistence()) })
				_, err = cloud.MergeValues(cctx, "engine", leavesOnly)
				require.NoError(b, err)
				// The Cloud's copies are compressed, as a busy Cloud's are by
				// the time an engine's later exports hold them.
				storedTestCompress(cloud)
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
				b.ReportMetric(per(hold)/float64(n)/1e3, "hold-us/held-row")
			})
		}
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
