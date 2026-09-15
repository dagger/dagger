package clientdb

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLogStreamSpillFailureRollsBackAndStopsStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.log")
	stream, err := openLogStream(t.Context(), path, metricCodec, 64, nil, nil)
	require.NoError(t, err)

	baseline := Metric{Data: bytes.Repeat([]byte("a"), 900)}
	last, err := stream.Append([]Metric{baseline})
	require.NoError(t, err)
	require.Equal(t, int64(1), last)
	require.Eventually(t, func() bool {
		stream.mu.Lock()
		tailEmpty := len(stream.tail) == 0 && stream.tailBase == 2
		stream.mu.Unlock()
		stream.spill.mu.RLock()
		committed := stream.spill.committedLastID == 1
		stream.spill.mu.RUnlock()
		return tailEmpty && committed
	}, 5*time.Second, time.Millisecond)

	stream.spill.mu.RLock()
	committedOffset := stream.spill.committed
	stream.spill.mu.RUnlock()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, committedOffset, info.Size())

	injectedErr := errors.New("injected spill write failure")
	writeStarted := make(chan struct{})
	failWrite := make(chan struct{})
	var startOnce sync.Once
	stream.spill.testWriteHook = func(buf []byte) (int, error) {
		startOnce.Do(func() { close(writeStarted) })
		<-failWrite
		partial := min(7, len(buf))
		written, err := stream.spill.file.Write(buf[:partial])
		if err != nil {
			return written, err
		}
		return written, injectedErr
	}

	failedSpillRow := Metric{Data: bytes.Repeat([]byte("b"), 900)}
	last, err = stream.Append([]Metric{failedSpillRow})
	require.NoError(t, err)
	require.Equal(t, int64(2), last)
	select {
	case <-writeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("spiller did not reach the injected write failure")
	}

	// This append cannot fit under the hard cap while row 2 is waiting to
	// spill. It must wake with the fatal I/O error, without publishing row 3.
	blockedAppend := make(chan error, 1)
	go func() {
		_, err := stream.Append([]Metric{{Data: bytes.Repeat([]byte("c"), 200)}})
		blockedAppend <- err
	}()
	require.Eventually(t, func() bool {
		stream.mu.Lock()
		defer stream.mu.Unlock()
		return stream.capWaiters == 1
	}, 5*time.Second, time.Millisecond)
	close(failWrite)
	select {
	case err := <-blockedAppend:
		require.ErrorIs(t, err, injectedErr)
	case <-time.After(5 * time.Second):
		t.Fatal("capacity waiter did not wake after the spill became fatal")
	}

	// Rollback removes the partially written frame and leaves the committed
	// prefix and sparse index unchanged.
	info, err = os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, committedOffset, info.Size())
	fileRows, err := stream.spill.readSince(t.Context(), 0, 10)
	require.NoError(t, err)
	require.Equal(t, []Metric{{ID: 1, Data: baseline.Data}}, fileRows)

	// The failed-to-spill row remains intact in the tail and readable. No torn
	// frame is exposed at the file/tail boundary.
	page, err := stream.Since(t.Context(), 0, 10)
	require.NoError(t, err)
	require.Equal(t, []Metric{{ID: 1, Data: baseline.Data}}, page)
	page, err = stream.Since(t.Context(), 1, 10)
	require.NoError(t, err)
	require.Equal(t, []Metric{{ID: 2, Data: failedSpillRow.Data}}, page)

	stream.mu.Lock()
	tailRows := len(stream.tail)
	tailBytes := stream.tailBytes
	nextID := stream.nextID
	stream.mu.Unlock()
	_, err = stream.Append([]Metric{{Data: []byte("must not grow")}})
	require.ErrorIs(t, err, injectedErr)
	stream.mu.Lock()
	require.Equal(t, tailRows, len(stream.tail))
	require.Equal(t, tailBytes, stream.tailBytes)
	require.Equal(t, nextID, stream.nextID)
	stream.mu.Unlock()

	closeDone := make(chan error, 1)
	go func() { closeDone <- stream.close() }()
	select {
	case err := <-closeDone:
		require.ErrorIs(t, err, injectedErr)
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after a fatal spill error")
	}
}
