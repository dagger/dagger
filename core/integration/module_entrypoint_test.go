package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestBuiltinDangModuleEntrypoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "entrypoint"
`).
		WithDirectory(
			".dagger/modules/tiny/entrypoint",
			c.Host().Directory("./testdata/modules/dang/module-entrypoint"),
		).
		With(daggerCallAt("tiny", "hello"))

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}

func (ModuleSuite) TestBuiltinDangModuleEntrypointFromSubdir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithDirectory(
			".dagger/modules/tiny/entrypoint",
			c.Host().Directory("./testdata/modules/dang/module-entrypoint"),
		).
		WithNewFile("sub/dir/.keep", "").
		WithWorkdir("sub/dir").
		With(daggerCallAt("tiny", "hello"))

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}

// The engine records which interface it drove a module through, but the span is
// internal: it belongs in a trace, not in the output of an ordinary call.
func (ModuleSuite) TestModuleEntrypointInterfaceSpanIsInternal(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	out, err := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.app]
source = ".dagger/modules/app"
`).
		WithNewFile(".dagger/modules/app/dagger-module.toml", `name = "app"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithDirectory(
			".dagger/modules/app/entrypoint",
			c.Host().Directory("./testdata/modules/dang/module-entrypoint"),
		).
		With(daggerCallAt("app", "hello")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))

	require.NoError(t, c.Close()) // close + flush logs
	require.NotContains(t, logs.String(), "module entrypoint interface")
	require.NotContains(t, logs.String(), "legacy runtime interface")
}

// entrypoint.source can be a module reference, resolved the way runtime.source
// is. One entrypoint served from a git repository can then back many modules,
// with nothing generated into them.
func (ModuleSuite) TestDangModuleEntrypointFromModuleRef(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The served repository holds only Dang files under entrypoint/: an
	// entrypoint directory is not a module and carries no manifest.
	served := c.Directory().WithDirectory(
		"entrypoint",
		c.Host().Directory("./testdata/modules/dang/module-entrypoint"),
	)
	gitDaemon, repoURL := gitService(ctx, t, c, served)
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	out, err := goGitBase(t, c).
		WithServiceBinding(gitHost, gitDaemon).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "`+repoURL+`#main:entrypoint"
`).
		With(daggerCallAt("tiny", "hello")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}

// An entrypoint directory usually lives inside the repository of the SDK that
// owns it, below that SDK's own module manifest. The reference names the
// entrypoint directory, so the engine must not walk up to the enclosing module
// and evaluate that module's Dang files instead.
func (ModuleSuite) TestDangModuleEntrypointFromModuleRefInsideModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The root module's Dang file does not type check on its own. Evaluating it
	// as the entrypoint fails, so the test passes only when the engine reads
	// entrypoint/ and nothing above it.
	served := c.Directory().
		WithNewFile("dagger-module.toml", `name = "owner"

[runtime]
source = "dang"
`).
		WithNewFile("main.dang", `type Owner {
  pub broken: String! {
    missingDependency.value
  }
}
`).
		WithDirectory(
			"entrypoint",
			c.Host().Directory("./testdata/modules/dang/module-entrypoint"),
		)
	gitDaemon, repoURL := gitService(ctx, t, c, served)
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)

	out, err := goGitBase(t, c).
		WithServiceBinding(gitHost, gitDaemon).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "`+repoURL+`#main:entrypoint"
`).
		With(daggerCallAt("tiny", "hello")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}

// The workspace an entrypoint receives has its working directory at the module
// it serves, so a shared entrypoint can find the module without a path
// generated into it.
func (ModuleSuite) TestModuleEntrypointWorkspaceCwdIsModuleDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithDirectory(
			".dagger/modules/tiny/entrypoint",
			c.Host().Directory("./testdata/modules/dang/module-entrypoint-cwd"),
		).
		With(daggerCallAt("tiny", "where")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, ".dagger/modules/tiny", strings.TrimPrefix(strings.TrimSpace(out), "/"))
}

// A module source loaded from a workspace carries that workspace, and its path
// is relative to it. The entrypoint must be handed that workspace, scoped to
// the module, even when the calling client's current workspace is a different
// tree: here the test's own, which has no mods/tiny/marker.txt.
func (ModuleSuite) TestModuleEntrypointWorkspaceIsModuleSourceWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	served := c.Directory().
		WithNewFile("mods/tiny/dagger-module.toml", `name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		WithNewFile("mods/tiny/marker.txt", "from-git-workspace").
		WithDirectory(
			"mods/tiny/entrypoint",
			c.Host().Directory("./testdata/modules/dang/module-entrypoint-workspace"),
		)
	gitDaemon, repoURL := gitService(ctx, t, c, served)

	err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Branch("main").
		AsWorkspace().
		ModuleSource("mods/tiny").
		AsModule().
		Serve(ctx)
	require.NoError(t, err)

	res, err := testutil.QueryWithClient[struct {
		Tiny struct {
			Marker string
		}
	}](c, t, `{tiny{marker}}`, nil)
	require.NoError(t, err)
	require.Equal(t, "/mods/tiny from-git-workspace", res.Tiny.Marker)
}

