package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestModuleFunctionCacheImplicitInputs(t *testing.T) {
	sessionCtx := engine.ContextWithClientMetadata(context.Background(), &engine.ClientMetadata{
		ClientID:  "client-1",
		SessionID: "session-1",
	})

	for _, tc := range []struct {
		name               string
		mod                *Module
		fn                 *Function
		expectCallScope    bool
		expectSessionScope bool
	}{
		{
			name: "default policy",
			mod: &Module{
				NameField: "test",
			},
			fn: &Function{
				Name: "fn",
			},
			expectSessionScope: false,
		},
		{
			name: "explicit per-session policy",
			mod: &Module{
				NameField: "test",
			},
			fn: &Function{
				Name:        "fn",
				CachePolicy: FunctionCachePolicyPerSession,
			},
			expectSessionScope: true,
		},
		{
			name: "default policy with module disable-default-caching",
			mod: &Module{
				NameField:                     "test",
				DisableDefaultFunctionCaching: true,
			},
			fn: &Function{
				Name: "fn",
			},
			expectSessionScope: true,
		},
		{
			name: "explicit never policy",
			mod: &Module{
				NameField: "test",
			},
			fn: &Function{
				Name:        "fn",
				CachePolicy: FunctionCachePolicyNever,
			},
			expectCallScope: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modRes, err := dagql.NewObjectResultForCall(
				tc.mod,
				func() *dagql.Server {
					dag := newCoreDagqlServerForTest(t, &Query{})
					dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Module]{Typed: &Module{}}))
					return dag
				}(),
				&dagql.ResultCall{
					Kind:        dagql.ResultCallKindSynthetic,
					SyntheticOp: "modfunc_test_module",
					Type:        dagql.NewResultCallType((&Module{}).Type()),
				},
			)
			require.NoError(t, err)

			modFn := &ModuleFunction{
				mod:      modRes,
				metadata: tc.fn,
			}

			inputs := modFn.cacheImplicitInputs()
			byName := mapImplicitInputsByName(inputs)
			callInput, hasCallInput := byName[dagql.PerCallInput.Name]
			require.Equal(t, tc.expectCallScope, hasCallInput)
			if tc.expectCallScope {
				require.NotEmpty(t, resolveImplicitInputString(t, callInput, sessionCtx))
			}

			sessionInput, hasSessionInput := byName[dagql.PerSessionInput.Name]
			require.Equal(t, tc.expectSessionScope, hasSessionInput)
			if tc.expectSessionScope {
				require.Equal(
					t,
					"session-1",
					resolveImplicitInputString(t, sessionInput, sessionCtx),
				)
			}
		})
	}
}

func TestModuleFunctionCacheImplicitInputsNilSafe(t *testing.T) {
	require.Nil(t, (*ModuleFunction)(nil).cacheImplicitInputs())
	require.Nil(t, (&ModuleFunction{}).cacheImplicitInputs())
	dag := newCoreDagqlServerForTest(t, &Query{})
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*Module]{Typed: &Module{}}))
	modRes, err := dagql.NewObjectResultForCall(
		&Module{},
		dag,
		&dagql.ResultCall{
			Kind:        dagql.ResultCallKindSynthetic,
			SyntheticOp: "modfunc_test_nil_safe_module",
			Type:        dagql.NewResultCallType((&Module{}).Type()),
		},
	)
	require.NoError(t, err)
	require.Nil(t, (&ModuleFunction{
		mod: modRes,
	}).cacheImplicitInputs())
}

// TestCallerInModuleFunction verifies the gate keys off an active function call,
// not merely a module in context: a client that only has a module loaded (e.g.
// `dagger generate` walking a schema) is not treated as a runtime caller, while
// an active function call is.
func TestCallerInModuleFunction(t *testing.T) {
	t.Parallel()

	// Module in context but no active function call: not a runtime caller.
	moduleOnly := ContextWithQuery(t.Context(), &Query{Server: &mockServer{
		moduleSource: &ModuleSource{
			Kind:  ModuleSourceKindLocal,
			Local: &LocalModuleSource{ContextDirectoryPath: "."},
		},
	}})
	got, err := callerInModuleFunction(moduleOnly)
	require.NoError(t, err)
	require.False(t, got)

	// An active function call: a runtime caller.
	inFn := ContextWithQuery(t.Context(), &Query{Server: &mockServer{
		functionCall: &FunctionCall{Name: "caller"},
	}})
	got, err = callerInModuleFunction(inFn)
	require.NoError(t, err)
	require.True(t, got)
}

