package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

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

func TestFilterGeneratorsByInclude(t *testing.T) {
	ctx := context.Background()
	generators := []*core.Generator{
		{Node: modTreeNode("hello-with-generators-java", "generate-files")},
		{Node: modTreeNode("hello-with-generators-java", "generate-other-files")},
	}

	t.Run("workspace-qualified patterns still match", func(t *testing.T) {
		filtered, err := filterGeneratorsByInclude(
			ctx,
			generators,
			[]string{"hello-with-generators-java:generate-*"},
			false,
		)
		require.NoError(t, err)
		require.Len(t, filtered, 2)
	})

	t.Run("single generator module keeps legacy include semantics", func(t *testing.T) {
		filtered, err := filterGeneratorsByInclude(
			ctx,
			generators,
			[]string{"generate-*"},
			true,
		)
		require.NoError(t, err)
		require.Len(t, filtered, 2)
	})

	t.Run("legacy include does not match without compat fallback", func(t *testing.T) {
		filtered, err := filterGeneratorsByInclude(
			ctx,
			generators,
			[]string{"generate-*"},
			false,
		)
		require.NoError(t, err)
		require.Empty(t, filtered)
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

func TestWorkspaceMigrationWarningsKeepsGapWarningsAggregated(t *testing.T) {
	plan := &workspace.MigrationPlan{
		Warnings: []string{
			"migration gap one",
			"migration gap two",
		},
		MigrationGapCount:   2,
		MigrationReportPath: ".dagger/migration-report.md",
	}

	appendWorkspaceMigrationNonGapWarnings(plan, []string{"hint warning"})

	require.Equal(t, []string{
		"hint warning",
		"2 migration gap(s) need manual review; see .dagger/migration-report.md",
	}, workspaceMigrationWarnings(plan))
}

func TestWorkspaceFilterWithDirectoryArgs(t *testing.T) {
	args := workspaceFilterWithDirectoryArgs(nil, core.CopyFilter{
		Include: []string{"app/**"},
		Exclude: []string{".git"},
	})

	require.Len(t, args, 4)
	require.Equal(t, "path", args[0].Name)
	require.Equal(t, "source", args[1].Name)
	require.Equal(t, "include", args[2].Name)
	require.Equal(t, "exclude", args[3].Name)
	for _, arg := range args {
		require.NotEqual(t, "directory", arg.Name)
	}
}

func TestResolveWorkspaceRefreshModules(t *testing.T) {
	t.Run("explicit selection keeps order and removes duplicates", func(t *testing.T) {
		cfg := &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"alpha": {Source: "github.com/example/alpha@main"},
				"beta":  {Source: "github.com/example/beta@main"},
				"gamma": {Source: "github.com/example/gamma@main"},
			},
		}

		mods, err := resolveWorkspaceRefreshModules(cfg, []string{"gamma", "alpha", "gamma"})
		require.NoError(t, err)
		require.Equal(t, []workspaceRefreshModule{
			{Name: "gamma", Source: "github.com/example/gamma@main"},
			{Name: "alpha", Source: "github.com/example/alpha@main"},
		}, mods)
	})

	t.Run("missing modules return error", func(t *testing.T) {
		cfg := &workspace.Config{
			Modules: map[string]workspace.ModuleEntry{
				"alpha": {Source: "github.com/example/alpha@main"},
			},
		}

		_, err := resolveWorkspaceRefreshModules(cfg, []string{"alpha", "missing", "other"})
		require.ErrorContains(t, err, "workspace module(s) not found: missing, other")
	})

	t.Run("selection is required", func(t *testing.T) {
		cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{}}

		_, err := resolveWorkspaceRefreshModules(cfg, nil)
		require.ErrorContains(t, err, "at least one workspace module name is required")
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
