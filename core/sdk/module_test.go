package sdk

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
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

func TestProperty06InitializationArgumentsAreEmptyOrRejectedInStableOrder(t *testing.T) {
	t.Parallel()

	decoded, err := DecodeInitArgs(core.JSON(`{}`))
	require.NoError(t, err)
	require.Empty(t, decoded)

	for seed := int64(0); seed < 256; seed++ {
		rng := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic property schedule
		args := make(map[string]any)
		want := make([]string, 0)
		for index := 0; index < 1+rng.Intn(12); index++ {
			key := fmt.Sprintf("unknown%03d", rng.Intn(128))
			args[key] = rng.Int63()
		}
		for key := range args {
			want = append(want, key)
		}
		slices.Sort(want)
		require.Equal(t, want, unknownInitSDKArgs(args, nil), "seed %d", seed)
	}
}

func TestProperty16ModuleSDKClonesDoNotAliasConfigurationOrFunctions(t *testing.T) {
	t.Parallel()

	base := &module{
		rawConfig: map[string]any{"base": "value"},
		funcs:     map[string]*core.Function{"codegen": {Name: "codegen"}},
	}
	for seed := 0; seed < 128; seed++ {
		left := base.CloneForModuleSource(&core.ModuleSource{}).(*module)
		right := base.CloneForModuleSource(&core.ModuleSource{}).(*module)
		left.rawConfig[fmt.Sprintf("left-%d", seed)] = seed
		left.funcs[fmt.Sprintf("left-%d", seed)] = &core.Function{}
		right.rawConfig[fmt.Sprintf("right-%d", seed)] = seed
		right.funcs[fmt.Sprintf("right-%d", seed)] = &core.Function{}

		require.Len(t, base.rawConfig, 1)
		require.Len(t, base.funcs, 1)
		require.Len(t, left.rawConfig, 2)
		require.Len(t, right.rawConfig, 2)
		require.NotEqual(t, left.rawConfig, right.rawConfig)
		require.NotEqual(t, left.funcs, right.funcs)
	}
}

func TestRustModuleSDKSurfaceReportsOnlyImplementedHooks(t *testing.T) {
	t.Parallel()

	sdk := &module{funcs: map[string]*core.Function{
		"codegen":        {},
		"initModule":     {},
		"generateClient": {},
		"moduleRuntime":  {},
	}}
	_, codegen := sdk.AsCodeGenerator()
	_, initializer := sdk.AsModuleInitializer()
	_, client := sdk.AsClientGenerator()
	_, runtime := sdk.AsRuntime()
	_, runtimeTarget := sdk.AsRuntimeTarget()
	_, clientInitializer := sdk.AsClientInitializer()
	_, moduleTypes := sdk.AsModuleTypes()
	_, asModule := sdk.AsModule()
	require.True(t, codegen)
	require.True(t, initializer)
	require.True(t, client)
	require.True(t, runtime)
	require.True(t, asModule)
	require.False(t, runtimeTarget)
	require.False(t, clientInitializer)
	require.False(t, moduleTypes)
}

func moduleSDKTestSyntheticCall(op string, typ dagql.Typed) *dagql.ResultCall {
	return &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType(typ.Type()),
	}
}
