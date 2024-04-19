package solver

import (
	"context"
	"testing"
	"time"

	"github.com/moby/buildkit/identity"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func depKeys(cks ...ExportableCacheKey) []CacheKeyWithSelector {
	var keys []CacheKeyWithSelector
	for _, ck := range cks {
		keys = append(keys, CacheKeyWithSelector{CacheKey: ck})
	}
	return keys
}

func testCacheKey(dgst digest.Digest, output Index, deps ...ExportableCacheKey) *CacheKey {
	k := NewCacheKey(dgst, "", output)
	k.deps = make([][]CacheKeyWithSelector, len(deps))
	for i, dep := range deps {
		k.deps[i] = depKeys(dep)
	}
	return k
}

func testCacheKeyWithDeps(dgst digest.Digest, output Index, deps [][]CacheKeyWithSelector) *CacheKey {
	k := NewCacheKey(dgst, "", output)
	k.deps = deps
	return k
}

func expKey(k *CacheKey) ExportableCacheKey {
	return ExportableCacheKey{CacheKey: k, Exporter: &exporter{k: k}}
}

func TestInMemoryCache(t *testing.T) {
	ctx := context.TODO()

	m := NewInMemoryCacheManager()

	cacheFoo, err := m.Save(NewCacheKey(dgst("foo"), "", 0), testResult("result0"), time.Now())
	require.NoError(t, err)

	keys, err := m.Query(nil, 0, dgst("foo"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err := m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 1)

	res, err := m.Load(ctx, matches[0])
	require.NoError(t, err)
	require.Equal(t, "result0", unwrap(res))

	// another record
	cacheBar, err := m.Save(NewCacheKey(dgst("bar"), "", 0), testResult("result1"), time.Now())
	require.NoError(t, err)

	keys, err = m.Query(nil, 0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err = m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 1)

	res, err = m.Load(ctx, matches[0])
	require.NoError(t, err)
	require.Equal(t, "result1", unwrap(res))

	// invalid request
	keys, err = m.Query(nil, 0, dgst("baz"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 0)

	// second level
	k := testCacheKey(dgst("baz"), Index(1), *cacheFoo, *cacheBar)
	cacheBaz, err := m.Save(k, testResult("result2"), time.Now())
	require.NoError(t, err)

	keys, err = m.Query(nil, 0, dgst("baz"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 0)

	keys, err = m.Query(depKeys(*cacheFoo), 0, dgst("baz"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 0)

	keys, err = m.Query(depKeys(*cacheFoo), 1, dgst("baz"), Index(1))
	require.NoError(t, err)
	require.Equal(t, len(keys), 0)

	keys, err = m.Query(depKeys(*cacheFoo), 0, dgst("baz"), Index(1))
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err = m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 1)

	res, err = m.Load(ctx, matches[0])
	require.NoError(t, err)
	require.Equal(t, "result2", unwrap(res))

	keys2, err := m.Query(depKeys(*cacheBar), 1, dgst("baz"), Index(1))
	require.NoError(t, err)
	require.Equal(t, len(keys2), 1)

	require.Equal(t, keys[0].ID, keys2[0].ID)

	k = testCacheKey(dgst("baz"), Index(1), *cacheFoo)
	_, err = m.Save(k, testResult("result3"), time.Now())
	require.NoError(t, err)

	keys, err = m.Query(depKeys(*cacheFoo), 0, dgst("baz"), Index(1))
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err = m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 2)

	k = testCacheKeyWithDeps(dgst("bax"), 0, [][]CacheKeyWithSelector{
		{{CacheKey: *cacheFoo}, {CacheKey: *cacheBaz}},
		{{CacheKey: *cacheBar}},
	})
	_, err = m.Save(k, testResult("result4"), time.Now())
	require.NoError(t, err)

	// foo, bar, baz should all point to result4
	keys, err = m.Query(depKeys(*cacheFoo), 0, dgst("bax"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	id := keys[0].ID

	keys, err = m.Query(depKeys(*cacheBar), 1, dgst("bax"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)
	require.Equal(t, keys[0].ID, id)

	keys, err = m.Query(depKeys(*cacheBaz), 0, dgst("bax"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)
	require.Equal(t, keys[0].ID, id)
}

func TestInMemoryCacheSelector(t *testing.T) {
	ctx := context.TODO()

	m := NewInMemoryCacheManager()

	cacheFoo, err := m.Save(NewCacheKey(dgst("foo"), "", 0), testResult("result0"), time.Now())
	require.NoError(t, err)

	_, err = m.Save(testCacheKeyWithDeps(dgst("bar"), 0, [][]CacheKeyWithSelector{
		{{CacheKey: *cacheFoo, Selector: dgst("sel0")}},
	}), testResult("result1"), time.Now())
	require.NoError(t, err)

	keys, err := m.Query(depKeys(*cacheFoo), 0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 0)

	keys, err = m.Query([]CacheKeyWithSelector{{Selector: "sel-invalid", CacheKey: *cacheFoo}}, 0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 0)

	keys, err = m.Query([]CacheKeyWithSelector{{Selector: dgst("sel0"), CacheKey: *cacheFoo}}, 0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err := m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 1)

	res, err := m.Load(ctx, matches[0])
	require.NoError(t, err)
	require.Equal(t, "result1", unwrap(res))
}

func TestInMemoryCacheSelectorNested(t *testing.T) {
	ctx := context.TODO()

	m := NewInMemoryCacheManager()

	cacheFoo, err := m.Save(NewCacheKey(dgst("foo"), "", 0), testResult("result0"), time.Now())
	require.NoError(t, err)

	_, err = m.Save(testCacheKeyWithDeps(dgst("bar"), 0, [][]CacheKeyWithSelector{
		{{CacheKey: *cacheFoo, Selector: dgst("sel0")}, {CacheKey: expKey(NewCacheKey(dgst("second"), "", 0))}},
	}), testResult("result1"), time.Now())
	require.NoError(t, err)

	keys, err := m.Query(
		[]CacheKeyWithSelector{{Selector: dgst("sel0"), CacheKey: *cacheFoo}},
		0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err := m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 1)

	res, err := m.Load(ctx, matches[0])
	require.NoError(t, err)
	require.Equal(t, "result1", unwrap(res))

	keys, err = m.Query(depKeys(*cacheFoo), 0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 0)

	keys, err = m.Query([]CacheKeyWithSelector{{Selector: dgst("bar"), CacheKey: *cacheFoo}}, 0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 0)

	keys, err = m.Query(depKeys(expKey(NewCacheKey(dgst("second"), "", 0))), 0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err = m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 1)

	res, err = m.Load(ctx, matches[0])
	require.NoError(t, err)
	require.Equal(t, "result1", unwrap(res))

	keys, err = m.Query(depKeys(expKey(NewCacheKey(dgst("second"), "", 0))), 0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)
}

func TestInMemoryCacheReleaseParent(t *testing.T) {
	storage := NewInMemoryCacheStorage()
	results := NewInMemoryResultStorage()
	m := NewCacheManager(context.TODO(), identity.NewID(), storage, results)

	res0 := testResult("result0")
	cacheFoo, err := m.Save(NewCacheKey(dgst("foo"), "", 0), res0, time.Now())
	require.NoError(t, err)

	res1 := testResult("result1")
	_, err = m.Save(testCacheKey(dgst("bar"), 0, *cacheFoo), res1, time.Now())
	require.NoError(t, err)

	keys, err := m.Query(nil, 0, dgst("foo"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err := m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 1)

	err = storage.Release(res0.ID())
	require.NoError(t, err)

	// foo becomes unloadable
	keys, err = m.Query(nil, 0, dgst("foo"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err = m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 0)

	keys, err = m.Query(depKeys(expKey(keys[0])), 0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err = m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 1)

	// releasing bar releases both foo and bar
	err = storage.Release(res1.ID())
	require.NoError(t, err)

	keys, err = m.Query(nil, 0, dgst("foo"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 0)
}

// TestInMemoryCacheRestoreOfflineDeletion deletes a result while the
// cachemanager is not running and checks that it syncs up on restore
func TestInMemoryCacheRestoreOfflineDeletion(t *testing.T) {
	storage := NewInMemoryCacheStorage()
	results := NewInMemoryResultStorage()
	m := NewCacheManager(context.TODO(), identity.NewID(), storage, results)

	res0 := testResult("result0")
	cacheFoo, err := m.Save(NewCacheKey(dgst("foo"), "", 0), res0, time.Now())
	require.NoError(t, err)

	res1 := testResult("result1")
	_, err = m.Save(testCacheKey(dgst("bar"), 0, *cacheFoo), res1, time.Now())
	require.NoError(t, err)

	results2 := NewInMemoryResultStorage()
	_, err = results2.Save(res1, time.Now()) // only add bar
	require.NoError(t, err)

	m = NewCacheManager(context.TODO(), identity.NewID(), storage, results2)

	keys, err := m.Query(nil, 0, dgst("foo"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err := m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 0)

	keys, err = m.Query(depKeys(expKey(keys[0])), 0, dgst("bar"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	matches, err = m.Records(context.TODO(), keys[0])
	require.NoError(t, err)
	require.Equal(t, len(matches), 1)
}

func TestCarryOverFromSublink(t *testing.T) {
	storage := NewInMemoryCacheStorage()
	results := NewInMemoryResultStorage()
	m := NewCacheManager(context.TODO(), identity.NewID(), storage, results)

	cacheFoo, err := m.Save(NewCacheKey(dgst("foo"), "", 0), testResult("resultFoo"), time.Now())
	require.NoError(t, err)

	_, err = m.Save(testCacheKeyWithDeps(dgst("res"), 0, [][]CacheKeyWithSelector{
		{{CacheKey: *cacheFoo, Selector: dgst("sel0")}, {CacheKey: expKey(NewCacheKey(dgst("content0"), "", 0))}},
	}), testResult("result0"), time.Now())
	require.NoError(t, err)

	cacheBar, err := m.Save(NewCacheKey(dgst("bar"), "", 0), testResult("resultBar"), time.Now())
	require.NoError(t, err)

	keys, err := m.Query([]CacheKeyWithSelector{
		{CacheKey: *cacheBar, Selector: dgst("sel0")},
		{CacheKey: expKey(NewCacheKey(dgst("content0"), "", 0))},
	}, 0, dgst("res"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)

	keys, err = m.Query([]CacheKeyWithSelector{
		{Selector: dgst("sel0"), CacheKey: *cacheBar},
	}, 0, dgst("res"), 0)
	require.NoError(t, err)
	require.Equal(t, len(keys), 1)
}

func dgst(s string) digest.Digest {
	return digest.FromBytes([]byte(s))
}

func testResult(v string) Result {
	return &dummyResult{
		id:    identity.NewID(),
		value: v,
	}
}
