package dagql

import (
	"context"
	"testing"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// HoldEntry refuses a number the cache doesn't have, and an entry known only
// through other caches' holdings, which has no value.
func TestHoldEntryRefusesWhatHasNoValue(t *testing.T) {
	t.Parallel()
	ctx, c, _ := transferTestCache(t)
	_, _, err := c.HoldEntry(ctx, 12345)
	require.ErrorIs(t, err, ErrUnknownEntry)

	cloud := newCloudCache(t)
	_, err = cloud.AttachRemoteHolding(t.Context(), HolderKey{Cache: "cache-a", Number: 1}, holdingOf("known-only-through-holdings"))
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	number := cloud.holderEntries[HolderKey{Cache: "cache-a", Number: 1}]
	cloud.egraphMu.RUnlock()
	require.NotZero(t, number)
	_, _, err = cloud.HoldEntry(t.Context(), uint64(number))
	require.ErrorIs(t, err, errEntryHasNoValue)
}

// A held entry outlives every session that owned it, and is collected when
// the hold is released. The held result is an export root and an offer
// receiver as it is.
func TestHoldEntryHoldsWithoutASession(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	res := persistedListTestResult(t, ctx, c, srv, "held", &transferTestValue{Text: "pending"})
	number := uint64(res.cacheSharedResult().id)
	held, release, err := c.HoldEntry(ctx, number)
	require.NoError(t, err)
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	_, err = c.Prune(ctx, []CachePrunePolicy{{All: true}})
	require.NoError(t, err)
	c.egraphMu.RLock()
	_, kept := c.resultsByID[sharedResultID(number)]
	c.egraphMu.RUnlock()
	require.True(t, kept, "the hold keeps the entry")

	out, err := c.testOfferParts(ctx, held, []PersistedPartOffer{testLiveOffer()})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{held}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		require.Len(t, values.Bundle.Values, 1)
		require.Equal(t, number, values.Bundle.Values[0].SenderNumber)
		return nil
	}))

	require.NoError(t, release(ctx))
	require.NoError(t, release(ctx), "releasing twice is harmless")
	c.egraphMu.RLock()
	_, kept = c.resultsByID[sharedResultID(number)]
	c.egraphMu.RUnlock()
	require.False(t, kept, "released, and nothing else owns it")
}

// A cached nil result is held, and its export is a null record.
func TestHoldEntryHoldsANilResult(t *testing.T) {
	t.Parallel()
	ctx, c, _ := transferTestCache(t)
	frame := &ResultCall{Kind: ResultCallKindField, Field: "held-nothing", Type: NewResultCallType((&transferTestValue{}).Type())}
	frame.Type.NonNull = false
	res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) { return nil, nil })
	require.NoError(t, err)
	held, release, err := c.HoldEntry(ctx, uint64(res.cacheSharedResult().id))
	require.NoError(t, err)
	defer func() { require.NoError(t, release(ctx)) }()
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{held}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		require.Len(t, values.Bundle.Values, 1)
		require.Equal(t, persistedResultKindNull, values.Bundle.Values[0].Record.Envelope.Kind, "a null record")
		return nil
	}))
}

// The hold is a use: a new publication that finds a held entry's value
// expired retires the entry rather than replacing its value in place, so an
// export or offer under the hold keeps reading the one value.
func TestHoldEntryIsAUse(t *testing.T) {
	t.Parallel()
	ctx, c, srv := transferTestCache(t)
	res := persistedListTestResult(t, ctx, c, srv, "held-expired", &transferTestValue{Text: "old"})
	row := res.cacheSharedResult()
	_, release, err := c.HoldEntry(ctx, uint64(row.id))
	require.NoError(t, err)
	defer func() { require.NoError(t, release(ctx)) }()
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	mergeTestExpire(c, row)

	frame := row.loadResultCall().clone()
	fresh, err := c.GetOrInitCall(ctx, "fresh-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: "new"}, frame)
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, c.ReleaseSession(ctx, "fresh-session")) }()
	require.NotEqual(t, row.id, fresh.cacheSharedResult().id, "retired, not replaced in place")
	c.egraphMu.RLock()
	replacements := row.replacements
	c.egraphMu.RUnlock()
	require.Zero(t, replacements)
}
