package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
)

// An export's report names the rows that kept its roots out, with what kept
// each busy, how long exports have found it busy and how many roots it kept
// out, most first. A row the next bundle carries is reported no longer busy,
// with how long it was, and forgotten. A refused root's row isn't busy.
func TestBusyRowsReport(t *testing.T) {
	var busy busyRows
	start := time.Unix(1000, 0)
	notReady := fmt.Errorf("%w: test", dagql.ErrPersistStateNotReady)
	report := busy.observe(start, []dagql.LeftOutRoot{
		{Index: 0, Row: 7, Kind: dagql.BusyBookkeeping, Err: notReady},
		{Index: 1, Row: 5, Kind: dagql.BusyRunning, Err: notReady},
		{Index: 2, Row: 5, Kind: dagql.BusyRunning, Err: notReady},
		{Index: 3, Row: 9, Err: errors.New("refused")},
	}, nil)
	require.Equal(t, busyReport{rows: 2, named: []string{"5:running:0s:2", "7:bookkeeping:0s:1"},
		reasons: []string{"5:persist state not ready: test", "7:persist state not ready: test"}}, report)

	report = busy.observe(start.Add(3*time.Second), []dagql.LeftOutRoot{
		{Index: 0, Row: 7, Kind: dagql.BusyBookkeeping, Err: notReady},
		{Index: 1, Row: 11, Kind: dagql.BusyAbandoned, Err: notReady},
	}, []uint64{5, 6})
	require.Equal(t, busyReport{
		rows:    2,
		named:   []string{"7:bookkeeping:3s:1", "11:abandoned:0s:1"},
		reasons: []string{"7:persist state not ready: test", "11:persist state not ready: test"},
		cleared: 1,
		freed:   []string{"5:running:3s"},
	}, report)

	report = busy.observe(start.Add(4*time.Second), nil, []uint64{5})
	require.Equal(t, busyReport{}, report, "a row no longer busy is forgotten")
}

// The rows remembered as busy are bounded: past the bound, a row newly found
// busy is still reported, with no age.
func TestBusyRowsAreBounded(t *testing.T) {
	var busy busyRows
	start := time.Unix(1000, 0)
	notReady := fmt.Errorf("%w: test", dagql.ErrPersistStateNotReady)
	var left []dagql.LeftOutRoot
	for i := range maxBusyRows {
		left = append(left, dagql.LeftOutRoot{Index: i, Row: uint64(i + 1), Kind: dagql.BusyRunning, Err: notReady})
	}
	report := busy.observe(start, left, nil)
	require.Equal(t, maxBusyRows, report.rows)
	require.Len(t, report.named, maxLoggedBusyRows)
	require.Len(t, busy.rows, maxBusyRows)

	report = busy.observe(start.Add(time.Second), []dagql.LeftOutRoot{{Row: maxBusyRows + 1, Kind: dagql.BusyRunning, Err: notReady}}, nil)
	require.Equal(t, []string{fmt.Sprintf("%d:running:0s:1", maxBusyRows+1)}, report.named)
	require.Len(t, busy.rows, maxBusyRows)
}

// An export's log line reports its busy rows: a busy file's row, what keeps
// it busy and how many roots it kept out.
func TestRemoteCacheAdapterExportReportsBusyRows(t *testing.T) {
	e := newAdapterTestEngine(t)
	busy := e.busyFile(t, "busy")
	first, _ := e.publishFile(t, "on-busy-first", busy)
	second, _ := e.publishFile(t, "on-busy-second", busy)
	var report busyReport
	e.adapter.testBusyReport = func(r busyReport) { report = r }
	require.NoError(t, e.adapter.Export(e.ctx, protocol.Export{Roots: []uint64{entryNumber(t, first), entryNumber(t, second)}}, nil, func(context.Context, remotecache.Export) error { return nil }))
	require.Equal(t, 1, report.rows)
	require.Equal(t, []string{fmt.Sprintf("%d:%s:0s:2", entryNumber(t, busy), dagql.BusyRunning)}, report.named)
	require.Len(t, report.reasons, 1, "the busy row's reason, from the capture's own error")
	require.True(t, strings.HasPrefix(report.reasons[0], fmt.Sprintf("%d:%s", entryNumber(t, busy), dagql.ErrPersistStateNotReady)), report.reasons[0])
}

