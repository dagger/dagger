package compression

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

// layerBytes is n bytes that compress like a layer: random runs between
// repeated text.
func layerBytes(n int) []byte {
	rng := rand.New(rand.NewSource(int64(n)))
	var b bytes.Buffer
	for b.Len() < n {
		if rng.Intn(2) == 0 {
			b.WriteString("packagefile fmt=/work/lib/p0123456789ab.a\n")
		} else {
			run := make([]byte, 64+rng.Intn(512))
			rng.Read(run)
			b.Write(run)
		}
	}
	return b.Bytes()[:n]
}

// compressLayer compresses data as an exported layer is, writing it in
// pieces as a diff does.
func compressLayer(t *testing.T, data []byte) []byte {
	t.Helper()
	compress, _ := New(Zstd).Type.Compress(context.Background(), New(Zstd))
	var out bytes.Buffer
	w, err := compress(&out, "")
	require.NoError(t, err)
	for rest := data; len(rest) > 0; {
		n := min(len(rest), 32<<10)
		_, err := w.Write(rest[:n])
		require.NoError(t, err)
		rest = rest[n:]
	}
	require.NoError(t, w.Close())
	return out.Bytes()
}

func decompressLayer(t *testing.T, blob []byte) []byte {
	t.Helper()
	dec, err := zstd.NewReader(bytes.NewReader(blob))
	require.NoError(t, err)
	defer dec.Close()
	data, err := io.ReadAll(dec)
	require.NoError(t, err)
	return data
}

func TestZstdLayerNamesTheSizeOfALayerThatFitsTheWindow(t *testing.T) {
	// A layer that fits in one 128 KB block was already written with its
	// size by the encoder itself.
	for _, size := range []int{200 << 10, 3 << 20, zstdSizedLimit} {
		data := layerBytes(size)
		blob := compressLayer(t, data)

		var h zstd.Header
		require.NoError(t, h.Decode(blob))
		require.True(t, h.HasFCS, "size %d", size)
		require.EqualValues(t, size, h.FrameContentSize)
		// The decoder reserves from the window: it fits the layer, not
		// zstd's usual 8 MB.
		if !h.SingleSegment {
			require.Less(t, h.WindowSize, uint64(2*size+1024), "size %d", size)
		}
		require.Equal(t, data, decompressLayer(t, blob))
	}
}

func TestZstdLayerLargerThanTheWindowStillRoundTrips(t *testing.T) {
	data := layerBytes(zstdSizedLimit + 1<<20)
	blob := compressLayer(t, data)

	var h zstd.Header
	require.NoError(t, h.Decode(blob))
	require.False(t, h.HasFCS)
	require.Equal(t, data, decompressLayer(t, blob))
}

func TestZstdLayerOfNothingIsWhatTheEncoderWrote(t *testing.T) {
	var want bytes.Buffer
	enc, err := zstd.NewWriter(&want)
	require.NoError(t, err)
	require.NoError(t, enc.Close())

	require.Equal(t, want.Bytes(), compressLayer(t, nil))
}
