package core

// Workspace alignment: mostly aligned; coverage targets post-workspace nested CLI execution from inside module-launched containers, though setup still relies on historical module helpers.
// Scope: Running the Dagger CLI from inside a module-executed container with privileged nesting enabled.
// Intent: Keep nested CLI execution separate from host-side `dagger listen` behavior and the remaining module runtime umbrella coverage.

import (
	"context"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestNestedClientCreatedByModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, cli *dagger.File, modDir *dagger.Directory) (string, error) {
	return dag.Container().From("`+alpineImage+`").
		WithMountedFile("/bin/dagger", cli).
		WithMountedDirectory("/dir", modDir).
		WithWorkdir("/dir").
		WithExec([]string{"dagger", "develop", "--recursive"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		WithExec([]string{"dagger", "call", "str"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Stdout(ctx)
}

func (m *Test) Str() string {
	return "yoyoyo"
}
`,
		).
		WithWorkdir("/work/some/sub/dir").
		With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
		WithWorkdir("/work").
		With(daggerExec("install", "./some/sub/dir"))

	out, err := modGen.
		With(daggerCall("fn",
			"--cli", testCLIBinPath,
			"--modDir", ".",
		)).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "yoyoyo", out)
}
