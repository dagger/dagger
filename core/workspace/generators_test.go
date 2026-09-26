package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneratorPolicies(t *testing.T) {
	config := &Config{Modules: map[string]ModuleEntry{
		"toolchain": {Generate: ModuleSkip{Skip: []string{"skip-raw"}}},
		"alias":     {Generate: ModuleSkip{Skip: []string{"skip-alias"}}},
		"project":   {Generate: ModuleSkip{Skip: []string{"skip-project"}}},
		"unrelated": {Generate: ModuleSkip{Skip: []string{"skip-unrelated"}}},
	}}
	policies := GeneratorPolicies([]GeneratorModule{
		{Name: "toolchain", Source: "implementation"},
		{Name: "alias", Source: "implementation", Context: "implementation"},
		{Name: "project", Source: "implementation", Context: "project-files"},
		{Name: "other-project", Source: "implementation", Context: "other-files"},
		{Name: "unrelated", Source: "other-implementation"},
	}, config)
	require.True(t, policies["toolchain"].Replaced)
	require.True(t, policies["alias"].Replaced)
	require.False(t, policies["project"].Replaced)
	require.Equal(t, []string{"skip-project", "skip-raw", "skip-alias"}, policies["project"].Skip)
	require.Equal(t, []string{"skip-raw", "skip-alias"}, policies["other-project"].Skip)
	require.Equal(t, GeneratorPolicy{Skip: []string{"skip-unrelated"}}, policies["unrelated"])
}
