package core

// These tests cover how the CLI reports process exit codes. They verify large
// exit codes are normalized the same way a shell observes them.
//
// See also:
// - module_error_test.go: module-specific error surfaces.

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger/core"
)

type ReturnCodesSuite struct{}

func TestReturnCodes(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ReturnCodesSuite{})
}

func (ReturnCodesSuite) TestLargeExitCode(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("ExpectAny", func(ctx context.Context, t *testctx.T) {
		exit, err := core.NewQuery(c).Container().From(alpineImage).
			WithExec([]string{"sh", "-c", "exit 254"}, core.ContainerWithExecOpts{Expect: core.ReturnTypeAny}).
			ExitCode(ctx)
		require.NoError(t, err)
		require.Equal(t, 254, exit)
	})

	t.Run("ExpectFailure", func(ctx context.Context, t *testctx.T) {
		exit, err := core.NewQuery(c).Container().From(alpineImage).
			WithExec([]string{"sh", "-c", "exit 254"}, core.ContainerWithExecOpts{Expect: core.ReturnTypeFailure}).
			ExitCode(ctx)
		require.NoError(t, err)
		require.Equal(t, 254, exit)
	})

	t.Run("ExpectSuccessShouldError", func(ctx context.Context, t *testctx.T) {
		_, err := core.NewQuery(c).Container().From(alpineImage).
			WithExec([]string{"sh", "-c", "exit 254"}, core.ContainerWithExecOpts{Expect: core.ReturnTypeSuccess}).
			ExitCode(ctx)
		require.Error(t, err)
	})
}
