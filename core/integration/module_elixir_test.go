package core

import (
	_ "embed"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModuleElixirInit(t *testing.T) {
	t.Parallel()

	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := daggerCliBase(t, c).
			With(daggerExec("init", "--name=bare", "--sdk=elixir"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("camel-cases Dagger module name", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		for _, name := range []string{"My-Module", "MyModule"} {
			modGen := daggerCliBase(t, c).
				With(daggerExec("init", "-vv", "--name="+name, "--sdk=elixir"))

			sourceEnts, err := modGen.Directory("dagger").Entries(ctx)
			require.NoError(t, err)
			require.Contains(t, sourceEnts, "my_module")

			out, err := modGen.
				With(daggerQuery(`{myModule{containerEcho(stringArg:"hello"){stdout}}}`)).
				Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"myModule":{"containerEcho":{"stdout":"hello\n"}}}`, out)
		}
	})

	t.Run("with source", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := daggerCliBase(t, c).
			With(daggerExec("init", "-vv", "--name=bare", "--sdk=elixir", "--source=some/subdir"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		sourceSubdirEnts, err := modGen.Directory("some/subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, sourceSubdirEnts, "bare")

		sourceRootEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, sourceRootEnts, "src")
	})
}
