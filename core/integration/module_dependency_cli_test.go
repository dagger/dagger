package core

import (
	"context"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (CLISuite) TestModuleDependencyUpdate(ctx context.Context, t *testctx.T) {
	const (
		stalePin = "b20176e68d27edc9660960ec27f323d33dba633b"
		v041Pin  = "5c8b312cd7c8493966d28c118834d4e9565c7c62"
	)
	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup(
		"",
		"git-sha",
		[]any{
			"https://github.com/shykes/daggerverse",
			"refs/tags/docker/v0.4.1",
		},
		stalePin,
	))
	lockContents, err := lock.Marshal()
	require.NoError(t, err)

	c := connect(ctx, t)
	ctr := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.foo]
source = "."
`).
		WithNewFile("dagger.lock", string(lockContents)).
		WithNewFile("dagger-module.toml", `name = "foo"

[runtime]
source = "go"

[[dependencies]]
name = "docker"
source = "github.com/shykes/daggerverse/docker@docker/v0.4.1"
pin = "`+stalePin+`"
`).
		With(daggerExec("module", "deps", "update", "docker"))

	moduleConfig, err := ctr.File("dagger-module.toml").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, moduleConfig, v041Pin)
	require.NotContains(t, moduleConfig, stalePin)

	updatedLock, err := ctr.File("dagger.lock").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, string(lockContents), updatedLock)
}

func (CLISuite) TestModuleDependencyPinOverridesWorkspaceLock(ctx context.Context, t *testctx.T) {
	const (
		stalePin = "b20176e68d27edc9660960ec27f323d33dba633b"
		v041Pin  = "5c8b312cd7c8493966d28c118834d4e9565c7c62"
	)
	lock := workspace.NewLock()
	require.NoError(t, lock.SetLookup(
		"",
		"git-sha",
		[]any{"https://github.com/shykes/daggerverse", "HEAD"},
		stalePin,
	))
	lockContents, err := lock.Marshal()
	require.NoError(t, err)

	c := connect(ctx, t)
	out, err := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.foo]
source = "."
`).
		WithNewFile("dagger.lock", string(lockContents)).
		WithNewFile("dagger-module.toml", `name = "foo"

[runtime]
source = "go"

[[dependencies]]
name = "docker"
source = "github.com/shykes/daggerverse/docker"
pin = "`+v041Pin+`"
`).
		With(daggerExec("module", "deps", "list")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, v041Pin)
	require.NotContains(t, out, stalePin)
}
