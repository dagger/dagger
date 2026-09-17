package core

import (
	"context"
	"strings"

	"dagger.io/dagger"
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
