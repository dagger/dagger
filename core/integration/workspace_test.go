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

const dangSDK = "github.com/vito/dang/dagger-sdk@ca623b0182926bd94c82f0cad5149d3a1288a3b8"

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
// and source code. Uses "dagger module init" to scaffold the workspace and
// module, then overwrites main.dang with the provided source.
func initDangModule(name, source string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(daggerExec("module", "init", "--sdk="+dangSDK, name)).
			WithNewFile(".dagger/modules/"+name+"/main.dang", source)
	}
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
		requireErrOut(t, err, "outside workspace root")
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
		requireErrOut(t, err, "outside workspace root")
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
