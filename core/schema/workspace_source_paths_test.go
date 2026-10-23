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
	})
	require.NoError(t, err)
	require.Equal(t, []string{"app", "custom-sdk", "leaf", "shared"}, moduleDirs)
	require.Equal(t, []string{"app", "app/src", "custom-sdk", "leaf", "shared"}, contentDirs)
	for dir, count := range reads {
		require.Equal(t, 1, count, dir)
	}
}
