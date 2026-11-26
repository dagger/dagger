//go:build go1.27 && goexperiment.jsonv2

package dagql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// A restore decodes each stored row's header in place: it allocates what
// json.Unmarshal of the header does, and no copy of the header. Read
// through a decoder, it allocated a buffer of its own for every header on
// top, more than the header's own length. A field the header has no place
// for is refused, as it always was.
//
// Not parallel: it counts the process's allocation, which other tests
// running beside it would change.
func TestStoredRowHeaderIsDecodedInPlace(t *testing.T) {
	var h storedRowHeader
	h.Envelope.Version, h.Envelope.Kind = persistedResultEnvelopeVersion, persistedResultKindObject
	for i := range 20 {
		layers := map[string]int64{}
		for j := range 10 {
			layers[fmt.Sprintf("part-%d-layer-%d", i, j)] = int64(1000 * (j + 1))
		}
		h.Envelope.StoredParts = append(h.Envelope.StoredParts, storedPruneTestPart(fmt.Sprintf("part-%d", i), layers))
	}
	raw, err := json.Marshal(h)
	require.NoError(t, err)

	var got storedRowHeader
	require.NoError(t, decodeStoredRowHeader(raw, &got))
	require.Equal(t, h, got)

	const rounds = 100
	allocated := func(decode func(*storedRowHeader) error) uint64 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range rounds {
			var out storedRowHeader
			require.NoError(t, decode(&out))
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / rounds
	}
	unmarshal := allocated(func(out *storedRowHeader) error { return json.Unmarshal(raw, out) })
	decoded := allocated(func(out *storedRowHeader) error { return decodeStoredRowHeader(raw, out) })
	t.Logf("a %d-byte header: %d bytes allocated decoding it, %d by json.Unmarshal", len(raw), decoded, unmarshal)
	require.Less(t, decoded, unmarshal+uint64(len(raw))/2, "no copy of the header")

	extra := append(bytes.TrimSuffix(raw, []byte("}")), []byte(`,"unknown":1}`)...)
	require.Error(t, decodeStoredRowHeader(extra, &got), "a field the header has no place for")
}
