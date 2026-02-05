package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	dagger "github.com/dagger/dagger/internal/testutil"
)

type ReturnCodesSuite struct{}

func TestReturnCodes(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ReturnCodesSuite{})
}

func (ReturnCodesSuite) TestLargeExitCode(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("ExpectAny", func(ctx context.Context, t *testctx.T) {
		exit, err := c.Container().From(alpineImage).
			WithExec([]string{"sh", "-c", "exit 254"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeAny}).
			ExitCode(ctx)
		require.NoError(t, err)
		require.Equal(t, 254, exit)
	})

	t.Run("ExpectFailure", func(ctx context.Context, t *testctx.T) {
		exit, err := c.Container().From(alpineImage).
			WithExec([]string{"sh", "-c", "exit 254"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeFailure}).
			ExitCode(ctx)
		require.NoError(t, err)
		require.Equal(t, 254, exit)
	})

	t.Run("ExpectSuccessShouldError", func(ctx context.Context, t *testctx.T) {
		_, err := c.Container().From(alpineImage).
			WithExec([]string{"sh", "-c", "exit 254"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeSuccess}).
			ExitCode(ctx)
		require.Error(t, err)
	})
}
