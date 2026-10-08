package clientdb

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpillFileAppendReadAndRecover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.log")
	spill, err := openSpillFile(t.Context(), path, metricCodec, nil)
	require.NoError(t, err)

	want := make([]Metric, 600)
	for i := range want {
		want[i] = Metric{ID: int64(i + 1), Data: []byte{byte(i), byte(i >> 8)}}
	}
	require.NoError(t, spill.append(want))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, info.Size(), spill.committed)
	require.Len(t, spill.index, 3)
	require.Equal(t, int64(1), spill.index[0].id)
	require.Equal(t, int64(257), spill.index[1].id)
	require.Equal(t, int64(513), spill.index[2].id)

	got, err := spill.readSince(t.Context(), 250, 20)
	require.NoError(t, err)
	require.Equal(t, want[250:270], got)

	row, found, err := spill.readID(t.Context(), 513)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want[512], row)

	_, found, err = spill.readID(t.Context(), 601)
	require.NoError(t, err)
	require.False(t, found)

	require.NoError(t, spill.close())

	var recovered []Metric
	spill, err = openSpillFile(t.Context(), path, metricCodec, func(row Metric) {
		recovered = append(recovered, row)
	})
	require.NoError(t, err)
	require.Equal(t, want, recovered)
	require.Equal(t, int64(600), spill.lastID)
	require.Equal(t, int64(600), spill.committedLastID)
	require.Equal(t, int64(600), spill.rowCount)
	require.Len(t, spill.index, 3)
	require.NoError(t, spill.close())
}

// TestSpillFileReadIDs covers the batch reader a scoped load uses: one pass
// over ascending IDs that mixes runs within a stride, jumps across strides,
// IDs it already passed, and IDs the file does not hold -- with payloads of
// varying size, so a reused frame buffer that leaked into a decoded row
// would show.
func TestSpillFileReadIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.log")
	spill, err := openSpillFile(t.Context(), path, metricCodec, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, spill.close()) })

	// IDs are sparse (every other one) to exercise absent IDs between rows.
	rows := make([]Metric, 900)
	for i := range rows {
		data := make([]byte, 1+(i*37)%300)
		for j := range data {
			data[j] = byte(i + j)
		}
		rows[i] = Metric{ID: int64(2*i + 1), Data: data}
	}
	require.NoError(t, spill.append(rows))

	ids := []int64{-1, 0, 1, 2, 3, 5, 7, 300, 301, 303, 511, 513, 515, 1001, 1203, 1799, 1800, 1801}
	var got []Metric
	require.NoError(t, spill.readIDs(t.Context(), ids, func(row Metric) {
		got = append(got, row)
	}))
	var want []Metric
	for _, id := range ids {
		if id > 0 && id%2 == 1 && id <= 1799 {
			want = append(want, rows[(id-1)/2])
		}
	}
	require.Equal(t, want, got)

	// Every row, in one pass.
	all := make([]int64, len(rows))
	for i, row := range rows {
		all[i] = row.ID
	}
	got = got[:0]
	require.NoError(t, spill.readIDs(t.Context(), all, func(row Metric) {
		got = append(got, row)
	}))
	require.Equal(t, rows, got)

	for _, row := range []Metric{rows[0], rows[255], rows[256], rows[899]} {
		id, err := frameRowID(metricCodec.encode(row))
		require.NoError(t, err)
		require.Equal(t, row.ID, id)
	}
	spanID, err := frameRowID(spanCodec.encode(Span{ID: 1234, SpanID: "x"}))
	require.NoError(t, err)
	require.Equal(t, int64(1234), spanID)
	logID, err := frameRowID(logCodec.encode(Log{ID: 5678, Body: []byte("x")}))
	require.NoError(t, err)
	require.Equal(t, int64(5678), logID)
}

func TestSpillFileRecoversCompletePrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.log")
	spill, err := openSpillFile(t.Context(), path, metricCodec, nil)
	require.NoError(t, err)
	require.NoError(t, spill.append([]Metric{{ID: 1, Data: []byte("complete")}}))
	require.NoError(t, spill.close())

	completeInfo, err := os.Stat(path)
	require.NoError(t, err)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	var prefix [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(prefix[:], 100)
	_, err = file.Write(append(prefix[:n], []byte("partial")...))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	spill, err = openSpillFile(t.Context(), path, metricCodec, nil)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, completeInfo.Size(), info.Size())
	got, err := spill.readSince(t.Context(), 0, 10)
	require.NoError(t, err)
	require.Equal(t, []Metric{{ID: 1, Data: []byte("complete")}}, got)
	require.NoError(t, spill.close())
}

func TestSpillFileRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.log")
	require.NoError(t, os.WriteFile(path, []byte{storeFormatVersion + 1}, 0o600))

	_, err := openSpillFile(t.Context(), path, metricCodec, nil)
	require.ErrorContains(t, err, "unsupported telemetry store format version")
}

func TestSpillFileRecoveryHonorsContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.log")
	spill, err := openSpillFile(t.Context(), path, metricCodec, nil)
	require.NoError(t, err)
	rows := make([]Metric, sparseIndexStride)
	for i := range rows {
		rows[i].ID = int64(i + 1)
	}
	require.NoError(t, spill.append(rows))
	require.NoError(t, spill.close())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = openSpillFile(ctx, path, metricCodec, nil)
	require.ErrorIs(t, err, context.Canceled)
}
