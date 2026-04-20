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
			With(daggerExec("init", "--sdk=dang", "--name="+name)).
			WithNewFile("main.dang", source).
			WithWorkdir("../../").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "./toolchains/"+name))
	}
}

// initStandaloneDangModule creates a standalone Dang module in the current
// working directory and overwrites main.dang with the provided source.
func initStandaloneDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(daggerExec("init", "--sdk=dang", "--source=.", "--name="+name)).
			WithNewFile("main.dang", source)
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
// start path and stops at the workspace boundary.
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

	t.Run("do not find file in child directory", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--name=leaf.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})

	t.Run("do not find non-existent file", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--name=nonexistent.txt", "--from=a/b", "result")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", strings.TrimSpace(out))
	})
}

// TestNestedWorkspacePaths verifies that relative paths use the workspace
// directory while absolute paths and upward search use the workspace boundary.
func (WorkspaceSuite) TestNestedWorkspacePaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithExec([]string{"mkdir", "-p", "app"}).
		WithNewFile("repo.txt", "hello from boundary").
		WithNewFile("app/app.txt", "hello from workspace").
		WithWorkdir("/work/app").
		With(initStandaloneDangModule("paths", `
type Paths {
  pub workspaceValue: String!
  pub boundaryValue: String!
  pub foundValue: String!
  pub workspacePath: String!
  pub workspaceAddress: String!

  new(ws: Workspace!) {
    self.workspaceValue = ws.file("app.txt").contents
    self.boundaryValue = ws.file("/repo.txt").contents
    self.foundValue = ws.findUp(name: "repo.txt", from: ".") ?? ""
    self.workspacePath = ws.path
    self.workspaceAddress = ws.address
    self
  }
}
`))

	out, err := ctr.With(daggerCall("workspace-value")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello from workspace", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("boundary-value")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello from boundary", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("found-value")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "/repo.txt", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("workspace-path")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "/app", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("workspace-address")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "file:///work/app", strings.TrimSpace(out))
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

	t.Run("dagger call", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerCall("greeter", "read")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from workspace", strings.TrimSpace(out))
	})

	t.Run("dagger shell", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerShell("greeter | read")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from workspace", out)
	})
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

	t.Run("file traversal with ..", func(ctx context.Context, t *testctx.T) {
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

	t.Run("absolute path resolves from workspace boundary", func(ctx context.Context, t *testctx.T) {
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

// TestBlueprintFunctionsIncludesOtherModules verifies that `dagger functions`
// in a workspace with a blueprint module shows both the blueprint's own
// functions AND entrypoint functions for the other (non-blueprint) workspace
// modules.
func (WorkspaceSuite) TestBlueprintFunctionsIncludesOtherModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Set up a workspace with:
	// - a blueprint module ("ci") whose functions should be promoted
	// - two regular modules ("lint" and "test") whose constructors should
	//   appear as entrypoint functions alongside the blueprint's functions
	base := workspaceBase(t, c).
		// Create the blueprint module
		With(initDangBlueprint("ci", `
type Ci {
  pub source: Directory!

  new(source: Workspace!) {
    self.source = source.directory(".")
    self
  }

  pub build: String! {
    "built!"
  }

  pub deploy: String! {
    "deployed!"
  }
}
`)).
		// Create two additional non-blueprint modules
		With(initDangModule("lint", `
type Lint {
  pub check: String! {
    "lint passed"
  }
}
`)).
		With(initDangModule("test", `
type Test {
  pub run: String! {
    "tests passed"
  }
}
`))

	t.Run("dagger functions shows all modules", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)

		// Blueprint functions should be promoted to the top level.
		require.Contains(t, out, "build")
		require.Contains(t, out, "deploy")

		// Non-blueprint modules should appear as entrypoint functions.
		require.Contains(t, out, "lint")
		require.Contains(t, out, "test")
	})

	t.Run("dagger call blueprint function", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("build")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "built!", strings.TrimSpace(out))
	})

	t.Run("dagger call sibling module function", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("lint", "check")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "lint passed", strings.TrimSpace(out))
	})

	t.Run("query root exposes blueprint entrypoint methods", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{build,lint{check},test{run}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"build":"built!","lint":{"check":"lint passed"},"test":{"run":"tests passed"}}`, out)
	})

	t.Run("dagger shell blueprint function", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerShell("build")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "built!", out)
	})

	t.Run("dagger shell sibling module function", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerShell("lint | check")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "lint passed", out)
	})

	t.Run("dagger shell multiple sibling modules", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerShell("lint | check; test | run")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "lint passed")
		require.Contains(t, out, "tests passed")
	})
}

func (WorkspaceSuite) TestEntrypointProxyShadowsCoreFields(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		With(initDangBlueprint("ci", `
type Ci {
  pub build: String! {
    "built!"
  }

  pub container: String! {
    "custom container"
  }
}
`))

	t.Run("both proxies appear in functions", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "build")
		require.Contains(t, out, "container")
	})

	t.Run("proxy shadows core field", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("container")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "custom container", strings.TrimSpace(out))
	})
}

func (WorkspaceSuite) TestEntrypointProxyConstructorArgOverlap(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		With(initDangBlueprint("ci", `
type Ci {
  pub prefix: String!

  new(prefix: String! = "ctor") {
    self.prefix = prefix
    self
  }

  pub echo(prefix: String! = "method"): String! {
    self.prefix + ":" + prefix
  }
}
`))

	t.Run("proxy works with overlapping arg names", func(ctx context.Context, t *testctx.T) {
		// Constructor args are set via `with`, method args are on the proxy.
		// No collision even though both use "prefix".
		out, err := base.With(daggerCall("--prefix", "ctor", "echo", "--prefix", "method")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ctor:method", strings.TrimSpace(out))
	})

	t.Run("graphql with works for constructor args", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{with(prefix:"ctor"){echo(prefix:"method")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"with":{"echo":"ctor:method"}}`, out)
	})
}

