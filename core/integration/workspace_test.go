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

const dangSDK = "github.com/vito/dang/dagger-sdk@be6466632453a52120517e5551c266a239d3899b"

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
		// Ensure .dagger/ exists so that module init creates a workspace
		// module rather than defaulting to standalone in an empty dir.
		return ctr.
			WithExec([]string{"mkdir", "-p", ".dagger"}).
			With(daggerExec("module", "init", "--sdk="+dangSDK, name)).
			WithNewFile(".dagger/modules/"+name+"/main.dang", source)
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

// workspaceWithConfig returns a container with a workspace containing a config.toml.
func workspaceWithConfig(t testing.TB, c *dagger.Client, configTOML string) *dagger.Container {
	t.Helper()
	return workspaceBase(t, c).
		WithNewFile(".dagger/config.toml", configTOML)
}

func (WorkspaceSuite) TestConfigReadFullConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	configTOML := `[modules.my-module]
source = "modules/my-module"
blueprint = true

[modules.jest]
source = "github.com/dagger/jest"
`
	ctr := workspaceWithConfig(t, c, configTOML)

	out, err := ctr.With(daggerExec("workspace", "config")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "modules/my-module"`)
	require.Contains(t, out, "blueprint = true")
	require.Contains(t, out, `source = "github.com/dagger/jest"`)
}

func (WorkspaceSuite) TestConfigReadScalar(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	configTOML := `[modules.my-module]
source = "modules/my-module"
blueprint = true
`
	ctr := workspaceWithConfig(t, c, configTOML)

	// Read string value
	out, err := ctr.With(daggerExec("workspace", "config", "modules.my-module.source")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "modules/my-module", strings.TrimSpace(out))

	// Read bool value
	out, err = ctr.With(daggerExec("workspace", "config", "modules.my-module.blueprint")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "true", strings.TrimSpace(out))
}

func (WorkspaceSuite) TestConfigReadTable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	configTOML := `[modules.my-module]
source = "modules/my-module"
blueprint = true

[modules.jest]
source = "github.com/dagger/jest"
`
	ctr := workspaceWithConfig(t, c, configTOML)

	// Read a module table
	out, err := ctr.With(daggerExec("workspace", "config", "modules.my-module")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "modules/my-module"`)
	require.Contains(t, out, "blueprint = true")

	// Read the modules table (should flatten with dotted keys)
	out, err = ctr.With(daggerExec("workspace", "config", "modules")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `my-module.source = "modules/my-module"`)
	require.Contains(t, out, "my-module.blueprint = true")
	require.Contains(t, out, `jest.source = "github.com/dagger/jest"`)
}

func (WorkspaceSuite) TestConfigReadKeyNotSet(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	configTOML := `[modules.my-module]
source = "modules/my-module"
`
	ctr := workspaceWithConfig(t, c, configTOML)

	_, err := ctr.With(daggerExec("workspace", "config", "modules.nonexistent.source")).Stdout(ctx)
	require.Error(t, err)
	requireErrOut(t, err, "not set")
}

func (WorkspaceSuite) TestConfigWriteString(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	configTOML := `[modules.jest]
source = "github.com/dagger/jest"
`
	ctr := workspaceWithConfig(t, c, configTOML)

	// Write a new source value
	ctr = ctr.With(daggerExec("workspace", "config", "modules.jest.source", "github.com/eunomie/jest"))

	// Verify by reading back
	out, err := ctr.With(daggerExec("workspace", "config", "modules.jest.source")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "github.com/eunomie/jest", strings.TrimSpace(out))
}

func (WorkspaceSuite) TestConfigWriteBool(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	configTOML := `[modules.my-module]
source = "modules/my-module"
blueprint = true
`
	ctr := workspaceWithConfig(t, c, configTOML)

	// Write a bool value
	ctr = ctr.With(daggerExec("workspace", "config", "modules.my-module.blueprint", "false"))

	// Verify by reading back
	out, err := ctr.With(daggerExec("workspace", "config", "modules.my-module.blueprint")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "false", strings.TrimSpace(out))
}

func (WorkspaceSuite) TestConfigWriteArray(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	configTOML := `[modules.jest]
source = "github.com/dagger/jest"
`
	ctr := workspaceWithConfig(t, c, configTOML)

	// Write a comma-separated array
	ctr = ctr.With(daggerExec("workspace", "config", "modules.jest.config.tags", "main,develop"))

	// Verify the raw config file contains the array
	out, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `tags`)
	require.Contains(t, out, "main")
	require.Contains(t, out, "develop")
}

