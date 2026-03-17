package core

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type WorkspaceSuite struct{}

func TestWorkspace(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceSuite{})
}

// workspaceBase returns a container with git, the dagger CLI, and an
// initialized git repo at /work — the starting point for workspace tests.
func workspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git", "ripgrep"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"}).
		With(daggerExec("init"))
}

// initDangModule creates a Dang module in the workspace with the given name
// and source code. Uses "dagger init" and "dagger toolchain install" to
// scaffold the workspace and module, then overwrites main.dang with the
// provided source.
func initDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithWorkdir("toolchains/"+name).
			With(daggerExec("init", "--sdk=dang", "--name="+name)).
			WithNewFile("main.dang", source).
			WithWorkdir("../../").
			With(daggerExec("toolchain", "install", "./toolchains/"+name))
	}
}

// initDangBlueprint creates a Dang blueprint module and an app module that
// uses it. The blueprint source is written to blueprints/<name>/ and the app
// module is initialized at the workspace root with --blueprint pointing to it.
func initDangBlueprint(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			// Create the blueprint module
			WithWorkdir("blueprints/"+name).
			With(daggerExec("init", "--sdk=dang", "--name="+name)).
			WithNewFile("main.dang", source).
			WithWorkdir("../../").
			// Init the workspace root module using the blueprint
			With(daggerExec("init", "--blueprint=./blueprints/"+name))
	}
}

// TestWorkspaceBlueprint verifies that a blueprint module accepting a Workspace
// argument can access the host filesystem, just like a toolchain module.
func (WorkspaceSuite) TestBlueprint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("hello.txt", "hello from workspace").
		With(initDangBlueprint("greeter", `
type Greeter {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub read: String! {
    source.file("hello.txt").contents
  }
}
`))

	out, err := ctr.With(daggerCall("read")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello from workspace", strings.TrimSpace(out))
}

