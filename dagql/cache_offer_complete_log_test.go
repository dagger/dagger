package dagql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// An offer on a part its row's record shows complete is obsolete. A read of
// the row's complete parts logs it once, naming the row, the part, whether
// the row's gate settled the part and how the offer came; a read served from
// what the last one read logs nothing more. An offer on a part still pending
// is logged by no read.
func TestOfferOnACompletePartIsLoggedByTheRead(t *testing.T) {
	ctx, c, srv := statePartCache(t, filepath.Join(t.TempDir(), "cache.db"))
	t.Cleanup(func() { require.NoError(t, c.Close(context.Background())) })
	logs := &offerLogs{}
	ctx = logs.into(ctx)
	eager := &statePartValue{}
	eager.done.Store(true)
	complete := persistedListTestResult(t, ctx, c, srv, "eager", eager)
	pending := persistedListTestResult(t, ctx, c, srv, "lazy", &statePartValue{lazy: true})
	offer := testLiveOffer()
	for _, r := range []AnyResult{complete, pending} {
		c.egraphMu.Lock()
		owner, err := c.newOfferOwnerLocked(ctx, offer.Owner)
		if err == nil {
			// As a merge's commit attaches one (applyMergedOffersLocked).
			err = c.testAttachPartOfferLocked(r.cacheSharedResult(), offer.Address, &partOffer{record: offer, owner: owner, came: offerCameByMerge})
		}
		c.egraphMu.Unlock()
		require.NoError(t, err)
	}
	key, err := partAddressKey(offer.Address)
	require.NoError(t, err)

	require.Equal(t, []string{key}, c.completePartKeys(ctx, complete.cacheSharedResult()))
	require.Empty(t, c.completePartKeys(ctx, pending.cacheSharedResult()))
	require.Equal(t, []string{key}, c.completePartKeys(ctx, complete.cacheSharedResult()), "the second read is served from the first")

	lines := logs.lines(t, "remote cache offer on a complete part")
	require.Len(t, lines, 1)
	require.EqualValues(t, complete.cacheSharedResult().id, lines[0].int(t, "entry"))
	require.Equal(t, key, lines[0].fields["part"])
	require.Equal(t, false, lines[0].fields["settled"], "the gate never saw the eager value's part")
	require.Equal(t, "merge", lines[0].fields["came"])
}