func (WorkspaceSuite) TestConfigWriteInvalidKey(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	configTOML := `[modules.jest]
source = "github.com/dagger/jest"
`
	ctr := workspaceWithConfig(t, c, configTOML)

	// Try to set an unknown key
	_, err := ctr.With(daggerExec("workspace", "config", "modules.jest.badfield", "value")).Stdout(ctx)
	require.Error(t, err)
	requireErrOut(t, err, "unknown config key")
}

func (WorkspaceSuite) TestConfigWritePreservesComments(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	configTOML := `# My workspace config
[modules.jest]
source = "github.com/dagger/jest"
`
	ctr := workspaceWithConfig(t, c, configTOML)

	// Write a value
	ctr = ctr.With(daggerExec("workspace", "config", "modules.jest.source", "github.com/eunomie/jest"))

	// Verify the comment is preserved
	out, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "# My workspace config")
	require.Contains(t, out, `"github.com/eunomie/jest"`)
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

// TestWorkspaceConfigDefaultString verifies that a string config value in
// config.toml flows through the user defaults pipeline and is used as the
// default for a constructor arg.
func (WorkspaceSuite) TestWorkspaceConfigDefaultString(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		With(initDangModule("greeter", `
type Greeter {
  pub greeting: String!

  new(greeting: String!) {
    self.greeting = greeting
    self
  }

  pub greet: String! {
    greeting
  }
}
`)).
		// Overwrite config.toml to add a config default
		WithNewFile(".dagger/config.toml", `[modules.greeter]
source = "modules/greeter"

[modules.greeter.config]
greeting = "hello from config"
`)

	out, err := ctr.With(daggerCall("greeter", "greet")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello from config", strings.TrimSpace(out))
}

// TestWorkspaceConfigDefaultEnvExpansion verifies that ${VAR} expansion works
// in workspace config defaults, flowing through the user defaults pipeline.
func (WorkspaceSuite) TestWorkspaceConfigDefaultEnvExpansion(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		With(initDangModule("greeter", `
type Greeter {
  pub greeting: String!

  new(greeting: String!) {
    self.greeting = greeting
    self
  }

  pub greet: String! {
    greeting
  }
}
`)).
		WithNewFile(".dagger/config.toml", `[modules.greeter]
source = "modules/greeter"

[modules.greeter.config]
greeting = "${MY_GREETING}"
`).
		WithEnvVariable("MY_GREETING", "expanded greeting")

	out, err := ctr.With(daggerCall("greeter", "greet")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "expanded greeting", strings.TrimSpace(out))
}

// TestWorkspaceConfigDefaultSecret verifies that env:// references in
// workspace config defaults resolve as Secrets through the user defaults pipeline.
func (WorkspaceSuite) TestWorkspaceConfigDefaultSecret(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		With(initDangModule("auth", `
type Auth {
  pub token: Secret!

  new(token: Secret!) {
    self.token = token
    self
  }

  pub reveal: String! {
    token.plaintext
  }
}
`)).
		WithNewFile(".dagger/config.toml", `[modules.auth]
source = "modules/auth"

[modules.auth.config]
token = "env://MY_TOKEN"
`).
		WithEnvVariable("MY_TOKEN", "supersecret")

	out, err := ctr.With(daggerCall("auth", "reveal")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "supersecret", strings.TrimSpace(out))
}

// TestWorkspaceConfigDefaultBool verifies that boolean config values work
// through the user defaults pipeline.
func (WorkspaceSuite) TestWorkspaceConfigDefaultBool(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		With(initDangModule("toggler", `
type Toggler {
  pub enabled: Boolean!

  new(enabled: Boolean!) {
    self.enabled = enabled
    self
  }

  pub check: String! {
    if (enabled) { "on" } else { "off" }
  }
}
`)).
		WithNewFile(".dagger/config.toml", `[modules.toggler]
source = "modules/toggler"

[modules.toggler.config]
enabled = true
`)

	out, err := ctr.With(daggerCall("toggler", "check")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "on", strings.TrimSpace(out))
}

// TestWorkspaceConfigDefaultInteger verifies that integer config values work
// through the user defaults pipeline.
func (WorkspaceSuite) TestWorkspaceConfigDefaultInteger(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		With(initDangModule("counter", `
type Counter {
  pub count: Int!

  new(count: Int!) {
    self.count = count
    self
  }

  pub value: Int! {
    count
  }
}
`)).
		WithNewFile(".dagger/config.toml", `[modules.counter]
source = "modules/counter"

[modules.counter.config]
count = 42
`)

	out, err := ctr.With(daggerCall("counter", "value")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "42", strings.TrimSpace(out))
}

// TestWorkspaceMigrateNonLocalSource verifies that `dagger migrate`
// moves source files from a non-"." source directory to .dagger/modules/<name>/
// and removes the old source directory.
func (WorkspaceSuite) TestWorkspaceMigrateNonLocalSource(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Set up a legacy project with source = "ci" — the source code lives in ci/
	ctr := workspaceBase(t, c).
		// Create legacy dagger.json with source = "ci"
		WithNewFile("dagger.json", `{
  "name": "myapp",
  "sdk": {"source": "`+dangSDK+`"},
  "source": "ci"
}`).
		// Create source files in ci/
		WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! {
    "hello from migrated source"
  }
}
`).
		// Commit so git status is clean (workspace detection needs git)
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})

	// Run migration
	ctr = ctr.With(daggerExec("migrate"))

	// Verify: old ci/ directory should be removed
	_, err := ctr.WithExec([]string{"test", "-d", "ci"}).Sync(ctx)
	require.Error(t, err, "old source directory 'ci' should have been removed")

	// Verify: source files should now be at .dagger/modules/myapp/
	out, err := ctr.WithExec([]string{"test", "-f", ".dagger/modules/myapp/main.dang"}).Sync(ctx)
	require.NoError(t, err, "source file should exist at new location")
	_ = out

	// Verify: .dagger/modules/myapp/dagger.json should exist
	djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger.json"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, djson, `"name": "myapp"`)
	// source should be empty (omitted) since files are now co-located
	require.NotContains(t, djson, `"source": "ci"`)

	// Verify: .dagger/config.toml should exist and reference the module
	configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, configOut, "modules/myapp")

	// Verify: root dagger.json should be removed
	_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
	require.Error(t, err, "root dagger.json should have been removed")
}

// TestWorkspaceMigrateLocalSource verifies that `dagger migrate`
// does NOT move source files when source = "." (the default case).
func (WorkspaceSuite) TestWorkspaceMigrateLocalSource(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Set up a legacy project with source = "." (implicit, SDK set but no source field)
	// We need toolchains for needsProjectModuleMigration to be true with source="."
	ctr := workspaceBase(t, c).
		WithNewFile("dagger.json", `{
  "name": "myapp",
  "sdk": {"source": "`+dangSDK+`"},
  "toolchains": [
    {"name": "test-tc", "source": "`+dangSDK+`"}
  ]
}`).
		WithNewFile("main.dang", `
type Myapp {
  pub greet: String! {
    "hello from root source"
  }
}
`).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})

	// Run migration
	ctr = ctr.With(daggerExec("migrate"))

	// Verify: main.dang should still be at root (not moved)
	_, err := ctr.WithExec([]string{"test", "-f", "main.dang"}).Sync(ctx)
	require.NoError(t, err, "source file should remain at root for source='.'")

	// Verify: .dagger/modules/myapp/dagger.json should exist with source pointing back to root
	djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger.json"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, djson, `"name": "myapp"`)
	require.Contains(t, djson, `"source": "../../../"`)

	// Verify: .dagger/config.toml should exist
	_, err = ctr.WithExec([]string{"test", "-f", ".dagger/config.toml"}).Sync(ctx)
	require.NoError(t, err, ".dagger/config.toml should exist after migration")

	// Verify: root dagger.json should be removed
	_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
	require.Error(t, err, "root dagger.json should have been removed")
}

// TestWorkspaceMigrateSummary verifies that the migration output includes
// relevant summary information.
func (WorkspaceSuite) TestWorkspaceMigrateSummary(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceBase(t, c).
		WithNewFile("dagger.json", `{
  "name": "myapp",
  "sdk": {"source": "`+dangSDK+`"},
  "source": "ci",
  "dependencies": [
    {"name": "dep1", "source": "./lib/dep1"}
  ],
  "include": ["extra/"]
}`).
		WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! { "hi" }
}
`).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "initial"})

	// Run migration — should print summary
	out, err := ctr.With(daggerExec("migrate")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "Migrated to workspace format")
}

