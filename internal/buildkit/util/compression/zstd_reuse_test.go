package compression

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

// zstdLayerGolden is each layer's blob digest, by size, as the writer
// compressed layerBytes(size) before it kept its encoders and buffers
// between layers: keeping them changes no byte.
var zstdLayerGolden = map[int]string{
	0:                  "429ed24358dddc6eff5fcf1c9a514fb568dcba9e383d8c065353b0446064bc1f",
	1 << 10:            "fc53a504f42a867e73bf48579b7371654c5398415988ee38d8c702b66c49778c",
	64 << 10:           "d78084ed6419360801909dc2d272d9c13fdfc1dc5b085bfcd26050170856cc5b",
	200 << 10:          "62466b7df4906aa3778c20e7bbf8087fc7144d6aeba9779b0e15ee358cacd1d2",
	3 << 20:            "b5b5b0d425e112d6bd3852325e8c2ca02a432b07fdf2cde9255446889e36c599",
	zstdSizedLimit:     "6e2d8cb719c6ccecb1511cd36c7dd22836730af8d21eef67c4f08be64dffd8bd",
	zstdSizedLimit + 1: "c830dfc3b3bcde07d0ea447cbe73d195bb93b231b22c0bf680ad75851139dbb3",
	20 << 20:           "472758c46fe3e33d4009cc156202c240522dc42be8885d904e231e9844ff5f60",
	33 << 20:           "36ba2a82a6d522600d91935c9f403f0f2696e8e9709df2b6b59ec98c09630da9",
}

// zstdManyLayersGolden is the digest of the 300 blobs zstdManyLayers makes,
// before the writer kept anything between layers.
const zstdManyLayersGolden = "d84089d7213fdbffe6bc018f0028cec8c6cbc7abf570763240c430f0190afb95"

