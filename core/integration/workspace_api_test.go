package core

// These tests cover the GraphQL Workspace object after a workspace has already
// been selected or injected into the session. They verify API behavior, not how
// the workspace was found.
//
// See also:
// - workspace_selection_test.go: explicit workspace selection.
// - contextual_workspace_test.go: workspace find-up from the current directory.
// - module_loading_test.go: module source selection and entrypoint arbitration.

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// WorkspaceAPISuite owns behavior of the Workspace object once a Workspace has
// already been injected or passed explicitly.
type WorkspaceAPISuite struct{}

func TestWorkspaceAPI(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceAPISuite{})
}

// TestWorkspaceFileAndDirectory should cover the core file-system accessors on
// Workspace.
func (WorkspaceAPISuite) TestWorkspaceFileAndDirectory(ctx context.Context, t *testctx.T) {
	t.Run("file reads workspace content", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := workspaceFixture(t, c, "workspace-api").
			WithNewFile("data.txt", "file content here")

		out, err := ctr.With(daggerCall("reader", "read")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "file content here", strings.TrimSpace(out))
	})

	t.Run("directory reads entries and subdirectories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		t.Run("directory entries", func(ctx context.Context, t *testctx.T) {
			ctr := workspaceFixture(t, c, "workspace-api").
				WithNewFile("a.txt", "aaa").
				WithNewFile("b.txt", "bbb").
				WithNewFile("sub/c.txt", "ccc")

			out, err := ctr.With(daggerCall("lister", "ls")).Stdout(ctx)
			require.NoError(t, err)
			entries := strings.TrimSpace(out)
			require.Contains(t, entries, "a.txt")
			require.Contains(t, entries, "b.txt")
			require.Contains(t, entries, "sub")
		})

		t.Run("subdirectory", func(ctx context.Context, t *testctx.T) {
			ctr := workspaceFixture(t, c, "workspace-api").
				WithNewFile("sub/foo.txt", "foo").
				WithNewFile("sub/bar.txt", "bar")

			out, err := ctr.With(daggerCall("subdir", "ls")).Stdout(ctx)
			require.NoError(t, err)
			entries := strings.TrimSpace(out)
			require.Contains(t, entries, "foo.txt")
			require.Contains(t, entries, "bar.txt")
			require.NotContains(t, entries, "sub/")
		})
	})

	t.Run("directory exclude and gitignore filters apply", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		t.Run("exclude patterns", func(ctx context.Context, t *testctx.T) {
			ctr := workspaceFixture(t, c, "workspace-api").
				WithNewFile("keep.txt", "keep me").
				WithNewFile("drop.log", "drop me")

			out, err := ctr.With(daggerCall("filtered", "ls")).Stdout(ctx)
			require.NoError(t, err)
			entries := strings.TrimSpace(out)
			require.Contains(t, entries, "keep.txt")
			require.NotContains(t, entries, "drop.log")
		})

		t.Run("gitignore filters", func(ctx context.Context, t *testctx.T) {
			base := workspaceFixture(t, c, "workspace-api").
				WithNewFile(".gitignore", "*.log\nbuild/\n").
				WithNewFile("keep.txt", "kept").
				WithNewFile("drop.log", "dropped").
				WithNewFile("build/out.bin", "binary").
				WithNewFile("src/app.txt", "app").
				WithNewFile("src/debug.log", "debug log").
				WithExec([]string{"git", "add", "."}).
				WithExec([]string{"git", "commit", "-m", "init"})

			t.Run("root directory respects gitignore", func(ctx context.Context, t *testctx.T) {
				ctr := base
				out, err := ctr.With(daggerCall("gi-root", "ls")).Stdout(ctx)
				require.NoError(t, err)
				entries := strings.TrimSpace(out)
				require.Contains(t, entries, "keep.txt")
				require.Contains(t, entries, "src")
				require.NotContains(t, entries, "drop.log")
				require.NotContains(t, entries, "build")
			})

			t.Run("subdirectory respects gitignore", func(ctx context.Context, t *testctx.T) {
				ctr := base
				out, err := ctr.With(daggerCall("gi-sub", "ls")).Stdout(ctx)
				require.NoError(t, err)
				entries := strings.TrimSpace(out)
				require.Contains(t, entries, "app.txt")
				require.NotContains(t, entries, "debug.log")
			})

			t.Run("without gitignore includes all files", func(ctx context.Context, t *testctx.T) {
				ctr := base
				out, err := ctr.With(daggerCall("gi-off", "ls")).Stdout(ctx)
				require.NoError(t, err)
				entries := strings.TrimSpace(out)
				require.Contains(t, entries, "keep.txt")
				require.Contains(t, entries, "drop.log")
				require.Contains(t, entries, "build")
			})
		})

		t.Run("workspace config ignore filters directory and file access", func(ctx context.Context, t *testctx.T) {
			ctr := workspaceFixture(t, c, "workspace-api").
				WithNewFile("keep.txt", "keep me").
				WithNewFile("ignored/drop.txt", "drop me").
				WithNewFile("ignored/secret.txt", "secret").
				WithNewFile("sub/keep.txt", "keep sub").
				WithNewFile("sub/secret.txt", "drop sub")

			out, err := ctr.With(daggerCall("lister", "ls")).Stdout(ctx)
			require.NoError(t, err)
			entries := strings.TrimSpace(out)
			require.Contains(t, entries, "keep.txt")
			require.NotContains(t, entries, "ignored")

			out, err = ctr.With(daggerCall("subdir", "ls")).Stdout(ctx)
			require.NoError(t, err)
			entries = strings.TrimSpace(out)
			require.Contains(t, entries, "keep.txt")
			require.NotContains(t, entries, "secret.txt")

			_, err = ctr.With(daggerCall("ignored-reader", "read")).Sync(ctx)
			requireErrOut(t, err, "no such file or directory")
		})
	})
}