// TestModuleFunctionLoadWorkspaceArgRejectsFunctionCallRuntimeCaller verifies an
// omitted Workspace argument is not filled from caller context when a function
// call is active (i.e. a module function calling a dependency).
func TestModuleFunctionLoadWorkspaceArgRejectsFunctionCallRuntimeCaller(t *testing.T) {
	t.Parallel()

	query := &Query{
		Server: &mockServer{
			functionCall: &FunctionCall{Name: "caller"},
		},
	}
	ctx := ContextWithQuery(t.Context(), query)
	dag := newCoreDagqlServerForTest(t, query)

	_, err := (&ModuleFunction{}).loadWorkspaceArg(ctx, dag)
	require.ErrorIs(t, err, ErrNoCurrentWorkspace)
	require.Contains(t, err.Error(), "workspace arguments are not inherited by module runtime calls")
	require.Contains(t, err.Error(), "pass a Workspace explicitly")
}

func mapImplicitInputsByName(inputs []dagql.ImplicitInput) map[string]dagql.ImplicitInput {
	byName := make(map[string]dagql.ImplicitInput, len(inputs))
	for _, input := range inputs {
		byName[input.Name] = input
	}
	return byName
}

func resolveImplicitInputString(t *testing.T, input dagql.ImplicitInput, ctx context.Context) string {
	t.Helper()

	value, err := input.Resolver(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, value)

	strVal, ok := value.(dagql.String)
	require.True(t, ok, "expected dagql.String implicit input value, got %T", value)
	return strVal.String()
}

type runtimeLessTestSDK struct {
	moduleOwnershipRuntime
}

func (*runtimeLessTestSDK) HasNoRuntime() bool { return true }

func TestModuleFunctionLoadRuntimeSpan(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sdk   SDK
		spans []string
	}{
		{name: "runtime", sdk: &moduleOwnershipRuntime{}, spans: []string{"load sdk runtime"}},
		{name: "no runtime", sdk: &runtimeLessTestSDK{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newModuleOwnershipTest(t)
			src := newTypeDefDetachedResult(t, f.dag, "load-runtime-source", &ModuleSource{
				Kind:    ModuleSourceKindDir,
				SDKImpl: tc.sdk,
			})
			mod := newTypeDefDetachedResult(t, f.dag, "load-runtime-module", &Module{
				NameField: "test",
				Source:    dagql.NonNull(src),
			})

			sr := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
			ctx, root := tp.Tracer("modfunc-test").Start(t.Context(), "call")
			_, err := (&ModuleFunction{mod: mod}).loadFunctionRuntime(ctx)
			root.End()
			require.NoError(t, err)

			var spans []string
			for _, span := range sr.Ended() {
				if span.Name() != "call" {
					spans = append(spans, span.Name())
				}
			}
			require.Equal(t, tc.spans, spans)
		})
	}
}

type unkeyedReceiverScope struct{}

func (unkeyedReceiverScope) Apply(_ context.Context, _ *dagql.Server, value dagql.AnyObjectResult) (dagql.AnyObjectResult, error) {
	return value, nil
}

