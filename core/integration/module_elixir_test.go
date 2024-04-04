package core

import (
	_ "embed"
	//"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	//"github.com/tidwall/gjson"
	//"dagger.io/dagger"
)

func TestModuleElixirInit(t *testing.T) {
	t.Parallel()

	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=elixir"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})
}