// TestEntrypointProxyCoreAPIShadow verifies that when a module provides
// functions whose names collide with core API fields (e.g. "container",
// "file", "directory"), the proxies shadow the core fields on the outer
// server. The core API remains functional for engine-internal plumbing
// because it uses the inner server. Both proxy and namespaced paths work.
func (WorkspaceSuite) TestEntrypointProxyCoreAPIShadow(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		With(initStandaloneDangModule("shadows", `
type Shadows {
  pub container: String! {
    "my-container"
  }

  pub file: String! {
    "my-file"
  }

  pub directory: String! {
    "my-directory"
  }

  pub hello: String! {
    "hello!"
  }
}
`))

	t.Run("non-conflicting proxy works", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("hello")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello!", strings.TrimSpace(out))
	})

	t.Run("proxy shadows core field", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("container")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "my-container", strings.TrimSpace(out))

		out, err = base.With(daggerCall("file")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "my-file", strings.TrimSpace(out))

		out, err = base.With(daggerCall("directory")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "my-directory", strings.TrimSpace(out))
	})
}

// TestEntrypointProxySelfNamedMethod verifies that a module whose main object
// has a method with the same name as the module itself (e.g. module "test"
// with method "test") doesn't cause infinite recursion. The proxy for "test"
// would shadow the constructor; the inner server prevents the loop.
func (WorkspaceSuite) TestEntrypointProxySelfNamedMethod(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		With(initStandaloneDangModule("test", `
type Test {
  pub test: String! {
    "test-result"
  }

  pub other: String! {
    "other-result"
  }
}
`))

	t.Run("self-named proxy works", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("test")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "test-result", strings.TrimSpace(out))
	})

	t.Run("other proxy works", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("other")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "other-result", strings.TrimSpace(out))
	})
}

// TestEntrypointProxyCoreAPIShadowWithCoreReturnTypes verifies that a module
// returning core types (Directory, File, Container) from methods whose names
// collide with core API fields works correctly. The proxies shadow the core
// fields on the outer server but engine-internal plumbing uses the inner
// server, so there's no breakage.
func (WorkspaceSuite) TestEntrypointProxyCoreAPIShadowWithCoreReturnTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		With(initStandaloneDangModule("dirs", `
type Dirs {
  """
  Returns a directory — same name as the core API field.
  """
  pub directory: Directory! {
    Dagger.directory.withNewFile("hello.txt", "hello from dirs")
  }

  """
  Returns a file — same name as the core API field.
  """
  pub file: File! {
    Dagger.file("greeting.txt", "hi")
  }

  """
  Returns a container — same name as the core API field.
  """
  pub container: Container! {
    Dagger.container.from("alpine:3.20")
  }

  pub greet: String! {
    "greetings!"
  }
}
`))

	t.Run("non-conflicting proxy works", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "greetings!", strings.TrimSpace(out))
	})

	t.Run("proxy directory returns custom dir", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("directory", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello.txt", strings.TrimSpace(out))
	})

	t.Run("proxy file returns custom file", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("file", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi", strings.TrimSpace(out))
	})

	t.Run("proxy container runs", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("container", "with-exec", "--args=cat,/etc/alpine-release", "stdout")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, strings.TrimSpace(out), "3.20")
	})
}

