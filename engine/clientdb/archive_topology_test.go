package clientdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/archive"
	"github.com/stretchr/testify/require"
)

func appendTopologyChain(t *testing.T, db *DB, n int) HighWater {
	t.Helper()
	var spans []Span
	for i := 1; i <= n; i++ {
		row := Span{TraceID: selectionTrace, SpanID: fmt.Sprint(i), Attributes: []byte("[]"), Links: []byte("[]")}
		if i > 1 {
			row.ParentSpanID = sql.NullString{String: fmt.Sprint(i - 1), Valid: true}
		}
		if i == n-2 {
			row.StatusCode = 1
		}
		spans = append(spans, row)
	}
	_, err := db.AppendSpans(spans)
	require.NoError(t, err)
	_, err = db.AppendLogs([]Log{selectionLog(t, "2", selectionText("output"))})
	require.NoError(t, err)
	cut, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	return cut
}

// TestArchiveSpanViewSharesTopology: span views at one cut share a single
// immutable topology, while each keeps its own selection.
func TestArchiveSpanViewSharesTopology(t *testing.T) {
	db, err := openStore(t.Context(), t.TempDir(), "client", telemetryTailBudget)
	require.NoError(t, err)
	defer db.Close()
	cut := appendTopologyChain(t, db, 8)

	initial, err := db.ArchiveSpanView(t.Context(), selectionTrace, cut, &archive.SpanSelection{DagUIView: true})
	require.NoError(t, err)
	listen, err := db.ArchiveSpanView(t.Context(), selectionTrace, cut, &archive.SpanSelection{NoRoot: true, Listen: []string{"3"}})
	require.NoError(t, err)
	full, err := db.ArchiveSpanView(t.Context(), selectionTrace, cut, nil)
	require.NoError(t, err)
	require.Same(t, initial.archiveTopology, listen.archiveTopology)
	require.Same(t, initial.archiveTopology, full.archiveTopology)

	require.True(t, initial.partial)
	require.False(t, initial.selected["8"])
	require.True(t, listen.selected["8"], "a request's selection never leaks into another's")
	require.Nil(t, full.selected)
	require.False(t, full.partial)
	require.True(t, full.Includes(Span{ID: 8, SpanID: "8"}))
	require.False(t, initial.Includes(Span{ID: 8, SpanID: "8"}))
	require.True(t, initial.nodes["2"].hasLogs)

	// A later cut is a different topology; the earlier one is unchanged.
	_, err = db.AppendSpans([]Span{{TraceID: selectionTrace, SpanID: "9", ParentSpanID: sql.NullString{String: "8", Valid: true}, Attributes: []byte("[]"), Links: []byte("[]")}})
	require.NoError(t, err)
	later, err := db.Checkpoint(t.Context())
	require.NoError(t, err)
	grown, err := db.ArchiveSpanView(t.Context(), selectionTrace, later, nil)
	require.NoError(t, err)
	require.NotSame(t, full.archiveTopology, grown.archiveTopology)
	require.Len(t, grown.nodes, 9)
	require.Len(t, full.nodes, 8)

	// A cut beyond the store fails, and the failure is not cached.
	_, err = db.ArchiveSpanView(t.Context(), selectionTrace, HighWater{Spans: later.Spans, Logs: later.Logs + 5}, nil)
	require.ErrorContains(t, err, "truncated")
	db.archiveTopos.mu.Lock()
	require.Len(t, db.archiveTopos.entries, 2)
	db.archiveTopos.mu.Unlock()
}

func TestArchiveTopologyCacheCoalescesConcurrentBuilds(t *testing.T) {
	c := newArchiveTopologyCache()
	key := archiveTopologyKey{trace: selectionTrace, spans: 10, logs: 10}
	release := make(chan struct{})
	var builds atomic.Int32
	build := func(context.Context) (*archiveTopology, error) {
		builds.Add(1)
		<-release
		return &archiveTopology{}, nil
	}
	const callers = 16
	results := make([]*archiveTopology, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			topo, err := c.get(t.Context(), key, build)
			require.NoError(t, err)
			results[i] = topo
		})
	}
	require.Eventually(t, func() bool { return builds.Load() == 1 }, 5*time.Second, time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	close(release)
	wg.Wait()
	require.EqualValues(t, 1, builds.Load())
	for _, topo := range results {
		require.Same(t, results[0], topo)
	}
}

func TestArchiveTopologyCacheRetriesCanceledBuilderAndBounds(t *testing.T) {
	c := newArchiveTopologyCache()
	key := archiveTopologyKey{trace: selectionTrace, spans: 1}

	// The first builder's request is canceled mid-build; a concurrent waiter
	// with a live context builds again instead of inheriting the cancellation.
	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	started := make(chan struct{})
	leaderDone := make(chan error, 1)
	go func() {
		_, err := c.get(leaderCtx, key, func(ctx context.Context) (*archiveTopology, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		})
		leaderDone <- err
	}()
	<-started
	waiterDone := make(chan *archiveTopology, 1)
	var rebuilt atomic.Int32
	go func() {
		topo, err := c.get(t.Context(), key, func(context.Context) (*archiveTopology, error) {
			rebuilt.Add(1)
			return &archiveTopology{}, nil
		})
		require.NoError(t, err)
		waiterDone <- topo
	}()
	time.Sleep(10 * time.Millisecond)
	cancelLeader()
	require.ErrorIs(t, <-leaderDone, context.Canceled)
	require.NotNil(t, <-waiterDone)
	require.EqualValues(t, 1, rebuilt.Load())

	// Non-cancellation failures are returned, not cached.
	boom := errors.New("boom")
	failKey := archiveTopologyKey{trace: selectionTrace, spans: 2}
	_, err := c.get(t.Context(), failKey, func(context.Context) (*archiveTopology, error) { return nil, boom })
	require.ErrorIs(t, err, boom)
	topo, err := c.get(t.Context(), failKey, func(context.Context) (*archiveTopology, error) { return &archiveTopology{}, nil })
	require.NoError(t, err)
	require.NotNil(t, topo)

	// The cache is LRU-bounded.
	for i := range 3 * archiveTopologyCacheSize {
		_, err := c.get(t.Context(), archiveTopologyKey{trace: selectionTrace, spans: int64(100 + i)}, func(context.Context) (*archiveTopology, error) { return &archiveTopology{}, nil })
		require.NoError(t, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	require.Len(t, c.entries, archiveTopologyCacheSize)
	require.Len(t, c.lru, archiveTopologyCacheSize)
}
