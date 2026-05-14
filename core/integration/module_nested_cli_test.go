package core

// These tests cover running the Dagger CLI from inside a container launched by
// module code. They verify privileged nested execution, not host-side session
// serving.
//
// See also:
// - listen_test.go: host-side `dagger listen` sessions.
// - module_runtime_behavior_test.go: general module execution behavior.

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
		With(daggerExec("module", "init", "--source=.", "--sdk=go", "test", ".")).
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
		With(daggerExec("module", "init", "--source=.", "--sdk=go", "dep", ".")).
		WithWorkdir("/work").
		With(daggerExec("module", "install", "./some/sub/dir"))

	out, err := modGen.
		With(daggerCall("fn",
			"--cli", testCLIBinPath,
			"--modDir", ".",
		)).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "yoyoyo", out)
}