// Each busy row an export's line names comes with the capture's own error for
// it, which says which check found it busy: of two rows of one kind, one that
// changed during the capture and one whose expiry changed read apart. An
// export with no busy row says nothing of reasons.
func TestBusyRowsReportEachRowsReason(t *testing.T) {
	var busy busyRows
	changed := fmt.Errorf("%w: row 5 ownership changed", dagql.ErrPersistStateNotReady)
	expired := fmt.Errorf("%w: root 6 expiry changed", dagql.ErrPersistStateNotReady)
	report := busy.observe(time.Unix(1000, 0), []dagql.LeftOutRoot{
		{Index: 0, Row: 5, Kind: dagql.BusyOther, Err: changed, Busy: []dagql.BusyRow{{Row: 5, Kind: dagql.BusyOther, Err: changed}, {Row: 6, Kind: dagql.BusyOther, Err: expired}}},
		{Index: 1, Row: 6, Kind: dagql.BusyOther, Err: expired},
	}, nil)
	require.Equal(t, []string{"6:other:0s:2", "5:other:0s:1"}, report.named)
	require.Equal(t, []string{"6:persist state not ready: root 6 expiry changed", "5:persist state not ready: row 5 ownership changed"}, report.reasons)
	require.Contains(t, report.attrs(), "busy_reasons")
	require.Equal(t, []any{"busy_rows", 0, "busy_cleared", 0}, busy.observe(time.Unix(1001, 0), nil, nil).attrs(), "an export with no busy row says nothing of reasons")
}

// A root over several busy rows counts against each of them: the report
// counts every row a left-out root reaches, not only the one it was left out
// for.
func TestBusyRowsCountEveryRowARootReaches(t *testing.T) {
	var busy busyRows
	notReady := fmt.Errorf("%w: test", dagql.ErrPersistStateNotReady)
	report := busy.observe(time.Unix(1000, 0), []dagql.LeftOutRoot{
		{Index: 0, Row: 5, Kind: dagql.BusyRunning, Err: notReady, Busy: []dagql.BusyRow{{Row: 5, Kind: dagql.BusyRunning}, {Row: 6, Kind: dagql.BusyBookkeeping}}},
		{Index: 1, Row: 6, Kind: dagql.BusyBookkeeping, Err: notReady, Busy: []dagql.BusyRow{{Row: 6, Kind: dagql.BusyBookkeeping}}},
	}, nil)
	require.Equal(t, busyReport{rows: 2, named: []string{"6:bookkeeping:0s:2", "5:running:0s:1"}}, report)
}

// A root an export leaves out busy is handed on as ready once what kept it
// out can be captured, and not before: for the busy file, its own
// evaluation; for the file above it, the row below it. The next export
// carries both.
func TestRemoteCacheAdapterHandsOnABusyRootOnceItIsReady(t *testing.T) {
	e := newAdapterTestEngine(t)
	busy := e.busyFile(t, "busy")
	above, _ := e.publishFile(t, "on-busy", busy)
	roots := []uint64{entryNumber(t, busy), entryNumber(t, above)}
	var skipped []protocol.SkippedRoot
	require.NoError(t, e.adapter.Export(e.ctx, protocol.Export{Roots: roots}, nil, func(_ context.Context, ex remotecache.Export) error {
		skipped = ex.Skipped
		return nil
	}))
	require.Len(t, skipped, 2)
	for _, root := range skipped {
		require.Equal(t, protocol.SkipBusy, root.Reason)
	}

	took := make(chan []uint64, 1)
	go func() {
		if ready, err := e.adapter.TakeReadyRoots(e.ctx); err == nil {
			took <- ready
		}
	}()
	select {
	case ready := <-took:
		t.Fatalf("roots handed on as ready while their row runs: %v", ready)
	case <-time.After(100 * time.Millisecond):
	}
	e.bodies.unpause()
	select {
	case ready := <-took:
		require.Equal(t, roots, ready)
	case <-time.After(10 * time.Second):
		t.Fatal("the busy roots were never handed on as ready")
	}

	var again remotecache.Export
	require.NoError(t, e.adapter.Export(e.ctx, protocol.Export{Roots: roots}, nil, func(_ context.Context, ex remotecache.Export) error {
		again = ex
		return nil
	}))
	require.Empty(t, again.Skipped, "the next export carries both")
}

