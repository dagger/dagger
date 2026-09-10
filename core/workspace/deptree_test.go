package workspace

import (
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/stretchr/testify/require"
)

func TestLocalModuleRefs(t *testing.T) {
	t.Parallel()

	cfg := &modules.ModuleConfig{
		Toolchains: []*modules.ModuleConfigDependency{
			{Name: "tc-local", Source: "./toolchain"},
			{Name: "tc-remote", Source: "github.com/acme/tc@main"},
			nil,
		},
		Dependencies: []*modules.ModuleConfigDependency{
			{Name: "dep-remote", Source: "github.com/acme/dep@main", Pin: "sha256:abc"},
			{Name: "dep-local", Source: "../libs/foo"},
		},
		Blueprint: &modules.ModuleConfigDependency{Name: "bp", Source: "./blueprint"},
	}

	refs := LocalModuleRefs(cfg)
	require.Len(t, refs, 2, "only local toolchains + deps, blueprint excluded")
	require.Equal(t, "tc-local", refs[0].Name, "toolchains come first, in order")
	require.Equal(t, "dep-local", refs[1].Name, "dependencies follow toolchains")
}

func TestLocalModuleRefsEmpty(t *testing.T) {
	t.Parallel()

	require.Nil(t, LocalModuleRefs(nil))
	require.Nil(t, LocalModuleRefs(&modules.ModuleConfig{
		Dependencies: []*modules.ModuleConfigDependency{
			{Name: "remote", Source: "github.com/acme/dep@main"},
		},
	}))
}

func TestAddMigratedModuleSDK(t *testing.T) {
	t.Parallel()

	t.Run("creates a builtin SDK install", func(t *testing.T) {
		cfg := &Config{Modules: map[string]ModuleEntry{}}
		AddMigratedModuleSDK(cfg, "go", "libs/foo", "foo")
		entry, ok := cfg.Modules["dagger-go-sdk"]
		require.True(t, ok)
		require.Equal(t, "go", entry.Source)
		require.Equal(t, SDKEntry{Module: "dagger-go-sdk", Scopes: map[string]SDKScope{
			"libs/foo": {IsModule: true, Name: "foo"},
		}}, cfg.SDKs["go"])
	})

	t.Run("shares one entry across modules with the same runtime", func(t *testing.T) {
		cfg := &Config{Modules: map[string]ModuleEntry{}}
		AddMigratedModuleSDK(cfg, "go", ".dagger/modules/myapp", "myapp")
		AddMigratedModuleSDK(cfg, "go", "libs/foo", "foo")
		require.Len(t, cfg.Modules, 1)
		require.Equal(t, map[string]SDKScope{
			".dagger/modules/myapp": {IsModule: true, Name: "myapp"},
			"libs/foo":              {IsModule: true, Name: "foo"},
		}, cfg.SDKs["go"].Scopes)
	})

	t.Run("separate entries for different runtimes", func(t *testing.T) {
		cfg := &Config{Modules: map[string]ModuleEntry{}}
		AddMigratedModuleSDK(cfg, "go", "a", "a")
		AddMigratedModuleSDK(cfg, "python", "b", "b")
		require.Contains(t, cfg.Modules, "dagger-go-sdk")
		require.Contains(t, cfg.Modules, "dagger-python-sdk")
	})

	t.Run("external SDK keeps its ref as source", func(t *testing.T) {
		cfg := &Config{Modules: map[string]ModuleEntry{}}
		AddMigratedModuleSDK(cfg, "github.com/acme/custom-sdk", "libs/foo", "foo")
		entry, ok := cfg.Modules["custom-sdk"]
		require.True(t, ok)
		require.Equal(t, "github.com/acme/custom-sdk", entry.Source)
		require.Equal(t, SDKEntry{Module: "custom-sdk", Scopes: map[string]SDKScope{
			"libs/foo": {IsModule: true, Name: "foo"},
		}}, cfg.SDKs["custom"])
	})
}

func TestHasOwnWorkspaceSemantics(t *testing.T) {
	t.Parallel()

	require.False(t, HasOwnWorkspaceSemantics(nil))
	require.False(t, HasOwnWorkspaceSemantics(&modules.ModuleConfig{
		SDK:    &modules.SDK{Source: "go"},
		Source: "src",
	}), "a normal sdk+source:subdir toolchain must convert, not be treated as a nested workspace")
	require.True(t, HasOwnWorkspaceSemantics(&modules.ModuleConfig{
		Toolchains: []*modules.ModuleConfigDependency{{Name: "tc", Source: "./tc"}},
	}))
	require.True(t, HasOwnWorkspaceSemantics(&modules.ModuleConfig{
		Blueprint: &modules.ModuleConfigDependency{Name: "bp", Source: "./bp"},
	}))
}
