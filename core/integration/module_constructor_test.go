package core

// These tests cover `New` constructors in module source. They verify passing
// constructor arguments, storing constructor values on object fields, reporting
// constructor errors, and matching behavior across SDKs.
//
// See also:
// - module_definition_test.go: module API definition rules.
// - module_runtime_behavior_test.go: runtime behavior after construction.

import (
	"context"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestConstructor(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk     string
		fixture string
	}

	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk:     "go",
				fixture: "go/constructor-basic",
			},
			{
				sdk:     "python",
				fixture: "python/constructor-basic",
			},
			{
				sdk:     "typescript",
				fixture: "typescript/constructor-basic",
			},
		} {
			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				ctr := moduleFixture(t, c, tc.fixture)

				out, err := ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "foo")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "abc")

				out, err = ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "gimme-foo")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "abc")

				out, err = ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "42")

				out, err = ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "gimme-bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "42")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "123")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "gimme-bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "123")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "baz")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "x\ny\nz")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "gimme-baz")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "x\ny\nz")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "gimme-dir-ents")).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, strings.TrimSpace(out), "dagger.json")
			})
		}
	})

	t.Run("fields only", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk:     "go",
				fixture: "go/constructor-fields-only",
			},
			{
				sdk:     "python",
				fixture: "python/constructor-fields-only",
			},
			{
				sdk:     "typescript",
				fixture: "typescript/constructor-fields-only",
			},
		} {
			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				ctr := moduleFixture(t, c, tc.fixture)

				out, err := ctr.With(daggerCall("alpine-version")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, distconsts.AlpineVersion, strings.TrimSpace(out))
			})
		}
	})

	t.Run("return error", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk:     "go",
				fixture: "go/constructor-return-error",
			},
			{
				sdk:     "python",
				fixture: "python/constructor-return-error",
			},
			{
				sdk:     "typescript",
				fixture: "typescript/constructor-return-error",
			},
		} {
			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				var logs safeBuffer
				c := connect(ctx, t, dagger.WithLogOutput(&logs))

				ctr := moduleFixture(t, c, tc.fixture)

				_, err := ctr.With(daggerCall("foo")).Stdout(ctx)
				require.Error(t, err)

				require.NoError(t, c.Close())

				t.Log(logs.String())
				require.Regexp(t, "too bad: so sad", logs.String())
			})
		}
	})

	t.Run("python: with default factory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		content := "default factory content"
		ctr := moduleFixture(t, c, "python/constructor-default-factory")

		out, err := ctr.With(daggerCall("foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("--foo=dagger.json", "foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"source": "python"`)

		_, err = ctr.With(daggerCall("bar")).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("typescript: with default factory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		content := "default factory content"
		ctr := moduleFixture(t, c, "typescript/constructor-default-factory")

		out, err := ctr.With(daggerCall("foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("--foo=dagger.json", "foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"source": "typescript"`)

		_, err = ctr.With(daggerCall("bar")).Sync(ctx)
		require.NoError(t, err)
	})
}
