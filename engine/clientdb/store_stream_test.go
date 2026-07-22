package clientdb

import (
	"bytes"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLogStreamAppendVisibilityAndSpill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.log")
	stream, err := openLogStream(t.Context(), path, metricCodec, 512, nil, nil)
	require.NoError(t, err)

	want := make([]Metric, 100)
	var previousID int64
	for i := range want {
		row := Metric{Data: bytes.Repeat([]byte{byte(i)}, 32)}
		last, err := stream.Append([]Metric{row})
		require.NoError(t, err)
		want[i] = row
		want[i].ID = last

		// The newest row must be readable as soon as Append returns, whether
		// the spiller has left it in the tail or already flushed it.
		got, err := stream.Since(t.Context(), previousID, 1)
		require.NoError(t, err)
		require.Equal(t, []Metric{want[i]}, got)
		previousID = last
	}

	require.Eventually(t, func() bool {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		return stream.tailBase > 1 && stream.tailBytes <= stream.budget && len(stream.tail) > 0
	}, 5*time.Second, time.Millisecond)

	var got []Metric
	var cursor int64
	for {
		page, err := stream.Since(t.Context(), cursor, 17)
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

func TestLogStreamCloseFlushesTailAndContinuesIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.log")
	stream, err := openLogStream(t.Context(), path, metricCodec, telemetryTailBudget, nil, nil)
	require.NoError(t, err)
	last, err := stream.Append([]Metric{{Data: nil}, {Data: []byte{}}, {Data: []byte("three")}})
	require.NoError(t, err)
	require.Equal(t, int64(3), last)
	require.NoError(t, stream.close())

	stream, err = openLogStream(t.Context(), path, metricCodec, telemetryTailBudget, nil, nil)
	require.NoError(t, err)
	last, err = stream.Append([]Metric{{Data: []byte("four")}})
	require.NoError(t, err)
	require.Equal(t, int64(4), last)

	got, err := stream.Since(t.Context(), 2, 10)
	require.NoError(t, err)
	// A behind read stops at the file/tail boundary; the next cursor read
	// continues from the in-memory tail.
	require.Equal(t, []Metric{{ID: 3, Data: []byte("three")}}, got)
	got, err = stream.Since(t.Context(), 3, 10)
	require.NoError(t, err)
	require.Equal(t, []Metric{{ID: 4, Data: []byte("four")}}, got)
	require.NoError(t, stream.close())
}

func TestLogStreamRejectsAppendAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.log")
	stream, err := openLogStream(t.Context(), path, metricCodec, telemetryTailBudget, nil, nil)
	require.NoError(t, err)
	require.NoError(t, stream.close())

	_, err = stream.Append([]Metric{{Data: []byte("late")}})
	require.ErrorIs(t, err, errStoreClosed)
}

func TestLogStreamHardCapBlocksUntilSpillDrains(t *testing.T) {
	stream, err := openLogStream(
		t.Context(),
		filepath.Join(t.TempDir(), "metrics.log"),
		metricCodec,
		64,
		nil,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, int64(1024), stream.hardCap)
	spillStarted, releaseSpill := blockSpillWrites(stream)

	_, err = stream.Append([]Metric{{Data: bytes.Repeat([]byte("a"), 900)}})
	require.NoError(t, err)
	select {
	case <-spillStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("spiller did not reach the write gate")
	}

	type appendOutcome struct {
		stats AppendStats
		err   error
	}
	appendDone := make(chan appendOutcome, 1)
	go func() {
		stats, err := stream.append([]Metric{{Data: bytes.Repeat([]byte("b"), 200)}})
		appendDone <- appendOutcome{stats: stats, err: err}
	}()
	require.Eventually(t, func() bool {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		return stream.capWaiters == 1
	}, 5*time.Second, time.Millisecond)
	select {
	case outcome := <-appendDone:
		t.Fatalf("Append returned before the spiller drained: %v", outcome.err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseSpill)
	select {
	case outcome := <-appendDone:
		require.NoError(t, outcome.err)
		require.Equal(t, int64(2), outcome.stats.LastID)
		require.Positive(t, outcome.stats.CapWaitDuration)
		require.Positive(t, outcome.stats.SpillLagRows)
		require.Positive(t, outcome.stats.SpillLagBytes)
	case <-time.After(5 * time.Second):
		t.Fatal("Append stayed blocked after the spiller drained")
	}
	require.NoError(t, stream.close())
}

func TestLogStreamHardCapWaiterUnblocksOnClose(t *testing.T) {
	stream, err := openLogStream(
		t.Context(),
		filepath.Join(t.TempDir(), "metrics.log"),
		metricCodec,
		64,
		nil,
		nil,
	)
	require.NoError(t, err)
	spillStarted, releaseSpill := blockSpillWrites(stream)

	_, err = stream.Append([]Metric{{Data: bytes.Repeat([]byte("a"), 900)}})
	require.NoError(t, err)
	select {
	case <-spillStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("spiller did not reach the write gate")
	}

	appendDone := make(chan error, 1)
	go func() {
		_, err := stream.Append([]Metric{{Data: bytes.Repeat([]byte("b"), 200)}})
		appendDone <- err
	}()
	require.Eventually(t, func() bool {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		return stream.capWaiters == 1
	}, 5*time.Second, time.Millisecond)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- stream.close()
	}()
	select {
	case err := <-appendDone:
		require.ErrorIs(t, err, errStoreClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("Append stayed blocked after stream close")
	}

	close(releaseSpill)
	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not finish after the spiller resumed")
	}
}

func blockSpillWrites(stream *logStream[Metric]) (<-chan struct{}, chan<- struct{}) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	stream.spill.testWriteHook = func(buf []byte) (int, error) {
		once.Do(func() { close(started) })
		<-release
		return stream.spill.file.Write(buf)
	}
	return started, release
}
