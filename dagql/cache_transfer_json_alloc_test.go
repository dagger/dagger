//go:build go1.27 && goexperiment.jsonv2

package dagql

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A value a blob-backed cache holds compressed is written into its holder's
// encoder in place: its envelope and call frame are decompressed into the
// encoder's spare buffer, not each into a buffer of its own. Encoding a
// bundle of such values allocated more than the values' envelopes and
// frames came to decompressed.
//
// Not parallel: it counts the process's allocation, which other tests
// running beside it would change.
func TestStoredValueIsWrittenInPlace(t *testing.T) {
	bundle := storedBodyTestExportOf(t, 100)
	ctx := cacheTestContext(context.Background())
	cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	roots := storedPersistenceTestMerge(t, ctx, cloud, bundle)
	// The Cloud compresses a record within seconds of its first use.
	var stored ValueBundle
	require.Eventually(t, func() bool {
		stored = storedBodyTestUse(t, ctx, cloud, roots)
		return !slices.ContainsFunc(stored.Values, func(v TransferredValue) bool { return v.storedEnvelope == nil })
	}, time.Minute, 100*time.Millisecond, "every record held compressed")
	var bodies int
	var largest int64
	for _, v := range stored.Values {
		body := &storedBody{envelope: v.storedEnvelope, frame: v.storedCall, frameBytes: v.storedCallBytes}
		envelope, err := body.envelopeJSON()
		require.NoError(t, err)
		bodies += len(envelope)
		largest = max(largest, int64(len(envelope)))
		if v.storedCall != nil {
			frame, err := body.frameJSON()
			require.NoError(t, err)
			bodies += len(frame)
			largest = max(largest, int64(len(frame)))
		}
	}

	// What the decoder allocates of its own decompressing the same bodies,
	// each into one buffer that holds it: nothing, but under the race
	// detector, which empties at random the sync.Pool the decoder keeps its
	// tables in. The encoding decompresses each body once too, and is
	// counted less the decoder's own.
	_, dec, err := storedBodyCodec()
	require.NoError(t, err)
	scratch := make([]byte, 0, largest)
	decompress := func() {
		for _, v := range stored.Values {
			for _, compressed := range [][]byte{v.storedEnvelope, v.storedCall} {
				if compressed == nil {
					continue
				}
				if scratch, err = dec.DecodeAll(compressed, scratch[:0]); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	// Encoded to a writer that keeps nothing: what is counted is the
	// encoding's, not a destination's growth.
	var written countingWriter
	encode := func() {
		written = 0
		if err := jsonv2.MarshalWrite(&written, stored, json.DefaultOptionsV1()); err != nil {
			t.Fatal(err)
		}
	}
	encode()
	decompress()
	const runs = 5
	allocatedBy := func(f func()) float64 {
		var total uint64
		for range runs {
			runtime.GC()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			f()
			runtime.ReadMemStats(&after)
			total += after.TotalAlloc - before.TotalAlloc
		}
		return float64(total) / runs
	}
	decoder := allocatedBy(decompress)
	allocated := allocatedBy(encode) - decoder
	t.Logf("%.0f bytes allocated encoding %d values whose envelopes and frames are %d bytes decompressed, %d bytes of JSON in all, less %.0f the decoder allocates of its own", allocated, len(stored.Values), bodies, written, decoder)
	require.Less(t, allocated, 0.5*float64(bodies))
}

// countingWriter counts what is written to it, and keeps none of it.
type countingWriter int

func (w *countingWriter) Write(p []byte) (int, error) {
	*w += countingWriter(len(p))
	return len(p), nil
}
