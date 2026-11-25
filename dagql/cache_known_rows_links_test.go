package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// linkTestCapture captures roots' closures with output's snapshot part
// selected, against known, and returns what it exported.
func linkTestCapture(t *testing.T, ctx context.Context, c *Cache, known *KnownRows, output AnyResult, roots ...AnyResult) *ExportedValues {
	t.Helper()
	selection := ValueSelection{
		Roots:   roots,
		Outputs: []SelectedValueOutput{{Result: output, Address: PersistedPartAddress{Part: "snapshot"}}},
		Known:   known,
	}
	var values *ExportedValues
	require.NoError(t, c.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, v *ExportedValues) error {
		values = v
		return nil
	}))
	return values
}

// linkTestChange moves res's transfer revision, as a change to what an
// export carries of it does, so that the recipient no longer knows it and an
// export carries it in full.
func linkTestChange(c *Cache, res AnyResult) {
	c.egraphMu.Lock()
	res.cacheSharedResult().transferRevision++
	c.egraphMu.Unlock()
}

// linkTestNewRowBelow is a root whose closure reaches a row the recipient
// doesn't know only through held rows nothing stands on: root, a and b are
// known, and deep, which carries the selected part, was changed since, so
// it goes in full, with core, its dependency, held below it.
func linkTestNewRowBelow(t *testing.T) (context.Context, *Cache, *KnownRows, ValueBundle, map[string]AnyResult) {
	t.Helper()
	ctx, c, srv := transferTestCache(t)
	core := persistedListTestResult(t, ctx, c, srv, "below-core", String("core"))
	deep := persistedListTestResult(t, ctx, c, srv, "below-deep", &transferTestValue{Text: "pending"})
	b := persistedListTestResult(t, ctx, c, srv, "below-b", String("b"))
	a := persistedListTestResult(t, ctx, c, srv, "below-a", String("a"))
	root := persistedListTestResult(t, ctx, c, srv, "below-root", String("root"))
	transferTestDependency(c, ctx, deep, core)
	transferTestDependency(c, ctx, b, deep)
	transferTestDependency(c, ctx, a, b)
	transferTestDependency(c, ctx, root, a)
	known := NewKnownRows()
	first, rows, _ := recordsOnlyCapture(t, ctx, c, known, root)
	known.Merged(rows)
	linkTestChange(c, deep)
	return ctx, c, known, first, map[string]AnyResult{"root": root, "a": a, "b": b, "deep": deep, "core": core}
}

// A held row nothing stands on, on the way down from a root to a row the
// recipient doesn't know, doesn't go: the root links past a and b straight
// to deep, which goes in full with its dependency, core. The Cloud, which
// has the rows from the first export, merges the bundle, stores deep's new
// record over its own dependency on core, and keeps every dependency its
// other entries had.
func TestContentExportLinksPastHeldRowsToANewRow(t *testing.T) {
	t.Parallel()
	ctx, c, known, first, rows := linkTestNewRowBelow(t)
	n := knownRowsTestNumber
	values := linkTestCapture(t, ctx, c, known, rows["deep"], rows["root"])
	held, full := heldNumbers(values.Bundle)
	require.Equal(t, map[uint64]bool{n(rows["deep"]): true}, full, "the changed row goes in full")
	require.Equal(t, map[uint64]bool{n(rows["root"]): true, n(rows["core"]): true}, held, "nothing stands on a or b")
	require.Equal(t, 2, values.LeftOutHeld)
	deep := uint64(bundleRow(t, values.Bundle, rows["deep"]).Ordinal)
	require.Equal(t, []uint64{deep}, bundleRow(t, values.Bundle, rows["root"]).DependencyIDs, "the root links straight to the new row")
	require.Equal(t, []uint64{uint64(bundleRow(t, values.Bundle, rows["core"]).Ordinal)}, bundleRow(t, values.Bundle, rows["deep"]).DependencyIDs, "a row in full keeps its dependencies")
	_, err := validateValueBundle(values.Bundle)
	require.NoError(t, err)

	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(cctx, "engine", first)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	depsOf := func() map[string]int {
		counts := map[string]int{}
		for _, name := range []string{"root", "a", "b", "deep"} {
			entry, deps := cloudEntryOfRecipe(t, cloud, c, rows[name])
			require.NotNil(t, entry, name)
			counts[name] = len(deps)
		}
		return counts
	}
	before := depsOf()
	require.Equal(t, map[string]int{"root": 1, "a": 1, "b": 1, "deep": 1}, before)
	reply, err = cloud.MergeValues(cctx, "engine", values.Bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed, "the Cloud merges a bundle whose held rows link past rows it doesn't carry")
	require.Empty(t, reply.Stale)
	landed := map[TransferOrdinal]bool{}
	for _, value := range reply.Values {
		landed[value.Ordinal] = true
	}
	require.True(t, landed[TransferOrdinal(deep)], "the new row lands")
	require.Equal(t, before, depsOf(), "a held row's links are no list of its entry's dependencies: every entry keeps its own")
}

