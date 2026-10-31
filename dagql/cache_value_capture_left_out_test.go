package dagql

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// leftOutTestRows publishes a busy-able row two roots share, with the two
// roots above it, and a ready root over a row of its own.
type leftOutTestRows struct {
	shared, first, second, base, ready AnyResult
}

func newLeftOutTestRows(t *testing.T, ctx context.Context, c *Cache, srv *Server) leftOutTestRows {
	t.Helper()
	rows := leftOutTestRows{
		shared: persistedListTestResult(t, ctx, c, srv, "busy-shared", String("shared")),
		first:  persistedListTestResult(t, ctx, c, srv, "busy-first", String("first")),
		second: persistedListTestResult(t, ctx, c, srv, "busy-second", String("second")),
		base:   persistedListTestResult(t, ctx, c, srv, "ready-base", String("base")),
		ready:  persistedListTestResult(t, ctx, c, srv, "ready-root", String("ready")),
	}
	transferTestDependency(c, ctx, rows.first, rows.shared)
	transferTestDependency(c, ctx, rows.second, rows.shared)
	transferTestDependency(c, ctx, rows.ready, rows.base)
	return rows
}

func (rows leftOutTestRows) all() []AnyResult {
	return []AnyResult{rows.shared, rows.first, rows.second, rows.base, rows.ready}
}

func heldOwnershipCounts(results []AnyResult) map[*sharedResult]int64 {
	owners := map[*sharedResult]int64{}
	for _, r := range results {
		owners[r.cacheSharedResult()] = r.cacheSharedResult().incomingOwnershipCount.Load()
	}
	return owners
}

func senderNumbers(bundle ValueBundle) map[uint64]bool {
	numbers := map[uint64]bool{}
	for _, value := range bundle.Values {
		numbers[value.SenderNumber] = true
	}
	return numbers
}

func rowNumber(res AnyResult) uint64 {
	return uint64(res.cacheSharedResult().id)
}

// A capture leaves out every root whose closure reaches a row it can't
// capture now, and bundles the others in the same capture: a busy row two
// roots share leaves out both, and a root whose closure is all ready goes,
// with its own rows only. The row is busy when the walk reaches it, when its
// record is copied, or when the copy is checked after. Each left-out root
// names the busy row and what keeps it busy, and the capture releases every
// row it held.
func TestExportLeavesOutRootsReachingABusyRow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		when string
		kind BusyKind
	}{
		{"walk", BusyAttaching},
		{"copy", BusyBookkeeping},
		{"after copy", BusyAttaching},
	} {
		t.Run(tc.when, func(t *testing.T) {
			t.Parallel()
			ctx, c, srv := transferTestCache(t)
			rows := newLeftOutTestRows(t, ctx, c, srv)
			res := rows.shared.cacheSharedResult()
			owners := heldOwnershipCounts(rows.all())
			attach := func() {
				c.egraphMu.Lock()
				res.attachDepsMu.Lock()
				res.attachDepsWaitCh = make(chan struct{})
				res.attachDepsMu.Unlock()
				c.egraphMu.Unlock()
			}
			switch tc.when {
			case "walk":
				attach()
			case "copy":
				res.lazyMu.Lock()
				res.lazyWhole.syncPending = true
				res.lazyMu.Unlock()
			case "after copy":
				c.testTransferCopied = func(id uint64) {
					if id == uint64(res.id) {
						attach()
					}
				}
			}
			selection := ValueSelection{Roots: []AnyResult{rows.first, rows.ready, rows.second}}
			calls := 0
			var values *ExportedValues
			err := c.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, exported *ExportedValues) error {
				calls++
				values = exported
				return nil
			})
			c.testTransferCopied = nil
			switch tc.when {
			case "walk", "after copy":
				close(res.attachDepsWaitCh)
			case "copy":
				res.lazyMu.Lock()
				res.lazyWhole.syncPending = false
				res.lazyMu.Unlock()
			}
			require.NoError(t, err)
			require.Equal(t, 1, calls)

			var left []int
			for _, root := range values.LeftOut {
				left = append(left, root.Index)
				require.ErrorIs(t, root.Err, ErrPersistStateNotReady)
				require.Equal(t, rowNumber(rows.shared), root.Row, "the left-out root names the busy row")
				require.Equal(t, tc.kind, root.Kind)
			}
			require.Equal(t, []int{0, 2}, left, "the roots that share the busy row are left out")
			require.Len(t, values.Bundle.Roots, 1)
			require.Equal(t, map[uint64]bool{rowNumber(rows.ready): true, rowNumber(rows.base): true}, senderNumbers(values.Bundle), "the ready root goes with its closure only")
			require.Equal(t, values.Bundle.Roots[0].Ordinal, ordinalOfNumber(t, values.Bundle, rowNumber(rows.ready)))
			_, err = validateValueBundle(values.Bundle)
			require.NoError(t, err, "the bundle of the kept roots is whole")
			for r, count := range owners {
				require.Equal(t, count, r.incomingOwnershipCount.Load(), "the capture releases every row it held")
			}
			require.Zero(t, c.activeGlobalOperations.Load())
		})
	}
}

