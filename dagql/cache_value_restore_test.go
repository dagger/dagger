package dagql

import (
	"context"
	"testing"
	"time"

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
	restored, err := r.RestoreValues(rctx, bundle, nil, "")
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

// A restore checks a file's next part while the part before it merges
// (CheckRestoreValues), and merges it after that part has committed
// (RestoreCheckedValues), as RestoreValues would have merged it then: a held
// row naming the earlier part's record holds, since the rest of the
// preparation reads the cache as it stands when the part merges. A checked
// part merges once.
func TestRestoreCheckedValuesMergesAsRestoreValues(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	now := time.Now().Unix()
	first, err := cloud.StoredBundle(ctx, []uint64{p}, now, &HeldBy{Cache: "cache-fresh"})
	require.NoError(t, err)
	second, err := cloud.StoredBundle(ctx, []uint64{r}, now, &HeldBy{Cache: "cache-fresh", Also: func(number uint64) bool { return number == p }})
	require.NoError(t, err)
	require.Equal(t, map[uint64]bool{p: true}, heldByAlsoTestHeld(second.Bundle), "the second part holds the first's record")

	wctx, w, _ := transferTestCache(t)
	want1, err := w.RestoreValues(wctx, first.Bundle, nil, "")
	require.NoError(t, err)
	want2, err := w.RestoreValues(wctx, second.Bundle, nil, "")
	require.NoError(t, err)
	require.True(t, want2.Committed)

	gctx, g, _ := transferTestCache(t)
	checked, err := g.CheckRestoreValues(second.Bundle)
	require.NoError(t, err)
	require.Positive(t, checked.Took())
	got1, err := g.RestoreValues(gctx, first.Bundle, nil, "")
	require.NoError(t, err)
	got2, err := g.RestoreCheckedValues(gctx, checked, nil, "")
	require.NoError(t, err)
	require.True(t, got2.Committed)
	require.Empty(t, got2.Stale)
	require.Equal(t, want1.Rows, got1.Rows)
	require.Equal(t, want2.Rows, got2.Rows)
	require.Equal(t, 1, got2.Rows.Held, "the held row holds on the first part's record")
	require.Len(t, got2.Imported(), len(want2.Imported()))
	for _, reply := range []MergeReply{want1, want2, got1, got2} {
		require.NoError(t, reply.Release(context.Background()))
	}

	_, err = g.RestoreCheckedValues(gctx, checked, nil, "")
	require.ErrorContains(t, err, "merged already")
	octx, other, _ := transferTestCache(t)
	again, err := g.CheckRestoreValues(second.Bundle)
	require.NoError(t, err)
	_, err = other.RestoreCheckedValues(octx, again, nil, "")
	require.ErrorContains(t, err, "another cache")
}
