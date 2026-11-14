package dagql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// A restore's part merges as a merge from the Cloud does: the same records
// land, and the reply says the same.
func TestRestoreValuesMergesAsMergeValues(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "root", "value")
	mctx, m, _ := transferTestCache(t)
	merged, err := m.MergeValues(mctx, cloudCacheID, bundle)
	require.NoError(t, err)
	rctx, r, _ := transferTestCache(t)
	restored, err := r.RestoreValues(rctx, bundle, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restored.Release(context.Background())) })
	require.True(t, restored.Committed)
	require.Equal(t, merged.Rows, restored.Rows)
	require.Len(t, restored.Imported(), len(merged.Imported()))
	require.Len(t, restored.Values, len(merged.Values))
	r.egraphMu.RLock()
	defer r.egraphMu.RUnlock()
	for _, value := range restored.Imported() {
		require.NotNil(t, r.resultsByID[sharedResultID(value.ResultID)], "the restored record has its entry")
	}
}