func digestOf(blob []byte) string {
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

// A layer's blob is the same byte for byte however many layers its writer's
// encoder and buffers compressed before it: each size, twice in a row and
// after every other.
func TestZstdLayerBlobsDontDependOnEarlierLayers(t *testing.T) {
	for round := range 2 {
		for size, want := range zstdLayerGolden {
			require.Equal(t, want, digestOf(compressLayer(t, layerBytes(size))), "size %d, round %d", size, round)
		}
	}
}

// zstdManyLayers compresses 300 layers in turn, mostly small, every fiftieth
// large enough to be frames, and returns the digest of their blobs' digests: enough layers for
// a kept encoder to run past its offsets' reset.
func zstdManyLayers(t *testing.T) string {
	t.Helper()
	h := sha256.New()
	for i := range 300 {
		size := 1<<10 + (i*7919)%(256<<10)
		if i%50 == 49 {
			size = 20<<20 + i
		}
		sum := sha256.Sum256(compressLayer(t, layerBytes(size)))
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestZstdManyLayersBlobsDontDependOnEarlierLayers(t *testing.T) {
	require.Equal(t, zstdManyLayersGolden, zstdManyLayers(t))
}

// An engine's overlay diff closes its compressed writer, then closes it
// again in a defer. The second Close writes nothing and gives the encoder
// back no second time: two layers compressed at once after it each get an
// encoder of their own, and every blob is the one it was.
func TestZstdLayerClosedTwice(t *testing.T) {
	comp := New(Zstd)
	compress, _ := comp.Type.Compress(context.Background(), comp)
	for _, size := range []int{64 << 10, 20 << 20} {
		data := layerBytes(size)
		var out bytes.Buffer
		w, err := compress(&out, "")
		require.NoError(t, err)
		_, err = w.Write(data)
		require.NoError(t, err)
		require.NoError(t, w.Close())
		require.NoError(t, w.Close())
		require.Equal(t, zstdLayerGolden[size], digestOf(out.Bytes()), "size %d", size)

		// Two layers at once, written in turns, so that a stream
		// sharing an encoder with the other would interleave.
		var outs [2]bytes.Buffer
		var ws [2]io.WriteCloser
		for i := range ws {
			ws[i], err = compress(&outs[i], "")
			require.NoError(t, err)
		}
		for rest := data; len(rest) > 0; {
			n := min(len(rest), 1<<20)
			for _, w := range ws {
				_, err := w.Write(rest[:n])
				require.NoError(t, err)
			}
			rest = rest[n:]
		}
		for i, w := range ws {
			require.NoError(t, w.Close())
			require.Equal(t, zstdLayerGolden[size], digestOf(outs[i].Bytes()), "size %d, layer %d at once", size, i)
		}
	}
}

// zstdLayerAllocated is the bytes compressing data allocates, on average
// over runs, its blob written to io.Discard.
func zstdLayerAllocated(t *testing.T, comp Config, data []byte, runs int) int64 {
	t.Helper()
	compress, _ := comp.Type.Compress(context.Background(), comp)
	layer := func() {
		w, err := compress(io.Discard, "")
		require.NoError(t, err)
		for rest := data; len(rest) > 0; {
			n := min(len(rest), 32<<10)
			_, err := w.Write(rest[:n])
			require.NoError(t, err)
			rest = rest[n:]
		}
		require.NoError(t, w.Close())
	}
	layer()
	// The collector frees what is kept idle; hold it off so the reading is
	// what a layer allocates while steady exports keep their encoders and
	// buffers in use, not when they ran.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		layer()
	}
	runtime.ReadMemStats(&after)
	return int64(after.TotalAlloc-before.TotalAlloc) / int64(runs)
}

// An engine exporting layers to the remote cache compresses thousands of
// them in a check. A layer's compression allocates a small part of its own
// size: the encoders, their history and the frames' buffers are kept from
// one layer to the next, not made again for each. Before they were, a
// 64 KB layer allocated 2.5 MB, a 3 MB layer 37 MB, and a 33 MB layer
// 260 MB. Under the race detector too, which CI runs this package with.
func TestZstdLayerAllocatesLittleOnceWarm(t *testing.T) {
	for _, c := range []struct {
		size, most int
	}{
		{64 << 10, 64 << 10},
		{3 << 20, 1 << 20},
		{33 << 20, 16 << 20},
	} {
		got := zstdLayerAllocated(t, New(Zstd), layerBytes(c.size), 20)
		require.Less(t, got, int64(c.most), "a %d-byte layer allocated %d bytes", c.size, got)
	}
}

// An encoder the pools keep between layers holds a history of the largest
// input it is given, a frame or a layer that fits in zstdSizedLimit, once:
// enough that it never has to move down to make room, not twice its window.
// At twice the window, 16 MB an encoder at the default level, a filling
// engine held 43 such histories, 688 MB, at the peak of a Go test check's
// burst of calls, with 16 frames compressing at once. The fastest level's
// window is half the input, so twice its window is what holds the input.
// Each kept encoder is measured against a new one as the library makes it,
// with a history of twice its window; their tables differ by level. An
// encoder sizes its history on its first block past the first, whatever the
// input's length, so a few blocks of input measure it, quickly under the
// race detector too.
func TestZstdKeptEncoderHoldsItsLargestInputOnce(t *testing.T) {
	data := layerBytes(256 << 10)
	frame := func(enc *zstd.Encoder, out []byte) { enc.EncodeAll(data, out) }
	sized := func(enc *zstd.Encoder, out []byte) {
		buf := bytes.NewBuffer(out)
		enc.ResetContentSize(buf, int64(len(data)))
		_, err := enc.Write(data)
		require.NoError(t, err)
		require.NoError(t, enc.Close())
	}
	// allocated is what a new encoder from get allocates to compress data.
	allocated := func(get func() *zstd.Encoder, compress func(*zstd.Encoder, []byte)) int64 {
		out := make([]byte, 0, 2*len(data))
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		compress(get(), out)
		runtime.ReadMemStats(&after)
		return int64(after.TotalAlloc - before.TotalAlloc)
	}
	for _, level := range []zstd.EncoderLevel{zstd.SpeedFastest, zstd.SpeedDefault, zstd.SpeedBetterCompression, zstd.SpeedBestCompression} {
		pools := newZstdEncoderPools(level)
		for _, c := range []struct {
			name        string
			pool        *zstdKept[zstd.Encoder]
			concurrency int
			compress    func(*zstd.Encoder, []byte)
		}{
			{"frame", pools.frames, 1, frame},
			{"layer with its size", pools.streams, 2, sized},
		} {
			kept := allocated(func() *zstd.Encoder {
				enc, err := pools.get(c.pool)
				require.NoError(t, err)
				t.Cleanup(func() { pools.put(c.pool, enc) })
				return enc
			}, c.compress)
			full := allocated(func() *zstd.Encoder {
				enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(c.concurrency))
				require.NoError(t, err)
				return enc
			}, c.compress)
			require.GreaterOrEqual(t, kept, int64(zstdSizedLimit), "a new %s %s encoder allocated %d bytes: its history can't hold a %d-byte input", level, c.name, kept, zstdSizedLimit)
			if level == zstd.SpeedFastest {
				require.InDelta(t, full, kept, 1<<20, "a new %s %s encoder allocated %d bytes, one with twice its window %d", level, c.name, kept, full)
			} else {
				require.Less(t, kept, full-6<<20, "a new %s %s encoder allocated %d bytes, one with twice its window %d", level, c.name, kept, full)
			}
		}
	}
}
