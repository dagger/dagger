package dagql

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// storedLightEnvelopes waits until the body worker has compressed every
// record the Cloud stores, and returns how many entries hold each light
// envelope.
func storedLightEnvelopes(t *testing.T, cloud *Cache) map[*PersistedResultEnvelope]int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		lights, waiting := map[*PersistedResultEnvelope]int{}, 0
		cloud.egraphMu.RLock()
		for _, res := range cloud.resultsByID {
			switch {
			case res.persistedEnvelope == nil:
			case res.stored == nil:
				waiting++
			default:
				lights[res.persistedEnvelope]++
			}
		}
		cloud.egraphMu.RUnlock()
		if waiting == 0 {
			return lights
		}
		require.False(t, time.Now().After(deadline), "%d records were not compressed", waiting)
		time.Sleep(10 * time.Millisecond)
	}
}

// The Cloud's stored entries of one kind of value share one light envelope,
// the few fields that tell a stored value by its presence and its kind: a
// store of 1,280 entries of one type holds one, not one each. Each entry's
// saved record still names the entry it is.
func TestStoredEntriesShareTheirLightEnvelopes(t *testing.T) {
	t.Parallel()
	ctx, cloud, _ := storedBenchCloudAt(t, filepath.Join(t.TempDir(), "cloud.db"))
	lights := storedLightEnvelopes(t, cloud)
	entries := 0
	for _, n := range lights {
		entries += n
	}
	require.Equal(t, storedBenchChain+storedBenchRoots*(storedBenchOwn+1), entries)
	require.Equal(t, 1, len(lights), "%d stored entries of one type hold %d light envelopes", entries, len(lights))

	require.NoError(t, cloud.persistCurrentState(ctx))
	rows, err := cloud.pdb.ListMirrorResults(ctx)
	require.NoError(t, err)
	saved := 0
	for _, row := range rows {
		if !isStoredRow(row.SelfPayload) {
			continue
		}
		env, _, _, err := decodeStoredRow(row.SelfPayload, nil)
		require.NoError(t, err)
		require.Equal(t, uint64(row.ID), env.ResultID, "the saved record of entry %d names its entry", row.ID)
		saved++
	}
	require.Equal(t, entries, saved)
}
