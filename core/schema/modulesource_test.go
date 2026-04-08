package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/stretchr/testify/require"
)

func TestModuleLocaLSource(t *testing.T) {
	schema := &moduleSourceSchema{}
	ctx := context.Background()

	src := &core.ModuleSource{
		Kind: core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/home/user/dagger-test-modules",
		},
	}

	for _, tc := range []struct {
		name           string
		source         *core.ModuleSource
		expectedResult string
		expectError    bool
		fn             func(ctx context.Context, source *core.ModuleSource, args struct{}) (string, error)
	}{
		{
			name:           "Local module source commit",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceCommit,
		},
		{
			name:           "Local module source html repo url",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceHTMLRepoURL,
		},
		{
			name:           "Local module source version",
			source:         src,
			expectedResult: "",
			expectError:    false,
			fn:             schema.moduleSourceVersion,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.fn(ctx, tc.source, struct{}{})
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedResult, result)
			}
		})
	}
}

func TestLegacyWorkspaceFieldHandling(t *testing.T) {
	t.Parallel()

	local := &core.ModuleSource{
		Kind: core.ModuleSourceKindLocal,
		Local: &core.LocalModuleSource{
			ContextDirectoryPath: "/work/repo-b",
		},
		SourceRootSubpath: ".",
		ConfigBlueprint: &modules.ModuleConfigDependency{
			Name:   "bp",
			Source: "./blueprint",
		},
		ConfigToolchains: []*modules.ModuleConfigDependency{{
			Name:   "go",
			Source: "./toolchains/go",
		}},
	}

	require.True(t, local.UsesLegacyWorkspaceFields())
	require.Equal(t, []string{"blueprint", "toolchains"}, local.LegacyWorkspaceFieldNames())

	stripped := local.StripLegacyWorkspaceFields()
	require.Nil(t, stripped.ConfigBlueprint)
	require.Nil(t, stripped.ConfigToolchains)
	require.False(t, stripped.UsesLegacyWorkspaceFields())

	require.EqualError(t,
		local.DirectLegacyWorkspaceLoadError(),
		"cannot load this ref as a module: its dagger.json uses legacy workspace fields \"blueprint, toolchains\"\n\nload it as a workspace instead, for example with `-W`",
	)
	require.EqualError(t,
		local.NestedLegacyWorkspaceLoadError(),
		"workspace module source \"/work/repo-b\" points at a legacy workspace, not a plain module: its dagger.json uses legacy workspace fields \"blueprint, toolchains\"\n\nrun `dagger migrate` in \"/work/repo-b\", then update this source to point at one of the migrated modules under \"/work/repo-b/.dagger/modules\"",
	)

	remote := &core.ModuleSource{
		Kind: core.ModuleSourceKindGit,
		Git: &core.GitModuleSource{
			CloneRef: "https://github.com/acme/repo-b",
			Version:  "main",
		},
		SourceRootSubpath: ".",
		ConfigBlueprint: &modules.ModuleConfigDependency{
			Name:   "bp",
			Source: "./blueprint",
		},
	}

	require.EqualError(t,
		remote.NestedLegacyWorkspaceLoadError(),
		"workspace module source \"https://github.com/acme/repo-b@main\" points at a legacy workspace, not a plain module: its dagger.json uses legacy workspace fields \"blueprint\"\n\nuse a migrated ref that points at one of its real modules. If you control that repo, migrate it first",
	)
}
