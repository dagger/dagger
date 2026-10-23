package schema

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/stretchr/testify/require"
)

func TestCollectWorkspaceModuleReadPaths(t *testing.T) {
	configs := map[string]*modules.ModuleConfig{
		"app": {
			Source:       "src",
			Dependencies: []*modules.ModuleConfigDependency{{Source: "../shared"}, {Source: "../../outside"}},
			SDK:          &modules.SDK{Source: "../custom-sdk"},
		},
		"shared":     {Dependencies: []*modules.ModuleConfigDependency{{Source: "../leaf"}, {Source: "../app"}}},
		"leaf":       {SDK: &modules.SDK{Source: "go"}},
		"custom-sdk": {Dependencies: []*modules.ModuleConfigDependency{{Source: "../leaf"}}},
	}
	reads := map[string]int{}
	moduleDirs, contentDirs, err := collectWorkspaceModuleReadPaths([]string{"app", "app"}, func(dir string) (*modules.ModuleConfig, error) {
		reads[dir]++
		cfg, ok := configs[dir]
		require.True(t, ok, "unexpected config read: %s", dir)
		return cfg, nil
	}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"app", "custom-sdk", "leaf", "shared"}, moduleDirs)
	require.Equal(t, []string{"app", "app/src", "custom-sdk", "leaf", "shared"}, contentDirs)
	for dir, count := range reads {
		require.Equal(t, 1, count, dir)
	}
}

func TestCollectWorkspaceModuleSourcePaths(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   []string
	}{
		{"../code", []string{"app", "code"}},
		{"../../outside", []string{"app"}},
		{"/outside", []string{"app"}},
	} {
		t.Run(tc.source, func(t *testing.T) {
			_, content, err := collectWorkspaceModuleReadPaths([]string{"app"}, func(string) (*modules.ModuleConfig, error) {
				return &modules.ModuleConfig{Source: tc.source}, nil
			}, nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, content)
		})
	}
}

func TestCollectWorkspaceAmbiguousModulePaths(t *testing.T) {
	configs := map[string]*modules.ModuleConfig{
		"app": {
			Dependencies: []*modules.ModuleConfigDependency{
				nil,
				{Source: "dep.v1"},
				{Source: "dep.v1"},
				{Source: "remote.example/repo"},
				{Source: "https://remote.example/repo"},
				{Source: "pinned.example/repo", Pin: "abc"},
			},
			Toolchains: []*modules.ModuleConfigDependency{{Source: "tool.v1"}},
			Blueprint:  &modules.ModuleConfigDependency{Source: "blueprint.v1"},
			SDK:        &modules.SDK{Source: "sdk.v1"},
		},
		"app/dep.v1":              {},
		"app/tool.v1":             {},
		"app/blueprint.v1":        {},
		"app/sdk.v1":              {},
		"app/remote.example/repo": nil,
	}
	reads := map[string]int{}
	moduleDirs, contentDirs, err := collectWorkspaceModuleReadPaths([]string{"app"}, func(dir string) (*modules.ModuleConfig, error) {
		reads[dir]++
		cfg, ok := configs[dir]
		require.True(t, ok, "unexpected config read: %s", dir)
		return cfg, nil
	}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"app", "app/blueprint.v1", "app/dep.v1", "app/sdk.v1", "app/tool.v1"}, moduleDirs)
	require.Equal(t, moduleDirs, contentDirs)
	for dir := range configs {
		require.Equal(t, 1, reads[dir], dir)
	}
}

func TestCollectWorkspaceSymlinkCycles(t *testing.T) {
	reads := 0
	dirs, content, err := collectWorkspaceModuleReadPaths([]string{"real"}, func(string) (*modules.ModuleConfig, error) {
		reads++
		return &modules.ModuleConfig{Dependencies: []*modules.ModuleConfigDependency{{Source: "a"}, {Source: "b"}}}, nil
	}, func(string) (string, error) { return "real", nil })
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	require.Equal(t, []string{"real", "real/a", "real/b"}, dirs)
	require.Equal(t, dirs, content)
}