// sourceWorkspaceManifest is the manifest of a module served by the
// module-entrypoint-source-workspace entrypoint.
const sourceWorkspaceManifest = `name = "tiny"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`

// sourceWorkspaceModule is a module context with a marker file above the
// module directory, outside the files the module includes.
func sourceWorkspaceModule(c *dagger.Client, marker string) *dagger.Directory {
	return c.Directory().
		WithNewFile("marker.txt", marker).
		WithNewFile("mods/tiny/dagger-module.toml", sourceWorkspaceManifest).
		WithDirectory(
			"mods/tiny/entrypoint",
			c.Host().Directory("./testdata/modules/dang/module-entrypoint-source-workspace"),
		)
}

// callerWithFiles is a caller's workspace holding its own marker and a file
// only the caller has. An entrypoint handed the caller's workspace would find
// both.
func callerWithFiles(t *testctx.T, c *dagger.Client) *dagger.Container {
	return goGitBase(t, c).
		WithNewFile("marker.txt", "from-caller").
		WithNewFile("caller.txt", "caller")
}

// A module loaded by git ref is in no workspace of the caller's. Its entrypoint
// gets the repository at the pinned commit, with its working directory at the
// module, so it can read files above the module and none of the caller's.
func (ModuleSuite) TestModuleEntrypointWorkspaceFromGitSource(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	gitDaemon, repoURL := gitService(ctx, t, c, sourceWorkspaceModule(c, "from-git-root"))
	gitHost, err := gitDaemon.Hostname(ctx)
	require.NoError(t, err)
	modRef := repoURL + "#main:mods/tiny"

	ctr := callerWithFiles(t, c).WithServiceBinding(gitHost, gitDaemon)

	out, err := ctr.With(daggerCallAt(modRef, "probe")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "/mods/tiny /marker.txt from-git-root none", strings.TrimSpace(out))

	// The workspace is content-addressed by the commit: two sessions get the
	// same one, and it is the one any client builds from that commit's tree.
	first, err := ctr.With(daggerCallAt(modRef, "address")).Stdout(ctx)
	require.NoError(t, err)
	second, err := ctr.
		WithEnvVariable("SECOND_SESSION", "1").
		With(daggerCallAt(modRef, "address")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(first), strings.TrimSpace(second))
	require.True(t, strings.HasPrefix(strings.TrimSpace(first), "directory://"), first)

	fromCommit, err := c.Git(repoURL, dagger.GitOpts{ExperimentalServiceHost: gitDaemon}).
		Branch("main").
		Tree().
		AsWorkspace().
		Address(ctx)
	require.NoError(t, err)
	require.Equal(t, fromCommit, strings.TrimSpace(first))
}

// A module loaded from a directory is in no workspace of the caller's. Its
// entrypoint gets the directory the module source was created from.
func (ModuleSuite) TestModuleEntrypointWorkspaceFromDirectorySource(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	err := sourceWorkspaceModule(c, "from-directory-root").
		AsModuleSource(dagger.DirectoryAsModuleSourceOpts{SourceRootPath: "mods/tiny"}).
		AsModule().
		Serve(ctx)
	require.NoError(t, err)

	res, err := testutil.QueryWithClient[struct {
		Tiny struct {
			Probe string
		}
	}](c, t, `{tiny{probe}}`, nil)
	require.NoError(t, err)
	require.Equal(t, "/mods/tiny /marker.txt from-directory-root none", res.Tiny.Probe)
}

// A local module outside the caller's workspace gets its own context on the
// caller's host, rooted where the module source is: its git root, or the
// module directory when there is none.
func (ModuleSuite) TestModuleEntrypointWorkspaceFromLocalSourceOutsideWorkspace(ctx context.Context, t *testctx.T) {
	t.Run("git root", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := callerWithFiles(t, c).
			WithDirectory("/outside", sourceWorkspaceModule(c, "from-local-root")).
			WithWorkdir("/outside").
			WithExec([]string{"git", "init"}).
			WithWorkdir("/work").
			With(daggerCallAt("/outside/mods/tiny", "probe")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/mods/tiny /marker.txt from-local-root none", strings.TrimSpace(out))
	})

	// With no git root and no workspace config, the engine gives the caller a
	// rootless workspace, which holds no files even though its path contains
	// the module.
	t.Run("rootless caller", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goGitBase(t, c).
			WithWorkdir("/plain").
			WithNewFile("caller.txt", "caller").
			WithNewFile("mods/tiny/dagger-module.toml", sourceWorkspaceManifest).
			WithNewFile("mods/tiny/marker.txt", "from-module").
			WithDirectory(
				"mods/tiny/entrypoint",
				c.Host().Directory("./testdata/modules/dang/module-entrypoint-source-workspace"),
			).
			With(daggerCallAt("./mods/tiny", "probe")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/ /marker.txt from-module none", strings.TrimSpace(out))
	})
}