// TestEntrypointProxyDirectoryField verifies that a container-based module
// with a Directory field can be constructed without triggering infinite
// recursion in the engine's ContainerRuntime.Call.
//
// ContainerRuntime.Call selects "directory" from the Query root to create a
// metadata directory. When the module has a "directory" field, the entrypoint
// proxy shadows the core field on the outer server. A raw GraphQL query
// resolves the constructor on the outer server directly, so
// ContainerRuntime.Call must use the inner server for its plumbing to avoid
// hitting the proxy.
//
// This test uses Go (a container-based SDK) and daggerQuery (raw GraphQL)
// because both are required to trigger the bug:
//   - Dang has a native runtime that doesn't use ContainerRuntime.Call
//   - daggerCall routes through proxy resolvers that delegate to the inner
//     server, masking the issue
func (WorkspaceSuite) TestEntrypointProxyDirectoryField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=playground", "--sdk=go", "--source=.")).
		WithNewFile("main.go", `package main

import (
	"dagger/playground/internal/dagger"
)

type Playground struct {
	*dagger.Directory
}

func New() Playground {
	return Playground{Directory: dag.Directory()}
}

func (p *Playground) SayHello() string {
	return "hello!"
}
`)

	// Query through entrypoint proxies — exercises ContainerRuntime.Call
	// because the proxy resolver delegates to the inner server, which
	// calls the container-based SDK. The "directory" proxy shadows the
	// core field on the outer server, but the inner server resolves
	// the core "directory" for engine plumbing.
	out, err := base.With(daggerQuery(`{sayHello, directory{entries}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"sayHello":"hello!", "directory":{"entries": []}}`, out)
}

// TestMainObjectWithPrefixedChildren mirrors the Elixir TestReturnChildObject
// test: module "objects" has main object Objects and children ObjectsA,
// ObjectsB.
//
// The child names are important: because ObjectsA already starts with
// "Objects" (the module name), namespaceObject is a no-op for it — it
// produces Name == gqlObjectName(OriginalName). A prior heuristic in
// mergeModuleQueryFields used that equality to identify main objects, which
// falsely matched ObjectsA and overwrote the real main object Objects.
// Children whose names don't carry the module prefix (e.g. "Child") would
// be namespaced to "ObjectsChild" and wouldn't trigger the bug.
func (WorkspaceSuite) TestMainObjectWithPrefixedChildren(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	source := `
type Objects {
  pub objectA: ObjectsA! {
    ObjectsA()
  }
}

type ObjectsA {
  pub message: String! {
    "Hello from A"
  }

  pub objectB: ObjectsB! {
    ObjectsB()
  }
}

type ObjectsB {
  pub message: String! {
    "Hello from B"
  }
}
`

	t.Run("standalone module", func(ctx context.Context, t *testctx.T) {
		base := workspaceBase(t, c).
			With(initStandaloneDangModule("objects", source))

		out, err := base.With(daggerCall("object-a", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello from A", strings.TrimSpace(out))

		out, err = base.With(daggerCall("object-a", "object-b", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello from B", strings.TrimSpace(out))
	})

	t.Run("toolchain module", func(ctx context.Context, t *testctx.T) {
		base := workspaceBase(t, c).
			With(initDangModule("objects", source))

		out, err := base.With(daggerCall("objects", "object-a", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello from A", strings.TrimSpace(out))

		out, err = base.With(daggerCall("objects", "object-a", "object-b", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello from B", strings.TrimSpace(out))
	})
}

// TestRenamedToolchainModule verifies that a toolchain module renamed via the
// workspace config "name" field still has its constructor correctly
// synthesized on Query. The SDK type keeps its original name (e.g.
// "HelloWorld") but the module is installed under the alias (e.g. "greeter").
// The namespaceObject function rewrites the main object's Name to match the
// alias, so this has always worked, but this test makes the coverage explicit.
func (WorkspaceSuite) TestRenamedToolchainModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Create a module named "hello-world" but install it as "greeter".
	base := workspaceBase(t, c).
		WithWorkdir("toolchains/hello-world").
		With(daggerExec("init", "--sdk=dang", "--name=hello-world")).
		WithNewFile("main.dang", `
type HelloWorld {
  pub greet(name: String! = "world"): String! {
    "hello, " + name + "!"
  }
}
`).
		WithWorkdir("../../").
		With(daggerExec("init")).
		WithNewFile("dagger.json", `
{
  "name": "app",
  "engineVersion": "v0.19.4",
  "toolchains": [
    {
      "name": "greeter",
      "source": "./toolchains/hello-world"
    }
  ]
}
`)

	t.Run("constructor appears under renamed alias", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("greeter", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello, world!", strings.TrimSpace(out))
	})

	t.Run("constructor accepts args", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("greeter", "greet", "--name", "dagger")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello, dagger!", strings.TrimSpace(out))
	})

	t.Run("functions list shows renamed alias", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "greeter")
	})

	t.Run("dagger shell renamed alias", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerShell("greeter | greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello, world!", out)
	})

	t.Run("dagger shell renamed alias with args", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerShell("greeter | greet --name dagger")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello, dagger!", out)
	})
}
