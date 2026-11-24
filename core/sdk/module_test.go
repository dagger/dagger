package sdk

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

func TestModuleSDKAttachDependencyResultsRetainsImplementationModuleAndSourceDir(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	root := &core.Query{}
	dag, err := dagql.NewServer(ctx, root)
	require.NoError(t, err)
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Module]{Typed: &core.Module{}}))
	dag.InstallObject(dagql.NewClass(dag, dagql.ClassOpts[*core.Directory]{Typed: &core.Directory{}}))

	mod, err := dagql.NewObjectResultForCall(
		&core.Module{NameField: "sdk"},
		dag,
		moduleSDKTestSyntheticCall("sdkModule", &core.Module{}),
	)
	require.NoError(t, err)
	sourceDir, err := dagql.NewObjectResultForCall(
		&core.Directory{},
		dag,
		moduleSDKTestSyntheticCall("sdkSourceDir", &core.Directory{}),
	)
	require.NoError(t, err)

	attachedMod, err := dagql.NewObjectResultForCall(
		&core.Module{NameField: "attached-sdk"},
		dag,
		moduleSDKTestSyntheticCall("attachedSDKModule", &core.Module{}),
	)
	require.NoError(t, err)
	attachedSourceDir, err := dagql.NewObjectResultForCall(
		&core.Directory{},
		dag,
		moduleSDKTestSyntheticCall("attachedSDKSourceDir", &core.Directory{}),
	)
	require.NoError(t, err)

	sdk := &module{
		mod:                      mod,
		optionalFullSDKSourceDir: sourceDir,
		funcs: map[string]*core.Function{
			"stale": {},
		},
	}

	deps, err := sdk.AttachDependencyResults(ctx, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		switch res.(type) {
		case dagql.ObjectResult[*core.Module]:
			return attachedMod, nil
		case dagql.ObjectResult[*core.Directory]:
			return attachedSourceDir, nil
		default:
			t.Fatalf("unexpected sdk dependency result %T", res)
			return nil, nil
		}
	})
	require.NoError(t, err)
	require.Len(t, deps, 2)

	require.Empty(t, sdk.funcs)
	require.Same(t, attachedMod.Self(), sdk.mod.Self())
	require.Same(t, attachedSourceDir.Self(), sdk.optionalFullSDKSourceDir.Self())
}

func moduleSDKTestSyntheticCall(op string, typ dagql.Typed) *dagql.ResultCall {
	return &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType(typ.Type()),
	}
}

func TestModuleSDKInstanceServedOncePerSession(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = engine.ContextWithClientMetadata(dagql.ContextWithCache(ctx, cache), &engine.ClientMetadata{ClientID: "cli", SessionID: "session"})

	builds := 0
	build := func(context.Context) (*moduleInstance, error) {
		builds++
		return &moduleInstance{}, nil
	}
	root := &core.Query{}
	key, err := moduleInstanceKey("session", root, 1, 0, map[string]any{"a": "x"})
	require.NoError(t, err)

	// A module's types, codegen and runtime calls, and the next module's,
	// share the SDK instance the session's first call served.
	first, err := sessionModuleInstance(ctx, key, build)
	require.NoError(t, err)
	second, err := sessionModuleInstance(ctx, key, build)
	require.NoError(t, err)
	require.Same(t, first, second)
	require.Equal(t, 1, builds)

	// Another configuration of the same SDK is another instance.
	other, err := moduleInstanceKey("session", root, 1, 0, map[string]any{"a": "y"})
	require.NoError(t, err)
	third, err := sessionModuleInstance(ctx, other, build)
	require.NoError(t, err)
	require.NotSame(t, first, third)
	require.Equal(t, 2, builds)
}

func TestModuleSDKInstanceFailedBuildNotKept(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	cache, err := dagql.NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close(context.Background())) })
	ctx = engine.ContextWithClientMetadata(dagql.ContextWithCache(ctx, cache), &engine.ClientMetadata{ClientID: "cli", SessionID: "session"})

	key, err := moduleInstanceKey("session", &core.Query{}, 1, 0, nil)
	require.NoError(t, err)
	_, err = sessionModuleInstance(ctx, key, func(context.Context) (*moduleInstance, error) {
		return nil, errors.New("sdk failed")
	})
	require.ErrorContains(t, err, "sdk failed")
	built := false
	_, err = sessionModuleInstance(ctx, key, func(context.Context) (*moduleInstance, error) {
		built = true
		return &moduleInstance{}, nil
	})
	require.NoError(t, err)
	require.True(t, built, "a failed build must not stick for the session")
}

func TestModuleSDKInstanceKey(t *testing.T) {
	t.Parallel()

	root := &core.Query{}
	key := func(session string, root *core.Query, mod, src uint64, config map[string]any) string {
		k, err := moduleInstanceKey(session, root, mod, src, config)
		require.NoError(t, err)
		return k
	}
	base := key("s", root, 1, 2, map[string]any{"a": "x", "b": true})
	require.Equal(t, base, key("s", root, 1, 2, map[string]any{"b": true, "a": "x"}), "equal configs key alike")
	for name, other := range map[string]string{
		"session": key("t", root, 1, 2, map[string]any{"a": "x", "b": true}),
		"root":    key("s", &core.Query{}, 1, 2, map[string]any{"a": "x", "b": true}),
		"module":  key("s", root, 3, 2, map[string]any{"a": "x", "b": true}),
		"source":  key("s", root, 1, 4, map[string]any{"a": "x", "b": true}),
		"config":  key("s", root, 1, 2, map[string]any{"a": "x"}),
	} {
		require.NotEqual(t, base, other, name)
	}
}
