package clientdb

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// inspectionStores opens live's inspection stores for the rest of the test.
func inspectionStores(t *testing.T, live *DB) []*DB {
	t.Helper()
	stores, release, err := live.InspectionStores(t.Context())
	require.NoError(t, err)
	t.Cleanup(release)
	return stores
}

func TestImportedTraceLifecycle(t *testing.T) {
	registry := NewDBs(t.TempDir())
	live, err := registry.Open(t.Context(), "live")
	require.NoError(t, err)
	other, err := registry.Open(t.Context(), "other")
	require.NoError(t, err)
	defer other.Close()

	// A loader can write an arbitrarily large partial capture without exposing
	// it. Failure discards it, and a subsequent load starts with an empty store.
	_, err = live.ImportTrace(t.Context(), "trace", func(dst *DB) error {
		_, err := dst.AppendSpans([]Span{{TraceID: "trace", SpanID: "partial"}})
		require.NoError(t, err)
		require.Len(t, inspectionStores(t, live), 1)
		require.False(t, live.HasImports())
		return errors.New("download failed")
	})
	require.ErrorContains(t, err, "download failed")
	require.Len(t, inspectionStores(t, live), 1)
	require.Empty(t, live.imports.loading)
	// The partial capture's files are gone.
	entries, err := os.ReadDir(live.imports.dir)
	require.NoError(t, err)
	require.Empty(t, entries)

	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	load := func(dst *DB) error {
		calls.Add(1)
		close(started)
		<-release
		require.Empty(t, dst.SpanIDs())
		_, err := dst.AppendSpans([]Span{{TraceID: "trace", SpanID: "complete"}})
		return err
	}
	var group sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		group.Go(func() {
			imported, err := live.ImportTrace(t.Context(), "trace", load)
			if err == nil {
				err = imported.Close()
			}
			errs <- err
		})
	}
	<-started
	require.Len(t, inspectionStores(t, live), 1)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = live.ImportTrace(canceled, "trace", load)
	require.ErrorIs(t, err, context.Canceled)
	close(release)
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), calls.Load())
	require.Len(t, inspectionStores(t, live), 2)
	require.Len(t, inspectionStores(t, other), 1)
	require.False(t, live.HasSpan("complete"))
	require.True(t, inspectionStores(t, live)[1].HasSpan("complete"))
	dir := live.imports.dir
	require.DirExists(t, dir)
	// An inspection reader retains the owning store, just as a live client
	// does. Closing one handle must not prematurely delete the snapshots.
	reader, err := registry.Open(t.Context(), "live")
	require.NoError(t, err)
	require.NoError(t, live.Close())
	require.DirExists(t, dir)
	require.NoError(t, reader.Close())
	_, err = os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist)
	reopened, err := registry.Open(t.Context(), "live")
	require.NoError(t, err)
	defer reopened.Close()
	require.Len(t, inspectionStores(t, reopened), 1, "imports are session-local, not persistent exports")
}

// TestImportedTraceReleasedWhenIdle checks that an imported trace nobody
// reads is closed after the idle hold -- its indexes released -- and that
// the next reader reopens it from disk, intact, while a reader that holds it
// keeps it open however long it takes.
func TestImportedTraceReleasedWhenIdle(t *testing.T) {
	registry := NewDBs(t.TempDir())
	live, err := registry.Open(t.Context(), "live")
	require.NoError(t, err)
	defer live.Close()
	live.imports.idleHold = 20 * time.Millisecond
	rows := importBenchRows(t, 300)
	imported, err := live.ImportTrace(t.Context(), "trace", func(dst *DB) error {
		_, err := dst.AppendSpans(rows)
		return err
	})
	require.NoError(t, err)
	require.Len(t, imported.SpanIDs(), len(rows))
	importOpen := func() bool { return live.imports.reg.OpenStats().Stores > 0 }

	// Its loader still holds it: not idle.
	time.Sleep(100 * time.Millisecond)
	require.True(t, importOpen())
	require.NoError(t, imported.Close())
	require.Eventually(t, func() bool { return !importOpen() }, 5*time.Second, 10*time.Millisecond,
		"an idle import must be released")

	// The next reader gets it back from disk, whole, and keeps it while it
	// reads.
	stores, release, err := live.InspectionStores(t.Context())
	require.NoError(t, err)
	require.Len(t, stores, 2)
	require.Len(t, stores[1].SpanIDs(), len(rows))
	got, err := stores[1].SelectSpansLatest(t.Context(), map[string]struct{}{rows[42].SpanID: {}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, rows[42].Name, got[0].Name)
	require.Equal(t, rows[42].ParentSpanID, got[0].ParentSpanID)
	time.Sleep(100 * time.Millisecond)
	require.True(t, importOpen(), "a reader holds it")
	release()
	require.Eventually(t, func() bool { return !importOpen() }, 5*time.Second, 10*time.Millisecond)

	// A repeated LoadTrace reuses the snapshot rather than loading again.
	again, err := live.ImportTrace(t.Context(), "trace", func(*DB) error {
		return errors.New("loaded twice")
	})
	require.NoError(t, err)
	require.Len(t, again.SpanIDs(), len(rows))
	require.NoError(t, again.Close())
}
