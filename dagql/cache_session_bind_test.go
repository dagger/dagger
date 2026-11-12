package dagql

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

// A result its call bound to its session (BindToSession) is served only to
// that session. Another session computes its own, while the first is still
// open and after it is released while something else still holds its
// result, and each session hits its own value afterwards.
func TestCacheBoundResultServedOnlyToItsSession(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	srv := cacheTestServer(t)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "bound-to-session",
	}
	var calls int
	call := func(ctx context.Context, session string) AnyResult {
		t.Helper()
		res, err := c.GetOrInitCall(ctx, session, srv, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(ctx context.Context) (AnyResult, error) {
			calls++
			BindToSession(ctx)
			return NewResultForCall(&cacheTestObject{Value: calls}, frame.clone())
		})
		assert.NilError(t, err)
		return res
	}

	aCtx := currentEntryTestSession(baseCtx, c, "session-a")
	a := call(aCtx, "session-a")
	assert.Equal(t, 1, calls)
	aID := a.cacheSharedResult().id

	// B is open at the same time as A, and computes its own.
	bCtx := currentEntryTestSession(baseCtx, c, "session-b")
	b := call(bCtx, "session-b")
	assert.Equal(t, 2, calls, "another session must not be served A's bound result")
	assert.Assert(t, !b.HitCache())
	bID := b.cacheSharedResult().id
	assert.Assert(t, bID != aID)

	// Each session hits its own value.
	again := call(aCtx, "session-a")
	assert.Assert(t, again.HitCache())
	assert.Equal(t, aID, again.cacheSharedResult().id)
	again = call(bCtx, "session-b")
	assert.Assert(t, again.HitCache())
	assert.Equal(t, bID, again.cacheSharedResult().id)
	assert.Equal(t, 2, calls)

	// Something other than a session holds A's result, as a cache usage pass
	// does, while A is released: a later session still computes its own.
	_, release, err := c.HoldEntry(baseCtx, uint64(aID))
	assert.NilError(t, err)
	assert.NilError(t, c.ReleaseSession(aCtx, "session-a"))
	assert.NilError(t, c.WaitSessionRelease(aCtx, "session-a"))
	cCtx := currentEntryTestSession(baseCtx, c, "session-c")
	later := call(cCtx, "session-c")
	assert.Equal(t, 3, calls, "a later session must not be served a released session's bound result")
	assert.Assert(t, later.cacheSharedResult().id != aID)

	// Binding declines persistence too.
	c.egraphMu.RLock()
	_, retained := c.persistedEdgesByResult[aID]
	c.egraphMu.RUnlock()
	assert.Assert(t, !retained, "a bound result is not kept past its session")

	assert.NilError(t, release(baseCtx))
	for _, s := range []struct {
		ctx context.Context
		id  string
	}{{bCtx, "session-b"}, {cCtx, "session-c"}} {
		assert.NilError(t, c.ReleaseSession(s.ctx, s.id))
	}
}
