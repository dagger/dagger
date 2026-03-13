package core

import (
	"context"
	"crypto/rand"
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

const dangSDK = "github.com/vito/dang/dagger-sdk@2de20f19b971dad3ee6038e6728736ef1f9a056b"

// workspaceBase returns a container with git, the dagger CLI, and an
// initialized git repo at /work — the starting point for workspace tests.
func workspaceBase(t testing.TB, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"})
}

// initDangModule creates a Dang module in the workspace with the given name
// and source code. Uses "dagger init" and "dagger toolchain install" to
// scaffold the workspace and module, then overwrites main.dang with the
// provided source.
func initDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithWorkdir("toolchains/"+name).
			With(daggerExec("init", "--sdk="+dangSDK, "--name="+name)).
			WithNewFile("main.dang", source).
			WithWorkdir("../../").
			With(daggerExec("init")).
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
			With(daggerExec("init", "--sdk="+dangSDK, "--name="+name)).
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
  pub file_paths: [String!]!

  new(ws: Workspace!, pattern: String!) {
    results := ws.search(pattern: pattern)
    self.file_paths = []
    for result in results {
      self.file_paths = self.file_paths + [result.filePath + ":" + "\(result.lineNumber)"]
    }
    self
  }
}

type FilesSearcher {
  pub files: [String!]!

  new(ws: Workspace!, pattern: String!, globs: [String!]) {
    results := ws.search(pattern: pattern, filesOnly: true, globs: globs)
    self.files = []
    for result in results {
      self.files = self.files + [result.filePath]
    }
    self
  }
}
`))

	t.Run("basic search", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("searcher", "--pattern=hello", "file-paths")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.Contains(t, lines, "hello.txt:1")
		require.Contains(t, lines, "src/main.go:4")
		require.Contains(t, lines, "src/util.go:4")
	})

	t.Run("files only", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("files-searcher", "--pattern=hello", "files")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.TrimSpace(out)
		require.Contains(t, lines, "hello.txt")
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
    ws2 := ws.withBranch("agent/test")
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
    ws2 := ws.withBranch(ws.branch)
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
    ws2 := ws.withBranch("existing-branch")
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
}

// TestWorkspaceCommit verifies that Workspace.commit writes changeset files
// and creates a git commit in the workspace.
func (WorkspaceSuite) TestCommit(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("commit changeset to worktree branch", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("committer", `
type Committer {
  new(ws: Workspace!) {
    ws2 := ws.withBranch("agent/work")
    before := ws2.directory(".")
    after := before.withNewFile("new-file.txt", "new content")
    changeset := before.diff(after)
    ws2.commit(changeset, "feat: add new file")
    self
  }
}
`))
		// Run the module to trigger the commit
		_, err := ctr.With(daggerCall("committer")).Stdout(ctx)
		require.NoError(t, err)

		// Verify the commit was created in the worktree
		logOut, err := ctr.
			WithWorkdir("/work-worktrees/agent-work").
			WithExec([]string{"git", "log", "--oneline", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, logOut, "feat: add new file")

		// Verify the file exists in the worktree
		fileOut, err := ctr.
			WithWorkdir("/work-worktrees/agent-work").
			WithExec([]string{"cat", "new-file.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "new content", fileOut)
	})

	t.Run("commit to current branch", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("selfcommit", `
type Selfcommit {
  new(ws: Workspace!) {
    before := ws.directory(".")
    after := before.withNewFile("committed.txt", "from agent")
    changeset := before.diff(after)
    ws.commit(changeset, "feat: self commit")
    self
  }
}
`))
		_, err := ctr.With(daggerCall("selfcommit")).Stdout(ctx)
		require.NoError(t, err)

		// Verify commit in the main repo
		logOut, err := ctr.
			WithExec([]string{"git", "log", "--oneline", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, logOut, "feat: self commit")

		fileOut, err := ctr.
			WithExec([]string{"cat", "committed.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "from agent", fileOut)
	})

	t.Run("commit with removed files", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("keep.txt", "keep").
			WithNewFile("remove.txt", "remove me").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("remover", `
type Remover {
  new(ws: Workspace!) {
    ws2 := ws.withBranch("agent/cleanup")
    before := ws2.directory(".")
    after := before.withoutFile("remove.txt")
    changeset := before.diff(after)
    ws2.commit(changeset, "chore: remove file")
    self
  }
}
`))
		_, err := ctr.With(daggerCall("remover")).Stdout(ctx)
		require.NoError(t, err)

		// Verify file was removed
		_, err = ctr.
			WithWorkdir("/work-worktrees/agent-cleanup").
			WithExec([]string{"test", "-f", "remove.txt"}).
			Sync(ctx)
		require.Error(t, err, "remove.txt should not exist")

		// Verify keep.txt still exists
		out, err := ctr.
			WithWorkdir("/work-worktrees/agent-cleanup").
			WithExec([]string{"cat", "keep.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep", out)
	})

	t.Run("multiple commits to same branch", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("hello.txt", "hello").
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "init"}).
			With(initDangModule("multi", `
type Multi {
  new(ws: Workspace!) {
    ws2 := ws.withBranch("agent/multi")

    before1 := ws2.directory(".")
    after1 := before1.withNewFile("first.txt", "first")
    ws2.commit(before1.diff(after1), "feat: first commit")

    before2 := ws2.directory(".")
    after2 := before2.withNewFile("second.txt", "second")
    ws2.commit(before2.diff(after2), "feat: second commit")

    self
  }
}
`))
		_, err := ctr.With(daggerCall("multi")).Stdout(ctx)
		require.NoError(t, err)

		// Verify both commits exist
		logOut, err := ctr.
			WithWorkdir("/work-worktrees/agent-multi").
			WithExec([]string{"git", "log", "--oneline", "-2"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, logOut, "feat: first commit")
		require.Contains(t, logOut, "feat: second commit")

		// Verify both files exist
		out, err := ctr.
			WithWorkdir("/work-worktrees/agent-multi").
			WithExec([]string{"cat", "first.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "first", out)

		out, err = ctr.
			WithWorkdir("/work-worktrees/agent-multi").
			WithExec([]string{"cat", "second.txt"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "second", out)
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
