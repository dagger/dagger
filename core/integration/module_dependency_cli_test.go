package core

import (
	"context"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (CLISuite) TestModuleDependencyUpdate(ctx context.Context, t *testctx.T) {
	const (
		stalePin = "b20176e68d27edc9660960ec27f323d33dba633b"
		v041Pin  = "5c8b312cd7c8493966d28c118834d4e9565c7c62"
	)

	c := connect(ctx, t)
	ctr := goGitBase(t, c).
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
}
