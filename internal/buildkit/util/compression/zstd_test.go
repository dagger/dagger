package compression

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"math/rand"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/util/compressutil"
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
	return compressLayerAs(t, New(Zstd), data)
}

// compressLayerAs is compressLayer with the compression comp.
func compressLayerAs(t *testing.T, comp Config, data []byte) []byte {
	t.Helper()
	compress, _ := comp.Type.Compress(context.Background(), comp)
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

// A framed writer cuts a layer larger than the window into frames of
// zstdFrameSize, the last one shorter, each compressed on its own as
// EncodeAll compresses it: its blob depends on its bytes alone, never on
// the machine's cores or on how the frames were scheduled, and every frame
// names its size. Run at several -cpu values, it shows the same bytes at
// each. A layer that fits the window is what an unframed writer writes.
func TestZstdFramedLayerLargerThanTheWindowIsFramesOfAFixedSize(t *testing.T) {
	framed := New(Zstd).SetFramed(true)
	small := layerBytes(zstdSizedLimit)
	require.Equal(t, compressLayer(t, small), compressLayerAs(t, framed, small))

	data := layerBytes(3*zstdFrameSize + 123)
	blob := compressLayerAs(t, framed, data)

	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	require.NoError(t, err)
	var want []byte
	for rest := data; len(rest) > 0; {
		n := min(len(rest), zstdFrameSize)
		want = enc.EncodeAll(rest[:n], want)
		rest = rest[n:]
	}
	require.Equal(t, want, blob)

	var h zstd.Header
	require.NoError(t, h.Decode(blob))
	require.True(t, h.HasFCS)
	require.EqualValues(t, zstdFrameSize, h.FrameContentSize)
	require.Equal(t, data, decompressLayer(t, blob))
	// The engine's layer imports read it as they read any zstd layer.
	r, err := compressutil.DecompressStream(bytes.NewReader(blob))
	require.NoError(t, err)
	imported, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, data, imported)
}

// Framing is zstd's alone: a gzip layer larger than zstd's window is one
// gzip stream, as the gzip package writes it, and an uncompressed one is its
// bytes.
func TestGzipAndUncompressedLayersAreNotFramed(t *testing.T) {
	for _, size := range []int{zstdSizedLimit + 1, 3*zstdFrameSize + 123} {
		data := layerBytes(size)
		var want bytes.Buffer
		zw, err := gzip.NewWriterLevel(&want, gzip.DefaultCompression)
		require.NoError(t, err)
		_, err = zw.Write(data)
		require.NoError(t, err)
		require.NoError(t, zw.Close())
		require.Equal(t, want.Bytes(), compressLayerAs(t, New(Gzip), data), "gzip, size %d", size)
		require.Equal(t, data, compressLayerAs(t, New(Uncompressed), data), "uncompressed, size %d", size)
	}
}

func BenchmarkZstdLayer(b *testing.B) {
	for _, comp := range []Config{New(Zstd), New(Zstd).SetFramed(true)} {
		b.Run(fmt.Sprintf("framed=%v", comp.Framed), func(b *testing.B) { benchmarkZstdLayer(b, comp) })
	}
}

func benchmarkZstdLayer(b *testing.B, comp Config) {
	data := layerBytes(256 << 20)
	compress, _ := comp.Type.Compress(context.Background(), comp)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		w, err := compress(io.Discard, "")
		if err != nil {
			b.Fatal(err)
		}
		for rest := data; len(rest) > 0; {
			n := min(len(rest), 32<<10)
			if _, err := w.Write(rest[:n]); err != nil {
				b.Fatal(err)
			}
			rest = rest[n:]
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func TestZstdLayerOfNothingIsWhatTheEncoderWrote(t *testing.T) {
	var want bytes.Buffer
	enc, err := zstd.NewWriter(&want)
	require.NoError(t, err)
	require.NoError(t, enc.Close())

	require.Equal(t, want.Bytes(), compressLayer(t, nil))
}