// A row that roots reach only through held rows nothing stands on is linked
// from a needed held row above it as often as it takes for every root that
// reached it to reach it still, and no more: out, which carries the
// selected part, is reached from root1 through l1 and through l3, and from
// root2 through l2. Each root links straight to out, once.
func TestContentExportLinksARowFromEveryRootThatReachedIt(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	out := persistedListTestResult(t, ctx, c, srv, "roots-out", &transferTestValue{Text: "pending"})
	l1 := persistedListTestResult(t, ctx, c, srv, "roots-l1", String("l1"))
	l2 := persistedListTestResult(t, ctx, c, srv, "roots-l2", String("l2"))
	l3 := persistedListTestResult(t, ctx, c, srv, "roots-l3", String("l3"))
	root1 := persistedListTestResult(t, ctx, c, srv, "roots-root1", String("root1"))
	root2 := persistedListTestResult(t, ctx, c, srv, "roots-root2", String("root2"))
	transferTestDependency(c, ctx, l1, out)
	transferTestDependency(c, ctx, l2, out)
	transferTestDependency(c, ctx, l3, out)
	transferTestDependency(c, ctx, root1, l1)
	transferTestDependency(c, ctx, root1, l3)
	transferTestDependency(c, ctx, root2, l2)
	known := NewKnownRows()
	_, rows1, _ := recordsOnlyCapture(t, ctx, c, known, root1)
	known.Merged(rows1)
	_, rows2, _ := recordsOnlyCapture(t, ctx, c, known, root2)
	known.Merged(rows2)

	values := linkTestCapture(t, ctx, c, known, out, root1, root2)
	held, full := heldNumbers(values.Bundle)
	n := knownRowsTestNumber
	require.Empty(t, full)
	require.Equal(t, map[uint64]bool{n(root1): true, n(root2): true, n(out): true}, held, "nothing stands on l1, l2 or l3")
	require.Equal(t, 3, values.LeftOutHeld)
	o := uint64(bundleRow(t, values.Bundle, out).Ordinal)
	require.Equal(t, []uint64{o}, bundleRow(t, values.Bundle, root1).DependencyIDs, "root1 links to out once, for both of its ways down")
	require.Equal(t, []uint64{o}, bundleRow(t, values.Bundle, root2).DependencyIDs, "root2 links to out too")
	_, err := validateValueBundle(values.Bundle)
	require.NoError(t, err)
}

// A content export without its held rows nothing stands on lands what the
// same capture with every known row lands, on a Cloud that merged the
// engine's first export: the same entries, dependencies, holdings, values
// and stored roots, and each output on the same entry.
func TestContentExportWithoutLinkRowsMergesAsTheWholeBundleDoes(t *testing.T) {
	t.Parallel()
	ctx, c, known, first, rows := linkTestNewRowBelow(t)
	kept := linkTestCapture(t, ctx, c, known, rows["deep"], rows["root"])
	require.Positive(t, kept.LeftOutHeld)
	c.testKeepEveryKnownRow = true
	whole := linkTestCapture(t, ctx, c, known, rows["deep"], rows["root"])
	c.testKeepEveryKnownRow = false
	require.Zero(t, whole.LeftOutHeld)
	require.Greater(t, len(whole.Bundle.Values), len(kept.Bundle.Values))

	landed := func(bundle ValueBundle) (map[string]string, map[string]string) {
		cctx, cloud := storedPartTestCache(t, WithBlobStore())
		reply, err := cloud.MergeValues(cctx, "engine", first)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		storedTestCompress(cloud)
		reply, err = cloud.MergeValues(cctx, "engine", bundle)
		require.NoError(t, err)
		require.True(t, reply.Committed)
		require.Empty(t, reply.Stale)
		number := map[TransferOrdinal]uint64{}
		for _, v := range reply.Values {
			number[v.Ordinal] = v.Number
		}
		cloud.egraphMu.RLock()
		recipeOf := map[sharedResultID]string{}
		for recipe, id := range cloud.entriesByRecipe {
			recipeOf[id] = recipe.String()
		}
		cloud.egraphMu.RUnlock()
		outputs := map[string]string{}
		for _, o := range bundle.Outputs {
			outputs[string(o.Address.Part)] = recipeOf[sharedResultID(number[o.Ordinal])]
		}
		return cloudStateOf(t, cloud), outputs
	}
	keptState, keptOutputs := landed(kept.Bundle)
	wholeState, wholeOutputs := landed(whole.Bundle)
	require.NotEmpty(t, keptOutputs)
	require.Equal(t, wholeOutputs, keptOutputs, "each output lands on the same entry")
	require.Equal(t, wholeState, keptState, "the same entries, dependencies, holdings, values and stored roots")
}

// A held row a link goes past is no row of the bundle, so the Cloud's commit
// doesn't check its copy: a copy that has expired there no longer refuses
// the merge, and stays as it is, expired, until an export carries the row as
// something stands on it or in full. Here the Cloud's copy of b, below the
// root and above the new row, expires before the export.
func TestContentExportLeavesAnExpiredCopyOfARowALinkGoesPast(t *testing.T) {
	t.Parallel()
	ctx, c, known, first, rows := linkTestNewRowBelow(t)
	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(cctx, "engine", first)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	entry, _ := cloudEntryOfRecipe(t, cloud, c, rows["b"])
	require.NotNil(t, entry)
	expired := time.Now().Unix() - 60
	cloud.egraphMu.Lock()
	entry.expiresAtUnix = expired
	cloud.egraphMu.Unlock()

	values := linkTestCapture(t, ctx, c, known, rows["deep"], rows["root"])
	held, _ := heldNumbers(values.Bundle)
	require.False(t, held[knownRowsTestNumber(rows["b"])], "the root links past b")
	reply, err = cloud.MergeValues(cctx, "engine", values.Bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed, "the copy of a row the bundle doesn't carry refuses nothing")
	require.Empty(t, reply.Stale)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	require.Equal(t, expired, entry.expiresAtUnix, "the Cloud's copy of b stays expired")
}
