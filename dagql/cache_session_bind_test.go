package dagql

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/engine/snapshots/config"
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

// A transfer leaves out a bound value and what is built on it: they are no
// use to another cache, where their session doesn't exist.
func TestCacheBoundResultIsNotTransferred(t *testing.T) {
	t.Parallel()

	ctx, c, srv := transferTestCache(t)
	sa := currentEntryTestSession(ctx, c, "session-a")
	frame := &ResultCall{Kind: ResultCallKindField, Field: "bound-transferred", Type: NewResultCallType((&transferTestValue{}).Type())}
	bound, err := c.GetOrInitCall(sa, "session-a", srv, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(ctx context.Context) (AnyResult, error) {
		BindToSession(ctx)
		return NewResultForCall(&transferTestValue{Text: "failed"}, frame.clone())
	})
	assert.NilError(t, err)
	wrapper := currentEntryTestPublish(t, sa, c, srv, "session-a", "bound-wrapper", String("wrapper"))
	assert.NilError(t, c.AddExplicitDependency(sa, wrapper, bound, "bound-dependency"))
	plain := persistedListTestResult(t, ctx, c, srv, "plain-root", &transferTestValue{Text: "plain"})

	var leftOut []LeftOutRoot
	var roots int
	assert.NilError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{bound, wrapper, plain}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		leftOut, roots = values.LeftOut, len(values.Bundle.Roots)
		return nil
	}))
	assert.Equal(t, 1, roots, "only the unbound root is sent")
	assert.Equal(t, 2, len(leftOut))
	for i, root := range leftOut {
		assert.Equal(t, i, root.Index)
		assert.Equal(t, BusyKind(""), root.Kind, "a bound value is refused, not busy")
	}
	assert.NilError(t, c.ReleaseSession(sa, "session-a"))
}

// A field of a bound result that returns a result the bound result holds
// answers for that session only. Another session makes the parent's call
// again, which binds its own: the parents' calls are the same, so the field's
// call made on the first one must still not hand the second session the
// result the first session's parent holds. Each session's own later calls of
// the field hit its own. The held result is either one the bound call made,
// or one that existed before it, an ordinary value another session is still
// served by its own call.
func TestCacheBoundResultsHeldResultIsServedOnlyToItsSession(t *testing.T) {
	t.Parallel()
	for _, made := range []bool{true, false} {
		name := "held result the bound call made"
		if !made {
			name = "held result that existed before"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			baseCtx := t.Context()
			c, err := NewCache(baseCtx, "", nil, nil)
			assert.NilError(t, err)
			parentCall := cacheTestIntCall("bound-parent")
			child := func(ctx context.Context, session string, v int) AnyResult {
				t.Helper()
				childCall := cacheTestIntCall(fmt.Sprintf("held-child-%d", v))
				if made {
					return cacheTestIntResult(childCall, v)
				}
				res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: childCall}, func(context.Context) (AnyResult, error) {
					return cacheTestIntResult(childCall, v), nil
				})
				assert.NilError(t, err)
				return res
			}
			parent := func(ctx context.Context, session string, v int) AnyResult {
				t.Helper()
				held := child(ctx, session, v)
				res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: parentCall.clone()}, func(ctx context.Context) (AnyResult, error) {
					BindToSession(ctx)
					return cacheTestDetachedResult(parentCall.clone(), &cacheTestOwnedDepsInt{Int: NewInt(0), ownedResults: []AnyResult{held}}), nil
				})
				assert.NilError(t, err)
				return res
			}
			heldBy := func(ctx context.Context, session string, p AnyResult) AnyResult {
				t.Helper()
				res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: &ResultCall{
					Kind:     ResultCallKindField,
					Type:     NewResultCallType(Int(0).Type()),
					Field:    "held",
					Receiver: &ResultCallRef{ResultID: uint64(p.cacheSharedResult().id)},
				}}, func(context.Context) (AnyResult, error) {
					return p.Unwrap().(*cacheTestOwnedDepsInt).ownedResults[0], nil
				})
				assert.NilError(t, err)
				return res
			}

			aCtx := currentEntryTestSession(baseCtx, c, "session-a")
			a := parent(aCtx, "session-a", 1)
			assert.Equal(t, 1, cacheTestUnwrapInt(t, heldBy(aCtx, "session-a", a)))

			bCtx := currentEntryTestSession(baseCtx, c, "session-b")
			b := parent(bCtx, "session-b", 2)
			assert.Assert(t, b.cacheSharedResult().id != a.cacheSharedResult().id, "B makes its own parent")
			assert.Equal(t, 2, cacheTestUnwrapInt(t, heldBy(bCtx, "session-b", b)), "B's parent's field must not hand B what A's parent holds")

			// Each session's own later calls hit its own: the parent, and the
			// field on it.
			for _, s := range []struct {
				ctx     context.Context
				session string
				want    int
			}{{aCtx, "session-a", 1}, {bCtx, "session-b", 2}} {
				p, err := c.GetOrInitCall(s.ctx, s.session, noopTypeResolver{}, &CallRequest{ResultCall: parentCall.clone()}, func(context.Context) (AnyResult, error) {
					t.Fatalf("%s must hit its own bound parent", s.session)
					return nil, nil
				})
				assert.NilError(t, err)
				again := heldBy(s.ctx, s.session, p)
				assert.Assert(t, again.HitCache(), "%s hits its own field", s.session)
				assert.Equal(t, s.want, cacheTestUnwrapInt(t, again))
			}

			if !made {
				// A result that existed before the bound call held it is an
				// ordinary value: another session's own call of it is served
				// it.
				childCall := cacheTestIntCall("held-child-1")
				res, err := c.GetOrInitCall(bCtx, "session-b", noopTypeResolver{}, &CallRequest{ResultCall: childCall}, func(context.Context) (AnyResult, error) {
					t.Fatal("A's ordinary value must be served to B's own call of it")
					return nil, nil
				})
				assert.NilError(t, err)
				assert.Assert(t, res.HitCache())
			}

			for _, s := range []struct {
				ctx context.Context
				id  string
			}{{aCtx, "session-a"}, {bCtx, "session-b"}} {
				assert.NilError(t, c.ReleaseSession(s.ctx, s.id))
			}
		})
	}
}

