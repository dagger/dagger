package dagql

import (
	"context"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// unchangedRowsTestLand lands bundle on an engine cache as a set file's part
// is restored, and returns the reply, its hold released.
func unchangedRowsTestLand(t *testing.T, ctx context.Context, engine *Cache, bundle ValueBundle) MergeReply {
	t.Helper()
	checked, err := engine.CheckRestoreValues(bundle)
	require.NoError(t, err)
	reply, err := engine.RestoreCheckedValues(ctx, checked, nil, "test")
	require.NoError(t, err)
	require.True(t, reply.Committed)
	require.Empty(t, reply.Stale)
	if reply.Release != nil {
		require.NoError(t, reply.Release(ctx))
	}
	return reply
}

// When an engine lands a record of the Cloud's that it merged already, at
// the same stamp and with nothing changing its entry since, it doesn't teach
// the record again: the merge counts it unchanged. A record the Cloud changed
// since, or one from another start of the Cloud cache, or one whose entry
// the engine replaced, is taught as before.
func TestAnEngineDoesNotTeachAgainARecordItMergedUnchanged(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "unchanged")
	engineCtx, engine, _ := transferTestCache(t)
	bundle := unchangedRowsTestBuild(t, ctx, cloud, nil, r)

	first := unchangedRowsTestLand(t, engineCtx, engine, bundle)
	require.Equal(t, 2, first.Rows.Created)
	require.Zero(t, first.Rows.Unchanged, "nothing was merged before")

	again := unchangedRowsTestLand(t, engineCtx, engine, bundle)
	require.Equal(t, 2, again.Rows.Kept)
	require.Equal(t, 2, again.Rows.Unchanged, "the same records at the same stamps teach nothing again")

	other := bundle
	other.SenderGeneration++
	restarted := unchangedRowsTestLand(t, engineCtx, engine, other)
	require.Equal(t, 2, restarted.Rows.Kept)
	require.Zero(t, restarted.Rows.Unchanged, "stamps of another start of the Cloud cache compare with none of these")
	// That merge noted the other start's stamps: the first one's are taught
	// again, and noted once more.
	unchangedRowsTestLand(t, engineCtx, engine, bundle)

	stored, _, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	changed := unchangedRowsTestLand(t, engineCtx, engine, unchangedRowsTestBuild(t, ctx, cloud, nil, r))
	require.Equal(t, 2, changed.Rows.Kept)
	require.Equal(t, 1, changed.Rows.Unchanged, "the root's stamp moved; its dependency's didn't")

	var rEngine uint64
	for _, value := range changed.Values {
		for _, sent := range bundle.Values {
			if sent.Ordinal == value.Ordinal && sent.SenderNumber == r {
				rEngine = value.Number
			}
		}
	}
	require.NotZero(t, rEngine)
	mergeTestExpire(engine, storedBundleTestEntry(engine, rEngine))
	replaced := unchangedRowsTestLand(t, engineCtx, engine, unchangedRowsTestBuild(t, ctx, cloud, nil, r))
	require.Equal(t, 1, replaced.Rows.Unchanged, "the root's expired value is replaced; only its dependency is unchanged")
}

// Two sets' files can carry one Cloud entry at one stamp with different
// calls: each sends the calls its own check's runs made. An engine that lands
// both keeps the entry's record from the first, and from the second still
// learns the call only the second carried: each call hits afterwards.
func TestAnUnchangedRecordStillTeachesTheCallsItComesWith(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	a := newCallRecordTestOf(t, ctx, cloud, callRecordTestFrame("outer-a"))
	b := newCallRecordTestOf(t, ctx, cloud, callRecordTestFrame("outer-b"))
	require.Equal(t, a.number, b.number, "both calls return the one Cloud entry")
	a.attach(t, ctx, a.span(t, true, 0))
	_, err := cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-b", Number: uint64(b.inner.cacheSharedResult().id)}, b.span(t, true, 0))
	require.NoError(t, err)
	requestA, err := a.frame.deriveRecipeDigest(a.producer)
	require.NoError(t, err)
	requestB, err := b.frame.deriveRecipeDigest(b.producer)
	require.NoError(t, err)
	only := func(request digest.Digest) *HeldBy {
		return &HeldBy{Calls: func(r digest.Digest) bool { return r == request }}
	}
	fileA := unchangedRowsTestBuild(t, ctx, cloud, only(requestA), a.number)
	fileB := unchangedRowsTestBuild(t, ctx, cloud, only(requestB), a.number)
	require.Len(t, fileA.Calls, 1)
	require.Len(t, fileB.Calls, 1)
	require.Len(t, fileA.Values, 1)
	require.Len(t, fileB.Values, 1)
	require.Equal(t, fileA.Values[0].SenderStamp, fileB.Values[0].SenderStamp, "one entry at one stamp")

	engineCtx, engine, srv := transferTestCache(t)
	unchangedRowsTestLand(t, engineCtx, engine, fileA)
	second := unchangedRowsTestLand(t, engineCtx, engine, fileB)
	require.Equal(t, 1, second.Rows.Unchanged, "the second file's record is the one the engine merged")
	for _, frame := range []*ResultCall{a.frame, b.frame} {
		ran := false
		_, err := engine.GetOrInitCall(engineCtx, "test-session", srv,
			&CallRequest{ResultCall: frame.clone(), IsPersistable: true},
			func(context.Context) (AnyResult, error) {
				ran = true
				return NewResultForCall(&transferTestValue{Text: "ran again"}, frame)
			})
		require.NoError(t, err)
		require.False(t, ran, "%s hits", frame.Field)
	}
}
