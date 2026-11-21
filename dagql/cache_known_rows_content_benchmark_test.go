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

// BenchmarkContentExportHeldSubtrees exports a part far below a root whose
// closure the Cloud already has, the shape of a stopping engine's content
// export: a chain of known rows from the root down to the part's row, each
// with a known side subtree nothing in the export needs. It reports the
// rows and bytes the bundle carries, the capture's time, and the Cloud
// commit's hold of the graph lock merging it.
func BenchmarkContentExportHeldSubtrees(b *testing.B) {
	for _, shape := range []struct{ chain, side int }{{10, 100}, {50, 200}} {
		b.Run(fmt.Sprintf("chain%d/side%d", shape.chain, shape.side), func(b *testing.B) {
			ctx := cacheTestContext(context.Background())
			// A persisted engine cache, as a content export's tests use.
			engine, err := NewCache(ctx, filepath.Join(b.TempDir(), "cache.db"), nil, nil)
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, engine.CloseDiscardingPersistence()) })
			srv := newDagqlServerForTest(b, &persistCodecRoot{})
			srv.InstallObject(NewClass(srv, ClassOpts[*transferTestValue]{}))
			ectx := srvToContext(ContextWithCache(ctx, engine), srv)
			publish := func(field string, value *transferTestValue) AnyResult {
				frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType(value.Type())}
				res, err := engine.GetOrInitCall(ectx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
					return NewResultForCall(value, frame)
				})
				require.NoError(b, err)
				return res
			}
			root := publish("root", &transferTestValue{Text: "root"})
			above := root
			var part AnyResult
			for i := range shape.chain {
				text := "link"
				if i == shape.chain-1 {
					text = "pending"
				}
				link := publish(fmt.Sprintf("link-%d", i), &transferTestValue{Text: text})
				transferTestDependency(engine, ectx, above, link)
				side := link
				for j := range shape.side {
					next := publish(fmt.Sprintf("side-%d-%d", i, j), &transferTestValue{Text: "side", Fields: mergeBenchBody(fmt.Sprintf("side-%d-%d", i, j), 2)})
					transferTestDependency(engine, ectx, side, next)
					side = next
				}
				above, part = link, link
			}
			known := NewKnownRows()
			var first ValueBundle
			require.NoError(b, engine.WithExportedValues(ectx, ValueSelection{Roots: []AnyResult{root}, Known: known}, config.RefConfig{}, func(_ context.Context, v *ExportedValues) error {
				first = v.Bundle
				known.Merged(v.Rows)
				return nil
			}))
			cctx := cacheTestContext(context.Background())
			cloud, err := NewCache(cctx, "", nil, nil, WithBlobStore())
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, cloud.CloseDiscardingPersistence()) })
			_, err = cloud.MergeValues(cctx, "engine", first)
			require.NoError(b, err)
			storedTestCompress(cloud)

			selection := ValueSelection{Roots: []AnyResult{root}, Outputs: []SelectedValueOutput{{Result: part, Address: PersistedPartAddress{Part: "snapshot"}}}, Known: known}
			var rows, bytes int
			var capture, hold time.Duration
			b.ResetTimer()
			for range b.N {
				var bundle ValueBundle
				start := time.Now()
				require.NoError(b, engine.WithExportedValues(ectx, selection, config.RefConfig{}, func(_ context.Context, v *ExportedValues) error {
					bundle = v.Bundle
					return nil
				}))
				capture += time.Since(start)
				raw, err := json.Marshal(bundle)
				require.NoError(b, err)
				rows, bytes = len(bundle.Values), len(raw)
				p, err := cloud.PrepareValueMerge(cctx, "engine", bundle)
				require.NoError(b, err)
				reply, err := cloud.CommitValueMerge(cctx, p)
				require.NoError(b, err)
				require.True(b, reply.Committed)
				hold += reply.CommitHold
			}
			per := func(d time.Duration) float64 { return float64(d) / float64(b.N) }
			b.ReportMetric(float64(rows), "rows")
			b.ReportMetric(float64(bytes), "bundle-bytes")
			b.ReportMetric(per(capture)/1e6, "capture-ms/op")
			b.ReportMetric(per(hold)/1e6, "hold-ms/op")
		})
	}
}
