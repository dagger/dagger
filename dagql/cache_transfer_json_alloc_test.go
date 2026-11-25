//go:build go1.27 && goexperiment.jsonv2

package dagql

import (
	"bytes"
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
// bundle of such values, into a buffer an earlier encoding grew, allocated
// more than the values' envelopes and frames came to decompressed.
func TestStoredValueIsWrittenInPlace(t *testing.T) {
	bundle := storedBodyTestExport(t)
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
	for _, v := range stored.Values {
		body := &storedBody{envelope: v.storedEnvelope, frame: v.storedCall, frameBytes: v.storedCallBytes}
		envelope, err := body.envelopeJSON()
		require.NoError(t, err)
		bodies += len(envelope)
		if v.storedCall != nil {
			frame, err := body.frameJSON()
			require.NoError(t, err)
			bodies += len(frame)
		}
	}

	var buf bytes.Buffer
	encode := func() {
		buf.Reset()
		if err := jsonv2.MarshalWrite(&buf, stored, json.DefaultOptionsV1()); err != nil {
			t.Fatal(err)
		}
	}
	encode()
	const runs = 5
	var total uint64
	for range runs {
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		encode()
		runtime.ReadMemStats(&after)
		total += after.TotalAlloc - before.TotalAlloc
	}
	allocated := float64(total) / runs
	t.Logf("%.0f bytes allocated encoding %d values whose envelopes and frames are %d bytes decompressed, %d bytes of JSON in all", allocated, len(stored.Values), bodies, buf.Len())
	require.Less(t, allocated, 0.5*float64(bodies))
}
