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