// TestWorkspaceFindUp verifies that Workspace.findUp searches up from the
// start path and stops at the workspace root.
func (WorkspaceSuite) TestFindUp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("root.txt", "at root").
		WithNewFile("a/target.txt", "in a").
		WithNewFile("a/b/other.txt", "in a/b").
		WithExec([]string{"mkdir", "-p", "a/b/c"}).
		WithNewFile("a/b/c/leaf.txt", "leaf").
		WithExec([]string{"mkdir", "-p", "a/somedir"}).
		WithNewFile("a/somedir/hi.txt", "hi").
		With(initDangModule("finder", `
type Finder {
  pub result: String!

  new(ws: Workspace!, name: String!, from: String!) {
    self.result = ws.findUp(name: name, from: from) ?? ""
    self
  }
}
`))

	t.Run("find file in start directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=other.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a/b/other.txt", strings.TrimSpace(out))
	})

	t.Run("find file in parent directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=target.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a/target.txt", strings.TrimSpace(out))
	})

	t.Run("find file at workspace root", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=root.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "root.txt", strings.TrimSpace(out))
	})

	t.Run("find directory in parent", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=somedir", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a/somedir", strings.TrimSpace(out))
	})

	t.Run("do not find file in child directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=leaf.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})

	t.Run("do not find non-existent file", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("finder", "--name=nonexistent.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}

// TestWorkspaceArg verifies that a module function accepting a Workspace
// argument can access the host filesystem.
func (WorkspaceSuite) TestWorkspaceArg(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("hello.txt", "hello from workspace").
		With(initDangModule("greeter", `
type Greeter {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub read: String! {
    source.file("hello.txt").contents
  }
}
`))

	out, err := ctr.With(daggerCall("greeter", "read")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello from workspace", strings.TrimSpace(out))
}

// TestWorkspaceDirectoryEntries verifies that Workspace.directory returns the
// correct entries from the host filesystem.
func (WorkspaceSuite) TestWorkspaceDirectoryEntries(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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
}

// TestWorkspaceDirectoryExclude verifies that include/exclude patterns work
// when calling Workspace.directory.
func (WorkspaceSuite) TestWorkspaceDirectoryExclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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
}

// TestWorkspaceNotCached verifies that functions accepting Workspace args are
// never persistently cached — changes to the host filesystem are reflected
// on subsequent calls without needing a cache buster.
func (WorkspaceSuite) TestWorkspaceNotCached(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Set up a module that lists workspace entries.
	base := workspaceBase(t, c).
		WithNewFile("original.txt", "original").
		With(initDangModule("cachechk", `
type Cachechk {
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

	// First call — should see original.txt.
	out, err := base.With(daggerCall("cachechk", "ls")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "original.txt")
	require.NotContains(t, out, "added.txt")

	// Add a file and call again — should see the new file without any cache buster.
	out, err = base.
		WithNewFile("added.txt", "added").
		With(daggerCall("cachechk", "ls")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "original.txt")
	require.Contains(t, out, "added.txt")
}

// TestWorkspaceFile verifies that Workspace.file returns the correct file
// content from the host filesystem.
func (WorkspaceSuite) TestWorkspaceFile(ctx context.Context, t *testctx.T) {
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
}

// TestWorkspaceExists verifies that Workspace.exists correctly reports
// the existence of files and directories on the host, including type
// filtering via expectedType and doNotFollowSymlinks.
func (WorkspaceSuite) TestWorkspaceExists(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("existing-file.txt", "hello").
		WithExec([]string{"mkdir", "-p", "existing-dir"}).
		WithExec([]string{"ln", "-s", "existing-file.txt", "existing-link"})

	t.Run("basic existence", func(ctx context.Context, t *testctx.T) {
		ctr := base.With(initDangModule("checker", `
type Checker {
  pub result: Boolean!

  new(ws: Workspace!, path: String!) {
    self.result = ws.exists(path)
    self
  }
}
`))
		t.Run("existing file", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerCall("checker", "--path=existing-file.txt", "result")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "true", strings.TrimSpace(out))
		})
		t.Run("existing directory", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerCall("checker", "--path=existing-dir", "result")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "true", strings.TrimSpace(out))
		})
		t.Run("non-existent path", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerCall("checker", "--path=no-such-file.txt", "result")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "false", strings.TrimSpace(out))
		})
	})

	t.Run("with expectedType", func(ctx context.Context, t *testctx.T) {
		ctr := base.With(initDangModule("typechecker", `
type Typechecker {
  pub result: Boolean!

  new(ws: Workspace!, path: String!, expectedType: ExistsType!) {
    self.result = ws.exists(path, expectedType: expectedType)
    self
  }
}
`))
		t.Run("file matches REGULAR_TYPE", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerCall("typechecker", "--path=existing-file.txt", "--expected-type=REGULAR_TYPE", "result")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "true", strings.TrimSpace(out))
		})
		t.Run("file does not match DIRECTORY_TYPE", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerCall("typechecker", "--path=existing-file.txt", "--expected-type=DIRECTORY_TYPE", "result")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "false", strings.TrimSpace(out))
		})
		t.Run("directory matches DIRECTORY_TYPE", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerCall("typechecker", "--path=existing-dir", "--expected-type=DIRECTORY_TYPE", "result")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "true", strings.TrimSpace(out))
		})
		t.Run("symlink matches SYMLINK_TYPE", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerCall("typechecker", "--path=existing-link", "--expected-type=SYMLINK_TYPE", "result")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "true", strings.TrimSpace(out))
		})
	})
}

// TestWorkspaceGlob verifies that Workspace.glob matches files and
// directories on the host filesystem.
func (WorkspaceSuite) TestWorkspaceGlob(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("README.md", "readme").
		WithNewFile("CHANGELOG.md", "changelog").
		WithNewFile("main.go", "package main").
		WithNewFile("src/app.go", "package src").
		WithNewFile("src/app_test.go", "package src").
		With(initDangModule("globber", `
type Globber {
  pub results: [String!]!

  new(ws: Workspace!, pattern: String!) {
    self.results = ws.glob(pattern)
    self
  }
}
`))

	t.Run("match by extension", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("globber", "--pattern=*.md", "results")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.Contains(t, lines, "README.md")
		require.Contains(t, lines, "CHANGELOG.md")
		require.NotContains(t, lines, "main.go")
	})

	t.Run("recursive glob", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("globber", "--pattern=**/*.go", "results")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.Contains(t, lines, "main.go")
		require.Contains(t, lines, "src/app.go")
		require.Contains(t, lines, "src/app_test.go")
	})

	t.Run("subdirectory glob", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("globber", "--pattern=src/*.go", "results")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.Contains(t, lines, "src/app.go")
		require.Contains(t, lines, "src/app_test.go")
		require.NotContains(t, lines, "main.go")
	})

	t.Run("no matches", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("globber", "--pattern=*.rs", "results")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}

// TestWorkspaceSearch verifies that Workspace.search runs ripgrep (or grep)
// on the host filesystem and returns structured results.
func (WorkspaceSuite) TestWorkspaceSearch(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("hello.txt", "Hello World\nGoodbye World\n").
		WithNewFile("src/main.go", "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n").
		WithNewFile("src/util.go", "package main\n\nfunc helper() string {\n\treturn \"hello\"\n}\n").
		WithNewFile("docs/readme.md", "# Hello\n\nThis is a hello world project.\n").
		With(initDangModule("searcher", `
type Searcher {
  pub filePaths: [String!]!

  new(ws: Workspace!, pattern: String!) {
    self.filePaths = []
    ws.search(pattern: pattern).{filePath, lineNumber}.each { result =>
      self.filePaths += [result.filePath + ":" + toString(result.lineNumber)]
    }
    self
  }
}
`)).
		With(initDangModule("files-searcher", `
type FilesSearcher {
  pub files: [String!]!

  new(ws: Workspace!, pattern: String!, globs: [String!]! = []) {
    self.files = []
    ws.search(pattern: pattern, filesOnly: true, globs: globs).{filePath}.each { result =>
      self.files += [result.filePath]
    }
    self
  }
}
`))

	t.Run("basic search", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("searcher", "--pattern=hello", "file-paths")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.NotContains(t, lines, "hello.txt:1") // case sensitive
		require.Contains(t, lines, "src/main.go:4")
		require.Contains(t, lines, "src/util.go:4")
		require.Contains(t, lines, "docs/readme.md:3")
	})

	t.Run("files only", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("files-searcher", "--pattern=hello", "files")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.NotContains(t, lines, "hello.txt") // case sensitive
		require.Contains(t, lines, "src/main.go")
		require.Contains(t, lines, "src/util.go")
		require.Contains(t, lines, "docs/readme.md")
	})

	t.Run("files only with glob filter", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("files-searcher", "--pattern=hello", "--globs=*.go", "files")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.Contains(t, lines, "src/main.go")
		require.Contains(t, lines, "src/util.go")
		require.NotContains(t, lines, "hello.txt")
		require.NotContains(t, lines, "readme.md")
	})

	t.Run("no matches", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("searcher", "--pattern=nonexistent_pattern_xyz", "file-paths")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}

// TestWorkspaceSubdirectory verifies that Workspace.directory can access
// a subdirectory of the workspace.
func (WorkspaceSuite) TestWorkspaceSubdirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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
	// Should NOT contain top-level workspace files.
	require.NotContains(t, entries, "sub/")
}

// TestWorkspacePathTraversal verifies that a module cannot use Workspace to
// escape the workspace root and access arbitrary host paths.
func (WorkspaceSuite) TestWorkspacePathTraversal(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("legit.txt", "legit")

	t.Run("directory traversal with ..", func(ctx context.Context, t *testctx.T) {
		ctr := base.With(initDangModule("escape-dir", `
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
		_, err := ctr.With(daggerCall("escape-dir", "ls")).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "resolves outside root")
	})

	t.Run("file traversal with ..", func(ctx context.Context, t *testctx.T) {
		ctr := base.With(initDangModule("escape-file", `
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
		_, err := ctr.With(daggerCall("escape-file", "read")).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "resolves outside root")
	})

	t.Run("absolute path treated as relative", func(ctx context.Context, t *testctx.T) {
		// Absolute paths are relative to workspace root, not the host root.
		// /sub should resolve to <workspace>/sub, not /sub on the host.
		ctr := base.
			WithNewFile("sub/inner.txt", "inner").
			With(initDangModule("abs-rel", `
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
		out, err := ctr.With(daggerCall("abs-rel", "ls")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "inner.txt")
	})
}

// TestWorkspaceArgNotExposedAsCLIFlag verifies that Workspace arguments are
// "magical" — injected by the server — and not exposed as CLI flags, but the
// function is still visible and callable.
func (WorkspaceSuite) TestWorkspaceArgNotExposedAsCLIFlag(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("test.txt", "test").
		With(initDangModule("magic", `
type Magic {
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

	// The function should be callable without passing --source (it's auto-injected).
	out, err := ctr.With(daggerCall("magic", "ls")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "test.txt")

	// --help should NOT show a --source flag for the constructor.
	help, err := ctr.With(daggerCall("magic", "--help")).Stdout(ctx)
	require.NoError(t, err)
	require.NotContains(t, help, "--source")
}

// TestWorkspaceDirectoryGitignore verifies that Workspace.directory with
// gitignore: true filters out files matched by .gitignore rules.
func (WorkspaceSuite) TestWorkspaceDirectoryGitignore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile(".gitignore", "*.log\nbuild/\n").
		WithNewFile("keep.txt", "kept").
		WithNewFile("drop.log", "dropped").
		WithNewFile("build/out.bin", "binary").
		WithNewFile("src/app.txt", "app").
		WithNewFile("src/debug.log", "debug log").
		// commit so .gitignore is well-established
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

	t.Run("without gitignore flag includes all files", func(ctx context.Context, t *testctx.T) {
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
}

// TestWorkspaceBranch verifies that Workspace.branch returns the current git branch.
func (WorkspaceSuite) TestBranch(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("detects current branch", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("brancher", `
type Brancher {
  pub branch: String!

  new(ws: Workspace!) {
    self.branch = ws.branch
    self
  }
}
`))
		out, err := ctr.With(daggerCall("brancher", "branch")).Stdout(ctx)
		require.NoError(t, err)
		// git init creates "master" by default
		branch := strings.TrimSpace(out)
		require.True(t, branch == "master" || branch == "main",
			"expected 'master' or 'main', got %q", branch)
	})

	t.Run("detects non-default branch", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			WithExec([]string{"git", "checkout", "-b", "feature/test"}).
			With(initDangModule("brancher", `
type Brancher {
  pub branch: String!

  new(ws: Workspace!) {
    self.branch = ws.branch
    self
  }
}
`))
		out, err := ctr.With(daggerCall("brancher", "branch")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "feature/test", strings.TrimSpace(out))
	})
}

// TestWorkspaceWithBranch verifies that Workspace.withBranch creates a git
// worktree and returns a workspace pointing to it.
func (WorkspaceSuite) TestWithBranch(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("creates worktree for new branch", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("wt", `
type Wt {
  pub branch: String!
  pub root: String!
  pub files: [String!]!

  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/test")
    self.branch = ws2.branch
    self.root = ws2.root
    self.files = ws2.glob("*")
    self
  }
}
`))
		out, err := ctr.With(daggerCall("wt", "branch")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "agent/test", strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("wt", "root")).Stdout(ctx)
		require.NoError(t, err)
		root := strings.TrimSpace(out)
		require.Contains(t, root, "-worktrees/agent-test")

		out, err = ctr.With(daggerCall("wt", "files")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello.txt")
	})

	t.Run("same branch is no-op", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("noop", `
type Noop {
  pub same: Boolean!

  new(ws: Workspace!) {
    let ws2 = ws.withBranch(ws.branch)
    self.same = ws.root == ws2.root
    self
  }
}
`))
		out, err := ctr.With(daggerCall("noop", "same")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "true", strings.TrimSpace(out))
	})

	t.Run("worktree for existing branch", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			// Create a branch but stay on master
			WithExec([]string{"git", "branch", "existing-branch"}).
			With(initDangModule("existing", `
type Existing {
  pub branch: String!
  pub files: [String!]!

  new(ws: Workspace!) {
    let ws2 = ws.withBranch("existing-branch")
    self.branch = ws2.branch
    self.files = ws2.glob("*")
    self
  }
}
`))
		out, err := ctr.With(daggerCall("existing", "branch")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "existing-branch", strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("existing", "files")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello.txt")
	})
	t.Run("with base ref", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("v1.txt", "version 1").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "v1"}).
			// Add a second commit on master
			WithNewFile("v2.txt", "version 2").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "v2"}).
			// Tag the first commit so we can reference it
			WithExec([]string{"git", "tag", "v1", "HEAD~1"}).
			With(initDangModule("baseref", `
type Baseref {
  pub files: [String!]!

  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/from-v1", base: "v1")
    self.files = ws2.glob("*")
    self
  }
}
`))
		out, err := ctr.With(daggerCall("baseref", "files")).Stdout(ctx)
		require.NoError(t, err)
		// Should have v1.txt (from v1) but NOT v2.txt (added after v1)
		require.Contains(t, out, "v1.txt")
		require.NotContains(t, out, "v2.txt",
			"branch based on v1 tag should not contain v2.txt")
	})
}

// TestWorkspaceCommit verifies that Workspace.commit writes changeset files
// and creates a git commit in the workspace.
func (WorkspaceSuite) TestStage(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("stage adds and modifies files", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("existing.txt", "original").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("stager", `
type Stager {
  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/stage")
    let before = ws2.directory(".")
    let after = before.
      withNewFile("new.txt", contents: "added").
      withNewFile("existing.txt", contents: "modified")
    ws2.stage(changes: after.changes(before))
    self
  }
}
`))
		staged := ctr.With(daggerCall("stager"))
		_, err := staged.Stdout(ctx)
		require.NoError(t, err)

		// New file should exist in the worktree.
		out, err := staged.
			WithWorkdir("/work-worktrees/agent-stage").
			WithExec([]string{"cat", "new.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "added", out)

		// Modified file should have new content.
		out, err = staged.
			WithWorkdir("/work-worktrees/agent-stage").
			WithExec([]string{"cat", "existing.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified", out)

		// Nothing should be committed yet.
		logOut, err := staged.
			WithWorkdir("/work-worktrees/agent-stage").
			WithExec([]string{"git", "log", "--oneline", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, logOut, "init", "no new commit should exist")

		// git status should show the changes as staged (added to index).
		statusOut, err := staged.
			WithWorkdir("/work-worktrees/agent-stage").
			WithExec([]string{"git", "status", "--porcelain"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, statusOut, "new.txt")
		require.Contains(t, statusOut, "existing.txt")
		// Verify they are staged (prefixed with A or M in first column)
		for _, line := range strings.Split(statusOut, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// First char is index status, should be A or M (staged)
			require.True(t, line[0] == 'A' || line[0] == 'M',
				"expected staged status, got %q", line)
		}
	})

	t.Run("stage removes files", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("keep.txt", "keep").
			WithNewFile("remove.txt", "gone").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("remstage", `
type Remstage {
  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/remstage")
    let before = ws2.directory(".")
    let after = before.withoutFile("remove.txt")
    ws2.stage(changes: after.changes(before))
    self
  }
}
`))
		staged := ctr.With(daggerCall("remstage"))
		_, err := staged.Stdout(ctx)
		require.NoError(t, err)

		// Removed file should be gone.
		_, err = staged.
			WithWorkdir("/work-worktrees/agent-remstage").
			WithExec([]string{"test", "-f", "remove.txt"}).
			Sync(ctx)
		require.Error(t, err, "remove.txt should not exist")

		// Kept file should still be there.
		out, err := staged.
			WithWorkdir("/work-worktrees/agent-remstage").
			WithExec([]string{"cat", "keep.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep", out)

		// Removal should be staged
		statusOut, err := staged.
			WithWorkdir("/work-worktrees/agent-remstage").
			WithExec([]string{"git", "status", "--porcelain"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, statusOut, "D  remove.txt")
	})

	t.Run("stage file in new subdirectory", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("subdir-stager", `
type SubdirStager {
  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/subdir")
    let before = ws2.directory(".")
    let after = before.withNewFile("pkg/newpkg/hello.go", contents: "package newpkg")
    ws2.stage(changes: after.changes(before))
    self
  }
}
`))
		staged := ctr.With(daggerCall("subdir-stager"))
		_, err := staged.Stdout(ctx)
		require.NoError(t, err)

		// File should exist in the worktree subdirectory.
		out, err := staged.
			WithWorkdir("/work-worktrees/agent-subdir").
			WithExec([]string{"cat", "pkg/newpkg/hello.go"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "package newpkg", out)

		// git status should show the file as staged.
		statusOut, err := staged.
			WithWorkdir("/work-worktrees/agent-subdir").
			WithExec([]string{"git", "status", "--porcelain"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, statusOut, "pkg/newpkg/hello.go")
	})

	t.Run("consecutive stages to different files without branch", func(ctx context.Context, t *testctx.T) {
		// Mirrors the LLM write tool pattern: each stage uses
		// directory(".", exclude: ["*"]) as the before (empty),
		// and adds a single new file as the after.
		ctr := workspaceBase(t, c).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("accum-nobranch", `
type AccumNobranch {
  pub run(ws: Workspace!): String! {
    let empty = ws.directory(".", exclude: ["*"])
    ws.stage(changes: empty.withNewFile("file-a.txt", contents: "aaa").changes(empty))
    ws.stage(changes: empty.withNewFile("file-b.txt", contents: "bbb").changes(empty))
    "done"
  }
}
`))
		staged := ctr.With(daggerCall("accum-nobranch", "run"))
		_, err := staged.Stdout(ctx)
		require.NoError(t, err)

		statusOut, err := staged.
			WithExec([]string{"git", "status", "--porcelain"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, statusOut, "file-a.txt",
			"file-a.txt from first stage should still be staged")
		require.Contains(t, statusOut, "file-b.txt",
			"file-b.txt from second stage should be staged")

		diffOut, err := staged.
			WithExec([]string{"git", "diff", "--cached"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, diffOut, "file-a.txt",
			"file-a.txt should appear in staged diff")
		require.Contains(t, diffOut, "file-b.txt",
			"file-b.txt should appear in staged diff")
	})

	t.Run("consecutive stages to same file accumulate", func(ctx context.Context, t *testctx.T) {
		// Build a 50-line file; each stage edits a different distant line
		// so the changes don't overlap in a diff context window.
		var original strings.Builder
		for i := 1; i <= 50; i++ {
			fmt.Fprintf(&original, "line %d original\n", i)
		}

		// Build version with line 10 edited
		var afterFirst strings.Builder
		for i := 1; i <= 50; i++ {
			if i == 10 {
				fmt.Fprintln(&afterFirst, "line 10 FIRST EDIT")
			} else {
				fmt.Fprintf(&afterFirst, "line %d original\n", i)
			}
		}

		// Build version with lines 10 AND 40 edited
		var afterSecond strings.Builder
		for i := 1; i <= 50; i++ {
			switch i {
			case 10:
				fmt.Fprintln(&afterSecond, "line 10 FIRST EDIT")
			case 40:
				fmt.Fprintln(&afterSecond, "line 40 SECOND EDIT")
			default:
				fmt.Fprintf(&afterSecond, "line %d original\n", i)
			}
		}

		// Place the "after" versions as sidecar files for the module to read.
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", original.String()).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			WithNewFile("/tmp/after-first.txt", afterFirst.String()).
			WithNewFile("/tmp/after-second.txt", afterSecond.String()).
			With(initDangModule("accum-stager", `
type AccumStager {
  pub run(
    ws: Workspace!,
    first: File!,
    second: File!,
  ): String! {
    let ws2 = ws.withBranch("agent/accum")

    let before1 = ws2.directory(".")
    let after1 = before1.withFile("hello.txt", first)
    ws2.stage(changes: after1.changes(before1))

    let before2 = ws2.directory(".")
    let after2 = before2.withFile("hello.txt", second)
    ws2.stage(changes: after2.changes(before2))

    "done"
  }
}
`))
		staged := ctr.With(daggerCall(
			"accum-stager", "run",
			"--first", "/tmp/after-first.txt",
			"--second", "/tmp/after-second.txt",
		))
		_, err := staged.Stdout(ctx)
		require.NoError(t, err)

		out, err := staged.
			WithWorkdir("/work-worktrees/agent-accum").
			WithExec([]string{"cat", "hello.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "line 10 FIRST EDIT",
			"first stage edit should be present on disk")
		require.Contains(t, out, "line 40 SECOND EDIT",
			"second stage edit should be present on disk")

		// Both edits should be staged — check git diff --cached
		diffOut, err := staged.
			WithWorkdir("/work-worktrees/agent-accum").
			WithExec([]string{"git", "diff", "--cached"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, diffOut, "FIRST EDIT",
			"first edit should appear in staged diff")
		require.Contains(t, diffOut, "SECOND EDIT",
			"second edit should appear in staged diff")
	})

	t.Run("force stage overwrites without merge", func(ctx context.Context, t *testctx.T) {
		// With force: true, modified files should be overwritten directly
		// (no 3-way merge), allowing recovery from conflicts.
		initialContent := "line1\nline2\nline3\nline4\nline5\n"
		agentContent := "line1\nline2\nagent-line3\nline4\nline5\n"

		ctr := workspaceBase(t, c).
			WithNewFile("shared.txt", initialContent).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("forcer", fmt.Sprintf(`
type Forcer {
  pub run(ws: Workspace!): String! {
    let ws2 = ws.withBranch("agent/force")
    let before = ws2.directory(".")
    let after = before.withNewFile("shared.txt", contents: %q)
    ws2.stage(changes: after.changes(before), force: true)
    "staged"
  }
}
`, agentContent)))

		// Create worktree, simulate user editing the SAME line (would conflict).
		ctr = ctr.
			WithExec([]string{"git", "worktree", "add", "-b", "agent/force",
				"/work-worktrees/agent-force"}).
			WithExec([]string{"sh", "-c",
				"printf 'line1\\nline2\\nuser-line3\\nline4\\nline5\\n' > /work-worktrees/agent-force/shared.txt"})

		// Force stage should succeed despite conflicting edits.
		out, err := ctr.With(daggerCall("forcer", "run")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "staged", strings.TrimSpace(out))

		// Working tree should have the agent's content (user edit overwritten).
		diskContent, err := ctr.With(daggerCall("forcer", "run")).
			WithWorkdir("/work-worktrees/agent-force").
			WithExec([]string{"cat", "shared.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, diskContent, "agent-line3",
			"force stage should write agent's content")
		require.NotContains(t, diskContent, "user-line3",
			"force stage should overwrite user's content")
	})

	t.Run("force stage recovers from conflict markers", func(ctx context.Context, t *testctx.T) {
		// Simulates the cascading conflict scenario: a file already has
		// conflict markers from a previous failed merge. A normal stage
		// would fail again, but force: true overwrites the file and
		// recovers.
		initialContent := "line1\nline2\nline3\nline4\nline5\n"
		resolvedContent := "line1\nline2\nresolved-line3\nline4\nline5\n"

		ctr := workspaceBase(t, c).
			WithNewFile("broken.txt", initialContent).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("recovery", fmt.Sprintf(`
type Recovery {
  pub run(ws: Workspace!): String! {
    let ws2 = ws.withBranch("agent/recovery")
    let before = ws2.directory(".")
    let after = before.withNewFile("broken.txt", contents: %q)
    ws2.stage(changes: after.changes(before), force: true)
    "recovered"
  }
}
`, resolvedContent)))

		// Create worktree, then manually write conflict markers into
		// the file (simulating what git merge-file leaves behind).
		conflictContent := "line1\nline2\n<<<<<<< broken.txt\nuser-line3\n=======\nagent-line3\n>>>>>>> /tmp/dagger-merge-after-12345\nline4\nline5\n"
		ctr = ctr.
			WithExec([]string{"git", "worktree", "add", "-b", "agent/recovery",
				"/work-worktrees/agent-recovery"}).
			WithExec([]string{"sh", "-c",
				fmt.Sprintf("printf '%s' > /work-worktrees/agent-recovery/broken.txt",
					strings.ReplaceAll(conflictContent, "'", "'\\''"))})

		// Force stage should succeed even though the file has conflict markers.
		out, err := ctr.With(daggerCall("recovery", "run")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "recovered", strings.TrimSpace(out))

		// Working tree should have the clean resolved content.
		diskContent, err := ctr.With(daggerCall("recovery", "run")).
			WithWorkdir("/work-worktrees/agent-recovery").
			WithExec([]string{"cat", "broken.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, diskContent, "resolved-line3",
			"force stage should write resolved content")
		require.NotContains(t, diskContent, "<<<<<<<",
			"conflict markers should be gone after force stage")
		require.NotContains(t, diskContent, ">>>>>>>",
			"conflict markers should be gone after force stage")
	})
}

func (WorkspaceSuite) TestApply(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("apply writes files without staging", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("existing.txt", "original").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("applier", `
type Applier {
  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/apply")
    let before = ws2.directory(".")
    let after = before.
      withNewFile("build/output.bin", contents: "binary data").
      withNewFile("existing.txt", contents: "modified")
    ws2.apply(changes: after.changes(before))
    self
  }
}
`))
		applied := ctr.With(daggerCall("applier"))
		_, err := applied.Stdout(ctx)
		require.NoError(t, err)

		// New file should exist on disk.
		out, err := applied.
			WithWorkdir("/work-worktrees/agent-apply").
			WithExec([]string{"cat", "build/output.bin"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "binary data", out)

		// Modified file should have new content.
		out, err = applied.
			WithWorkdir("/work-worktrees/agent-apply").
			WithExec([]string{"cat", "existing.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "modified", out)

		// Nothing should be staged in git.
		statusOut, err := applied.
			WithWorkdir("/work-worktrees/agent-apply").
			WithExec([]string{"git", "status", "--porcelain"}).
			Stdout(ctx)
		require.NoError(t, err)
		// Files should show as untracked or unstaged modifications,
		// NOT staged (no A or M in first column).
		for _, line := range strings.Split(statusOut, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			require.True(t, line[0] == '?' || line[0] == ' ',
				"expected untracked or unstaged, got %q", line)
		}
	})

	t.Run("apply removes files without staging", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("keep.txt", "keep").
			WithNewFile("remove.txt", "remove me").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("remapply", `
type Remapply {
  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/remapply")
    let before = ws2.directory(".")
    let after = before.withoutFile("remove.txt")
    ws2.apply(changes: after.changes(before))
    self
  }
}
`))
		applied := ctr.With(daggerCall("remapply"))
		_, err := applied.Stdout(ctx)
		require.NoError(t, err)

		// Removed file should be gone.
		_, err = applied.
			WithWorkdir("/work-worktrees/agent-remapply").
			WithExec([]string{"test", "-f", "remove.txt"}).
			Sync(ctx)
		require.Error(t, err, "remove.txt should not exist")

		// Kept file should still be there.
		out, err := applied.
			WithWorkdir("/work-worktrees/agent-remapply").
			WithExec([]string{"cat", "keep.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep", out)

		// The deletion should show as unstaged (not staged for commit).
		statusOut, err := applied.
			WithWorkdir("/work-worktrees/agent-remapply").
			WithExec([]string{"git", "status", "--porcelain"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, statusOut, "remove.txt")
		// Should be unstaged deletion (space + D)
		require.Contains(t, statusOut, " D remove.txt",
			"deletion should be unstaged")
	})
}

func (WorkspaceSuite) TestCommit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("stage and commit to worktree branch", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("committer", `
type Committer {
  pub hash: String!

  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/work")
    let before = ws2.directory(".")
    let after = before.withNewFile("new-file.txt", contents: "new content")
    ws2.stage(changes: after.changes(before))
    self.hash = ws2.commit(message: "feat: add new file")
    self
  }
}
`))
		// Run the module to trigger the commit — chain all
		// verification off this container so side effects are visible.
		committed := ctr.With(daggerCall("committer", "hash"))

		// The hash should be a full sha1
		hashOut, err := committed.Stdout(ctx)
		require.NoError(t, err)
		hash := strings.TrimSpace(hashOut)
		require.Len(t, hash, 40, "expected full sha1 commit hash, got %q", hash)

		// Verify the commit was created in the worktree
		logOut, err := committed.
			WithWorkdir("/work-worktrees/agent-work").
			WithExec([]string{"git", "log", "--oneline", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, logOut, "feat: add new file")

		// Verify the file exists in the worktree
		fileOut, err := committed.
			WithWorkdir("/work-worktrees/agent-work").
			WithExec([]string{"cat", "new-file.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content", fileOut)
	})

	t.Run("stage and commit to current branch", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("selfcommit", `