// A row whose evaluation is running is busy: running while a caller waits
// for it, abandoned once none does. Either way its roots are left out, and
// the others go.
func TestExportNamesARunningRowsKind(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		waiters int
		kind    BusyKind
	}{
		{1, BusyRunning},
		{0, BusyAbandoned},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			ctx, c, srv := transferTestCache(t)
			rows := newLeftOutTestRows(t, ctx, c, srv)
			res := rows.shared.cacheSharedResult()
			res.lazyMu.Lock()
			res.lazyWhole.attempt = &lazyEvalAttempt{done: make(chan struct{}), waiters: tc.waiters}
			res.lazyMu.Unlock()
			var values *ExportedValues
			err := c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{rows.first, rows.ready}}, config.RefConfig{}, func(_ context.Context, exported *ExportedValues) error {
				values = exported
				return nil
			})
			res.lazyMu.Lock()
			res.lazyWhole.attempt = nil
			res.lazyMu.Unlock()
			require.NoError(t, err)
			require.Len(t, values.LeftOut, 1)
			require.Equal(t, LeftOutRoot{Index: 0, Row: rowNumber(rows.shared), Kind: tc.kind, Err: values.LeftOut[0].Err}, values.LeftOut[0])
			require.Equal(t, map[uint64]bool{rowNumber(rows.ready): true, rowNumber(rows.base): true}, senderNumbers(values.Bundle))
		})
	}
}

// A root whose closure reaches a record that fails its check is left out as
// refused, with the record's error and no busy kind, and the other roots go
// in the same capture.
func TestExportRefusesOnlyTheRootsReachingABadRecord(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	ready := persistedListTestResult(t, ctx, c, srv, "refuse-ready", String("ready"))
	unrelated := persistedListTestResult(t, ctx, c, srv, "refuse-unrelated", String("unrelated"))
	// The bad record references a row that isn't its dependency.
	bad := persistedListTestResult(t, ctx, c, srv, "refuse-bad", &transferTestValue{Text: "bad", Child: rowNumber(unrelated)})
	above := persistedListTestResult(t, ctx, c, srv, "refuse-above", String("above"))
	transferTestDependency(c, ctx, above, bad)
	owners := heldOwnershipCounts([]AnyResult{ready, unrelated, bad, above})

	var values *ExportedValues
	err := c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{above, ready}}, config.RefConfig{}, func(_ context.Context, exported *ExportedValues) error {
		values = exported
		return nil
	})
	require.NoError(t, err)
	require.Len(t, values.LeftOut, 1)
	left := values.LeftOut[0]
	require.Equal(t, 0, left.Index)
	require.Equal(t, rowNumber(bad), left.Row)
	require.Empty(t, left.Kind, "a refusal has no busy kind")
	require.NotErrorIs(t, left.Err, ErrPersistStateNotReady)
	require.ErrorContains(t, left.Err, "is not a direct dependency")
	require.Equal(t, map[uint64]bool{rowNumber(ready): true}, senderNumbers(values.Bundle))
	for r, count := range owners {
		require.Equal(t, count, r.incomingOwnershipCount.Load(), "the capture releases every row it held")
	}
}

// A capture that leaves out every root consumes nothing, and its error
// names every root left out: busy, as before, when a root was busy.
func TestExportWithEveryRootLeftOut(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	rows := newLeftOutTestRows(t, ctx, c, srv)
	res := rows.shared.cacheSharedResult()
	res.lazyMu.Lock()
	res.lazyWhole.syncPending = true
	res.lazyMu.Unlock()
	defer func() {
		res.lazyMu.Lock()
		res.lazyWhole.syncPending = false
		res.lazyMu.Unlock()
	}()
	owners := heldOwnershipCounts(rows.all())
	err := c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{rows.first, rows.second}}, config.RefConfig{}, func(context.Context, *ExportedValues) error {
		t.Fatal("a capture with no root delivered")
		return nil
	})
	require.ErrorIs(t, err, ErrPersistStateNotReady)
	var left *RootsLeftOutError
	require.True(t, errors.As(err, &left), "the error names the left-out roots: %v", err)
	require.Len(t, left.Roots, 2)
	for i, root := range left.Roots {
		require.Equal(t, i, root.Index)
		require.Equal(t, BusyBookkeeping, root.Kind)
	}
	for r, count := range owners {
		require.Equal(t, count, r.incomingOwnershipCount.Load(), "the capture releases every row it held")
	}
}

