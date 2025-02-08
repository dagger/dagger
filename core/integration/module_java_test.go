package core

import (
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"

	"github.com/dagger/dagger/testctx"
)

type JavaSuite struct{}

func TestJava(t *testing.T) {
	testctx.Run(testCtx, t, JavaSuite{}, Middleware()...)
}

func (JavaSuite) TestInit(_ context.Context, t *testctx.T) {
	t.Run("from upstream", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=github.com/dagger/dagger/sdk/java"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("from alias", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=java"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("from alias with ref", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=java@main"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})
}

func (JavaSuite) TestFields(_ context.Context, t *testctx.T) {
	t.Run("can set and retrieve field", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(javaModule(t, c, "fields")).
			With(daggerShell("with-version a.b.c | get-version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "a.b.c")
	})
}

func javaModule(t *testctx.T, c *dagger.Client, moduleName string) dagger.WithContainerFunc {
	t.Helper()
	modSrc, err := filepath.Abs(filepath.Join("./testdata/modules/java", moduleName))
	require.NoError(t, err)
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithDirectory("", c.Host().Directory(modSrc))
	}
}