type Selfcommit {
  new(ws: Workspace!) {
    let before = ws.directory(".")
    let after = before.withNewFile("committed.txt", contents: "from agent")
    ws.stage(changes: after.changes(before))
    ws.commit(message: "feat: self commit")
    self
  }
}
`))
		committed := ctr.With(daggerCall("selfcommit"))
		_, err := committed.Stdout(ctx)
		require.NoError(t, err)

		// Verify commit in the main repo
		logOut, err := committed.
			WithExec([]string{"git", "log", "--oneline", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, logOut, "feat: self commit")

		fileOut, err := committed.
			WithExec([]string{"cat", "committed.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "from agent", fileOut)
	})

	t.Run("stage and commit with removed files", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("keep.txt", "keep").
			WithNewFile("remove.txt", "remove me").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("remover", `
type Remover {
  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/cleanup")
    let before = ws2.directory(".")
    let after = before.withoutFile("remove.txt")
    ws2.stage(changes: after.changes(before))
    ws2.commit(message: "chore: remove file")
    self
  }
}
`))
		committed := ctr.With(daggerCall("remover"))
		_, err := committed.Stdout(ctx)
		require.NoError(t, err)

		// Verify file was removed
		_, err = committed.
			WithWorkdir("/work-worktrees/agent-cleanup").
			WithExec([]string{"test", "-f", "remove.txt"}).
			Sync(ctx)
		require.Error(t, err, "remove.txt should not exist")

		// Verify keep.txt still exists
		out, err := committed.
			WithWorkdir("/work-worktrees/agent-cleanup").
			WithExec([]string{"cat", "keep.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep", out)
	})

	t.Run("multiple stage+commit to same branch", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("multi", `