// A root kept out with nothing in flight on its rows, as a capture race
// leaves one, is ready as soon as it begins to wait; one whose row is busy
// waits until the row isn't. Each is handed on once.
func TestBusyWaitsHandOnEachRootOnceItsRowsAreFree(t *testing.T) {
	var w busyWaits
	w.wait(3, dagql.LeftOutRoot{Row: 5, Kind: dagql.BusyOther})
	w.wait(4, dagql.LeftOutRoot{Row: 4, Kind: dagql.BusyRunning, Busy: []dagql.BusyRow{{Row: 6, Kind: dagql.BusyRunning}, {Row: 7, Kind: dagql.BusyOther}}})
	select {
	case <-w.addedCh():
	default:
		t.Fatal("a root that begins to wait is looked at")
	}
	busy := map[uint64]bool{6: true}
	stillBusy := func(rows []uint64) []uint64 {
		var out []uint64
		for _, row := range rows {
			if busy[row] {
				out = append(out, row)
			}
		}
		return out
	}

	ready, waiting := w.take(stillBusy)
	require.Equal(t, []uint64{3}, ready, "nothing is in flight on the raced row")
	require.True(t, waiting)
	ready, waiting = w.take(stillBusy)
	require.Empty(t, ready)
	require.True(t, waiting)

	busy[6] = false
	ready, waiting = w.take(stillBusy)
	require.Equal(t, []uint64{4}, ready)
	require.False(t, waiting)
	ready, _ = w.take(stillBusy)
	require.Empty(t, ready, "a root is handed on once")
}

// What the engine's look for ready busy roots costs is counted: each look,
// what woke it, the rows it read and their time, and the roots it handed
// on, for its log line as a connection ends.
func TestRemoteCacheAdapterCountsItsLooksForReadyRoots(t *testing.T) {
	e := newAdapterTestEngine(t)
	busy := e.busyFile(t, "busy")
	above, _ := e.publishFile(t, "on-busy", busy)
	roots := []uint64{entryNumber(t, busy), entryNumber(t, above)}
	require.NoError(t, e.adapter.Export(e.ctx, protocol.Export{Roots: roots}, nil, func(context.Context, remotecache.Export) error { return nil }))

	took := make(chan []uint64, 1)
	go func() {
		if ready, err := e.adapter.TakeReadyRoots(e.ctx); err == nil {
			took <- ready
		}
	}()
	// The look as the roots began to wait, and the one their added wake
	// brings, both while the row runs: after them only an end wakes it.
	require.Eventually(t, func() bool {
		s := e.adapter.readyStats.snapshot()
		return s.looks >= 2 && s.addWakes >= 1
	}, 10*time.Second, 5*time.Millisecond, "the looks while the row runs")
	e.bodies.unpause()
	select {
	case ready := <-took:
		require.Equal(t, roots, ready)
	case <-time.After(10 * time.Second):
		t.Fatal("the busy roots were never handed on as ready")
	}
	stats := e.adapter.readyStats.snapshot()
	require.GreaterOrEqual(t, stats.looks, int64(3), "two looks while the row runs, and one after the evaluation's end")
	require.GreaterOrEqual(t, stats.endWakes, int64(1))
	require.GreaterOrEqual(t, stats.rows, int64(3*len(roots)), "each look reads both roots' rows")
	require.Positive(t, stats.lookTime)
	require.Equal(t, int64(len(roots)), stats.roots)
	require.Contains(t, stats.attrs(), "look_time")
}