// TestNestedModuleBeneathWorkspace verifies that a standalone dagger.json
// module nested inside a workspace takes precedence over the outer workspace
// when running from the module's directory. This tests the precedence rule:
//
//	./dagger.json > ../../.dagger/config.toml
//
// The user should be able to `cd` into the nested module and run `dagger call`
// / `dagger functions` without `-m .`.
func (WorkspaceSuite) TestNestedModuleBeneathWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Set up a workspace at /work with a module in .dagger/modules/outer.
	base := workspaceBase(t, c).
		With(initDangModule("outer", `
type Outer {
  pub greet: String! {
    "hello from outer"
  }
}
`))

	// Now create a standalone dagger.json module nested beneath the workspace.
	// Using "dagger module init" with a path creates the module WITHOUT adding
	// it to the workspace config.
	base = base.
		With(daggerExec("module", "init", "--sdk="+dangSDK, "inner", "./nested/inner")).
		WithNewFile("/work/nested/inner/main.dang", `
type Inner {
  pub msg: String!

  new(msg: String! = "hello from inner") {
    self.msg = msg
    self
  }

  pub greet: String! {
    msg
  }
}
`).
		WithWorkdir("/work/nested/inner")

	t.Run("standalone module not added to workspace config", func(ctx context.Context, t *testctx.T) {
		// The standalone module should NOT appear in the workspace config.
		out, err := base.
			WithWorkdir("/work").
			WithExec([]string{"cat", ".dagger/config.toml"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "inner")
	})

	t.Run("dagger functions lists the nested module", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "greet")
		// Should NOT show the outer workspace module's functions.
		require.NotContains(t, out, "outer")
	})

	t.Run("dagger call works without -m", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from inner", strings.TrimSpace(out))
	})

	t.Run("constructor flags work at top level", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerCall("--msg", "custom message", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "custom message", strings.TrimSpace(out))
	})
}

