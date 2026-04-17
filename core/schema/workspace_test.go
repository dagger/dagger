package schema

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core"
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

func TestSelectVisibleGeneratorModules(t *testing.T) {
	names := func(entries []workspaceGeneratorModule) []string {
		result := make([]string, 0, len(entries))
		for _, entry := range entries {
			result = append(result, entry.name)
		}
		return result
	}

	t.Run("wrapper hides raw blueprint alias", func(t *testing.T) {
		visible := selectVisibleGeneratorModules([]workspaceGeneratorModule{
			{name: "hello-with-generators", sourceDigest: "sha256:blueprint", isWrapper: false},
			{name: "app", sourceDigest: "sha256:blueprint", isWrapper: true},
		})
		require.Equal(t, []string{"app"}, names(visible))
	})

	t.Run("single raw module remains visible", func(t *testing.T) {
		visible := selectVisibleGeneratorModules([]workspaceGeneratorModule{
			{name: "hello-with-generators", sourceDigest: "sha256:blueprint", isWrapper: false},
		})
		require.Equal(t, []string{"hello-with-generators"}, names(visible))
	})

	t.Run("multiple wrappers sharing one implementation remain visible", func(t *testing.T) {
		visible := selectVisibleGeneratorModules([]workspaceGeneratorModule{
			{name: "hello-with-generators", sourceDigest: "sha256:blueprint", isWrapper: false},
			{name: "app", sourceDigest: "sha256:blueprint", isWrapper: true},
			{name: "ci", sourceDigest: "sha256:blueprint", isWrapper: true},
		})
		require.Equal(t, []string{"app", "ci"}, names(visible))
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
