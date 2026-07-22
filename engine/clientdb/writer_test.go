package clientdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteAgentConcurrentProducers(t *testing.T) {
	const producers = 64

	dbs := NewDBs(t.TempDir())
	keepAlive, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)
	var closeOnce sync.Once
	var closeErr error
	closeKeepAlive := func() {
		closeOnce.Do(func() { closeErr = keepAlive.Close() })
	}
	t.Cleanup(func() {
		closeKeepAlive()
		require.NoError(t, closeErr)
	})

	start := make(chan struct{})
	errs := make(chan error, producers*2)
	var wg sync.WaitGroup
	for i := range producers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			db, err := dbs.Open(t.Context(), "client")
			if err != nil {
				errs <- err
				return
			}
			defer func() {
				if err := db.Close(); err != nil {
					errs <- err
				}
			}()

			spanID := fmt.Sprintf("span-%d", i)
			_, err = db.Write(t.Context(), insertSpanBatch(testSpan(spanID)))
			if err != nil {
				errs <- err
				return
			}

			// A successful return is this batch's commit watermark: the row
			// must already be visible on the independent read pool.
			span, err := db.Read().SelectSpan(t.Context(), SelectSpanParams{
				TraceID: "trace",
				SpanID:  spanID,
			})
			if err != nil {
				errs <- err
				return
			}
			if span.SpanID != spanID {
				errs <- fmt.Errorf("selected span %q, expected %q", span.SpanID, spanID)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	spans, err := keepAlive.Read().SelectSpansSince(t.Context(), SelectSpansSinceParams{
		Limit: producers + 1,
	})
	require.NoError(t, err)
	require.Len(t, spans, producers)
	counts := make(map[string]int, producers)
	for _, span := range spans {
		counts[span.SpanID]++
	}
	for i := range producers {
		require.Equal(t, 1, counts[fmt.Sprintf("span-%d", i)])
	}

	done := keepAlive.writeAgent.done
	closeKeepAlive()
	require.NoError(t, closeErr)
	select {
	case <-done:
	default:
		t.Fatal("client DB writer goroutine still running after final close")
	}
}

func TestWriteAgentPropagatesBatchError(t *testing.T) {
	dbs := NewDBs(t.TempDir())
	db, err := dbs.Open(t.Context(), "client")
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, db.Close())
	})

	blocked := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWrites := func() {
		releaseOnce.Do(func() { close(release) })
	}
	t.Cleanup(releaseWrites)

	blockerErr := make(chan error, 1)
	go func() {
		_, err := db.Write(t.Context(), func(context.Context, *Queries) (time.Duration, error) {
			close(blocked)
			<-release
			return 0, nil
		})
		blockerErr <- err
	}()
	<-blocked

	wantErr := errors.New("batch failed")
	failed := testSpan("failed")
	failedErr := make(chan error, 1)
	go func() {
		_, err := db.Write(t.Context(), func(ctx context.Context, q *Queries) (time.Duration, error) {
			start := time.Now()
			_, err := q.InsertSpan(ctx, failed)
			if err != nil {
				return time.Since(start), err
			}
			return time.Since(start), wantErr
		})
		failedErr <- err
	}()

	succeeded := testSpan("succeeded")
	succeededErr := make(chan error, 1)
	go func() {
		_, err := db.Write(t.Context(), insertSpanBatch(succeeded))
		succeededErr <- err
	}()

	// Hold the first transaction until both following batches are queued, so
	// they are drained into the same transaction and exercise savepoint-level
	// error isolation.
	require.Eventually(t, func() bool {
		return len(db.writeAgent.queue) == 2
	}, 5*time.Second, time.Millisecond)
	releaseWrites()
	require.NoError(t, <-blockerErr)
	require.ErrorIs(t, <-failedErr, wantErr)
	require.NoError(t, <-succeededErr)

	_, err = db.Read().SelectSpan(t.Context(), SelectSpanParams{
		TraceID: failed.TraceID,
		SpanID:  failed.SpanID,
	})
	require.ErrorIs(t, err, sql.ErrNoRows, "failed batch should roll back to its savepoint")

	span, err := db.Read().SelectSpan(t.Context(), SelectSpanParams{
		TraceID: succeeded.TraceID,
		SpanID:  succeeded.SpanID,
	})
	require.NoError(t, err)
	require.Equal(t, succeeded.SpanID, span.SpanID)
}

func insertSpanBatch(span InsertSpanParams) WriteBatch {
	return func(ctx context.Context, q *Queries) (time.Duration, error) {
		start := time.Now()
		_, err := q.InsertSpan(ctx, span)
		return time.Since(start), err
	}
}

func testSpan(spanID string) InsertSpanParams {
	return InsertSpanParams{
		TraceID:           "trace",
		SpanID:            spanID,
		Name:              spanID,
		Kind:              "internal",
		ResourceSchemaUrl: "",
	}
}
