package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
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

func TestMatchWorkspaceInclude(t *testing.T) {
	ctx := context.Background()
	node := modTreeNode("go", "lint")

	t.Run("empty include matches everything", func(t *testing.T) {
		match, err := matchWorkspaceInclude(ctx, node, nil)
		require.NoError(t, err)
		require.True(t, match)
	})

	t.Run("module-prefixed pattern matches", func(t *testing.T) {
		match, err := matchWorkspaceInclude(ctx, node, []string{"go:lint"})
		require.NoError(t, err)
		require.True(t, match)
	})

	t.Run("wildcard module pattern matches", func(t *testing.T) {
		match, err := matchWorkspaceInclude(ctx, node, []string{"go:**"})
		require.NoError(t, err)
		require.True(t, match)
	})

	t.Run("other module does not match", func(t *testing.T) {
		match, err := matchWorkspaceInclude(ctx, node, []string{"helm:**"})
		require.NoError(t, err)
		require.False(t, match)
	})
}

func TestResolveWorkspacePath(t *testing.T) {
	t.Run("relative path resolves from workspace directory", func(t *testing.T) {
		require.Equal(t, "services/payment/src", resolveWorkspacePath("src", "services/payment"))
	})

	t.Run("dot resolves to workspace directory", func(t *testing.T) {
		require.Equal(t, "services/payment", resolveWorkspacePath(".", "services/payment"))
	})

	t.Run("absolute path resolves from workspace boundary", func(t *testing.T) {
		require.Equal(t, "shared/config", resolveWorkspacePath("/shared/config", "services/payment"))
	})

	t.Run("root absolute path resolves to boundary root", func(t *testing.T) {
		require.Equal(t, "", resolveWorkspacePath("/", "services/payment"))
	})
}

func TestWorkspaceAPIPath(t *testing.T) {
	t.Run("boundary root is slash", func(t *testing.T) {
		require.Equal(t, "/", workspaceAPIPath(""))
		require.Equal(t, "/", workspaceAPIPath("."))
	})

	t.Run("nested path is absolute from boundary", func(t *testing.T) {
		require.Equal(t, "/services/payment", workspaceAPIPath("services/payment"))
	})
}

func modTreeNode(parts ...string) *core.ModTreeNode {
	parent := &core.ModTreeNode{}
	for _, part := range parts {
		parent = &core.ModTreeNode{
			Parent: parent,
			Name:   part,
		}
	}
	return parent
}
