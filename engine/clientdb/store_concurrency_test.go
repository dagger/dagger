package clientdb

import (
	"bytes"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogStreamConcurrentProperty(t *testing.T) {
	const (
		writers         = 8
		readers         = 6
		batchesPerWrite = 250
	)

	var modelMu sync.RWMutex
	model := make(map[int64]Metric)
	var published atomic.Int64
	stream, err := openLogStream(
		t.Context(),
		filepath.Join(t.TempDir(), "metrics.log"),
		metricCodec,
		1024,
		nil,
		func(rows []Metric) {
			modelMu.Lock()
			for _, row := range rows {
				model[row.ID] = row
				published.Store(row.ID)
			}
			modelMu.Unlock()
		},
	)
	require.NoError(t, err)

	errCh := make(chan error, writers+readers)
	stopReaders := make(chan struct{})
	var readerGroup sync.WaitGroup
	for reader := range readers {
		readerGroup.Go(func() {
			rng := rand.New(rand.NewSource(int64(10_000 + reader)))
			for {
				select {
				case <-stopReaders:
					return
				default:
				}
				maxID := published.Load()
				var cursor int64
				if maxID > 0 {
					cursor = rng.Int63n(maxID + 1)
				}
				limit := rng.Intn(31) + 1
				rows, err := stream.Since(t.Context(), cursor, limit)
				if err != nil {
					errCh <- fmt.Errorf("reader %d since %d: %w", reader, cursor, err)
					return
				}
				if len(rows) > limit {
					errCh <- fmt.Errorf("reader %d got %d rows with limit %d", reader, len(rows), limit)
					return
				}
				for i, row := range rows {
					wantID := cursor + int64(i) + 1
					if row.ID != wantID {
						errCh <- fmt.Errorf("reader %d got ID %d at offset %d after %d, want %d", reader, row.ID, i, cursor, wantID)
						return
					}
					modelMu.RLock()
					want, found := model[row.ID]
					modelMu.RUnlock()
					if !found || !bytes.Equal(want.Data, row.Data) {
						errCh <- fmt.Errorf("reader %d row %d diverged from reference model", reader, row.ID)
						return
					}
				}
			}
		})
	}

	var writerGroup sync.WaitGroup
	for writer := range writers {
		writerGroup.Go(func() {
			rng := rand.New(rand.NewSource(int64(20_000 + writer)))
			for batch := 0; batch < batchesPerWrite; batch++ {
				rows := make([]Metric, rng.Intn(5)+1)
				for i := range rows {
					data := make([]byte, rng.Intn(192))
					_, _ = rng.Read(data)
					rows[i].Data = data
				}
				if _, err := stream.Append(rows); err != nil {
					errCh <- fmt.Errorf("writer %d batch %d: %w", writer, batch, err)
					return
				}
			}
		})
	}
	writerGroup.Wait()
	close(stopReaders)
	readerGroup.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}

	modelMu.RLock()
	want := make([]Metric, len(model))
	for id, row := range model {
		want[id-1] = row
	}
	modelMu.RUnlock()

	var got []Metric
	var cursor int64
	for {
		page, err := stream.Since(t.Context(), cursor, 37)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		cursor = page[len(page)-1].ID
	}
	require.Equal(t, want, got)
	require.NoError(t, stream.close())
}

func TestLogStreamAppendReturnVisibilityHammer(t *testing.T) {
	const (
		writers       = 16
		rowsPerWriter = 300
	)
	stream, err := openLogStream(
		t.Context(),
		filepath.Join(t.TempDir(), "metrics.log"),
		metricCodec,
		512,
		nil,
		nil,
	)
	require.NoError(t, err)

	errCh := make(chan error, writers)
	var group sync.WaitGroup
	for writer := range writers {
		group.Go(func() {
			for sequence := range rowsPerWriter {
				rows := []Metric{{Data: []byte(fmt.Sprintf("%d/%d", writer, sequence))}}
				last, err := stream.Append(rows)
				if err != nil {
					errCh <- fmt.Errorf("writer %d append %d: %w", writer, sequence, err)
					return
				}
				got, err := stream.Since(t.Context(), last-1, 1)
				if err != nil {
					errCh <- fmt.Errorf("writer %d read %d: %w", writer, sequence, err)
					return
				}
				if len(got) != 1 || got[0].ID != last || !bytes.Equal(got[0].Data, rows[0].Data) {
					errCh <- fmt.Errorf("append-returned row %d was not immediately visible", last)
					return
				}
			}
		})
	}
	group.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
	require.NoError(t, stream.close())
}
