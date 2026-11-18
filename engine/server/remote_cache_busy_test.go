package server

import (
	"context"
	"errors"
	"fmt"
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
	require.Equal(t, busyReport{rows: 2, named: []string{"5:running:0s:2", "7:bookkeeping:0s:1"}}, report)

	report = busy.observe(start.Add(3*time.Second), []dagql.LeftOutRoot{
		{Index: 0, Row: 7, Kind: dagql.BusyBookkeeping, Err: notReady},
		{Index: 1, Row: 11, Kind: dagql.BusyAbandoned, Err: notReady},
	}, []uint64{5, 6})
	require.Equal(t, busyReport{
		rows:    2,
		named:   []string{"7:bookkeeping:3s:1", "11:abandoned:0s:1"},
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