func TestModuleFunctionReceiverConvertedOncePerSession(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	installModuleObjectTestModuleClass(srv)
	installTypeDefTestClasses(srv)
	sessionCtx := func(session string) context.Context {
		return engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: session + "-client", SessionID: session})
	}
	ctx = sessionCtx("receiver-session")

	holderType := newTypeDefAttachedResult(t, ctx, cache, srv, "receiver-holder-type", NewObjectTypeDef("Holder", "", nil)).Self()
	holder := &ModuleObject{TypeDef: holderType, Fields: map[string]any{"state": "before"}}
	srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: holder}))
	receiver, err := dagql.NewObjectResultForCall(holder, srv, moduleObjectTestSyntheticCall("receiver-holder", holder))
	require.NoError(t, err)
	attached, err := cache.AttachResult(ctx, "receiver-session", srv, receiver)
	require.NoError(t, err)
	attachedObj, ok := dagql.UnwrapAs[*ModuleObject](attached)
	require.True(t, ok)
	opts := &CallOpts{ParentTyped: attached, ParentFields: attachedObj.Fields}

	first, err := moduleFunctionReceiverJSON(ctx, opts)
	require.NoError(t, err)
	require.JSONEq(t, `{"state":"before"}`, string(first))

	// Stored fields never change in place; changing them here shows whether
	// a call converts the receiver again.
	attachedObj.Fields["state"] = "after"
	again, err := moduleFunctionReceiverJSON(ctx, opts)
	require.NoError(t, err)
	require.Equal(t, string(first), string(again), "a second call in the session must reuse the converted receiver")

	other, err := moduleFunctionReceiverJSON(sessionCtx("other-session"), opts)
	require.NoError(t, err)
	require.JSONEq(t, `{"state":"after"}`, string(other), "another session must convert the receiver itself")

	unkeyed, err := moduleFunctionReceiverJSON(dagql.WithResultScope(ctx, unkeyedReceiverScope{}), opts)
	require.NoError(t, err)
	require.JSONEq(t, `{"state":"after"}`, string(unkeyed), "a scope without an identity must not reuse a conversion")
}

func TestModuleFunctionReceiverConvertedPerArtifactView(t *testing.T) {
	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	root := &Query{}
	server := &moduleObjectTestServer{mockServer: &mockServer{}, cache: cache, root: root}
	root.Server = server
	srv := newCoreDagqlServerForTest(t, root)
	server.dag = srv
	installModuleObjectTestModuleClass(srv)
	installTypeDefTestClasses(srv)
	ctx = engine.ContextWithClientMetadata(ContextWithQuery(dagql.ContextWithCache(ctx, cache), root), &engine.ClientMetadata{ClientID: "view-receiver-client", SessionID: "view-receiver-session"})
	obj, _ := collectionTestObject(t, TypeDefKindString, []any{"a", "b", "c"})
	obj.TypeDef = newTypeDefAttachedResult(t, ctx, cache, srv, "view-receiver-type", obj.TypeDef).Self()
	source := newTypeDefAttachedResult(t, ctx, cache, srv, "view-receiver-source", &ModuleSource{Kind: ModuleSourceKindLocal, Local: &LocalModuleSource{ContextDirectoryPath: "/test"}})
	obj.Module = newTypeDefAttachedResult(t, ctx, cache, srv, "view-receiver-module", &Module{NameField: "test", Source: dagql.NonNull(source), Deps: NewSchemaBuilder(nil, nil)})
	class := dagql.NewClass(srv, dagql.ClassOpts[*ModuleObject]{Typed: obj})
	viewField, err := obj.artifactViewField(srv)
	require.NoError(t, err)
	viewField.Spec.Module, viewField.Spec.ModuleProvider = nil, nil
	class.Install(viewField)
	srv.InstallObject(class)

	typ := &ModuleObjectType{mod: obj.Module, typeDef: obj.TypeDef}
	receiver, err := typ.ConvertFromSDKResult(dagql.ContextWithCall(ctx, moduleObjectTestSyntheticCall("view-receiver", obj)), map[string]any{"Keys": []any{"a", "b", "c"}})
	require.NoError(t, err)
	receiver, err = cache.AttachResult(ctx, "view-receiver-session", srv, receiver)
	require.NoError(t, err)
	opts := &CallOpts{ParentTyped: receiver}

	for _, key := range []string{"b", "c", "b"} {
		view := &ArtifactView{state: artifactViewState{Rules: []artifactViewRule{{Dimension: "/cases", Type: obj.TypeDef.Name, Keys: []string{key}, UniqueType: true}}}}
		require.NoError(t, view.setKey())
		viewCtx := dagql.WithResultScope(ctx, view)
		direct, err := moduleFunctionReceiverFields(viewCtx, opts)
		require.NoError(t, err)
		want, err := json.Marshal(direct)
		require.NoError(t, err)
		got, err := moduleFunctionReceiverJSON(viewCtx, opts)
		require.NoError(t, err)
		require.Equal(t, string(want), string(got), "each view must receive its own filtered receiver")
		require.Equal(t, []any{key}, direct["Keys"])
	}
}
