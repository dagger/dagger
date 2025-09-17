package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type LocalDefaultsSuite struct{}

func TestLocalDefaults(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(LocalDefaultsSuite{})
}

func (LocalDefaultsSuite) TestSimple(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Container().
		From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithDirectory("./defaults", c.Host().Directory("./testdata/modules/go/defaults"))
	for _, tc := range []struct {
		description    string
		dotEnvPath     string
		dotEnvContents string
		workdir        string
		command        []string
		expect         dagger.ReturnType
		stdout         string
	}{
		{
			"module-specific: <CONSTRUCTOR ARG>=<VALUE>",
			"./defaults/.env",
			"MESSAGE=salut",
			"./defaults",
			[]string{"dagger", "call", "hello"},
			dagger.ReturnTypeSuccess,
			"salut",
		},
	} {
		t.Run(tc.description, func(ctx context.Context, t *testctx.T) {
			stdout, err := ctr.
				WithNewFile(tc.dotEnvPath, tc.dotEnvContents).
				WithWorkdir(tc.workdir).
				WithExec(tc.command, dagger.ContainerWithExecOpts{
					Expect:                        tc.expect,
					ExperimentalPrivilegedNesting: true,
				}).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, stdout, tc.stdout)
		})
	}
}
