package testutil

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moby/buildkit/solver"
	digest "github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func RunCacheStorageTests(t *testing.T, st func() solver.CacheKeyStorage) {
	for _, tc := range []func(*testing.T, solver.CacheKeyStorage){
		testResults,
		testLinks,
		testResultReleaseSingleLevel,
		testResultReleaseMultiLevel,
		testBacklinks,
		testWalkIDsByResult,
	} {
		runStorageTest(t, tc, st)
	}
}

func runStorageTest(t *testing.T, fn func(t *testing.T, st solver.CacheKeyStorage), st func() solver.CacheKeyStorage) {
	require.True(t, t.Run(getFunctionName(fn), func(t *testing.T) {
		fn(t, st())
	}))
}

func testResults(t *testing.T, st solver.CacheKeyStorage) {
	t.Parallel()
	timeNow := time.Now()
	err := st.AddResult("foo", solver.CacheResult{
		ID:        "foo0",
		CreatedAt: timeNow,
	})
	require.NoError(t, err)

	// Windows time.Now() resolution can be quite coarse, so frequently
	// time.Now() will be the same between two nearby calls.
	if time.Now() == timeNow {
		time.Sleep(time.Duration(100) * time.Millisecond)
	}
	require.NotEqual(t, timeNow, time.Now(), "time has stopped")

	err = st.AddResult("foo", solver.CacheResult{
		ID:        "foo1",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	err = st.AddResult("bar", solver.CacheResult{
		ID:        "bar0",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	m := map[string]solver.CacheResult{}
	err = st.WalkResults("foo", func(r solver.CacheResult) error {
		m[r.ID] = r
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, len(m), 2)
	f0, ok := m["foo0"]
	require.True(t, ok)
	f1, ok := m["foo1"]
	require.True(t, ok)
	require.True(t, f0.CreatedAt.Before(f1.CreatedAt), "f0.CreatedAt %v was not Before f1.CreatedAt %v", f0.CreatedAt, f1.CreatedAt)

	m = map[string]solver.CacheResult{}
	err = st.WalkResults("bar", func(r solver.CacheResult) error {
		m[r.ID] = r
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, len(m), 1)
	_, ok = m["bar0"]
	require.True(t, ok)

	// empty result
	err = st.WalkResults("baz", func(r solver.CacheResult) error {
		require.Fail(t, "unreachable")
		return nil
	})
	require.NoError(t, err)

	res, err := st.Load("foo", "foo1")
	require.NoError(t, err)

	require.Equal(t, res.ID, "foo1")

	_, err = st.Load("foo1", "foo1")
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, solver.ErrNotFound))

	_, err = st.Load("foo", "foo2")
	require.Error(t, err)
	require.Equal(t, true, errors.Is(err, solver.ErrNotFound))
}

func testLinks(t *testing.T, st solver.CacheKeyStorage) {
	t.Parallel()

	l0 := solver.CacheInfoLink{
		Input: 0, Output: 1, Digest: digest.FromBytes([]byte(">target0")),
	}
	err := st.AddLink("foo", l0, "target0")
	require.NoError(t, err)

	err = st.AddLink("bar", l0, "target0-second")
	require.NoError(t, err)

	m := map[string]struct{}{}
	err = st.WalkLinks("foo", l0, func(id string) error {
		m[id] = struct{}{}
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, len(m), 1)
	_, ok := m["target0"]
	require.True(t, ok)

	l1 := solver.CacheInfoLink{
		Input: 0, Output: 1, Digest: digest.FromBytes([]byte(">target1")),
	}
	m = map[string]struct{}{}
	err = st.WalkLinks("foo", l1, func(id string) error {
		m[id] = struct{}{}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, len(m), 0)

	err = st.AddLink("foo", l1, "target1")
	require.NoError(t, err)

	m = map[string]struct{}{}
	err = st.WalkLinks("foo", l1, func(id string) error {
		m[id] = struct{}{}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, len(m), 1)

	_, ok = m["target1"]
	require.True(t, ok)

	err = st.AddLink("foo", l1, "target1-second")
	require.NoError(t, err)

	m = map[string]struct{}{}
	err = st.WalkLinks("foo", l1, func(id string) error {
		m[id] = struct{}{}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, len(m), 2)
	_, ok = m["target1"]
	require.True(t, ok)
	_, ok = m["target1-second"]
	require.True(t, ok)
}

func testResultReleaseSingleLevel(t *testing.T, st solver.CacheKeyStorage) {
	t.Parallel()

	err := st.AddResult("foo", solver.CacheResult{
		ID:        "foo0",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	err = st.AddResult("foo", solver.CacheResult{
		ID:        "foo1",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	err = st.Release("foo0")
	require.NoError(t, err)

	m := map[string]struct{}{}
	st.WalkResults("foo", func(res solver.CacheResult) error {
		m[res.ID] = struct{}{}
		return nil
	})

	require.Equal(t, len(m), 1)
	_, ok := m["foo1"]
	require.True(t, ok)

	err = st.Release("foo1")
	require.NoError(t, err)

	m = map[string]struct{}{}
	st.WalkResults("foo", func(res solver.CacheResult) error {
		m[res.ID] = struct{}{}
		return nil
	})

	require.Equal(t, len(m), 0)

	st.Walk(func(id string) error {
		require.False(t, true, fmt.Sprintf("id %s should have been released", id))
		return nil
	})
}

func testBacklinks(t *testing.T, st solver.CacheKeyStorage) {
	t.Parallel()

	err := st.AddResult("foo", solver.CacheResult{
		ID:        "foo-result",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	err = st.AddResult("sub0", solver.CacheResult{
		ID:        "sub0-result",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	l0 := solver.CacheInfoLink{
		Input: 0, Output: 1, Digest: digest.FromBytes([]byte("to-sub0")),
	}
	err = st.AddLink("foo", l0, "sub0")
	require.NoError(t, err)

	backlinks := 0
	st.WalkBacklinks("sub0", func(id string, link solver.CacheInfoLink) error {
		require.Equal(t, id, "foo")
		require.Equal(t, link.Input, solver.Index(0))
		require.Equal(t, link.Digest, rootKey(digest.FromBytes([]byte("to-sub0")), 1))
		backlinks++
		return nil
	})
	require.Equal(t, backlinks, 1)
}

func testResultReleaseMultiLevel(t *testing.T, st solver.CacheKeyStorage) {
	t.Parallel()

	err := st.AddResult("foo", solver.CacheResult{
		ID:        "foo-result",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	err = st.AddResult("sub0", solver.CacheResult{
		ID:        "sub0-result",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	l0 := solver.CacheInfoLink{
		Input: 0, Output: 1, Digest: digest.FromBytes([]byte("to-sub0")),
	}
	err = st.AddLink("foo", l0, "sub0")
	require.NoError(t, err)

	err = st.AddResult("sub1", solver.CacheResult{
		ID:        "sub1-result",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	err = st.AddLink("foo", l0, "sub1")
	require.NoError(t, err)

	// delete one sub doesn't delete parent

	err = st.Release("sub0-result")
	require.NoError(t, err)

	m := map[string]struct{}{}
	err = st.WalkResults("foo", func(res solver.CacheResult) error {
		m[res.ID] = struct{}{}
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, len(m), 1)
	_, ok := m["foo-result"]
	require.True(t, ok)

	require.False(t, st.Exists("sub0"))

	m = map[string]struct{}{}
	err = st.WalkLinks("foo", l0, func(id string) error {
		m[id] = struct{}{}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, len(m), 1)

	_, ok = m["sub1"]
	require.True(t, ok)

	// release foo removes the result but doesn't break the chain

	err = st.Release("foo-result")
	require.NoError(t, err)

	require.True(t, st.Exists("foo"))

	m = map[string]struct{}{}
	err = st.WalkResults("foo", func(res solver.CacheResult) error {
		m[res.ID] = struct{}{}
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, len(m), 0)

	m = map[string]struct{}{}
	err = st.WalkLinks("foo", l0, func(id string) error {
		m[id] = struct{}{}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, len(m), 1)

	// release sub1 now releases foo as well
	err = st.Release("sub1-result")
	require.NoError(t, err)

	require.False(t, st.Exists("sub1"))
	require.False(t, st.Exists("foo"))

	st.Walk(func(id string) error {
		require.False(t, true, fmt.Sprintf("id %s should have been released", id))
		return nil
	})
}

func testWalkIDsByResult(t *testing.T, st solver.CacheKeyStorage) {
	t.Parallel()

	err := st.AddResult("foo", solver.CacheResult{
		ID:        "foo-result",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	err = st.AddResult("foo2", solver.CacheResult{
		ID:        "foo-result",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	err = st.AddResult("bar", solver.CacheResult{
		ID:        "bar-result",
		CreatedAt: time.Now(),
	})
	require.NoError(t, err)

	m := map[string]struct{}{}
	err = st.WalkIDsByResult("foo-result", func(id string) error {
		m[id] = struct{}{}
		return nil
	})
	require.NoError(t, err)

	_, ok := m["foo"]
	require.True(t, ok)

	_, ok = m["foo2"]
	require.True(t, ok)

	_, ok = m["bar"]
	require.False(t, ok)
}

func getFunctionName(i interface{}) string {
	fullname := runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
	dot := strings.LastIndex(fullname, ".") + 1
	return strings.Title(fullname[dot:]) //nolint:staticcheck // ignoring "SA1019: strings.Title is deprecated", as for our use we don't need full unicode support
}

func rootKey(dgst digest.Digest, output solver.Index) digest.Digest {
	return digest.FromBytes([]byte(fmt.Sprintf("%s@%d", dgst, output)))
}