// TestWorkspacePathSafety should cover path normalization and traversal
// protection on Workspace APIs.
func (WorkspaceAPISuite) TestWorkspacePathSafety(ctx context.Context, t *testctx.T) {
	t.Run("parent-directory traversal is rejected", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := workspaceFixture(t, c, "workspace-api").
			WithNewFile("legit.txt", "legit")

		t.Run("directory traversal", func(ctx context.Context, t *testctx.T) {
			ctr := base
			_, err := ctr.With(daggerCall("escape-dir", "ls")).Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, "escapes workspace root")
		})

		t.Run("file traversal", func(ctx context.Context, t *testctx.T) {
			ctr := base
			_, err := ctr.With(daggerCall("escape-file", "read")).Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, "escapes workspace root")
		})
	})

	t.Run("absolute paths resolve from the workspace boundary", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := workspaceFixture(t, c, "workspace-api").
			WithNewFile("legit.txt", "legit")

		ctr := base.
			WithNewFile("sub/inner.txt", "inner")
		out, err := ctr.With(daggerCall("abs-rel", "ls")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "inner.txt")
	})
}

// TestWorkspaceFindUp should cover upward search behavior on Workspace.
func (WorkspaceAPISuite) TestWorkspaceFindUp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "workspace-api").
		WithNewFile("root.txt", "at root").
		WithNewFile("a/target.txt", "in a").
		WithNewFile("a/b/other.txt", "in a/b").
		WithExec([]string{"mkdir", "-p", "a/b/c"}).
		WithNewFile("a/b/c/leaf.txt", "leaf").
		WithExec([]string{"mkdir", "-p", "a/somedir"}).
		WithNewFile("a/somedir/hi.txt", "hi")

	t.Run("find file in start directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=other.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/a/b/other.txt", strings.TrimSpace(out))
	})

	t.Run("find file in parent directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=target.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/a/target.txt", strings.TrimSpace(out))
	})

	t.Run("find file at workspace root", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=root.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/root.txt", strings.TrimSpace(out))
	})

	t.Run("find directory in parent", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=somedir", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/a/somedir", strings.TrimSpace(out))
	})

	t.Run("does not find child directory content", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=leaf.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})

	t.Run("does not find missing file", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=nonexistent.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}