type Multi {
  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/multi")

    let before1 = ws2.directory(".")
    let after1 = before1.withNewFile("first.txt", contents: "first")
    ws2.stage(changes: after1.changes(before1))
    ws2.commit(message: "feat: first commit")

    let before2 = ws2.directory(".")
    let after2 = before2.withNewFile("second.txt", contents: "second")
    ws2.stage(changes: after2.changes(before2))
    ws2.commit(message: "feat: second commit")

    self
  }
}
`))
		committed := ctr.With(daggerCall("multi"))
		_, err := committed.Stdout(ctx)
		require.NoError(t, err)

		// Verify both commits exist
		logOut, err := committed.
			WithWorkdir("/work-worktrees/agent-multi").
			WithExec([]string{"git", "log", "--oneline", "-2"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, logOut, "feat: first commit")
		require.Contains(t, logOut, "feat: second commit")

		// Verify both files exist
		out, err := committed.
			WithWorkdir("/work-worktrees/agent-multi").
			WithExec([]string{"cat", "first.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "first", out)

		out, err = committed.
			WithWorkdir("/work-worktrees/agent-multi").
			WithExec([]string{"cat", "second.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "second", out)
	})

	t.Run("multiple stages then single commit", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("multistage", `
type Multistage {
  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/multistage")

    let before1 = ws2.directory(".")
    let after1 = before1.withNewFile("first.txt", contents: "first")
    ws2.stage(changes: after1.changes(before1))

    let before2 = ws2.directory(".")
    let after2 = before2.withNewFile("second.txt", contents: "second")
    ws2.stage(changes: after2.changes(before2))

    ws2.commit(message: "feat: both changes")

    self
  }
}
`))
		committed := ctr.With(daggerCall("multistage"))
		_, err := committed.Stdout(ctx)
		require.NoError(t, err)

		// Verify single commit with both files
		logOut, err := committed.
			WithWorkdir("/work-worktrees/agent-multistage").
			WithExec([]string{"git", "log", "--oneline", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, logOut, "feat: both changes")

		// Only one new commit (plus init)
		logAll, err := committed.
			WithWorkdir("/work-worktrees/agent-multistage").
			WithExec([]string{"git", "log", "--oneline"}).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(logAll), "\n")
		require.Equal(t, 2, len(lines), "expected init + 1 commit, got: %s", logAll)

		// Verify both files exist
		out, err := committed.
			WithWorkdir("/work-worktrees/agent-multistage").
			WithExec([]string{"cat", "first.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "first", out)

		out, err = committed.
			WithWorkdir("/work-worktrees/agent-multistage").
			WithExec([]string{"cat", "second.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "second", out)
	})

	t.Run("stage same file twice overwrites prior staged version", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("overwrite", `
