package dagql

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// unchangedRowsTestSource exports a root and its dependency from an engine,
// for a Cloud to merge: the bundle, and the two's numbers in it.
func unchangedRowsTestSource(t *testing.T, text string) (ValueBundle, uint64, uint64) {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	r := persistedListTestResult(t, actx, a, asrv, "bundle-root", &transferTestValue{Text: text})
	p := persistedListTestResult(t, actx, a, asrv, "bundle-dep", &transferTestValue{Text: "dep"})
	transferTestDependency(a, actx, r, p)
	return exportTestBundle(t, actx, a, r), uint64(r.cacheSharedResult().id), uint64(p.cacheSharedResult().id)
}

// unchangedRowsTestCloudOf is a new Cloud cache with a database, so that it
// has an identity its bundles name, that merged the source: its numbers of
// the source's root and dependency.
func unchangedRowsTestCloudOf(t *testing.T, bundle ValueBundle, root, dep uint64) (context.Context, *Cache, uint64, uint64) {
	t.Helper()
	ctx, cloud := storedPersistenceTestCloud(t, filepath.Join(t.TempDir(), "cloud.db"))
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	var r, p uint64
	for _, value := range reply.Values {
		for _, sent := range bundle.Values {
			if sent.Ordinal == value.Ordinal && sent.SenderNumber == root {
				r = value.Number
			}
			if sent.Ordinal == value.Ordinal && sent.SenderNumber == dep {
				p = value.Number
			}
		}
	}
	require.NotZero(t, r)
	require.NotZero(t, p)
	return ctx, cloud, r, p
}

// unchangedRowsTestCloud is a Cloud cache with an identity that holds a root
// and its dependency (unchangedRowsTestCloudOf).
func unchangedRowsTestCloud(t *testing.T, text string) (context.Context, *Cache, uint64, uint64) {
	t.Helper()
	bundle, root, dep := unchangedRowsTestSource(t, text)
	return unchangedRowsTestCloudOf(t, bundle, root, dep)
}

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

// When an engine lands a record of the Cloud's that it merged already, from
// the same start of the same Cloud cache at the same stamp, and with nothing
// changing its entry since, it doesn't teach the record again: the merge
// counts it unchanged. A record the Cloud changed since, one from another
// cache or another start of it, or one whose entry the engine replaced, is
// taught as before.
func TestAnEngineDoesNotTeachAgainARecordItMergedUnchanged(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := unchangedRowsTestCloud(t, "unchanged")
	engineCtx, engine, _ := transferTestCache(t)
	bundle := unchangedRowsTestBuild(t, ctx, cloud, nil, r)

	first := unchangedRowsTestLand(t, engineCtx, engine, bundle)
	require.Equal(t, 2, first.Rows.Created)
	require.Zero(t, first.Rows.Unchanged, "nothing was merged before")

	again := unchangedRowsTestLand(t, engineCtx, engine, bundle)
	require.Equal(t, 2, again.Rows.Kept)
	require.Equal(t, 2, again.Rows.Unchanged, "the same records at the same stamps teach nothing again")

	identity := cloud.Identity()
	require.Equal(t, identity.String(), bundle.SenderCache)
	identity.Generation++
	other := bundle
	other.SenderCache = identity.String()
	restarted := unchangedRowsTestLand(t, engineCtx, engine, other)
	require.Equal(t, 2, restarted.Rows.Kept)
	require.Zero(t, restarted.Rows.Unchanged, "stamps of another start of the Cloud cache compare with none of these")
	// That merge noted the other start's stamps: the first one's are taught
	// again, and noted once more.
	unchangedRowsTestLand(t, engineCtx, engine, bundle)
	nameless := bundle
	nameless.SenderCache = ""
	require.Zero(t, unchangedRowsTestLand(t, engineCtx, engine, nameless).Rows.Unchanged, "stamps of a cache with no identity compare with none")
	require.Zero(t, unchangedRowsTestLand(t, engineCtx, engine, bundle).Rows.Unchanged, "nor are they noted")

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
	ctx, cloud := storedPersistenceTestCloud(t, filepath.Join(t.TempDir(), "cloud.db"))
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
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

// An engine's reply to a landing is the same whether or not it teaches an
// unchanged record again, row by row: the service reads from it what the
// engine holds, and a record taught nothing again changes none of it.
func TestAnUnchangedRecordsReplyIsTheSame(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := unchangedRowsTestCloud(t, "replies")
	skipCtx, skip, _ := transferTestCache(t)
	allCtx, all, _ := transferTestCache(t)
	all.testNoUnchangedRows = true
	land := func(bundle ValueBundle) {
		t.Helper()
		a := unchangedRowsTestLand(t, skipCtx, skip, bundle)
		b := unchangedRowsTestLand(t, allCtx, all, bundle)
		require.Zero(t, b.Rows.Unchanged)
		a.Rows.Unchanged = 0
		require.Equal(t, b.Rows, a.Rows)
		require.Equal(t, b.Roots, a.Roots)
		require.Equal(t, b.Values, a.Values, "row by row")
	}
	first := unchangedRowsTestBuild(t, ctx, cloud, nil, r)
	land(first)
	land(first)
	stored, _, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	changed := unchangedRowsTestBuild(t, ctx, cloud, nil, r)
	land(changed)
	land(changed)
	reply := unchangedRowsTestLand(t, skipCtx, skip, changed)
	require.Equal(t, 2, reply.Rows.Unchanged, "the engine that skips did skip")
}

// Two new Cloud caches that made the same writes number and stamp their
// entries alike, from one start count. Their bundles name each cache's own
// identity, so an engine that merged one's records teaches the other's again:
// they are another cache's, which can hold another record under the number.
func TestRecordsOfAnotherCloudCacheAreTaughtAgain(t *testing.T) {
	t.Parallel()
	source, root, dep := unchangedRowsTestSource(t, "two clouds")
	actx, a, ar, _ := unchangedRowsTestCloudOf(t, source, root, dep)
	bctx, b, br, _ := unchangedRowsTestCloudOf(t, source, root, dep)
	require.Equal(t, ar, br)
	fromA := unchangedRowsTestBuild(t, actx, a, nil, ar)
	fromB := unchangedRowsTestBuild(t, bctx, b, nil, br)
	require.NotEqual(t, fromA.SenderCache, fromB.SenderCache)
	require.Equal(t, a.Identity().Generation, b.Identity().Generation)
	require.Equal(t, fromA.Values[0].SenderNumber, fromB.Values[0].SenderNumber)
	for i := range fromA.Values {
		require.Equal(t, fromA.Values[i].SenderStamp, fromB.Values[i].SenderStamp, "the same writes give the same stamps")
	}
	engineCtx, engine, _ := transferTestCache(t)
	unchangedRowsTestLand(t, engineCtx, engine, fromA)
	again := unchangedRowsTestLand(t, engineCtx, engine, fromB)
	require.Equal(t, 2, again.Rows.Kept)
	require.Zero(t, again.Rows.Unchanged)
}
