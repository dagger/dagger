package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func checkEmpty(t *testing.T, ei *edgeIndex) {
	require.Equal(t, len(ei.items), 0)
	require.Equal(t, len(ei.backRefs), 0)
}

func TestIndexSimple(t *testing.T) {
	idx := newEdgeIndex()

	e1 := &edge{}
	e2 := &edge{}
	e3 := &edge{}

	k1 := NewCacheKey(dgst("foo"), "", 0)
	v := idx.LoadOrStore(k1, e1)
	require.Nil(t, v)

	k2 := NewCacheKey(dgst("bar"), "", 0)
	v = idx.LoadOrStore(k2, e2)
	require.Nil(t, v)

	v = idx.LoadOrStore(NewCacheKey(dgst("bar"), "", 0), e3)
	require.Equal(t, v, e2)

	v = idx.LoadOrStore(NewCacheKey(dgst("bar"), "", 0), e3)
	require.Equal(t, v, e2)

	v = idx.LoadOrStore(NewCacheKey(dgst("foo"), "", 0), e3)
	require.Equal(t, v, e1)

	idx.Release(e1)
	idx.Release(e2)
	checkEmpty(t, idx)
}

func TestIndexMultiLevelSimple(t *testing.T) {
	idx := newEdgeIndex()

	e1 := &edge{}
	e2 := &edge{}
	e3 := &edge{}

	k1 := testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{{CacheKey: expKey(NewCacheKey("s0", "", 0)), Selector: dgst("s0")}},
	})

	v := idx.LoadOrStore(k1, e1)
	require.Nil(t, v)

	k2 := testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{{CacheKey: expKey(NewCacheKey("s0", "", 0)), Selector: dgst("s0")}},
	})

	v = idx.LoadOrStore(k2, e2)
	require.Equal(t, v, e1)

	k2 = testCacheKeyWithDeps(dgst("foo"), 1, k1.Deps())
	v = idx.LoadOrStore(k2, e2)
	require.Equal(t, v, e1)

	v = idx.LoadOrStore(k1, e2)
	require.Equal(t, v, e1)

	// update selector
	k2 = testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{{CacheKey: expKey(NewCacheKey("s0", "", 0))}},
	})
	v = idx.LoadOrStore(k2, e2)
	require.Nil(t, v)

	// add one dep to e1
	k2 = testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{
			{CacheKey: expKey(NewCacheKey("s0", "", 0)), Selector: dgst("s0")},
			{CacheKey: expKey(NewCacheKey("s1", "", 1))},
		},
	})
	v = idx.LoadOrStore(k2, e2)
	require.Equal(t, v, e1)

	// recheck with only the new dep key
	k2 = testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{
			{CacheKey: expKey(NewCacheKey("s1", "", 1))},
		},
	})
	v = idx.LoadOrStore(k2, e2)
	require.Equal(t, v, e1)

	// combine e1 and e2
	k2 = testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{
			{CacheKey: expKey(NewCacheKey("s0", "", 0))},
			{CacheKey: expKey(NewCacheKey("s1", "", 1))},
		},
	})
	v = idx.LoadOrStore(k2, e2)
	require.Equal(t, v, e1)

	// initial e2 now points to e1
	k2 = testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{{CacheKey: expKey(NewCacheKey("s0", "", 0))}},
	})
	v = idx.LoadOrStore(k2, e2)
	require.Equal(t, v, e1)

	idx.Release(e1)

	// e2 still remains after e1 is gone
	k2 = testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{{CacheKey: expKey(NewCacheKey("s0", "", 0))}},
	})
	v = idx.LoadOrStore(k2, e3)
	require.Equal(t, v, e2)

	idx.Release(e2)
	checkEmpty(t, idx)
}

func TestIndexThreeLevels(t *testing.T) {
	idx := newEdgeIndex()

	e1 := &edge{}
	e2 := &edge{}
	e3 := &edge{}

	k1 := testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{{CacheKey: expKey(NewCacheKey("s0", "", 0)), Selector: dgst("s0")}},
	})

	v := idx.LoadOrStore(k1, e1)
	require.Nil(t, v)

	v = idx.LoadOrStore(k1, e2)
	require.Equal(t, v, e1)

	k2 := testCacheKeyWithDeps(dgst("bar"), 0, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{{CacheKey: expKey(k1)}},
	})
	v = idx.LoadOrStore(k2, e2)
	require.Nil(t, v)

	k2 = testCacheKeyWithDeps(dgst("bar"), 0, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{
			{CacheKey: expKey(k1)},
			{CacheKey: expKey(NewCacheKey("alt", "", 0))},
		},
	})
	v = idx.LoadOrStore(k2, e2)
	require.Nil(t, v)

	k2 = testCacheKeyWithDeps(dgst("bar"), 0, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{
			{CacheKey: expKey(NewCacheKey("alt", "", 0))},
		},
	})
	v = idx.LoadOrStore(k2, e3)
	require.Equal(t, v, e2)

	// change dep in a low key
	k1 = testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{
			{CacheKey: expKey(NewCacheKey("f0", "", 0))},
			{CacheKey: expKey(NewCacheKey("f0_", "", 0))},
		},
		{{CacheKey: expKey(NewCacheKey("s0", "", 0)), Selector: dgst("s0")}},
	})
	k2 = testCacheKeyWithDeps(dgst("bar"), 0, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{{CacheKey: expKey(k1)}},
	})
	v = idx.LoadOrStore(k2, e3)
	require.Equal(t, v, e2)

	// reload with only f0_ still matches
	k1 = testCacheKeyWithDeps(dgst("foo"), 1, [][]CacheKeyWithSelector{
		{
			{CacheKey: expKey(NewCacheKey("f0_", "", 0))},
		},
		{{CacheKey: expKey(NewCacheKey("s0", "", 0)), Selector: dgst("s0")}},
	})
	k2 = testCacheKeyWithDeps(dgst("bar"), 0, [][]CacheKeyWithSelector{
		{{CacheKey: expKey(NewCacheKey("f0", "", 0))}},
		{{CacheKey: expKey(k1)}},
	})
	v = idx.LoadOrStore(k2, e3)
	require.Equal(t, v, e2)

	idx.Release(e1)
	idx.Release(e2)
	checkEmpty(t, idx)
}
