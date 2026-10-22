package dagql

import (
	"io"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// rangeTestBlob is size bytes that don't repeat with any short period, so a
// chunk placed at the wrong offset shows.
func rangeTestBlob(size int) string {
	rng := rand.New(rand.NewPCG(1, 2))
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(rng.Uint32())
	}
	return string(data)
}

// A blob of at least the parallel threshold is read through several ranged
// requests, one per chunk, and its bytes arrive in order.
func TestPartRangeReadsLargeBlob(t *testing.T) {
	const size = 40 << 20
	data := rangeTestBlob(size)
	transport, source, offer, descriptor := contentReaderFixture(t, data)
	reader, err := source.Provider(t.Context(), offer, nil).ReaderAt(t.Context(), descriptor)
	require.NoError(t, err)
	got, err := io.ReadAll(io.NewSectionReader(reader, 0, size))
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.True(t, string(got) == data, "the blob's bytes arrive whole and in order")
	require.Equal(t, 5, transport.total(), "one request per 8 MiB chunk")
	transport.mu.Lock()
	ranges := slices.Clone(transport.ranges)
	transport.mu.Unlock()
	slices.Sort(ranges)
	require.Equal(t, []string{
		"bytes=0-8388607",
		"bytes=16777216-25165823",
		"bytes=25165824-33554431",
		"bytes=33554432-41943039",
		"bytes=8388608-16777215",
	}, ranges)
}
