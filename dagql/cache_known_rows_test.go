package dagql

import (
	"context"
	"sync"
	"testing"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// knownRowsTestClosure is a core, core over its own dependency base, under
// two roots that share it.
type knownRowsTestClosure struct {
	base, core, first, second AnyResult
}

func newKnownRowsTestClosure(t *testing.T, ctx context.Context, c *Cache, srv *Server) knownRowsTestClosure {
	t.Helper()
	cl := knownRowsTestClosure{
		base:   persistedListTestResult(t, ctx, c, srv, "known-base", String("base")),
		core:   persistedListTestResult(t, ctx, c, srv, "known-core", String("core")),
		first:  persistedListTestResult(t, ctx, c, srv, "known-first", String("first")),
		second: persistedListTestResult(t, ctx, c, srv, "known-second", String("second")),
	}
	transferTestDependency(c, ctx, cl.core, cl.base)
	transferTestDependency(c, ctx, cl.first, cl.core)
	transferTestDependency(c, ctx, cl.second, cl.core)
	return cl
}

// recordsOnlyCapture captures root's closure with no outputs against known,
// and returns the bundle, the rows it carried, and the entries whose records
// it copied.
func recordsOnlyCapture(t *testing.T, ctx context.Context, c *Cache, known *KnownRows, root AnyResult) (ValueBundle, ExportedRows, map[uint64]bool) {
	t.Helper()
	var mu sync.Mutex
	copied := map[uint64]bool{}
	c.testTransferCopied = func(id uint64) {
		mu.Lock()
		copied[id] = true
		mu.Unlock()
	}
	defer func() { c.testTransferCopied = nil }()
	var bundle ValueBundle
	var rows ExportedRows
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}, Known: known}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle, rows = values.Bundle, values.Rows
		return nil
	}))
	return bundle, rows, copied
}

func knownRowsTestNumber(res AnyResult) uint64 {
	return uint64(res.cacheSharedResult().id)
}

// heldNumbers are the sender numbers of the bundle's held rows, and full
// those of the rows it carries in full.
func heldNumbers(bundle ValueBundle) (held, full map[uint64]bool) {
	held, full = map[uint64]bool{}, map[uint64]bool{}
	for _, row := range bundle.Values {
		if row.Held != "" {
			held[row.SenderNumber] = true
		} else {
			full[row.SenderNumber] = true
		}
	}
	return held, full
}

// A records-only export sends a row the recipient merged from an earlier
// export as a held row: it copies no record for it and walks nothing below
// it, so a record that two exports' closures share travels once.
func TestRecordsOnlyExportHoldsKnownRows(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	cl := newKnownRowsTestClosure(t, ctx, c, srv)
	known := NewKnownRows()

	first, rows, _ := recordsOnlyCapture(t, ctx, c, known, cl.first)
	held, _ := heldNumbers(first)
	require.Empty(t, held, "nothing is known before a merge")
	known.Merged(rows)

	second, rows, copied := recordsOnlyCapture(t, ctx, c, known, cl.second)
	held, full := heldNumbers(second)
	core, base := knownRowsTestNumber(cl.core), knownRowsTestNumber(cl.base)
	require.Equal(t, map[uint64]bool{core: true}, held, "the shared core travels as a held row")
	require.Equal(t, map[uint64]bool{knownRowsTestNumber(cl.second): true}, full, "nothing below the held row is walked")
	require.False(t, copied[core], "the held row's record isn't copied")
	require.False(t, copied[base])
	require.Equal(t, []uint64{core}, rows.Held)
	_, err := validateValueBundle(second)
	require.NoError(t, err)
}

// A known row that changed since its export, or that an export the recipient
// didn't merge sent as held, is carried in full again. The retry of a refused
// export holds nothing that export carried or held; it can hold a row below
// those that an earlier merged export carried.
func TestRecordsOnlyExportCarriesUnprovenRowsInFull(t *testing.T) {
	t.Parallel()
	t.Run("changed", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := transferTestCache(t)
		cl := newKnownRowsTestClosure(t, ctx, c, srv)
		known := NewKnownRows()
		_, rows, _ := recordsOnlyCapture(t, ctx, c, known, cl.first)
		known.Merged(rows)
		c.egraphMu.Lock()
		cl.core.cacheSharedResult().transferRevision++
		c.egraphMu.Unlock()

		second, _, _ := recordsOnlyCapture(t, ctx, c, known, cl.second)
		held, full := heldNumbers(second)
		require.Equal(t, map[uint64]bool{knownRowsTestNumber(cl.base): true}, held, "only the unchanged row below it is held")
		require.True(t, full[knownRowsTestNumber(cl.core)], "the changed row goes in full")
	})
	t.Run("not merged", func(t *testing.T) {
		t.Parallel()
		ctx, c, srv := transferTestCache(t)
		cl := newKnownRowsTestClosure(t, ctx, c, srv)
		known := NewKnownRows()
		_, rows, _ := recordsOnlyCapture(t, ctx, c, known, cl.first)
		known.Merged(rows)
		refusedBundle, refused, _ := recordsOnlyCapture(t, ctx, c, known, cl.second)
		known.NotMerged(refused)

		held, _ := heldNumbers(refusedBundle)
		require.Equal(t, map[uint64]bool{knownRowsTestNumber(cl.core): true}, held)
		retry, _, _ := recordsOnlyCapture(t, ctx, c, known, cl.second)
		held, full := heldNumbers(retry)
		require.Equal(t, map[uint64]bool{knownRowsTestNumber(cl.second): true, knownRowsTestNumber(cl.core): true}, full, "the retry carries what the refused export carried or held")
		require.Equal(t, map[uint64]bool{knownRowsTestNumber(cl.base): true}, held, "the row below, which the first export carried, is still held")
	})
}