type Overwrite {
  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/overwrite")

    let before1 = ws2.directory(".")
    let after1 = before1.withNewFile("foo.txt", contents: "foo 1")
    ws2.stage(changes: after1.changes(before1))

    let before2 = ws2.directory(".")
    let after2 = before2.withNewFile("foo.txt", contents: "foo 2")
    ws2.stage(changes: after2.changes(before2))

    ws2.commit(message: "feat: final version of foo")

    self
  }
}
`))
		committed := ctr.With(daggerCall("overwrite"))
		_, err := committed.Stdout(ctx)
		require.NoError(t, err)

		// foo.txt should have the second version
		out, err := committed.
			WithWorkdir("/work-worktrees/agent-overwrite").
			WithExec([]string{"cat", "foo.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo 2", out)

		// Should be a single commit
		logAll, err := committed.
			WithWorkdir("/work-worktrees/agent-overwrite").
			WithExec([]string{"git", "log", "--oneline"}).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(strings.TrimSpace(logAll), "\n")
		require.Equal(t, 2, len(lines), "expected init + 1 commit, got: %s", logAll)
		require.Contains(t, logAll, "feat: final version of foo")
	})

	t.Run("stage does not clobber user edits to same file", func(ctx context.Context, t *testctx.T) {
		// A file has 5 lines. The user edits line 1 locally. The agent
		// edits line 5. After stage+commit: the agent's line-5 edit is
		// committed and staged; the user's line-1 edit survives on disk
		// as an unstaged modification. Both edits coexist.
		initialContent := "line1\nline2\nline3\nline4\nline5\n"
		// Agent changes line 5 only.
		agentContent := "line1\nline2\nline3\nline4\nagent-was-here\n"

		ctr := workspaceBase(t, c).
			WithNewFile("shared.txt", initialContent).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("clobber", fmt.Sprintf(`
