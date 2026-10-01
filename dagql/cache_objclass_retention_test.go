package dagql

import (
	"context"
	"runtime"
	"testing"
	"weak"

	"gotest.tools/v3/assert"

	"github.com/dagger/dagger/engine"
)

// A cached object result remembers the class that first wrapped it, so a
// reader whose schema lacks the type can reuse it. Module classes have field
// resolvers that capture the server they were installed into, so the cache
// entry must not keep that class, and with it the whole server, alive.
func TestCacheObjClassDoesNotRetainServer(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	cache, err := NewCache(ctx, "", nil, nil)
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, cache)

	shared, producer := func() (*sharedResult, weak.Pointer[Server]) {
		srv := cacheTestServer(t)
		Fields[cacheTestQuery]{
			NodeFunc("obj", func(ctx context.Context, _ ObjectResult[cacheTestQuery], _ struct{}) (Result[*cacheTestObject], error) {
				return NewResultForCurrentCall(ctx, &cacheTestObject{Value: 7})
			}).IsPersistable(),
		}.Install(srv)
		// Like a module object's fields, a resolver that captures its server.
		Fields[*cacheTestObject]{
			Func("view", func(context.Context, *cacheTestObject, struct{}) (String, error) {
				return NewString(string(srv.View)), nil
			}),
		}.Install(srv)

		ctx := srvToContext(ctx, srv)
		obj, err := srv.Root().Select(ctx, srv, Selector{Field: "obj"})
		assert.NilError(t, err)
		shared := obj.cacheSharedResult()
		// While the producing server is alive, a reader without the type
		// reuses its class.
		reader := newDagqlServerForTest(t, cacheTestQuery{})
		rewrapped, err := wrapSharedResultWithResolver(ctx, shared, true, reader)
		assert.NilError(t, err)
		_, ok := rewrapped.(AnyObjectResult).ObjectType().FieldSpec("view", "")
		assert.Assert(t, ok)
		cacheTestReleaseSession(t, cache, ctx)
		return shared, weak.Make(srv)
	}()

	for range 3 {
		runtime.GC()
	}
	assert.Assert(t, producer.Value() == nil, "producing server is still reachable from the cache entry")

	// With the producing server gone, a reader without the type rebuilds a
	// schema that has it from the result's call, and remembers that class.
	ctx = ContextWithCache(engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  "reader-client",
		SessionID: "reader-session",
	}), cache)
	resolved := cacheTestServer(t)
	reader := newDagqlServerForTest(t, cacheTestQuery{})
	_, err = wrapSharedResultWithResolver(ctx, shared, true, reader)
	assert.ErrorContains(t, err, "unknown object type")
	reader.SetResultServerForCall(func(context.Context, *ResultCall) (*Server, error) {
		return resolved, nil
	})
	rewrapped, err := wrapSharedResultWithResolver(ctx, shared, true, reader)
	assert.NilError(t, err)
	value, err := rewrapped.(AnyObjectResult).Select(srvToContext(ctx, resolved), resolved, Selector{Field: "value"})
	assert.NilError(t, err)
	assert.Equal(t, 7, cacheTestUnwrapInt(t, value))
	class, ok := shared.loadPayloadState().objClass.load(rewrapped.Type().Name())
	assert.Assert(t, ok)
	_, ok = class.FieldSpec("view", "")
	assert.Assert(t, !ok, "remembered class should be the resolved server's")
	cacheTestReleaseSession(t, cache, ctx)
}
