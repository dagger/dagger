package sdk

import (
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

func moduleSDKTestSyntheticCall(op string, typ dagql.Typed) *dagql.ResultCall {
	return &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType(typ.Type()),
	}
}
