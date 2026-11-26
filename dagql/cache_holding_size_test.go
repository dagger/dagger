package dagql

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// A holding holds little beside its sets: once a second engine cache holds,
// in one session, each of 20,000 entries another engine cache holds,
// ending that session and collecting its holdings frees under 257 bytes a
// holding, its slot in its cache's index and its session's included. A
// holding with all its fields in place took 208 bytes alone, beside them.
//
// Not parallel: it measures the process's live heap, which other tests
// running beside it would change.
func TestRemoteHoldingsHoldLittleBesideTheirSets(t *testing.T) {
	cloud := newCloudCache(t)
	a := newCloudApplier(t, cloud)
	const entries = 20_000
	for i := range entries {
		a.call(callRow{
			key:      HolderKey{Cache: "engine-a", Number: uint64(i + 1)},
			holding:  holdingOf("held-" + strconv.Itoa(i)),
			retained: retainedAt(1, int64(i+1), 0),
		})
	}
	require.Empty(t, a.collect())
	for i := range entries {
		a.call(callRow{
			key:     HolderKey{Cache: "engine-b", Number: uint64(i + 1)},
			session: "session-b",
			holding: holdingOf("held-" + strconv.Itoa(i)),
		})
	}
	require.Empty(t, a.collect())
	before := storedLayersTestLiveHeap()
	a.endSession("engine-b", "session-b")
	require.Equal(t, entries, len(a.collect()))
	freed := before - storedLayersTestLiveHeap()
	perHolding := float64(freed) / entries
	t.Logf("collecting %d holdings freed %d bytes, %.0f a holding", entries, freed, perHolding)
	require.Less(t, perHolding, 257.0, "each holding took %.0f bytes", perHolding)
}