type Clobber {
  pub hash: String!

  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/clobber")
    let before = ws2.directory(".")
    let after = before.withNewFile("shared.txt", contents: %q)
    ws2.stage(changes: after.changes(before))
    self.hash = ws2.commit(message: "feat: agent edits shared")
    self
  }
}
`, agentContent)))

		// Create the worktree, then simulate the user editing line 1.
		ctr = ctr.
			WithExec([]string{"git", "worktree", "add", "-b", "agent/clobber",
				"/work-worktrees/agent-clobber"}).
			WithExec([]string{"sh", "-c",
				"printf 'user-was-here\\nline2\\nline3\\nline4\\nline5\\n' > /work-worktrees/agent-clobber/shared.txt"})

		committed := ctr.With(daggerCall("clobber", "hash"))
		hashOut, err := committed.Stdout(ctx)
		require.NoError(t, err)
		hash := strings.TrimSpace(hashOut)
		require.Len(t, hash, 40, "expected full sha1 commit hash, got %q", hash)

		// The committed content should be the agent's version only
		// (line 5 changed, line 1 unchanged).
		commitShow, err := committed.
			WithWorkdir("/work-worktrees/agent-clobber").
			WithExec([]string{"git", "show", "HEAD:shared.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, agentContent, commitShow,
			"committed content should be agent's version")

		// The working tree should have BOTH edits merged:
		// line 1 = user's edit, line 5 = agent's edit.
		mergedContent := "user-was-here\nline2\nline3\nline4\nagent-was-here\n"
		diskContent, err := committed.
			WithWorkdir("/work-worktrees/agent-clobber").
			WithExec([]string{"cat", "shared.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, mergedContent, diskContent,
			"working tree should merge both user and agent edits")

		// The committed diff (HEAD vs HEAD~1) should show only the agent's
		// edit (line 5), not the user's edit (line 1).
		commitDiff, err := committed.
			WithWorkdir("/work-worktrees/agent-clobber").
			WithExec([]string{"git", "diff", "HEAD~1", "HEAD"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, commitDiff, "-line5")
		require.Contains(t, commitDiff, "+agent-was-here")
		require.NotContains(t, commitDiff, "user-was-here",
			"committed diff should not contain user's edit")

		// git diff (unstaged) should show only the user's edit (line 1).
		unstagedDiff, err := committed.
			WithWorkdir("/work-worktrees/agent-clobber").
			WithExec([]string{"git", "diff"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, unstagedDiff, "-line1")
		require.Contains(t, unstagedDiff, "+user-was-here")
		require.NotContains(t, unstagedDiff, "agent-was-here",
			"unstaged diff should not contain agent's edit")
	})

	t.Run("stage preserves user unstaged changes", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("preserver", `
