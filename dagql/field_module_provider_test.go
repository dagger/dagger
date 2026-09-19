package dagql

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

type fieldModuleProviderTestServer struct {
	srv   *Server
	cache *Cache
}

func newFieldModuleProviderTestServer(t *testing.T) *fieldModuleProviderTestServer {
	t.Helper()
	cache, err := NewCache(t.Context(), "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	srv := newIdentityOptTestServer(t)
	Fields[identityOptTestQuery]{
		Func("toolModule", func(context.Context, identityOptTestQuery, struct{}) (Int, error) {
			return NewInt(1), nil
		}),
	}.Install(srv)
	return &fieldModuleProviderTestServer{srv: srv, cache: cache}
}

func (f *fieldModuleProviderTestServer) sessionContext(t *testing.T, session string) context.Context {
	t.Helper()
	ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{
		ClientID:  session,
		SessionID: session,
	})
	ctx = ContextWithCache(ctx, f.cache)
	t.Cleanup(func() { _ = f.cache.ReleaseSession(context.Background(), session) })
	return ctx
}

func (f *fieldModuleProviderTestServer) install(name string, module *ResultCallModule, provider FieldModuleProvider) *int {
	calls := new(int)
	Fields[identityOptTestQuery]{
		{
			Spec: &FieldSpec{
				Name:           name,
				Type:           String(""),
				Module:         module,
				ModuleProvider: provider,
			},
			Func: func(ctx context.Context, _ ObjectResult[identityOptTestQuery], _ map[string]Input, _ call.View) (AnyResult, error) {
				*calls++
				return NewResultForCurrentCall(ctx, NewString("value"))
			},
		},
	}.Install(f.srv)
	return calls
}

// A field's module provider runs while each call is prepared, with the calling
// context and the server the selection went through. The static description
// stays on the spec; only the result reference comes from the provider, and it
// is a reference the calling session acquired.
func TestFieldModuleProviderSuppliesResultReferencePerCall(t *testing.T) {
	f := newFieldModuleProviderTestServer(t)
	static := &ResultCallModule{Name: "mod", Ref: "ref", Pin: "pin"}

	var providerCalls int
	var seenSessions []string
	var seenServers []*Server
	provider := func(ctx context.Context, srv *Server) (*ResultCallModule, error) {
		providerCalls++
		md, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, err
		}
		seenSessions = append(seenSessions, md.SessionID)
		seenServers = append(seenServers, srv)
		var module AnyResult
		if err := srv.Select(ctx, srv.Root(), &module, Selector{Field: "toolModule"}); err != nil {
			return nil, err
		}
		id, err := module.ID()
		if err != nil {
			return nil, err
		}
		return &ResultCallModule{
			ResultRef: &ResultCallRef{ResultID: id.EngineResultID()},
			Name:      static.Name,
			Ref:       static.Ref,
			Pin:       static.Pin,
		}, nil
	}
	fieldCalls := f.install("provided", static, provider)
	spec, ok := f.srv.Root().ObjectType().FieldSpec("provided", "")
	require.True(t, ok)
	require.Nil(t, spec.Module.ResultRef, "the installed spec must stay free of result references")

	firstCtx := f.sessionContext(t, "provider-first")
	first, err := f.srv.Root().Select(firstCtx, f.srv, Selector{Field: "provided"})
	require.NoError(t, err)
	require.Equal(t, 1, providerCalls)
	require.Equal(t, 1, *fieldCalls)
	require.Equal(t, []string{"provider-first"}, seenSessions)
	require.Same(t, f.srv, seenServers[0])
	firstFrame, err := first.ResultCall()
	require.NoError(t, err)
	require.NotNil(t, firstFrame.Module)
	require.Equal(t, "mod", firstFrame.Module.Name)
	require.Equal(t, "ref", firstFrame.Module.Ref)
	require.Equal(t, "pin", firstFrame.Module.Pin)
	require.NotZero(t, firstFrame.Module.ResultRef.ResultID)
	require.Nil(t, firstFrame.Module.ResultRef.Call, "runtime frames stay result-backed")
	firstID, err := first.RecipeID(firstCtx)
	require.NoError(t, err)
	require.NotNil(t, firstID.Module())
	require.Equal(t, "mod", firstID.Module().Name())
	require.Equal(t, "toolModule", firstID.Module().ID().Field())
	require.Nil(t, spec.Module.ResultRef, "dispatch must not write the reference back into the schema")

	// A later session re-runs the provider in its own context; the cached
	// field result is still shared because the module reference is the same
	// row.
	nextCtx := f.sessionContext(t, "provider-next")
	next, err := f.srv.Root().Select(nextCtx, f.srv, Selector{Field: "provided"})
	require.NoError(t, err)
	require.Equal(t, 2, providerCalls)
	require.Equal(t, 1, *fieldCalls, "the module reference is stable, so the call hits the cache")
	require.Equal(t, []string{"provider-first", "provider-next"}, seenSessions)
	nextID, err := next.RecipeID(nextCtx)
	require.NoError(t, err)
	require.Equal(t, firstID.Digest(), nextID.Digest())
}

