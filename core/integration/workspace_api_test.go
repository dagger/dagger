package core

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

		ctr := workspaceBase(t, c).
			WithNewFile("data.txt", "file content here").
			With(initDangModule("reader", `
type Reader {
  pub content: String!

  new(ws: Workspace!) {
    self.content = ws.file("data.txt").contents
    self
  }

  pub read: String! {
    content
  }
}
`))

		out, err := ctr.With(daggerCall("reader", "read")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "file content here", strings.TrimSpace(out))
	})

	t.Run("directory reads entries and subdirectories", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		t.Run("directory entries", func(ctx context.Context, t *testctx.T) {
			ctr := workspaceBase(t, c).
				WithNewFile("a.txt", "aaa").
				WithNewFile("b.txt", "bbb").
				WithNewFile("sub/c.txt", "ccc").
				With(initDangModule("lister", `
type Lister {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))

			out, err := ctr.With(daggerCall("lister", "ls")).Stdout(ctx)
			require.NoError(t, err)
			entries := strings.TrimSpace(out)
			require.Contains(t, entries, "a.txt")
			require.Contains(t, entries, "b.txt")
			require.Contains(t, entries, "sub/")
		})

		t.Run("subdirectory", func(ctx context.Context, t *testctx.T) {
			ctr := workspaceBase(t, c).
				WithNewFile("sub/foo.txt", "foo").
				WithNewFile("sub/bar.txt", "bar").
				With(initDangModule("subdir", `
type Subdir {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory("sub")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))

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
			ctr := workspaceBase(t, c).
				WithNewFile("keep.txt", "keep me").
				WithNewFile("drop.log", "drop me").
				With(initDangModule("filtered", `
type Filtered {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".", exclude: ["*.log"])
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))

			out, err := ctr.With(daggerCall("filtered", "ls")).Stdout(ctx)
			require.NoError(t, err)
			entries := strings.TrimSpace(out)
			require.Contains(t, entries, "keep.txt")
			require.NotContains(t, entries, "drop.log")
		})

		t.Run("gitignore filters", func(ctx context.Context, t *testctx.T) {
			base := workspaceBase(t, c).
				WithNewFile(".gitignore", "*.log\nbuild/\n").
				WithNewFile("keep.txt", "kept").
				WithNewFile("drop.log", "dropped").
				WithNewFile("build/out.bin", "binary").
				WithNewFile("src/app.txt", "app").
				WithNewFile("src/debug.log", "debug log").
				WithExec([]string{"git", "add", "."}).
				WithExec([]string{"git", "commit", "-m", "init"})

			t.Run("root directory respects gitignore", func(ctx context.Context, t *testctx.T) {
				ctr := base.With(initDangModule("gi-root", `
type GiRoot {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".", gitignore: true)
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))
				out, err := ctr.With(daggerCall("gi-root", "ls")).Stdout(ctx)
				require.NoError(t, err)
				entries := strings.TrimSpace(out)
				require.Contains(t, entries, "keep.txt")
				require.Contains(t, entries, "src/")
				require.NotContains(t, entries, "drop.log")
				require.NotContains(t, entries, "build/")
			})

			t.Run("subdirectory respects gitignore", func(ctx context.Context, t *testctx.T) {
				ctr := base.With(initDangModule("gi-sub", `
type GiSub {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory("src", gitignore: true)
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))
				out, err := ctr.With(daggerCall("gi-sub", "ls")).Stdout(ctx)
				require.NoError(t, err)
				entries := strings.TrimSpace(out)
				require.Contains(t, entries, "app.txt")
				require.NotContains(t, entries, "debug.log")
			})

			t.Run("without gitignore includes all files", func(ctx context.Context, t *testctx.T) {
				ctr := base.With(initDangModule("gi-off", `
type GiOff {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))
				out, err := ctr.With(daggerCall("gi-off", "ls")).Stdout(ctx)
				require.NoError(t, err)
				entries := strings.TrimSpace(out)
				require.Contains(t, entries, "keep.txt")
				require.Contains(t, entries, "drop.log")
				require.Contains(t, entries, "build/")
			})
		})
	})
}

// TestWorkspacePathSafety should cover path normalization and traversal
// protection on Workspace APIs.
func (WorkspaceAPISuite) TestWorkspacePathSafety(ctx context.Context, t *testctx.T) {
	t.Run("parent-directory traversal is rejected", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := workspaceBase(t, c).
			WithNewFile("legit.txt", "legit")

		t.Run("directory traversal", func(ctx context.Context, t *testctx.T) {
			ctr := base.With(initStandaloneDangModule("escape-dir", `
type EscapeDir {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory("../..")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))
			_, err := ctr.With(daggerCall("ls")).Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, "resolves outside root")
		})

		t.Run("file traversal", func(ctx context.Context, t *testctx.T) {
			ctr := base.With(initStandaloneDangModule("escape-file", `
type EscapeFile {
  pub content: String!

  new(source: Workspace!) {
    self.content = source.file("../../etc/hostname").contents
    self
  }

  pub read: String! {
    content
  }
}
`))
			_, err := ctr.With(daggerCall("read")).Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, "resolves outside root")
		})
	})

	t.Run("absolute paths resolve from the workspace boundary", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		base := workspaceBase(t, c).
			WithNewFile("legit.txt", "legit")

		ctr := base.
			WithNewFile("sub/inner.txt", "inner").
			With(initStandaloneDangModule("abs-rel", `
type AbsRel {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory("/sub")
    self
  }

  pub ls: [String!] {
    source.entries
  }
}
`))
		out, err := ctr.With(daggerCall("ls")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "inner.txt")
	})
}

// TestWorkspaceFindUp should cover upward search behavior on Workspace.
func (WorkspaceAPISuite) TestWorkspaceFindUp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("root.txt", "at root").
		WithNewFile("a/target.txt", "in a").
		WithNewFile("a/b/other.txt", "in a/b").
		WithExec([]string{"mkdir", "-p", "a/b/c"}).
		WithNewFile("a/b/c/leaf.txt", "leaf").
		WithExec([]string{"mkdir", "-p", "a/somedir"}).
		WithNewFile("a/somedir/hi.txt", "hi").
		With(initStandaloneDangModule("finder", `
type Finder {
  pub result: String!

  new(ws: Workspace!, name: String!, from: String!) {
    self.result = ws.findUp(name: name, from: from) ?? ""
    self
  }
}
`))

	t.Run("find file in start directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--name=other.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/a/b/other.txt", strings.TrimSpace(out))
	})

	t.Run("find file in parent directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--name=target.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/a/target.txt", strings.TrimSpace(out))
	})

	t.Run("find file at workspace root", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--name=root.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/root.txt", strings.TrimSpace(out))
	})

	t.Run("find directory in parent", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--name=somedir", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/a/somedir", strings.TrimSpace(out))
	})

	t.Run("does not find child directory content", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--name=leaf.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})

	t.Run("does not find missing file", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--name=nonexistent.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}
