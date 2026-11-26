package dagql

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// A blob-backed cache keeps its pool's measure through every kind of write
// that changes it, and what it reports (BlobCacheUsage) is what a walk of
// the pool finds after each: roots merged and stored, their parts stored,
// another engine holding them, a part dropped for its blob, a root's edge
// removed, a prune of each stage, a collection, and a save and restore.
func TestPoolMeasureIsKeptThroughEveryWrite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cloud.db")
	ctx, cloud := storedPersistenceTestCloud(t, path)
	usage := func() BlobCacheUsage {
		t.Helper()
		require.NoError(t, cloud.VerifyPoolMeasure())
		u, err := cloud.BlobCacheUsage()
		require.NoError(t, err)
		return u
	}
	require.Zero(t, usage().PoolValues)

	// Two roots of engine cache-a, each with a dependency of its own and
	// one they share.
	actx, a, asrv := transferTestCache(t)
	shared := persistedListTestResult(t, actx, a, asrv, "pool-shared", &transferTestValue{Text: "shared"})
	var roots []uint64
	for _, name := range []string{"pool-first", "pool-second"} {
		root := persistedListTestResult(t, actx, a, asrv, name, &transferTestValue{Text: name})
		own := persistedListTestResult(t, actx, a, asrv, name+"-dep", &transferTestValue{Text: name + "-dep"})
		transferTestDependency(a, actx, root, own)
		transferTestDependency(a, actx, root, shared)
		roots = append(roots, storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, root))[0])
		require.Positive(t, usage().PoolValues, "a merged root and its closure")
	}
	merged := usage()
	require.Equal(t, 5, merged.PoolValues, "two roots, their own dependencies and the shared one")

	// Their parts, sharing a base layer.
	for i, root := range roots {
		top := []string{"first-top", "second-top"}[i]
		stored, _, err := cloud.SetStoredPart(ctx, root, storedPruneTestPart("snapshot", map[string]int64{"pool-base": 100, top: int64(10 * (i + 1))}), 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	withParts := usage()
	require.Equal(t, 3, withParts.PoolBlobs, "the shared base counts once")
	require.Equal(t, int64(130), withParts.PoolBlobBytes)

	// Another engine holds them, as placement leaves them.
	storedPruneTestPlace(t, ctx, cloud, roots)
	require.Equal(t, withParts.PoolBytes, usage().PoolBytes, "holdings add nothing to the pool")

	// The store no longer has the first root's top layer.
	dropped, _, err := cloud.DropStoredParts(testDigest("first-top"))
	require.NoError(t, err)
	require.Len(t, dropped, 1)
	afterDrop := usage()
	require.Equal(t, int64(120), afterDrop.PoolBlobBytes, "the base and the second top")
	require.Equal(t, 2, afterDrop.PoolBlobs)

	// A prune of the disk stage under its maximum measures nothing and
	// drops nothing; over it, it drops the first root.
	storedPruneTestUse(cloud, roots[0], time.Now().Add(-2*time.Hour))
	storedPruneTestUse(cloud, roots[1], time.Now().Add(-time.Hour))
	report, err := cloud.Prune(ctx, []CachePrunePolicy{{MaxUsedSpace: 120, TargetSpace: 100}})
	require.NoError(t, err)
	require.Zero(t, report.DroppedValues)
	require.Equal(t, afterDrop, usage())

	// The memory stage, a byte over its maximum, drops the older root and
	// what only it kept.
	pool, err := cloud.PruneMetadataEstimate(ctx, afterDrop.PoolBytes-1, afterDrop.PoolBytes-2)
	require.NoError(t, err)
	require.Equal(t, 1, pool.RemovedPersistedRootCount)
	afterPrune := usage()
	require.Less(t, afterPrune.PoolValues, afterDrop.PoolValues)
	require.Equal(t, afterPrune.PoolBytes, pool.AfterPrune.EstimatedBytes, "the prune reads the kept measure")

	// The rest goes with its retention edge.
	_, removed, _, err := cloud.removePrunedEdge(ctx, sharedResultID(roots[1]))
	require.NoError(t, err)
	require.True(t, removed)
	require.Zero(t, usage().PoolValues)

	// Collecting the engines' holdings leaves nothing.
	for _, engine := range []CacheID{"cache-a", "cache-b"} {
		_, _, err := cloud.CollectRemoteHoldings(ctx, cloud.ReleaseRemoteCache(ctx, engine).Candidates, nil)
		require.NoError(t, err)
		usage()
	}

	// A root merged and stored again, saved, and restored: the restore
	// counts the pool once.
	again := persistedListTestResult(t, actx, a, asrv, "pool-again", &transferTestValue{Text: "again"})
	root := storedPersistenceTestMerge(t, ctx, cloud, exportTestBundle(t, actx, a, again))[0]
	stored, _, err := cloud.SetStoredPart(ctx, root, storedPruneTestPart("snapshot", map[string]int64{"again-base": 7}), 0)
	require.NoError(t, err)
	require.True(t, stored)
	saved := usage()
	require.Equal(t, 1, saved.PoolValues)
	require.NoError(t, cloud.Close(ctx))
	ctx, cloud = storedPersistenceTestCloud(t, path)
	defer func() { require.NoError(t, cloud.Close(ctx)) }()
	require.Equal(t, CachePersistenceResetNone, cloud.PersistenceResetReason())
	restored := usage()
	require.Equal(t, saved.PoolValues, restored.PoolValues)
	require.Equal(t, saved.PoolBytes, restored.PoolBytes)
	require.Equal(t, saved.PoolBlobBytes, restored.PoolBlobBytes)
}

// One blob that two roots' parts name at different sizes, as two engines can
// describe it, is still one blob, as the store holds it: the kept measure and
// the disk stage's snapshot both count it once, at the larger size. A prune
// frees it only with the last root that names it, so a maximum the larger
// size alone doesn't pass drops neither root, and one it passes drops both;
// the blobs a sweep keeps (LiveBlobs) follow.
func TestOneBlobAtTwoSizesIsOneBlob(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	newer := storedPruneTestRoot(t, ctx, cloud, "two-sizes-newer")
	older := storedPruneTestRoot(t, ctx, cloud, "two-sizes-older")
	storedPruneTestUse(cloud, older, time.Now().Add(-2*time.Hour))
	storedPruneTestUse(cloud, newer, time.Now().Add(-time.Hour))
	for number, size := range map[uint64]int64{newer: 60_000_000, older: 61_000_000} {
		stored, _, err := cloud.SetStoredPart(ctx, number, storedPruneTestPart("snapshot", map[string]int64{"the toolchain": size}), 0)
		require.NoError(t, err)
		require.True(t, stored)
	}
	storedPruneTestPlace(t, ctx, cloud, []uint64{newer, older})
	usage := func() BlobCacheUsage {
		t.Helper()
		require.NoError(t, cloud.VerifyPoolMeasure())
		u, err := cloud.BlobCacheUsage()
		require.NoError(t, err)
		require.Equal(t, u.PoolBlobBytes, cloud.snapshotPruneState(pruneSnapshotDisk, 0).usedBytes, "the prune's snapshot measures what the cache keeps")
		return u
	}
	live := func() []digest.Digest {
		t.Helper()
		blobs, err := cloud.LiveBlobs()
		require.NoError(t, err)
		return blobs
	}
	before := usage()
	require.Equal(t, 1, before.PoolBlobs, "one blob")
	require.Equal(t, int64(61_000_000), before.PoolBlobBytes, "at the larger size")
	require.Equal(t, []digest.Digest{testDigest("the toolchain")}, live())

	report, err := cloud.Prune(ctx, []CachePrunePolicy{{MaxUsedSpace: 100_000_000, TargetSpace: 75_000_000}})
	require.NoError(t, err)
	require.Zero(t, report.DroppedValues, "under the maximum, nothing goes")
	require.True(t, storedPruneTestEntryState(cloud, older).value)
	require.True(t, storedPruneTestEntryState(cloud, newer).value)
	require.Equal(t, before, usage())

	report, err = cloud.Prune(ctx, []CachePrunePolicy{{MaxUsedSpace: 50_000_000, TargetSpace: 10_000_000}})
	require.NoError(t, err)
	require.Equal(t, int64(61_000_000), report.ReclaimedBytes, "the blob, once")
	require.False(t, storedPruneTestEntryState(cloud, older).value)
	require.False(t, storedPruneTestEntryState(cloud, newer).value, "the blob goes only with the last root that names it")
	after := usage()
	require.Zero(t, after.PoolBlobs)
	require.Zero(t, after.PoolBlobBytes)
	require.Empty(t, live())
}
