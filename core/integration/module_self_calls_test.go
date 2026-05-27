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
	tcs := []struct {
		sdk     string
		fixture string
	}{
		{sdk: "go", fixture: "go/self-calls"},
		{sdk: "dang", fixture: "dang/self-calls"},
	}

	for _, tc := range tcs {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			modGen := moduleFixture(t, c, tc.fixture)

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

			if tc.sdk == "go" {
				t.Run("can expose exported scalar fields", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.
						With(daggerQueryAt(".", `{message}`)).
						Stdout(ctx)
					require.NoError(t, err)
					require.JSONEq(t, `{"message":"hello from field"}`, out)
				})

				t.Run("can self-call with enum arguments", func(ctx context.Context, t *testctx.T) {
					// The engine exposes enum values in SCREAMING_SNAKE; the
					// self-call schema emitter must match, or the generated
					// self-client sends an unknown wire value and this fails.
					out, err := modGen.
						With(daggerQueryAt(".", `{describeSelf}`)).
						Stdout(ctx)
					require.NoError(t, err)
					require.JSONEq(t, `{"describeSelf":"got green"}`, out)
				})
			}
		})
	}
}

func (ModuleSuite) TestSelfCallsAsDependency(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := moduleFixture(t, c, "go/self-calls-as-dep")

	t.Run("can call dependency function that self-calls", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{viaDep(stringArg:"hello")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"viaDep":"direct-hello\n"}`, out)
	})

	t.Run("can use object returned by dependency self-call", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{viaDepContainer(stringArg:"hello")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"viaDepContainer":"container-hello\n"}`, out)
	})

	t.Run("can self-call from dependency secondary object", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{viaDepWorker(stringArg:"hello")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"viaDepWorker":"worker-hello\n"}`, out)
	})
}

func (ModuleSuite) TestSelfCallsAsTransitiveDependency(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := moduleFixture(t, c, "go/self-calls-transitive")

	t.Run("can call transitive dependency function that self-calls", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{viaTransitiveDep(stringArg:"hello")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"viaTransitiveDep":"transitive-hello\n"}`, out)
	})
}
