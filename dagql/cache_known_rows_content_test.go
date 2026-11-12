package dagql

import (
	"context"
	"testing"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// contentCapture captures root's closure with output's snapshot part
// selected, against known, and returns what it exported.
func contentCapture(t *testing.T, ctx context.Context, c *Cache, known *KnownRows, root, output AnyResult) *ExportedValues {
	t.Helper()
	selection := ValueSelection{
		Roots:   []AnyResult{root},
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

// bundleRow is the bundle's row of the entry res.
func bundleRow(t *testing.T, bundle ValueBundle, res AnyResult) TransferredValue {
	t.Helper()
	for _, row := range bundle.Values {
		if row.SenderNumber == knownRowsTestNumber(res) {
			return row
		}
	}
	t.Fatalf("no row of entry %d", knownRowsTestNumber(res))
	return TransferredValue{}
}

// A content export sends every row the recipient already has as a held row,
// as a records-only export does, but keeps its links to the rows below it:
// the rows its selected part sits on and the rows on the way down to it go
// held too, and the part sits on its held row. Here every row is known, so
// every row goes held, the selected part goes, and the Cloud, which has the
// rows from the first export, merges the bundle.
func TestContentExportHoldsEveryKnownRow(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	base := persistedListTestResult(t, ctx, c, srv, "content-base", String("base"))
	side := persistedListTestResult(t, ctx, c, srv, "content-side", String("side"))
	core := persistedListTestResult(t, ctx, c, srv, "content-core", String("core"))
	out := persistedListTestResult(t, ctx, c, srv, "content-out", &transferTestValue{Text: "pending"})
	mid := persistedListTestResult(t, ctx, c, srv, "content-mid", String("mid"))
	root := persistedListTestResult(t, ctx, c, srv, "content-root", String("root"))
	transferTestDependency(c, ctx, side, base)
	transferTestDependency(c, ctx, out, core)
	transferTestDependency(c, ctx, mid, out)
	transferTestDependency(c, ctx, root, mid)
	transferTestDependency(c, ctx, root, side)
	known := NewKnownRows()
	first, rows, _ := recordsOnlyCapture(t, ctx, c, known, root)
	known.Merged(rows)
	require.Equal(t, 6, known.Len())

	values := contentCapture(t, ctx, c, known, root, out)
	held, full := heldNumbers(values.Bundle)
	n := knownRowsTestNumber
	require.Empty(t, full, "the recipient has every row")
	require.Equal(t, map[uint64]bool{n(root): true, n(mid): true, n(out): true, n(core): true, n(side): true, n(base): true}, held)
	require.Len(t, values.Rows.Held, 6)
	midRow := bundleRow(t, values.Bundle, mid)
	require.Equal(t, []uint64{uint64(bundleRow(t, values.Bundle, out).Ordinal)}, midRow.DependencyIDs, "a held row keeps its links to the rows below it")
	require.Len(t, values.Bundle.Outputs, 1)
	require.Equal(t, ordinalOfNumber(t, values.Bundle, n(out)), values.Bundle.Outputs[0].Ordinal, "the selected part sits on its held row")
	_, err := validateValueBundle(values.Bundle)
	require.NoError(t, err)

	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(cctx, "engine", first)
	require.NoError(t, err)
	require.True(t, reply.Committed)
	reply, err = cloud.MergeValues(cctx, "engine", values.Bundle)
	require.NoError(t, err)
	require.True(t, reply.Committed, "the Cloud merges a content export's held rows")
	require.Empty(t, reply.Stale)
	landed := map[TransferOrdinal]bool{}
	for _, value := range reply.Values {
		landed[value.Ordinal] = true
	}
	require.True(t, landed[values.Bundle.Outputs[0].Ordinal], "the part's held row lands on an entry, for the service to store the part on")
}

// A content export carries in full the rows the recipient doesn't have, and
// holds the rest: a held row above a new row keeps its link to it, so the
// bundle reaches the new row.
func TestContentExportCarriesNewRowsInFull(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	srv.InstallObject(NewClass(srv, ClassOpts[*statePartValue]{}))
	core := persistedListTestResult(t, ctx, c, srv, "new-core", String("core"))
	out := persistedListTestResult(t, ctx, c, srv, "new-out", &transferTestValue{Text: "pending"})
	transferTestDependency(c, ctx, out, core)
	known := NewKnownRows()
	_, rows, _ := recordsOnlyCapture(t, ctx, c, known, out)
	known.Merged(rows)

	value := &statePartValue{}
	value.done.Store(true)
	layer := persistedListTestResult(t, ctx, c, srv, "new-layer", value)
	root := persistedListTestResult(t, ctx, c, srv, "new-root", String("root"))
	transferTestDependency(c, ctx, root, out)
	transferTestDependency(c, ctx, root, layer)

	values := contentCapture(t, ctx, c, known, root, out)
	held, full := heldNumbers(values.Bundle)
	n := knownRowsTestNumber
	require.Equal(t, map[uint64]bool{n(root): true, n(layer): true}, full, "the new rows go in full")
	require.Equal(t, map[uint64]bool{n(out): true, n(core): true}, held)
	_, err := validateValueBundle(values.Bundle)
	require.NoError(t, err)
}

// A held row carries no record, so none of its offers: a row the export
// reaches only through the owner of a held row's offer isn't in the bundle,
// which would otherwise carry a row its roots don't reach. Here a known row
// has an offer whose owner names a row it doesn't depend on, beside the
// selected part.
func TestContentExportLeavesOutWhatOnlyAHeldRowsOfferReaches(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	offered := persistedListTestResult(t, ctx, c, srv, "offer-row", &transferTestValue{Text: "pending"})
	owned := persistedListTestResult(t, ctx, c, srv, "offer-owned", String("owned"))
	transferTestOffer(t, c, ctx, offered, owned)
	out := persistedListTestResult(t, ctx, c, srv, "offer-out", &transferTestValue{Text: "pending"})
	root := persistedListTestResult(t, ctx, c, srv, "offer-root", String("root"))
	transferTestDependency(c, ctx, root, offered)
	transferTestDependency(c, ctx, root, out)
	known := NewKnownRows()
	_, rows, _ := recordsOnlyCapture(t, ctx, c, known, root)
	known.Merged(rows)

	values := contentCapture(t, ctx, c, known, root, out)
	held, full := heldNumbers(values.Bundle)
	n := knownRowsTestNumber
	require.Empty(t, full)
	require.Equal(t, map[uint64]bool{n(root): true, n(offered): true, n(out): true}, held, "the row only the held row's offer reaches isn't carried")
	_, err := validateValueBundle(values.Bundle)
	require.NoError(t, err)
}
