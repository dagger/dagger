package dagql

import (
	"context"
	"sync/atomic"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/engine"
)

// A resolver that borrows a session resource its key doesn't name makes its
// result require that resource: a session that holds it is served the
// result, and one that doesn't computes its own. What borrowed nothing is
// served to every session.
func TestCacheResolverBorrowedSessionResources(t *testing.T) {
	t.Parallel()

	baseCtx := cacheTestContext(t.Context())
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	ctx := ContextWithCache(baseCtx, c)
	srv := cacheTestServer(t)
	handle := cacheTestVolatileSessionResourceHandle("BORROWED")

	var borrowedRuns, publicRuns, sharedRuns, unrequiredRuns atomic.Int32
	borrow := func(ctx context.Context, self ObjectResult[cacheTestQuery], field string) error {
		var held Result[*cacheTestObject]
		if err := srv.Select(ctx, self, &held, Selector{Field: field}); err != nil {
			return err
		}
		return RequireSessionResourcesOf(ctx, held)
	}
	Fields[cacheTestQuery]{
		// holder stands for what a resolver fetches with a session's
		// resource: it depends on the resource.
		NodeFunc("holder", func(ctx context.Context, _ ObjectResult[cacheTestQuery], _ struct{}) (Result[*cacheTestObject], error) {
			leaf, err := cacheTestSessionResourceLeaf(ctx, handle)
			if err != nil {
				return Result[*cacheTestObject]{}, err
			}
			return NewResultForCurrentCall(ctx, &cacheTestObject{Value: 1, dependencyResults: []AnyResult{leaf}})
		}),
		NodeFunc("publicHolder", func(ctx context.Context, _ ObjectResult[cacheTestQuery], _ struct{}) (Result[*cacheTestObject], error) {
			return NewResultForCurrentCall(ctx, &cacheTestObject{Value: 2})
		}),
		NodeFunc("shared", func(ctx context.Context, _ ObjectResult[cacheTestQuery], _ struct{}) (Result[*cacheTestObject], error) {
			return NewResultForCurrentCall(ctx, &cacheTestObject{Value: int(sharedRuns.Add(1))})
		}),
		NodeFunc("unrequired", func(ctx context.Context, _ ObjectResult[cacheTestQuery], _ struct{}) (Result[*cacheTestObject], error) {
			return NewResultForCurrentCall(ctx, &cacheTestObject{Value: int(unrequiredRuns.Add(1))})
		}),
		// borrowed returns a new value, which has no dependency on what it
		// was computed with.
		NodeFunc("borrowed", func(ctx context.Context, self ObjectResult[cacheTestQuery], _ struct{}) (Result[*cacheTestObject], error) {
			if err := borrow(ctx, self, "holder"); err != nil {
				return Result[*cacheTestObject]{}, err
			}
			return NewResultForCurrentCall(ctx, &cacheTestObject{Value: int(borrowedRuns.Add(1))})
		}),
		NodeFunc("borrowedPublic", func(ctx context.Context, self ObjectResult[cacheTestQuery], _ struct{}) (Result[*cacheTestObject], error) {
			if err := borrow(ctx, self, "publicHolder"); err != nil {
				return Result[*cacheTestObject]{}, err
			}
			return NewResultForCurrentCall(ctx, &cacheTestObject{Value: int(publicRuns.Add(1))})
		}),
		// borrowedShared returns a result the cache already holds under
		// another call's key.
		NodeFunc("borrowedShared", func(ctx context.Context, self ObjectResult[cacheTestQuery], _ struct{}) (Result[*cacheTestObject], error) {
			if err := borrow(ctx, self, "holder"); err != nil {
				return Result[*cacheTestObject]{}, err
			}
			var shared Result[*cacheTestObject]
			err := srv.Select(ctx, self, &shared, Selector{Field: "shared"})
			return shared, err
		}),
	}.Install(srv)

	session := func(name string, bound bool) context.Context {
		t.Helper()
		sctx := ContextWithCache(engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
			ClientID:  name + "-client",
			SessionID: name,
		}), c)
		if bound {
			assert.NilError(t, c.BindSessionResource(sctx, name, name+"-client", handle, "bound"))
		}
		t.Cleanup(func() { _ = c.ReleaseSession(context.WithoutCancel(sctx), name) })
		return sctx
	}
	selectObj := func(ctx context.Context, field string) int {
		t.Helper()
		var res Result[*cacheTestObject]
		assert.NilError(t, srv.Select(ctx, srv.Root(), &res, Selector{Field: field}))
		return res.Self().Value
	}

	assert.NilError(t, c.BindSessionResource(ctx, "test-session", "dagql-test-client", handle, "bound"))
	assert.Equal(t, 1, selectObj(ctx, "borrowed"))
	assert.Equal(t, 1, selectObj(ctx, "borrowedPublic"))
	assert.Equal(t, 1, selectObj(ctx, "unrequired"))
	assert.Equal(t, 1, selectObj(ctx, "borrowedShared"))
	assertCacheRequiredSessionResourcesExact(t, c)

	// A session holding the same resource is served every result.
	holding := session("holding", true)
	assert.Equal(t, 1, selectObj(holding, "borrowed"))
	assert.Equal(t, 1, selectObj(holding, "borrowedPublic"))
	assert.Equal(t, 1, selectObj(holding, "borrowedShared"))
	assert.Equal(t, 1, selectObj(holding, "shared"))
	assert.Equal(t, int32(1), borrowedRuns.Load())
	assert.Equal(t, int32(1), sharedRuns.Load())

	// A session without it computes its own result of each call that
	// borrowed it, and of the result one of them returned, which the cache
	// held under another call's key. What borrowed nothing, and what never
	// borrowed, it is served.
	without := session("without", false)
	assert.Equal(t, 2, selectObj(without, "borrowed"))
	assert.Equal(t, 1, selectObj(without, "borrowedPublic"))
	assert.Equal(t, 1, selectObj(without, "unrequired"))
	assert.Equal(t, 2, selectObj(without, "shared"))
	assert.Equal(t, int32(1), publicRuns.Load())
	assert.Equal(t, int32(1), unrequiredRuns.Load())
	assertCacheRequiredSessionResourcesExact(t, c)

	cacheTestReleaseSession(t, c, ctx)
}

// A borrowed result must be attached, so the cache can say what it requires.
func TestRequireSessionResourcesOfRefusesDetachedResult(t *testing.T) {
	t.Parallel()

	baseCtx := cacheTestContext(t.Context())
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	ctx := ContextWithCache(baseCtx, c)
	srv := cacheTestServer(t)
	Fields[cacheTestQuery]{
		NodeFunc("borrowsDetached", func(ctx context.Context, _ ObjectResult[cacheTestQuery], _ struct{}) (Result[*cacheTestObject], error) {
			leaf, err := cacheTestSessionResourceLeaf(ctx, cacheTestVolatileSessionResourceHandle("DETACHED"))
			if err != nil {
				return Result[*cacheTestObject]{}, err
			}
			if err := RequireSessionResourcesOf(ctx, leaf); err != nil {
				return Result[*cacheTestObject]{}, err
			}
			return NewResultForCurrentCall(ctx, &cacheTestObject{})
		}),
	}.Install(srv)

	var res Result[*cacheTestObject]
	err = srv.Select(ctx, srv.Root(), &res, Selector{Field: "borrowsDetached"})
	assert.ErrorContains(t, err, "not an attached result")
	cacheTestReleaseSession(t, c, ctx)
}
