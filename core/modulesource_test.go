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

type moduleSourceSelfCallsTestSDK struct {
	moduleSourceAttachTestSDK
}

func (sdk *moduleSourceSelfCallsTestSDK) AlwaysEnablesSelfCalls() bool {
	return true
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

func (sdk *moduleSourceAttachTestSDK) CloneForModuleSource(*ModuleSource) SDK {
	if sdk == nil {
		return nil
	}
	cp := *sdk
	return &cp
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

func TestModuleSourcePersistenceRetainsSelfCallsCapability(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	src := &ModuleSource{
		SDK:     &SDKConfig{Source: "dang"},
		SDKImpl: &moduleSourceSelfCallsTestSDK{},
	}
	require.True(t, src.SelfCallsEnabled())

	encoded, err := src.EncodePersistedObject(ctx, nil)
	require.NoError(t, err)

	decoded, err := (&ModuleSource{}).DecodePersistedObject(ctx, nil, 0, nil, encoded.JSON)
	require.NoError(t, err)
	decodedSrc, ok := decoded.(*ModuleSource)
	require.True(t, ok)
	require.True(t, decodedSrc.SelfCallsEnabled())
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

func TestWorkspaceContextDirPath(t *testing.T) {
	t.Parallel()

	// Workspace.directory resolves relative paths against the workspace cwd but
	// absolute paths against the workspace root. A module's contextual
	// (+defaultPath) path is always root-relative, so the helper must always
	// return an absolute, root-anchored path (relative defaultPath joined onto
	// the module root subpath first).
	testCases := []struct {
		name              string
		sourceRootSubpath string
		defaultPath       string
		expected          string
	}{
		{
			name:              "relative path joins module root subpath and anchors to root",
			sourceRootSubpath: "mod",
			defaultPath:       "sub",
			expected:          "/mod/sub",
		},
		{
			name:              "relative dot resolves to the module root subpath",
			sourceRootSubpath: "mod",
			defaultPath:       ".",
			expected:          "/mod",
		},
		{
			name:              "absolute path ignores the module root subpath",
			sourceRootSubpath: "mod",
			defaultPath:       "/etc",
			expected:          "/etc",
		},
		{
			name:              "empty subpath with dot resolves to the workspace root",
			sourceRootSubpath: "",
			defaultPath:       ".",
			expected:          "/",
		},
		{
			name:              "empty subpath with relative path anchors to root",
			sourceRootSubpath: "",
			defaultPath:       "foo",
			expected:          "/foo",
		},
		{
			name:              "parent traversal stays within the workspace tree",
			sourceRootSubpath: "a/b",
			defaultPath:       "..",
			expected:          "/a",
		},
		{
			name:              "sibling traversal stays within the workspace root",
			sourceRootSubpath: "mod",
			defaultPath:       "../sibling",
			expected:          "/sibling",
		},
		{
			// Root-anchoring clamps any traversal above the workspace root, so
			// the resulting path is always "..-free" and never trips
			// Workspace.directory's escape-the-root rejection.
			name:              "over-escaping traversal clamps to the workspace root",
			sourceRootSubpath: "mod",
			defaultPath:       "../../..",
			expected:          "/",
		},
		{
			name:              "unclean relative path is normalized",
			sourceRootSubpath: "mod",
			defaultPath:       "./sub/./x",
			expected:          "/mod/sub/x",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, workspaceContextDirPath(tc.sourceRootSubpath, tc.defaultPath))
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
