package core

import (
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestNestingCLI(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)
	defer c.Close()

	stdout, err := c.Container().
		WithExec([]string{"dagger", "version"}, dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		}).
		Stdout(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, stdout)
}
