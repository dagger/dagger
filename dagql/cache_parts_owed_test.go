package dagql

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ownedPartsCache is a cache with the share report, whose reports go to the
// returned channel, and an entry of value, of one part, "snapshot", complete,
// whose record can't be read from then on while value.fail is set: fail is
// set once the entry is made, and the entry's last read of its parts is
// forgotten.
func ownedPartsCache(t *testing.T, value *describedPartValue, fail error) (context.Context, *Cache, *sharedResult, chan []SnapshotSharedPart) {
	t.Helper()
	reports := make(chan []SnapshotSharedPart, 8)
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil, WithSnapshotShareReport(func(parts []SnapshotSharedPart) { reports <- parts }))
	require.NoError(t, err)
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*statePartValue]{}))
	srv.InstallObject(NewClass(srv, ClassOpts[*describedPartValue]{}))
	ctx = srvToContext(ContextWithCache(ctx, c), srv)
	res := persistedListTestResult(t, ctx, c, srv, "described", value)
	row := res.cacheSharedResult()
	value.fail = fail
	row.completeParts.Store(nil)
	return ctx, c, row, reports
}

func snapshotPartKey(t *testing.T) string {
	t.Helper()
	key, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	return key
}

func requireNoReport(t *testing.T, reports chan []SnapshotSharedPart) {
	t.Helper()
	select {
	case parts := <-reports:
		t.Fatalf("unexpected report %v", parts)
	default:
	}
}

// A span whose read of an entry's parts can't read the entry's record
// reports the parts its gate has settled, a subset. The entry then owes the
// remote cache a report of its complete parts: once its record can be read
// and an evaluation's end wakes the reports, the share report names every
// complete part, at the entry's replacement count. Without it, the remote
// cache would go on believing the engine lacks the rest.
func TestSpanOfSettledPartsOwesAFullReport(t *testing.T) {
	t.Parallel()
	value := &describedPartValue{done: true}
	ctx, c, row, reports := ownedPartsCache(t, value, fmt.Errorf("%w: busy", ErrPersistStateNotReady))
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })

	state, ok := c.resultState(ctx, row, nil)
	require.True(t, ok)
	require.Empty(t, state.Parts, "the span reports the settled parts, none here")
	require.Equal(t, 1, c.reportOwedParts(ctx), "still busy: the entry still owes")
	requireNoReport(t, reports)

	value.fail = nil
	c.wakeOwedPartReports()
	select {
	case parts := <-reports:
		require.Len(t, parts, 1)
		require.Equal(t, uint64(row.id), parts[0].ResultID)
		require.Equal(t, snapshotPartKey(t), parts[0].Part)
		require.Equal(t, row.replacements, parts[0].Replacements)
	case <-time.After(30 * time.Second):
		t.Fatal("no report of the entry's complete parts")
	}
	require.Equal(t, 0, c.reportOwedParts(ctx), "reported: nothing is owed")
	requireNoReport(t, reports)
}

// A cache that closes with an entry still owed reports it then, once every
// evaluation has ended.
func TestOwedPartsReportedAtClose(t *testing.T) {
	t.Parallel()
	value := &describedPartValue{done: true}
	ctx, c, row, reports := ownedPartsCache(t, value, fmt.Errorf("%w: busy", ErrPersistStateNotReady))
	_, ok := c.resultState(ctx, row, nil)
	require.True(t, ok)
	value.fail = nil
	require.NoError(t, c.Close(ctx))
	select {
	case parts := <-reports:
		require.Len(t, parts, 1)
		require.Equal(t, snapshotPartKey(t), parts[0].Part)
	default:
		t.Fatal("close made no report of the owed entry")
	}
}

// An entry whose record can't be described for another reason owes
// nothing: no read can name more than its settled parts.
func TestOwedPartsDroppedWhenTheRecordCantBeDescribed(t *testing.T) {
	t.Parallel()
	value := &describedPartValue{done: true}
	ctx, c, row, reports := ownedPartsCache(t, value, errors.New("boom"))
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	_, ok := c.resultState(ctx, row, nil)
	require.True(t, ok)
	require.Equal(t, 0, c.reportOwedParts(ctx))
	requireNoReport(t, reports)
}

// An entry that owes again while a pass reads it, as when a later span of it,
// or of the value that replaced it, also reported only its settled parts,
// still owes once that pass has reported: the pass served only the owing it
// read.
func TestOwedPartsOwedAgainDuringAPassStillOwe(t *testing.T) {
	t.Parallel()
	value := &describedPartValue{done: true}
	ctx, c, row, reports := ownedPartsCache(t, value, fmt.Errorf("%w: busy", ErrPersistStateNotReady))
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	_, ok := c.resultState(ctx, row, nil)
	require.True(t, ok)
	value.fail = nil
	c.testOwedPartsRead = func(read *sharedResult) {
		c.testOwedPartsRead = nil
		c.owePartsReport(read, nil)
	}
	require.Equal(t, 1, c.reportOwedParts(ctx), "owed again during the pass: still owes")
	require.Len(t, <-reports, 1, "the pass reported what it read")
	require.Equal(t, 0, c.reportOwedParts(ctx))
}