// An export captured without Known carries every record in full, known or
// not: the rows are the recipient connection's to know.
func TestExportWithoutKnownHoldsNothing(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	cl := newKnownRowsTestClosure(t, ctx, c, srv)
	known := NewKnownRows()
	_, rows, _ := recordsOnlyCapture(t, ctx, c, known, cl.first)
	known.Merged(rows)
	require.Equal(t, 3, known.Len())

	bundle := exportTestBundle(t, ctx, c, cl.second)
	held, _ := heldNumbers(bundle)
	require.Empty(t, held, "a selection without Known holds nothing")
}

// An export that selects outputs carries every record in full, known or not:
// only a records-only export holds rows.
func TestExportSelectingOutputsHoldsNothing(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	cl := newKnownRowsTestClosure(t, ctx, c, srv)
	known := NewKnownRows()
	_, rows, _ := recordsOnlyCapture(t, ctx, c, known, cl.first)
	known.Merged(rows)

	selection := ValueSelection{
		Roots:                         []AnyResult{cl.second},
		Outputs:                       []SelectedValueOutput{{Result: cl.first, Address: PersistedPartAddress{Part: "snapshot"}}},
		LeaveOutOutputsOutsideClosure: true,
		Known:                         known,
	}
	var bundle ValueBundle
	require.NoError(t, c.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	held, full := heldNumbers(bundle)
	require.Empty(t, held)
	require.Len(t, full, 3)
}

// A known row whose recipe another row of the closure shares goes in full: a
// merge refuses a bundle with a held row beside a record of its recipe.
func TestRecordsOnlyExportCarriesASharedRecipeInFull(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	old := persistedListTestResult(t, ctx, c, srv, "known-recipe", String("old"))
	known := NewKnownRows()
	_, rows, _ := recordsOnlyCapture(t, ctx, c, known, old)
	known.Merged(rows)

	// The old entry, which the root uses, is retired when its recipe gets a
	// new value, and the root comes to depend on both.
	root := persistedListTestResult(t, ctx, c, srv, "known-recipe-root", String("root"))
	transferTestDependency(c, ctx, root, old)
	currentEntryTestExpire(c, old)
	fresh := persistedListTestResult(t, ctx, c, srv, "known-recipe", String("fresh"))
	require.NotEqual(t, knownRowsTestNumber(old), knownRowsTestNumber(fresh))
	transferTestDependency(c, ctx, root, fresh)

	bundle, rows, _ := recordsOnlyCapture(t, ctx, c, known, root)
	held, full := heldNumbers(bundle)
	require.Empty(t, held, "the old entry goes in full beside the new one")
	require.Equal(t, map[uint64]bool{knownRowsTestNumber(root): true, knownRowsTestNumber(old): true, knownRowsTestNumber(fresh): true}, full)
	require.Empty(t, rows.Held)
	_, err := validateValueBundle(bundle)
	require.NoError(t, err)
}

// A held row goes in full only for a recipe a row the bundle carries shares:
// a row of its recipe that only a left-out root reaches doesn't count. Here
// root a reaches the old entry, known and busy, which a held row needs no
// copy of; root b reaches the new entry of its recipe and a busy row. b is
// left out, and a goes with the old entry held, where carrying it in full
// would have found it busy and left a out too.
func TestRecordsOnlyExportHoldsARowWhoseRecipeOnlyALeftOutRootShares(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	old := persistedListTestResult(t, ctx, c, srv, "left-out-recipe", String("old"))
	known := NewKnownRows()
	_, rows, _ := recordsOnlyCapture(t, ctx, c, known, old)
	known.Merged(rows)

	a := persistedListTestResult(t, ctx, c, srv, "left-out-recipe-a", String("a"))
	transferTestDependency(c, ctx, a, old)
	currentEntryTestExpire(c, old)
	fresh := persistedListTestResult(t, ctx, c, srv, "left-out-recipe", String("fresh"))
	require.NotEqual(t, knownRowsTestNumber(old), knownRowsTestNumber(fresh))
	blocker := persistedListTestResult(t, ctx, c, srv, "left-out-recipe-blocker", String("blocker"))
	b := persistedListTestResult(t, ctx, c, srv, "left-out-recipe-b", String("b"))
	transferTestDependency(c, ctx, b, fresh)
	transferTestDependency(c, ctx, b, blocker)
	for _, r := range []AnyResult{old, blocker} {
		res := r.cacheSharedResult()
		res.lazyMu.Lock()
		res.lazyWhole.syncPending = true
		res.lazyMu.Unlock()
		defer func() {
			res.lazyMu.Lock()
			res.lazyWhole.syncPending = false
			res.lazyMu.Unlock()
		}()
	}

	var values *ExportedValues
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{a, b}, Known: known}, config.RefConfig{}, func(_ context.Context, v *ExportedValues) error {
		values = v
		return nil
	}))
	require.Len(t, values.LeftOut, 1)
	require.Equal(t, 1, values.LeftOut[0].Index, "only b is left out")
	held, full := heldNumbers(values.Bundle)
	require.Equal(t, map[uint64]bool{knownRowsTestNumber(old): true}, held, "the old entry goes held: no row the bundle carries shares its recipe")
	require.Equal(t, map[uint64]bool{knownRowsTestNumber(a): true}, full)
	require.Equal(t, []uint64{knownRowsTestNumber(old)}, values.Rows.Held)
}
