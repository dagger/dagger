package schema

import (
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestResolveWorkspaceUpdateTargets(t *testing.T) {
	t.Run("all modules are sorted when no explicit selection", func(t *testing.T) {
		cfg := &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"zeta":  {},
				"alpha": {},
				"beta":  {},
			},
		}

		targets, err := resolveWorkspaceUpdateTargets(cfg, nil)
		require.NoError(t, err)
		require.Equal(t, []string{"alpha", "beta", "zeta"}, targets)
	})

	t.Run("explicit selection keeps order and removes duplicates", func(t *testing.T) {
		cfg := &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"alpha": {},
				"beta":  {},
				"gamma": {},
			},
		}

		targets, err := resolveWorkspaceUpdateTargets(cfg, []string{"gamma", "alpha", "gamma"})
		require.NoError(t, err)
		require.Equal(t, []string{"gamma", "alpha"}, targets)
	})

	t.Run("missing modules return error", func(t *testing.T) {
		cfg := &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"alpha": {},
			},
		}

		_, err := resolveWorkspaceUpdateTargets(cfg, []string{"alpha", "missing", "other"})
		require.ErrorContains(t, err, "workspace module(s) not found: missing, other")
	})

	t.Run("empty module set returns empty list", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{}}

		targets, err := resolveWorkspaceUpdateTargets(cfg, nil)
		require.NoError(t, err)
		require.Empty(t, targets)
	})
}