// An output selected on a row only left-out roots reach is left out with
// them, even when the selection doesn't ask to leave out outputs outside
// the closure: the row was in it.
func TestExportLeavesOutTheOutputsOfLeftOutRoots(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	ready := persistedListTestResult(t, ctx, c, srv, "outputs-ready", &transferTestValue{Text: "pending"})
	busy := persistedListTestResult(t, ctx, c, srv, "outputs-busy", &transferTestValue{Text: "pending"})
	res := busy.cacheSharedResult()
	res.lazyMu.Lock()
	res.lazyWhole.syncPending = true
	res.lazyMu.Unlock()
	defer func() {
		res.lazyMu.Lock()
		res.lazyWhole.syncPending = false
		res.lazyMu.Unlock()
	}()
	selection := ValueSelection{
		Roots: []AnyResult{busy, ready},
		Outputs: []SelectedValueOutput{
			{Result: busy, Address: PersistedPartAddress{Part: "snapshot"}},
			{Result: ready, Address: PersistedPartAddress{Part: "snapshot"}},
		},
	}
	var values *ExportedValues
	require.NoError(t, c.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, exported *ExportedValues) error {
		values = exported
		return nil
	}))
	require.Len(t, values.LeftOut, 1)
	require.Len(t, values.Bundle.Values, 1)
	require.Len(t, values.Bundle.Outputs, 1, "only the kept root's output")
	require.Equal(t, values.Bundle.Values[0].Ordinal, values.Bundle.Outputs[0].Ordinal)
}

func ordinalOfNumber(t *testing.T, bundle ValueBundle, number uint64) TransferOrdinal {
	t.Helper()
	for _, value := range bundle.Values {
		if value.SenderNumber == number {
			return value.Ordinal
		}
	}
	t.Fatalf("no row %d", number)
	return 0
}

// A left-out root names every busy row its closure reaches, not only the one
// it's left out for: a root over two running rows names both, and one over
// one of them names it.
func TestExportNamesEveryBusyRowARootReaches(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	first := persistedListTestResult(t, ctx, c, srv, "every-busy-first", String("first"))
	second := persistedListTestResult(t, ctx, c, srv, "every-busy-second", String("second"))
	both := persistedListTestResult(t, ctx, c, srv, "every-busy-both", String("both"))
	one := persistedListTestResult(t, ctx, c, srv, "every-busy-one", String("one"))
	transferTestDependency(c, ctx, both, first)
	transferTestDependency(c, ctx, both, second)
	transferTestDependency(c, ctx, one, second)
	for _, r := range []AnyResult{first, second} {
		res := r.cacheSharedResult()
		res.lazyMu.Lock()
		res.lazyWhole.attempt = &lazyEvalAttempt{done: make(chan struct{}), waiters: 1}
		res.lazyMu.Unlock()
		defer func() {
			res.lazyMu.Lock()
			res.lazyWhole.attempt = nil
			res.lazyMu.Unlock()
		}()
	}
	err := c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{both, one}}, config.RefConfig{}, func(context.Context, *ExportedValues) error {
		t.Fatal("a capture with no root delivered")
		return nil
	})
	var left *RootsLeftOutError
	require.True(t, errors.As(err, &left), "the error names the left-out roots: %v", err)
	require.Len(t, left.Roots, 2)
	require.Equal(t, []BusyRow{{Row: rowNumber(first), Kind: BusyRunning}, {Row: rowNumber(second), Kind: BusyRunning}}, left.Roots[0].Busy)
	require.Equal(t, []BusyRow{{Row: rowNumber(second), Kind: BusyRunning}}, left.Roots[1].Busy)
}

// An output selected on a ready row below a root that's left out is left
// out with it, wherever the walk meets the busy row among the root's
// dependencies, and the other roots go: the selection doesn't fail for an
// output outside the closure.
func TestExportLeavesOutOutputsBelowARootBlockedAtTheWalk(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	busy := persistedListTestResult(t, ctx, c, srv, "below-busy", String("busy"))
	root := persistedListTestResult(t, ctx, c, srv, "below-root", String("root"))
	ready := persistedListTestResult(t, ctx, c, srv, "below-ready-root", &transferTestValue{Text: "pending"})
	transferTestDependency(c, ctx, root, busy)
	selection := ValueSelection{Roots: []AnyResult{root, ready}, Outputs: []SelectedValueOutput{{Result: ready, Address: PersistedPartAddress{Part: "snapshot"}}}}
	// Siblings of the busy row, each with a selected output: the walk meets
	// the busy row before most of them.
	for i := range 8 {
		sibling := persistedListTestResult(t, ctx, c, srv, fmt.Sprintf("below-sibling-%d", i), &transferTestValue{Text: "pending"})
		transferTestDependency(c, ctx, root, sibling)
		selection.Outputs = append(selection.Outputs, SelectedValueOutput{Result: sibling, Address: PersistedPartAddress{Part: "snapshot"}})
	}
	res := busy.cacheSharedResult()
	c.egraphMu.Lock()
	res.attachDepsMu.Lock()
	res.attachDepsWaitCh = make(chan struct{})
	res.attachDepsMu.Unlock()
	c.egraphMu.Unlock()
	defer close(res.attachDepsWaitCh)
	var values *ExportedValues
	require.NoError(t, c.WithExportedValues(ctx, selection, config.RefConfig{}, func(_ context.Context, exported *ExportedValues) error {
		values = exported
		return nil
	}))
	require.Len(t, values.LeftOut, 1)
	require.Equal(t, 0, values.LeftOut[0].Index)
	require.Equal(t, map[uint64]bool{rowNumber(ready): true}, senderNumbers(values.Bundle))
	require.Len(t, values.Bundle.Outputs, 1, "only the kept root's output")
}