// TestFunctionsWithModFlag verifies that `dagger functions -m <module>` shows
// the module's promoted (auto-aliased) functions and hides the redundant
// constructor.
func (WorkspaceSuite) TestFunctionsWithModFlag(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		With(daggerExec("module", "init", "--sdk="+dangSDK, "greeter", "./greeter")).
		WithNewFile("/work/greeter/main.dang", `
type Greeter {
  pub name: String!

  new(name: String! = "world") {
    self.name = name
    self
  }

  pub greet: String! {
    "hello, " + name
  }

  pub shout: String! {
    "HELLO, " + name
  }
}
`).
		WithWorkdir("/work")

	runWithNiceFailure := func(ctx context.Context, base *dagger.Container, t *testctx.T, cmd ...string) string {
		exec := base.WithExec(append([]string{"dagger"}, cmd...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeAny,
		})
		out, err := exec.CombinedOutput(ctx)
		require.NoError(t, err)
		code, err := exec.ExitCode(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, code, "Command should not have failed. Output:\n\n%s", out)
		out, err = exec.Stdout(ctx)
		require.NoError(t, err)
		return out
	}

	t.Run("shows promoted functions not constructor", func(ctx context.Context, t *testctx.T) {
		out := runWithNiceFailure(ctx, base, t, "-m", "./greeter", "functions")
		lines := strings.Split(out, "\n")
		// The promoted functions should be listed.
		require.Contains(t, lines, "greet   -")
		require.Contains(t, lines, "shout   -")
		// The constructor should NOT appear — its functions are already promoted.
		for _, line := range lines {
			require.NotContains(t, line, "greeter")
		}
	})

	t.Run("call works with promoted functions", func(ctx context.Context, t *testctx.T) {
		out := runWithNiceFailure(ctx, base, t, "-m", "./greeter", "call", "greet")
		require.Equal(t, "hello, world", strings.TrimSpace(out))
	})

	t.Run("call works with constructor args and promoted functions", func(ctx context.Context, t *testctx.T) {
		out := runWithNiceFailure(ctx, base, t, "-m", "./greeter", "call", "--name", "dagger", "shout")
		require.Equal(t, "HELLO, dagger", strings.TrimSpace(out))
	})
}
