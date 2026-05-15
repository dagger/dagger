package core

// These tests cover a module invoking functions from its own Dagger API. They
// verify direct GraphQL self-queries and SDK-generated helpers for self-calls.
//
// See also:
// - module_current_module_test.go: `dag.CurrentModule()` introspection.
// - module_runtime_behavior_test.go: general module execution behavior.

import (
	"context"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestSelfAPICall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := moduleFixture(t, c, "go/self-api-call").
		With(daggerQueryAt(".", `{fnA}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"fnA": "hi from b"}`, out)
}

func (ModuleSuite) TestSelfCalls(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := moduleFixture(t, c, "go/self-calls")

	t.Run("can call with arguments", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{print(stringArg:"hello")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"print":"hello\n"}`, out)
	})

	t.Run("can call with optional arguments", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{printDefault}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"printDefault":"Hello Self Calls\n"}`, out)
	})
}