// A bound result's identity, which holds its session, never leaves the
// engine: a transfer sends neither the bound result nor what is made on it,
// and no record it sends names a session binding. A field of the bound result
// that returns an ordinary value sends that value by its own call. A bound
// result kept past its session as the dependency of a kept entry
// (TestCacheBoundResultStaysBoundAcrossRestart) is served to no later
// session, nor is anything keyed by its class.
func TestCacheBoundResultsIdentityNeverLeavesTheEngine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	ctx, c, srv := persistedListTestCache(t, path)
	sa := currentEntryTestSession(ctx, c, "session-a")
	frame := &ResultCall{Kind: ResultCallKindField, Field: "bound-identity", Type: NewResultCallType((&transferTestValue{}).Type())}
	bound, err := c.GetOrInitCall(sa, "session-a", srv, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(ctx context.Context) (AnyResult, error) {
		BindToSession(ctx)
		return NewResultForCall(&transferTestValue{Text: "bound"}, frame.clone())
	})
	assert.NilError(t, err)
	plain := persistedListTestResult(t, ctx, c, srv, "ordinary-held", &transferTestValue{Text: "ordinary"})
	on := func(field string, fn func() (AnyResult, error)) AnyResult {
		t.Helper()
		res, err := c.GetOrInitCall(sa, "session-a", srv, &CallRequest{ResultCall: &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType((&transferTestValue{}).Type()),
			Field:    field,
			Receiver: &ResultCallRef{ResultID: uint64(bound.cacheSharedResult().id)},
		}, IsPersistable: true}, func(context.Context) (AnyResult, error) { return fn() })
		assert.NilError(t, err)
		return res
	}
	held := on("held", func() (AnyResult, error) { return plain, nil })
	derivedFrame := &ResultCall{Kind: ResultCallKindField, Field: "derived", Type: NewResultCallType((&transferTestValue{}).Type()), Receiver: &ResultCallRef{ResultID: uint64(bound.cacheSharedResult().id)}}
	derived := on("derived", func() (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: "derived"}, derivedFrame)
	})

	var bundle []byte
	var leftOut []LeftOutRoot
	assert.NilError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{bound, held, derived}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		leftOut = values.LeftOut
		bundle, err = json.Marshal(values.Bundle)
		return err
	}))
	assert.Equal(t, 2, len(leftOut), "the bound result and what is made on it are left out")
	assert.Assert(t, !strings.Contains(string(bundle), boundSessionInputName), "no record sent names a session binding")
	assert.Assert(t, !strings.Contains(string(bundle), string(sessionBoundHandle("session-a"))), "no record sent names the session")

	assert.NilError(t, c.ReleaseSession(sa, "session-a"))
	assert.NilError(t, c.WaitSessionRelease(sa, "session-a"))
	assert.NilError(t, c.Close(ctx))
	ctx, c, srv = persistedListTestCache(t, path)
	later := currentEntryTestSession(ctx, c, "session-later")
	res, err := c.GetOrInitCall(later, "session-later", srv, &CallRequest{ResultCall: frame.clone(), IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: "later"}, frame.clone())
	})
	assert.NilError(t, err)
	assert.Assert(t, !res.HitCache(), "a later session is not served a kept bound result")
	assert.Equal(t, "later", res.Unwrap().(*transferTestValue).Text)
	assert.NilError(t, c.ReleaseSession(later, "session-later"))
}
