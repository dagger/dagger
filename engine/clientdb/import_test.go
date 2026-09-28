package clientdb

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

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
		require.Len(t, live.InspectionStores(), 1)
		// The owner's directory list contains only committed snapshots.
		require.Empty(t, live.imports.dirs)
		return errors.New("download failed")
	})
	require.ErrorContains(t, err, "download failed")
	require.Len(t, live.InspectionStores(), 1)
	require.Empty(t, live.imports.loading)

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
			_, err := live.ImportTrace(t.Context(), "trace", load)
			errs <- err
		})
	}
	<-started
	require.Len(t, live.InspectionStores(), 1)
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
	require.Len(t, live.InspectionStores(), 2)
	require.Len(t, other.InspectionStores(), 1)
	require.False(t, live.HasSpan("complete"))
	require.True(t, live.InspectionStores()[1].HasSpan("complete"))
	dir := live.imports.dirs[0]
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
	require.Len(t, reopened.InspectionStores(), 1, "imports are session-local, not persistent exports")
}