type Preserver {
  pub hash: String!

  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/preserve")
    let before = ws2.directory(".")
    let after = before.withNewFile("agent-file.txt", contents: "from agent")
    ws2.stage(changes: after.changes(before))
    self.hash = ws2.commit(message: "feat: agent change")
    self
  }
}
`))

		// Simulate local user edits in the worktree BEFORE running the
		// module. We first create the worktree so the user can put files
		// there, then run dagger call which stages+commits on top.
		ctr = ctr.
			WithExec([]string{"git", "worktree", "add", "-b", "agent/preserve",
				"/work-worktrees/agent-preserve"}).
			// User edits a tracked file (will show as unstaged diff)
			WithExec([]string{"sh", "-c",
				"echo user edit >> /work-worktrees/agent-preserve/hello.txt"})

		committed := ctr.With(daggerCall("preserver", "hash"))
		hashOut, err := committed.Stdout(ctx)
		require.NoError(t, err)
		hash := strings.TrimSpace(hashOut)
		require.Len(t, hash, 40, "expected full sha1 commit hash, got %q", hash)

		// Agent's committed file should be on disk.
		agentFile, err := committed.
			WithWorkdir("/work-worktrees/agent-preserve").
			WithExec([]string{"cat", "agent-file.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "from agent", agentFile)

		// User's local edit should still be there (unstaged).
		userFile, err := committed.
			WithWorkdir("/work-worktrees/agent-preserve").
			WithExec([]string{"cat", "hello.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, userFile, "user edit")

		// The git log should show the agent commit.
		logOut, err := committed.
			WithWorkdir("/work-worktrees/agent-preserve").
			WithExec([]string{"git", "log", "--oneline", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, logOut, "feat: agent change")

		// User's edit should show as unstaged (modified in working tree).
		statusOut, err := committed.
			WithWorkdir("/work-worktrees/agent-preserve").
			WithExec([]string{"git", "status", "--porcelain"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, statusOut, "hello.txt",
			"user's unstaged edit should show in git status")
		// Should be unstaged modification (space + M)
		require.Contains(t, statusOut, " M hello.txt",
			"user's change should be unstaged (second column M)")
	})
}

// TestWorkspaceContentAddressed verifies that when a module constructor takes
// a Workspace argument, the result is content-addressed: calling a function
// twice with the same workspace content should be cached (the function body
// should not re-execute).
//
// We use nonNestedDevEngine so that each `dagger call` starts a fresh session
// against the same engine. This avoids the session-local dagql cache that
// would mask caching bugs — we need to test the engine's persistent cache.
func (WorkspaceSuite) TestWorkspaceContentAddressed(ctx context.Context, t *testctx.T) {
	var marker = "FUNCTION_EXECUTED:" + rand.Text()

	daggerCallWithLogs := func(args ...string) dagger.WithContainerFunc {
		return func(ctr *dagger.Container) *dagger.Container {
			execArgs := append([]string{"dagger", "--progress=logs", "call"}, args...)
			return ctr.WithExec(execArgs, dagger.ContainerWithExecOpts{
				UseEntrypoint: true,
			})
		}
	}

	t.Run("storing a Directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := workspaceBase(t, c).
			// use a non-nested dev engine - if we use nesting, we'll just hit
			// session-local caches, we need to ensure that each `dagger call` runs with
			// a fresh session to really test the caching semantics
			With(nonNestedDevEngine(c)).
			WithNewFile("included-file", rand.Text()).
			With(initDangModule("cacheme", `
