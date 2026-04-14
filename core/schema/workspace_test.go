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

func TestWorkspaceDirectoryDagOpPath(t *testing.T) {
	ctx := context.Background()

	localWS := func(wsPath string) *core.Workspace {
		ws := &core.Workspace{Path: wsPath}
		ws.SetHostPath("/host/workspace")
		return ws
	}
	remoteWS := func(wsPath string) *core.Workspace {
		return &core.Workspace{Path: wsPath}
	}

	t.Run("local workspace always reports root", func(t *testing.T) {
		got, err := workspaceDirectoryDagOpPath(ctx, localWS("."), workspaceDirectoryArgs{Path: "/sub"})
		require.NoError(t, err)
		require.Equal(t, "/", got)
	})

	t.Run("remote workspace with filter reports root", func(t *testing.T) {
		got, err := workspaceDirectoryDagOpPath(ctx, remoteWS("."), workspaceDirectoryArgs{
			Path:       "/sub",
			CopyFilter: core.CopyFilter{Include: []string{"*.go"}},
		})
		require.NoError(t, err)
		require.Equal(t, "/", got)

		got, err = workspaceDirectoryDagOpPath(ctx, remoteWS("."), workspaceDirectoryArgs{
			Path:       "/sub",
			CopyFilter: core.CopyFilter{Exclude: []string{"node_modules/"}},
		})
		require.NoError(t, err)
		require.Equal(t, "/", got)
	})

	t.Run("remote workspace at boundary root reports root", func(t *testing.T) {
		got, err := workspaceDirectoryDagOpPath(ctx, remoteWS("."), workspaceDirectoryArgs{Path: "/"})
		require.NoError(t, err)
		require.Equal(t, "/", got)
	})

	// Regression: Workspace.directory on a remote workspace must report the
	// requested subpath. Reporting "/" (the wrapper default) made the DagOp
	// evaluate the full rootfs while the outer Directory's Dir stayed at "/",
	// so consumers like WithDirectory and Entries saw the repo root instead of
	// the subdirectory the caller asked for.
	t.Run("remote workspace with absolute subpath reports subpath", func(t *testing.T) {
		got, err := workspaceDirectoryDagOpPath(ctx, remoteWS("."), workspaceDirectoryArgs{Path: "/helm/dagger"})
		require.NoError(t, err)
		require.Equal(t, "/helm/dagger", got)
	})

	t.Run("remote workspace with relative path resolves from workspace dir", func(t *testing.T) {
		got, err := workspaceDirectoryDagOpPath(ctx, remoteWS("services/payment"), workspaceDirectoryArgs{Path: "src"})
		require.NoError(t, err)
		require.Equal(t, "/services/payment/src", got)
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
