package core

import (
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"

	"github.com/dagger/testctx"
)

type JavaSuite struct{}

func TestJava(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(JavaSuite{})
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

		out, err := javaModule(t, c, "fields").
			With(daggerShell("with-version a.b.c | get-version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "a.b.c")
	})
}

func (JavaSuite) TestDefaultValue(_ context.Context, t *testctx.T) {
	t.Run("can set a value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("echo", "--value=hello")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello", out)
	})

	t.Run("can use a default value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("echo")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "default value", out)
	})
}

func (JavaSuite) TestOptionalValue(_ context.Context, t *testctx.T) {
	t.Run("can run without a value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("echo-else")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "default value if null", out)
	})

	t.Run("can set a value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("echo-else", "--value", "foo")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "foo", out)
	})

	t.Run("ensure Optional and @Default work together", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("echo-opt-default")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "default value", out)
	})
}

func (JavaSuite) TestDefaultPath(_ context.Context, t *testctx.T) {
	t.Run("can set a path for a file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("file-name", "--file=./pom.xml")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "pom.xml", out)
	})

	t.Run("can use a default path for a file", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("file-name")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "dagger.json", out)
	})

	t.Run("can set a path for a dir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("file-names", "--dir", ".")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "pom.xml")
	})

	t.Run("can use a default path for a dir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("file-names")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "Defaults.java", out)
	})
}

func (JavaSuite) TestIgnore(_ context.Context, t *testctx.T) {
	t.Run("without ignore", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("files-no-ignore")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "dagger.json")
		require.Contains(t, out, "pom.xml")
	})

	t.Run("with ignore", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("files-ignore")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "dagger.json")
		require.NotContains(t, out, "pom.xml")
	})

	t.Run("with negated ignore", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "defaults").
			With(daggerCall("files-neg-ignore")).
			Stdout(ctx)

		require.NoError(t, err)
		require.NotContains(t, out, "dagger.json")
		require.NotContains(t, out, "pom.xml")
		require.Contains(t, out, "src")
	})
}

func (JavaSuite) TestConstructor(_ context.Context, t *testctx.T) {
	t.Run("value set", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "construct").
			With(daggerCall("--value", "from cli", "echo")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "from cli", out)
	})

	t.Run("default value from constructor", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := javaModule(t, c, "construct").
			With(daggerCall("echo")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "from constructor", out)
	})
}

func javaModule(t *testctx.T, c *dagger.Client, moduleName string) *dagger.Container {
	t.Helper()
	modSrc, err := filepath.Abs(filepath.Join("./testdata/modules/java", moduleName))
	require.NoError(t, err)

	sdkSrc, err := filepath.Abs("../../sdk/java")
	require.NoError(t, err)

	return goGitBase(t, c).
		WithDirectory("modules/"+moduleName, c.Host().Directory(modSrc)).
		WithDirectory("sdk/java", c.Host().Directory(sdkSrc)).
		WithWorkdir("/work/modules/" + moduleName)
}
