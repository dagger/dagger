package dagql

import (
	"path/filepath"
	"runtime"
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

// storedBodyTestBytes are a stored body's compressed envelope and frame.
func storedBodyTestBytes(b *storedBody) ([]byte, []byte) {
	return b.envelope, b.frame
}

// A stored body holds its record's compressed envelope and frame, and the
// entries its references name, in little beside their bytes: once a Cloud's
// 1,280 stored entries' records are compressed, letting go of their bodies
// but for their compressed bytes frees under 128 bytes an entry, what a
// body's struct alone took when it held them in two allocations and its
// references as eight bytes each. The Cloud is closed without its bodies
// after, and saves nothing.
//
// Not parallel: it measures the process's live heap, which other tests
// running beside it would change.
func TestStoredBodiesHoldLittleBesideTheirBytes(t *testing.T) {
	_, cloud, _ := storedBenchCloud(t)
	storedLightEnvelopes(t, cloud)
	cloud.egraphMu.Lock()
	defer cloud.egraphMu.Unlock()
	var held [][]byte
	var entries []*sharedResult
	for _, res := range cloud.resultsByID {
		if res.stored == nil {
			continue
		}
		envelope, frame := storedBodyTestBytes(res.stored)
		require.NotEmpty(t, frame, "entry %d's body holds its frame", res.id)
		held = append(held, envelope, frame)
		entries = append(entries, res)
	}
	before := storedLayersTestLiveHeap()
	for _, res := range entries {
		res.stored = nil
	}
	freed := before - storedLayersTestLiveHeap()
	runtime.KeepAlive(held)
	perEntry := float64(freed) / float64(len(entries))
	t.Logf("letting go of %d bodies but for their compressed bytes freed %d bytes, %.0f an entry", len(entries), freed, perEntry)
	require.Less(t, perEntry, 128.0, "each body took %.0f bytes beside its compressed bytes", perEntry)
}
