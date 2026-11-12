package dagql

import (
	"context"
	"path/filepath"
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

// A bound value doesn't adopt another session's live entry of its recipe,
// as an unbound value would (D1): B's call started before A's published, A's
// unbound value became the recipe's entry, and B's bound value, published
// after it, is still the one B gets, and hits afterwards.
func TestCacheBoundResultDoesNotAdoptAnotherSessionsEntry(t *testing.T) {
	t.Parallel()

	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	srv := cacheTestServer(t)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "bound-beside-live-entry",
	}
	aCtx := currentEntryTestSession(baseCtx, c, "session-a")
	bCtx := currentEntryTestSession(baseCtx, c, "session-b")

	bStarted, bFinish := make(chan struct{}), make(chan struct{})
	bDone := make(chan currentEntryTestOutcome, 1)
	go func() {
		res, err := c.GetOrInitCall(bCtx, "session-b", srv, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(ctx context.Context) (AnyResult, error) {
			close(bStarted)
			<-bFinish
			BindToSession(ctx)
			return NewResultForCall(&cacheTestObject{Value: 2}, frame.clone())
		})
		bDone <- currentEntryTestOutcome{res, err}
	}()
	<-bStarted

	a, err := c.GetOrInitCall(aCtx, "session-a", srv, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&cacheTestObject{Value: 1}, frame.clone())
	})
	assert.NilError(t, err)
	aID := a.cacheSharedResult().id
	assert.Equal(t, aID, currentEntryTestIndexed(t, c, frame))

	close(bFinish)
	b := <-bDone
	assert.NilError(t, b.err)
	bID := b.res.cacheSharedResult().id
	assert.Assert(t, bID != aID, "B must get its own bound value, not A's entry")
	assert.Equal(t, 2, b.res.Unwrap().(*cacheTestObject).Value)
	assert.Equal(t, aID, currentEntryTestIndexed(t, c, frame), "the index keeps naming A's entry")

	again, err := c.GetOrInitCall(bCtx, "session-b", srv, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(context.Context) (AnyResult, error) {
		t.Fatal("session B must hit its own value")
		return nil, nil
	})
	assert.NilError(t, err)
	assert.Equal(t, bID, again.cacheSharedResult().id)

	for _, s := range []struct {
		ctx context.Context
		id  string
	}{{aCtx, "session-a"}, {bCtx, "session-b"}} {
		assert.NilError(t, c.ReleaseSession(s.ctx, s.id))
	}
}

// A bound value saved as the dependency of a kept entry stays bound across a
// restart: its session is gone, so no later session is served it.
func TestCacheBoundResultStaysBoundAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, c, srv := persistedListTestCache(t, path)
	frame := &ResultCall{Kind: ResultCallKindField, Field: "bound-dependency", Type: NewResultCallType(String("").Type())}
	var calls int
	read := func(ctx context.Context, c *Cache, srv *Server, session string) AnyResult {
		t.Helper()
		res, err := c.GetOrInitCall(ctx, session, srv, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(ctx context.Context) (AnyResult, error) {
			calls++
			BindToSession(ctx)
			return NewResultForCall(String("failed"), frame.clone())
		})
		assert.NilError(t, err)
		return res
	}

	s1 := currentEntryTestSession(ctx, c, "session-1")
	bound := read(s1, c, srv, "session-1")
	boundID := bound.cacheSharedResult().id
	parent := currentEntryTestPublish(t, s1, c, srv, "session-1", "bound-parent", String("parent"))
	assert.NilError(t, c.AddExplicitDependency(s1, parent, bound, "bound-dependency"))
	assert.NilError(t, c.ReleaseSession(s1, "session-1"))
	assert.NilError(t, c.WaitSessionRelease(s1, "session-1"))
	assert.NilError(t, c.Close(ctx))

	ctx, c, srv = persistedListTestCache(t, path)
	c.egraphMu.RLock()
	_, restored := c.resultsByID[boundID]
	c.egraphMu.RUnlock()
	assert.Assert(t, restored, "the kept entry's dependency is restored")
	s2 := currentEntryTestSession(ctx, c, "session-2")
	later := read(s2, c, srv, "session-2")
	assert.Assert(t, !later.HitCache(), "a later session must not be served a restored bound value")
	assert.Equal(t, 2, calls)
	assert.NilError(t, c.ReleaseSession(s2, "session-2"))
}

// A bound value sent to another cache stays bound there: no session of the
// receiving cache is served it.
func TestCacheBoundResultStaysBoundInAnotherCache(t *testing.T) {
	t.Parallel()

	frame := &ResultCall{Kind: ResultCallKindField, Field: "bound-transferred", Type: NewResultCallType((&transferTestValue{}).Type())}
	var calls int
	call := func(ctx context.Context, c *Cache, srv *Server, session string) AnyResult {
		t.Helper()
		res, err := c.GetOrInitCall(ctx, session, srv, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(ctx context.Context) (AnyResult, error) {
			calls++
			BindToSession(ctx)
			return NewResultForCall(&transferTestValue{Text: "failed"}, frame.clone())
		})
		assert.NilError(t, err)
		return res
	}

	actx, a, asrv := transferTestCache(t)
	sa := currentEntryTestSession(actx, a, "session-a")
	sent := call(sa, a, asrv, "session-a")
	bundle := exportTestBundle(t, actx, a, sent)

	bctx, b, bsrv := transferTestCache(t)
	_, err := b.MergeValues(bctx, cloudCacheID, bundle)
	assert.NilError(t, err)
	sb := currentEntryTestSession(bctx, b, "session-b")
	got := call(sb, b, bsrv, "session-b")
	assert.Assert(t, !got.HitCache(), "the receiving cache must not serve another cache's bound value")
	assert.Equal(t, 2, calls)
	assert.NilError(t, b.ReleaseSession(sb, "session-b"))
	assert.NilError(t, a.ReleaseSession(sa, "session-a"))
}