type Cacheme {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".", exclude: ["*", "!included-file"])
    self
  }

  pub read: String! {
    print("`+marker+`")
    source.file("included-file").contents
  }
}
`))

		// First call — function should execute, marker appears in logs.
		first := base.With(daggerCallWithLogs("cacheme", "read"))
		out1, err := first.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out1, marker, "expected function to execute on first call")

		// Second call — same workspace content, function should be cached.
		// Uses a fresh session (non-nested), so only the engine's persistent
		// content-addressed cache can prevent re-execution.
		second := first.With(daggerCallWithLogs("cacheme", "read"))
		out2, err := second.CombinedOutput(ctx)
		require.NoError(t, err)
		// The marker should NOT appear in the second call's stderr, because the
		// function result should have been served from cache.
		require.NotContains(t, out2, marker,
			"expected function to be cached on second call with unchanged workspace content")

		// Third call - write to an unaffected file, function should still be cached
		third := second.
			WithNewFile("another-file", rand.Text()).
			With(daggerCallWithLogs("cacheme", "read"))
		out3, err := third.CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out3, marker,
			"expected function to be cached on third call with unchanged workspace content")

		// Fourth call - write to an affected file, function should not be cached
		newText := rand.Text()
		fourth := third.
			WithNewFile("included-file", newText).
			With(daggerCallWithLogs("cacheme", "read"))
		out4, err := fourth.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out4, newText,
			"expected function to pick up the new text")
		require.Contains(t, out4, marker,
			"expected function to be re-executed on fourth call with changed workspace content")
	})

	t.Run("storing a File", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := workspaceBase(t, c).
			// use a non-nested dev engine - if we use nesting, we'll just hit
			// session-local caches, we need to ensure that each `dagger call` runs with
			// a fresh session to really test the caching semantics
			With(nonNestedDevEngine(c)).
			WithNewFile("included-file", rand.Text()).
			With(initDangModule("cacheme", `
type Cacheme {
  pub source: File!

  new(source: Workspace!) {
    self.source = source.file("included-file")
    self
  }

  pub read: String! {
    print("`+marker+`")
    source.contents
  }
}
`))

		// First call — function should execute, marker appears in logs.
		first := base.With(daggerCallWithLogs("cacheme", "read"))
		out1, err := first.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out1, marker, "expected function to execute on first call")

		// Second call — same workspace content, function should be cached.
		// Uses a fresh session (non-nested), so only the engine's persistent
		// content-addressed cache can prevent re-execution.
		second := first.With(daggerCallWithLogs("cacheme", "read"))
		out2, err := second.CombinedOutput(ctx)
		require.NoError(t, err)
		// The marker should NOT appear in the second call's stderr, because the
		// function result should have been served from cache.
		require.NotContains(t, out2, marker,
			"expected function to be cached on second call with unchanged workspace content")

		// Third call - write to an unaffected file, function should still be cached
		third := second.
			WithNewFile("another-file", rand.Text()).
			With(daggerCallWithLogs("cacheme", "read"))
		out3, err := third.CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out3, marker,
			"expected function to be cached on third call with unchanged workspace content")

		// Fourth call - write to an affected file, function should not be cached
		newText := rand.Text()
		fourth := third.
			WithNewFile("included-file", newText).
			With(daggerCallWithLogs("cacheme", "read"))
		out4, err := fourth.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out4, newText,
			"expected function to pick up the new text")
		require.Contains(t, out4, marker,
			"expected function to be re-executed on fourth call with changed workspace content")
	})

	t.Run("storing the contents of a File", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := workspaceBase(t, c).
			// use a non-nested dev engine - if we use nesting, we'll just hit
			// session-local caches, we need to ensure that each `dagger call` runs with
			// a fresh session to really test the caching semantics
			With(nonNestedDevEngine(c)).
			WithNewFile("included-file", rand.Text()).
			With(initDangModule("cacheme", `
type Cacheme {
  pub source: String!

  new(source: Workspace!) {
    self.source = source.file("included-file").contents
    self
  }

  pub read: String! {
    print("`+marker+`")
    source
  }
}
`))

		// First call — function should execute, marker appears in logs.
		first := base.With(daggerCallWithLogs("cacheme", "read"))
		out1, err := first.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out1, marker, "expected function to execute on first call")

		// Second call — same workspace content, function should be cached.
		// Uses a fresh session (non-nested), so only the engine's persistent
		// content-addressed cache can prevent re-execution.
		second := first.With(daggerCallWithLogs("cacheme", "read"))
		out2, err := second.CombinedOutput(ctx)
		require.NoError(t, err)
		// The marker should NOT appear in the second call's stderr, because the
		// function result should have been served from cache.
		require.NotContains(t, out2, marker,
			"expected function to be cached on second call with unchanged workspace content")

		// Third call - write to an unaffected file, function should still be cached
		third := second.
			WithNewFile("another-file", rand.Text()).
			With(daggerCallWithLogs("cacheme", "read"))
		out3, err := third.CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out3, marker,
			"expected function to be cached on third call with unchanged workspace content")

		// Fourth call - write to an affected file, function should not be cached
		newText := rand.Text()
		fourth := third.
			WithNewFile("included-file", newText).
			With(daggerCallWithLogs("cacheme", "read"))
		out4, err := fourth.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out4, newText,
			"expected function to pick up the new text")
		require.Contains(t, out4, marker,
			"expected function to be re-executed on fourth call with changed workspace content")
	})
}
