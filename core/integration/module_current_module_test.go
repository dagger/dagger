package core

// These tests cover `dag.CurrentModule()` calls made from inside module code.
// They verify access to the generated call context, installed dependencies,
// module identity, module source, and workdir helpers.
//
// See also:
// - module_self_calls_test.go: modules invoking their own API.
// - module_runtime_behavior_test.go: general module execution behavior.

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestCurrentModuleAPI(ctx context.Context, t *testctx.T) {
	t.Run("generatedContextDirectory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := moduleFixture(t, c, "go/current-module").
			With(daggerCallAt(".", "generated-context-directory", "export", "--path=./out")).
			Directory("out").
			Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "dagger.gen.go")
	})

	t.Run("dependencies", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := moduleFixture(t, c, "go/current-module-deps").
			With(daggerCallAt(".", "fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out, "depA,depB")
	})

	t.Run("name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := moduleFixture(t, c, "go/current-module-name-wacky").
			With(daggerCallAt(".", "fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "WaCkY", strings.TrimSpace(out))
	})

	t.Run("source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := moduleFixture(t, c, "go/current-module").
			With(daggerCallAt(".", "source-file", "contents")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "nice", strings.TrimSpace(out))
	})

	t.Run("workdir", func(ctx context.Context, t *testctx.T) {
		t.Run("dir", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := moduleFixture(t, c, "go/current-module").
				With(daggerCallAt(".", "workdir-dir", "file", "--path=coolfile.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := moduleFixture(t, c, "go/current-module").
				With(daggerCallAt(".", "workdir-file", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("error on escape", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := moduleFixture(t, c, "go/current-module")

			_, err := ctr.
				With(daggerCallAt(".", "escape-file", "contents")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "../rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCallAt(".", "escape-file-abs", "contents")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "/rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCallAt(".", "escape-dir", "entries")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "../foo" escapes workdir`)

			_, err = ctr.
				With(daggerCallAt(".", "escape-dir-abs", "entries")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "/foo" escapes workdir`)
		})
	})
}
