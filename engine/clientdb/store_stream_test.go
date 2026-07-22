package clientdb

import (
	"bytes"
	"path/filepath"
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
