package snapshots_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/stretchr/testify/require"
)

// A profiled chain export records each layer it diffs into a blob, with the
// blob's digest and size, a fact naming the snapshot it diffed, and its wait
// for the layer's export lock. A layer whose blob already exists is not
// diffed again.
func TestExportChainProfiled(t *testing.T) {
	store := testutil.NewStore(t)
	a, _ := store.Build(t, nil, "a.txt", "exported prefix")
	ab, _ := store.Build(t, a, "b.txt", "exported suffix")

	ctx, root := profiledOp(context.Background(), "test.export")
	var chains [2][]string
	sizes := map[string]int64{}
	var diffTime time.Duration
	for i := range chains {
		chain, err := ab.ExportChain(ctx, transferConfig)
		require.NoError(t, err)
		diffTime += chain.Stats.DiffTime
		for _, layer := range chain.Layers {
			chains[i] = append(chains[i], layer.Descriptor.Digest.String())
			sizes[layer.Descriptor.Digest.String()] = layer.Descriptor.Size
		}
		require.NoError(t, chain.Release(context.Background()))
	}
	root.End(wcprof.OutcomeOK)
	require.Len(t, chains[0], 2)
	require.Equal(t, chains[0], chains[1])

	var diffs []string
	diffOps := map[uint64]bool{}
	snapshotsOf := map[uint64][]string{}
	var lockWaits int
	var profiled time.Duration
	for _, ev := range profiledUnder(t, root.ID()) {
		switch ev.Type {
		case "op":
			if ev.ParentID != root.ID() || snapshotterCall(ev) {
				continue // the diff's own walk, under it, or a snapshotter call
			}
			require.Equal(t, "snapshot.export.diff", ev.class)
			require.Equal(t, "io", ev.OpKind)
			require.Equal(t, "ok", ev.Outcome)
			require.Equal(t, root.ID(), ev.ParentID)
			require.Equal(t, sizes[ev.ident], ev.Bytes, "the blob's size")
			diffs = append(diffs, ev.ident)
			diffOps[ev.OpID] = true
			profiled += time.Duration(ev.EndNS - ev.StartNS)
		case "link":
			if snap, ok := strings.CutPrefix(ev.ident, "snapshot:"); ok && ev.LinkKind == "fact" {
				snapshotsOf[ev.ParentID] = append(snapshotsOf[ev.ParentID], snap)
			}
		case "wait":
			if ev.ParentID != root.ID() {
				continue
			}
			require.Equal(t, "lock", ev.Reason)
			require.Equal(t, "snapshots.exportLayer", ev.ident)
			lockWaits++
		}
	}
	require.ElementsMatch(t, chains[0], diffs, "the first export diffs each layer once, the second none")
	var diffed []string
	for id := range diffOps {
		require.Len(t, snapshotsOf[id], 1, "a diff names the snapshot it diffed once")
		diffed = append(diffed, snapshotsOf[id][0])
	}
	require.ElementsMatch(t, []string{a.SnapshotID(), ab.SnapshotID()}, diffed, "each layer's diff names its own snapshot")
	require.Equal(t, 4, lockWaits, "one lock wait per layer per export")
	require.Positive(t, diffTime)
	require.Equal(t, diffTime, profiled, "the diff ops span the export's measured diff time")
}