func TestFieldModuleProviderErrorsFailThePreparedCall(t *testing.T) {
	f := newFieldModuleProviderTestServer(t)
	static := &ResultCallModule{Name: "mod"}
	ctx := f.sessionContext(t, "provider-errors")

	boom := errors.New("boom")
	failingCalls := f.install("failing", static, func(context.Context, *Server) (*ResultCallModule, error) {
		return nil, boom
	})
	_, err := f.srv.Root().Select(ctx, f.srv, Selector{Field: "failing"})
	require.ErrorIs(t, err, boom)
	require.ErrorContains(t, err, "failed to resolve module for Query.failing")
	require.Zero(t, *failingCalls, "the resolver must not run without module provenance")

	staticOnlyCalls := f.install("staticOnly", static, func(context.Context, *Server) (*ResultCallModule, error) {
		return &ResultCallModule{Name: "mod"}, nil
	})
	_, err = f.srv.Root().Select(ctx, f.srv, Selector{Field: "staticOnly"})
	require.ErrorContains(t, err, "module provider returned no result reference")
	require.Zero(t, *staticOnlyCalls)

	nilCalls := f.install("nilModule", static, func(context.Context, *Server) (*ResultCallModule, error) {
		return nil, nil
	})
	_, err = f.srv.Root().Select(ctx, f.srv, Selector{Field: "nilModule"})
	require.ErrorContains(t, err, "module provider returned no result reference")
	require.Zero(t, *nilCalls)
}

// Fields without a provider keep today's behavior: a static module description
// is cloned into the prepared frame as-is (a frame with a static module and no
// result reference still cannot derive a digest, exactly as before), and
// builtin fields carry no module at all.
func TestFieldModuleWithoutProviderIsUnchanged(t *testing.T) {
	f := newFieldModuleProviderTestServer(t)
	ctx := f.sessionContext(t, "provider-absent")
	root, ok := f.srv.Root().(ObjectResult[identityOptTestQuery])
	require.True(t, ok)

	static := &ResultCallModule{Name: "static", Ref: "ref", Pin: "pin"}
	f.install("staticModule", static, nil)
	_, preselected, err := root.preselect(ctx, f.srv, Selector{Field: "staticModule"})
	require.NoError(t, err)
	frame := preselected.request.ResultCall
	require.NotNil(t, frame.Module)
	require.NotSame(t, static, frame.Module, "the frame must hold its own copy")
	require.Equal(t, static.Name, frame.Module.Name)
	require.Equal(t, static.Ref, frame.Module.Ref)
	require.Equal(t, static.Pin, frame.Module.Pin)
	require.Nil(t, frame.Module.ResultRef)
	_, err = f.srv.Root().Select(ctx, f.srv, Selector{Field: "staticModule"})
	require.ErrorContains(t, err, "missing result ref")

	f.install("builtin", nil, nil)
	res, err := f.srv.Root().Select(ctx, f.srv, Selector{Field: "builtin"})
	require.NoError(t, err)
	frame, err = res.ResultCall()
	require.NoError(t, err)
	require.Nil(t, frame.Module)
	id, err := res.RecipeID(ctx)
	require.NoError(t, err)
	require.Nil(t, id.Module())
}