// A pass stopped while it reads, as the worker is at close, leaves the entry
// it couldn't read owed, for the final pass: the failure is the pass's, not
// the record's.
func TestOwedPartsKeptWhenThePassIsStopped(t *testing.T) {
	t.Parallel()
	value := &describedPartValue{done: true}
	ctx, c, row, reports := ownedPartsCache(t, value, fmt.Errorf("%w: busy", ErrPersistStateNotReady))
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	_, ok := c.resultState(ctx, row, nil)
	require.True(t, ok)
	stopped, stop := context.WithCancel(ctx)
	stop()
	value.fail = context.Canceled
	require.Equal(t, 1, c.reportOwedParts(stopped), "stopped mid-read: still owes")
	requireNoReport(t, reports)
	value.fail = nil
	require.Equal(t, 0, c.reportOwedParts(ctx))
	require.Len(t, <-reports, 1)
}

// An owed report names only the parts the entry's spans left out: a part a
// span already reported, settled when it read the entry, isn't named again.
func TestOwedPartsReportOnlyWhatSpansLeftOut(t *testing.T) {
	t.Parallel()
	value := &describedPartValue{done: true}
	ctx, c, row, reports := ownedPartsCache(t, value, nil)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	c.owePartsReport(row, []string{snapshotPartKey(t)})
	require.Equal(t, 0, c.reportOwedParts(ctx), "readable: nothing owed after the pass")
	requireNoReport(t, reports)
}

// A span that reads the entry's record reports every complete part itself:
// what the entry owed from an earlier span is settled, and no report follows.
func TestSpanReadingTheRecordSettlesWhatWasOwed(t *testing.T) {
	t.Parallel()
	value := &describedPartValue{done: true}
	ctx, c, row, reports := ownedPartsCache(t, value, fmt.Errorf("%w: busy", ErrPersistStateNotReady))
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	state, ok := c.resultState(ctx, row, nil)
	require.True(t, ok)
	require.Empty(t, state.Parts)
	value.fail = nil
	state, ok = c.resultState(ctx, row, nil)
	require.True(t, ok)
	require.Equal(t, []string{snapshotPartKey(t)}, state.Parts, "this span reports the part itself")
	require.Equal(t, 0, c.reportOwedParts(ctx), "settled by that span")
	requireNoReport(t, reports)
}

// On an engine with the remote cache, an owed report gives each part's raw
// size as a span would: a directory's, the usage its snapshot chain's layers
// recorded, 300 + 20 + 1.
func TestOwedPartsCarryRawSizes(t *testing.T) {
	t.Parallel()
	reports := make(chan []SnapshotSharedPart, 8)
	mgr := &partSizeTestManager{
		fakeSnapshotManager: &fakeSnapshotManager{},
		usage:               map[string]int64{"top": 300, "mid": 20, "base": 1},
		parent:              map[string]string{"top": "mid", "mid": "base"},
	}
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, filepath.Join(t.TempDir(), "cache.db"), mgr, nil, WithSnapshotShareReport(func(parts []SnapshotSharedPart) { reports <- parts }))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, c.CloseDiscardingPersistence()) })
	_, created, err := c.AttachRemoteCacheBridge()
	require.NoError(t, err)
	require.True(t, created)
	srv := newDagqlServerForTest(t, &persistCodecRoot{})
	srv.InstallObject(NewClass(srv, ClassOpts[*statePartValue]{}))
	ctx = srvToContext(ContextWithCache(ctx, c), srv)
	value := &statePartValue{snapshotID: "top"}
	value.done.Store(true)
	row := persistedListTestResult(t, ctx, c, srv, "dir", value).cacheSharedResult()
	value.fail = fmt.Errorf("%w: busy", ErrPersistStateNotReady)
	row.completeParts.Store(nil)

	state, ok := c.resultState(ctx, row, nil)
	require.True(t, ok)
	require.Empty(t, state.Parts, "the span reports the settled parts, none here")
	value.fail = nil
	require.Equal(t, 0, c.reportOwedParts(ctx))
	parts := <-reports
	require.Len(t, parts, 1)
	require.Equal(t, snapshotPartKey(t), parts[0].Part)
	require.NotNil(t, parts[0].RawSize)
	require.Equal(t, int64(321), *parts[0].RawSize)
}
