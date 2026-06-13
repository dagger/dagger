package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
)

type moduleSourceAttachTestSDK struct {
	dep dagql.AnyResult
}

func (sdk *moduleSourceAttachTestSDK) AsRuntime() (Runtime, bool) {
	return nil, false
}

func (sdk *moduleSourceAttachTestSDK) AsModuleTypes() (ModuleTypes, bool) {
	return nil, false
}

func (sdk *moduleSourceAttachTestSDK) AsCodeGenerator() (CodeGenerator, bool) {
	return nil, false
}

func (sdk *moduleSourceAttachTestSDK) AsClientGenerator() (ClientGenerator, bool) {
	return nil, false
}

func (sdk *moduleSourceAttachTestSDK) AsModuleInitializer() (ModuleInitializer, bool) {
	return nil, false
}

func (sdk *moduleSourceAttachTestSDK) AsClientInitializer() (ClientInitializer, bool) {
	return nil, false
}

func (sdk *moduleSourceAttachTestSDK) AsRuntimeTarget() (RuntimeTarget, bool) {
	return nil, false
}

func (sdk *moduleSourceAttachTestSDK) AttachDependencyResults(
	ctx context.Context,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	attached, err := attach(sdk.dep)
	if err != nil {
		return nil, err
	}
	sdk.dep = attached
	return []dagql.AnyResult{attached}, nil
}

func TestModuleSourceAttachDependencyResultsRetainsSDKImpl(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dep, err := dagql.NewResultForCall(
		dagql.String("sdk dependency"),
		moduleSourceTestSyntheticCall("moduleSourceSDKDep", dagql.String("")),
	)
	require.NoError(t, err)
	attachedDep, err := dagql.NewResultForCall(
		dagql.String("attached sdk dependency"),
		moduleSourceTestSyntheticCall("moduleSourceAttachedSDKDep", dagql.String("")),
	)
	require.NoError(t, err)

	sdk := &moduleSourceAttachTestSDK{dep: dep}
	src := &ModuleSource{SDKImpl: sdk}

	deps, err := src.AttachDependencyResults(ctx, nil, func(res dagql.AnyResult) (dagql.AnyResult, error) {
		require.Equal(t, dep.Unwrap(), res.Unwrap())
		return attachedDep, nil
	})
	require.NoError(t, err)
	require.Len(t, deps, 1)
	require.Equal(t, attachedDep.Unwrap(), deps[0].Unwrap())
	require.Equal(t, attachedDep.Unwrap(), sdk.dep.Unwrap())
}

func TestGitModuleSourceSymbolic(t *testing.T) {
	testCases := []struct {
		name        string
		cloneRef    string
		rootSubpath string
		expected    string
	}{
		{
			name:        "Go-style URL",
			cloneRef:    "https://github.com/user/repo.git",
			rootSubpath: "subdir",
			expected:    "https://github.com/user/repo.git/subdir",
		},
		{
			name:        "SCP-like reference",
			cloneRef:    "git@github.com:user/repo.git",
			rootSubpath: "subdir",
			expected:    "git@github.com:user/repo.git/subdir",
		},
		{
			name:        "SCP-like reference with no subdir",
			cloneRef:    "git@github.com:user/repo.git",
			rootSubpath: "",
			expected:    "git@github.com:user/repo.git",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			src := &ModuleSource{
				Kind: ModuleSourceKindGit,
				Git: &GitModuleSource{
					CloneRef: tc.cloneRef,
				},
				SourceRootSubpath: tc.rootSubpath,
			}
			result := src.AsString()
			require.Equal(t, tc.expected, result, "AsString() returned unexpected result")
		})
	}
}

func moduleSourceTestSyntheticCall(op string, typ dagql.Typed) *dagql.ResultCall {
	return &dagql.ResultCall{
		Kind:        dagql.ResultCallKindSynthetic,
		SyntheticOp: op,
		Type:        dagql.NewResultCallType(typ.Type()),
	}
}
