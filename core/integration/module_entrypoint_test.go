package core

import (
	"context"
	"dagger.io/dagger"
	"strings"

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
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
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
		With(daggerCallAt("app", "message"))

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "loaded through the module entrypoint", strings.TrimSpace(out))
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
	// dagql rejects the self-reference before the driver's own chain sees the
	// directory twice. Either way the cycle is reported, not followed.
	require.Contains(t, out, "recursive call detected")
}
