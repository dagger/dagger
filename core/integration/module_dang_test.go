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
	assertEntries := func(t *testctx.T, out string, expected ...string) {
		t.Helper()
		require.ElementsMatch(t, expected, strings.Split(strings.TrimSpace(out), "\n"))
	}

	t.Run("positional defaultPath", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-directives").
			With(daggerCall("with-positional-default-path")).
			Stdout(ctx)
		require.NoError(t, err)
		assertEntries(t, out, "positional-default.txt")
	})

	t.Run("named defaultPath", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-directives").
			With(daggerCall("with-named-default-path")).
			Stdout(ctx)
		require.NoError(t, err)
		assertEntries(t, out, "named-default.txt")
	})

	t.Run("positional ignorePatterns", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-directives").
			With(daggerCall("with-positional-ignore")).
			Stdout(ctx)
		require.NoError(t, err)
		assertEntries(t, out, "keep.txt")
	})

	t.Run("named ignorePatterns", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-directives").
			With(daggerCall("with-named-ignore")).
			Stdout(ctx)
		require.NoError(t, err)
		assertEntries(t, out, "keep.txt")
	})

	t.Run("mixed syntax", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-directives").
			With(daggerCall("with-mixed-syntax")).
			Stdout(ctx)
		require.NoError(t, err)
		assertEntries(t, out, "keep.log", "keep.txt")
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

func (DangSuite) TestCoreTypeShadowing(_ context.Context, t *testctx.T) {
	t.Run("object shadows core container", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-shadowing").
			With(daggerCall("make-container", "value")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "container", strings.TrimSpace(out))
	})

	t.Run("object shadows core directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-shadowing").
			With(daggerCall("make-directory", "value")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "directory", strings.TrimSpace(out))
	})

	t.Run("qualified core container", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-shadowing").
			With(daggerCall("make-core-container", "stdout")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "core", strings.TrimSpace(out))
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

func (DangSuite) TestMapFields(_ context.Context, t *testctx.T) {
	t.Run("map field survives rehydration", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-map-field").
			With(daggerCall("env-json")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"PATH": "/usr/local/bin:/usr/bin:/bin"}`, out)
	})

	t.Run("map field mutation across calls", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-map-field").
			With(daggerCall("with-env", "--name", "HOME", "--value", "/root", "env-json")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": "/root"}`, out)
	})

	t.Run("map nested in anonymous object field", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-map-field").
			With(daggerCall("nested-json")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"FOO": "bar"}`, out)
	})

	t.Run("exposing a map via GraphQL errors", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := dangModule(t, c, "test-map-pub").
			With(daggerCall("env")).
			Stdout(ctx)
		requireErrOut(t, err, "cannot be exposed via GraphQL")
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

func (DangSuite) TestInterfaces(_ context.Context, t *testctx.T) {
	t.Run("define, implement, and consume within a module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-interface").
			With(daggerCall("local")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey, local", strings.TrimSpace(out))
	})

	t.Run("implement an interface defined by a dependency", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// A type that `implements` a dependency's interface must not be
		// required to provide the synthesized `id: ID!` field that Dagger
		// adds to every interface definition.
		out, err := dangModule(t, c, "test-interface").
			With(daggerCall("run")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi, world", strings.TrimSpace(out))
	})

	t.Run("consume structural interface value across dependencies", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "test-interface/cross-dep").
			With(daggerCall("run")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ok", strings.TrimSpace(out))
	})
}

// TestVersionedSyntax covers the Dang major version routing: modules pinned
// to an engineVersion before v0.21.5 are evaluated with Dang v1 (`.{ }` is
// selection), and modules at v0.21.5+ with Dang v2 (`.{ }` is dot-block
// application, `.{{ }}` is selection). See core/sdk/dang/README.md.
func (DangSuite) TestVersionedSyntax(_ context.Context, t *testctx.T) {
	t.Run("v1 selection for modules pinned before v0.21.5", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "legacy-selection").
			With(daggerCall("contents")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "old syntax", strings.TrimSpace(out))

		out, err = dangModule(t, c, "legacy-selection").
			With(daggerCall("size")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "10", strings.TrimSpace(out))
	})

	t.Run("v2 dot-block and selection for modules at v0.21.5+", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := dangModule(t, c, "dot-block").
			With(daggerCall("double", "--n", "21")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "42", strings.TrimSpace(out))

		out, err = dangModule(t, c, "dot-block").
			With(daggerCall("contents")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "new syntax", strings.TrimSpace(out))
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
