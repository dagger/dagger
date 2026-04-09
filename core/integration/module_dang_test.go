package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type DangSuite struct{}

func TestDang(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(DangSuite{})
}

func (DangSuite) TestDirectives(_ context.Context, t *testctx.T) {
	t.Run("positional defaultPath", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-directives").
			With(daggerCall("with-positional-default-path")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "got source")
	})

	t.Run("named defaultPath", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-directives").
			With(daggerCall("with-named-default-path")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "got source")
	})

	t.Run("positional ignorePatterns", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-directives").
			With(daggerCall("with-positional-ignore")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "got source")
	})

	t.Run("named ignorePatterns", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-directives").
			With(daggerCall("with-named-ignore")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "got source")
	})

	t.Run("mixed syntax", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-directives").
			With(daggerCall("with-mixed-syntax")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "got source")
	})
}

func (DangSuite) TestEnums(_ context.Context, t *testctx.T) {
	t.Run("get status", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-enum").
			With(daggerCall("get-status", "--status", "COMPLETED")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "COMPLETED")
	})

	t.Run("is completed true", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-enum").
			With(daggerCall("is-completed", "--status", "COMPLETED")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "true", strings.TrimSpace(out))
	})

	t.Run("is completed false", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-enum").
			With(daggerCall("is-completed", "--status", "PENDING")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "false", strings.TrimSpace(out))
	})

	t.Run("log level priority", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-enum").
			With(daggerCall("get-level-priority", "--level", "ERROR")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "3", strings.TrimSpace(out))
	})
}

func (DangSuite) TestMismatch(_ context.Context, t *testctx.T) {
	t.Run("child parent name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-mismatch").
			With(daggerCall("--n", "hello", "child", "parent-name")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello", strings.TrimSpace(out))
	})
}

func (DangSuite) TestPrivateArg(_ context.Context, t *testctx.T) {
	t.Run("default private value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-private-arg").
			With(daggerCall("display")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "84", strings.TrimSpace(out))
	})

	t.Run("custom private value", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-private-arg").
			With(daggerCall("--private", "10", "display")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "20", strings.TrimSpace(out))
	})

	t.Run("ls with default source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-private-arg").
			With(daggerCall("ls")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "main.dang")
		require.Contains(t, out, "dagger.json")
	})
}

func (DangSuite) TestScalars(_ context.Context, t *testctx.T) {
	t.Run("timestamp", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-scalar").
			With(daggerCall("get-timestamp", "--ts", "2024-01-01T00:00:00Z")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "2024-01-01")
	})

	t.Run("url", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-scalar").
			With(daggerCall("get-url", "--url", "https://example.com")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "example.com")
	})
}

func dangModule(t *testctx.T, c *dagger.Client, moduleName string) *dagger.Container {
	t.Helper()
	modSrc, err := filepath.Abs(filepath.Join("./testdata/modules/dang", moduleName))
	require.NoError(t, err)

	return goGitBase(t, c).
		WithDirectory("testdata/modules/dang/"+moduleName, c.Host().Directory(modSrc)).
		WithWorkdir("/work/testdata/modules/dang/" + moduleName)
}
