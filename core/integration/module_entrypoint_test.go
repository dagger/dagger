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

// A manifest with entrypoint kind "module" loads its entrypoint source as a
// module and calls ModuleEntrypoint on the object its constructor returns.
func (ModuleSuite) TestModuleKindModuleEntrypoint(ctx context.Context, t *testctx.T) {
	t.Run("a path under the module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.app]
source = ".dagger/modules/app"
`).
			WithNewFile(".dagger/modules/app/dagger-module.toml", `name = "app"

[entrypoint]
kind = "module"
source = "./entrypoint-module"
`).
			WithDirectory(
				".dagger/modules/app/entrypoint-module",
				c.Host().Directory("./testdata/modules/dang/entrypoint-module"),
			).
			With(daggerCallAt("app", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "loaded through the module entrypoint", strings.TrimSpace(out))
	})

	// entrypoint.source resolves like runtime.source, so a sibling module is a
	// valid entrypoint. Resolving it as a path under the module directory would
	// reject this.
	t.Run("a path beside the module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.app]
source = ".dagger/modules/app"
`).
			WithNewFile(".dagger/modules/app/dagger-module.toml", `name = "app"

[entrypoint]
kind = "module"
source = "../entrypoint-module"
`).
			WithDirectory(
				".dagger/modules/entrypoint-module",
				c.Host().Directory("./testdata/modules/dang/entrypoint-module"),
			).
			With(daggerCallAt("app", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "loaded through the module entrypoint", strings.TrimSpace(out))
	})
}

// entrypoint.source means what runtime.source means, so a runtime module is a
// valid entrypoint even though it does not implement ModuleEntrypoint. The
// engine drives it through the runtime adapter instead of calling types.
func (ModuleSuite) TestModuleKindModuleEntrypointWrapsARuntime(ctx context.Context, t *testctx.T) {
	t.Run("a built-in runtime name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := goGitBase(t, c).
			WithNewFile("dagger.toml", `[modules.app]
source = ".dagger/modules/app"
`).
			WithNewFile(".dagger/modules/app/dagger-module.toml", `name = "app"

[entrypoint]
kind = "module"
source = "dang"
`).
			WithNewFile(".dagger/modules/app/main.dang", `
type App {
  pub message: String! {
    "loaded through the runtime adapter"
  }
}
`).
			With(daggerCallAt("app", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "loaded through the runtime adapter", strings.TrimSpace(out))
	})
}

// A module entrypoint that names itself is a cycle. The engine reports the
// chain instead of looping.
func (ModuleSuite) TestModuleKindModuleEntrypointRejectsCycle(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.loop]
source = ".dagger/modules/loop"
`).
		WithNewFile(".dagger/modules/loop/dagger-module.toml", `name = "loop"

[entrypoint]
kind = "module"
source = "."
`).
		WithExec([]string{"dagger", "call", "-m", "loop", "message"}, dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		}).
		Stderr(ctx)
	require.NoError(t, err)
	// Resolving the entrypoint as a module reference means the existing
	// circular dependency check sees the loop first.
	require.Contains(t, out, `module "loop" has a circular dependency on itself`)
}
