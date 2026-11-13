package compression

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/require"
)

// zstdLayerGolden is each layer's blob digest, by framing and size, as the
// writer compressed layerBytes(size) before it kept its encoders and
// buffers between layers: keeping them changes no byte.
var zstdLayerGolden = map[bool]map[int]string{
	false: {
		0:                  "429ed24358dddc6eff5fcf1c9a514fb568dcba9e383d8c065353b0446064bc1f",
		1 << 10:            "fc53a504f42a867e73bf48579b7371654c5398415988ee38d8c702b66c49778c",
		64 << 10:           "d78084ed6419360801909dc2d272d9c13fdfc1dc5b085bfcd26050170856cc5b",
		200 << 10:          "62466b7df4906aa3778c20e7bbf8087fc7144d6aeba9779b0e15ee358cacd1d2",
		3 << 20:            "b5b5b0d425e112d6bd3852325e8c2ca02a432b07fdf2cde9255446889e36c599",
		zstdSizedLimit:     "6e2d8cb719c6ccecb1511cd36c7dd22836730af8d21eef67c4f08be64dffd8bd",
		zstdSizedLimit + 1: "7cabcacf9ed046bdedc03d32d15a468600c0cf67d4e42760191bd0fe92eda8ed",
		20 << 20:           "45f66a1015339d9c756a6effcb59551741be1485e77af196c0ac209992c34110",
		33 << 20:           "2ef0774f7c02e45a6d77a855bb7e8511b454d334212dec4a824effbef7229a2b",
	},
	true: {
		0:                  "429ed24358dddc6eff5fcf1c9a514fb568dcba9e383d8c065353b0446064bc1f",
		1 << 10:            "fc53a504f42a867e73bf48579b7371654c5398415988ee38d8c702b66c49778c",
		64 << 10:           "d78084ed6419360801909dc2d272d9c13fdfc1dc5b085bfcd26050170856cc5b",
		200 << 10:          "62466b7df4906aa3778c20e7bbf8087fc7144d6aeba9779b0e15ee358cacd1d2",
		3 << 20:            "b5b5b0d425e112d6bd3852325e8c2ca02a432b07fdf2cde9255446889e36c599",
		zstdSizedLimit:     "6e2d8cb719c6ccecb1511cd36c7dd22836730af8d21eef67c4f08be64dffd8bd",
		zstdSizedLimit + 1: "c830dfc3b3bcde07d0ea447cbe73d195bb93b231b22c0bf680ad75851139dbb3",
		20 << 20:           "472758c46fe3e33d4009cc156202c240522dc42be8885d904e231e9844ff5f60",
		33 << 20:           "36ba2a82a6d522600d91935c9f403f0f2696e8e9709df2b6b59ec98c09630da9",
	},
}

// zstdManyLayersGolden is the digest of the 300 blobs zstdManyLayers makes,
// by framing, before the writer kept anything between layers.
var zstdManyLayersGolden = map[bool]string{
	false: "f7303eef83c99c27d539830bc8c2dbc4582a553026244c373d04a27ef53709c1",
	true:  "d84089d7213fdbffe6bc018f0028cec8c6cbc7abf570763240c430f0190afb95",
}

func digestOf(blob []byte) string {
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

// A layer's blob is the same byte for byte however many layers its writer's
// encoder and buffers compressed before it: each size, twice in a row and
// after every other.
func TestZstdLayerBlobsDontDependOnEarlierLayers(t *testing.T) {
	for _, comp := range []Config{New(Zstd), New(Zstd).SetFramed(true)} {
		for round := range 2 {
			for size, want := range zstdLayerGolden[comp.Framed] {
				require.Equal(t, want, digestOf(compressLayerAs(t, comp, layerBytes(size))), "framed=%v size %d, round %d", comp.Framed, size, round)
			}
		}
	}
}

// zstdManyLayers compresses 300 layers in turn, mostly small, every fiftieth
// framed, and returns the digest of their blobs' digests: enough layers for
// a kept encoder to run past its offsets' reset.
func zstdManyLayers(t *testing.T, comp Config) string {
	t.Helper()
	h := sha256.New()
	for i := range 300 {
		size := 1<<10 + (i*7919)%(256<<10)
		if i%50 == 49 {
			size = 20<<20 + i
		}
		sum := sha256.Sum256(compressLayerAs(t, comp, layerBytes(size)))
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestZstdManyLayersBlobsDontDependOnEarlierLayers(t *testing.T) {
	for _, comp := range []Config{New(Zstd), New(Zstd).SetFramed(true)} {
		require.Equal(t, zstdManyLayersGolden[comp.Framed], zstdManyLayers(t, comp), "framed=%v", comp.Framed)
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
	// The collector empties sync.Pools; hold it off so the reading is what a
	// layer allocates while a fill keeps the pools in use, not when they ran.
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
// one layer to the next, not made again for each.
func TestZstdLayerAllocatesLittleOnceWarm(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector drops sync.Pool items at random")
	}
	for _, comp := range []Config{New(Zstd), New(Zstd).SetFramed(true)} {
		for _, c := range []struct {
			size, most int
		}{
			{64 << 10, 512 << 10},
			{3 << 20, 4 << 20},
			{33 << 20, 32 << 20},
		} {
			got := zstdLayerAllocated(t, comp, layerBytes(c.size), 20)
			require.Less(t, got, int64(c.most), "framed=%v: a %d-byte layer allocated %d bytes", comp.Framed, c.size, got)
		}
	}
}
