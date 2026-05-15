package core

// These tests cover CLI commands that inspect a module without calling its
// functions. They verify function listing and load-error reporting without
// exercising module mutation commands.
//
// See also:
// - module_call_test.go: invoking module functions.
// - module_error_test.go: runtime error surfaces from module code.

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (CLISuite) TestModuleFunctions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := moduleFixture(t, c, "go/functions-introspection")

	t.Run("top-level", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("-m", ".")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn-a   doc for FnA")
		require.Contains(t, lines, "fn-b   doc for FnB")
		require.Contains(t, lines, "fn-c   doc for FnC")
		require.Contains(t, lines, "prim   doc for Prim")
	})

	t.Run("top-level from subdir with explicit module", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithWorkdir("/work/some/subdir").
			With(daggerFunctions("-m", "../..")).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn-a   doc for FnA")
		require.Contains(t, lines, "fn-b   doc for FnB")
		require.Contains(t, lines, "fn-c   doc for FnC")
		require.Contains(t, lines, "prim   doc for Prim")
	})

	t.Run("plain module is not loaded from subdir", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithWorkdir("/work/some/subdir").
			With(daggerFunctions()).
			Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "No functions found.")
	})

	t.Run("return core object", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("-m", ".", "fn-a")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "file                          Retrieves a file at the given path.")
		require.Contains(t, lines, "as-tarball                    Package the container state as an OCI image, and return it as a tar archive")
	})

	t.Run("return primitive", func(ctx context.Context, t *testctx.T) {
		_, err := ctr.With(daggerFunctions("-m", ".", "prim")).Stdout(ctx)
		requireErrOut(t, err, `function "prim" returns type "STRING_KIND" with no further functions available`)
	})

	t.Run("alt casing", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("-m", ".", "fnA")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "file                          Retrieves a file at the given path.")
		require.Contains(t, lines, "as-tarball                    Package the container state as an OCI image, and return it as a tar archive")
	})

	t.Run("return user interface", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("-m", ".", "fn-b")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "quack   quack that thang")
	})

	t.Run("return user object", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("-m", ".", "fn-c")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "field-a   doc for FieldA")
		require.Contains(t, lines, "field-b   doc for FieldB")
		require.Contains(t, lines, "field-c   doc for FieldC")
		require.Contains(t, lines, "field-d   doc for FieldD")
		require.Contains(t, lines, "fn-d      doc for FnD")
	})

	t.Run("return user object nested", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("-m", ".", "fn-c", "field-d")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "other-field-a   doc for OtherFieldA")
		require.Contains(t, lines, "other-field-b   doc for OtherFieldB")
		require.Contains(t, lines, "other-field-c   doc for OtherFieldC")
		require.Contains(t, lines, "other-field-d   doc for OtherFieldD")
		require.Contains(t, lines, "fn-e            doc for FnE")
	})

	t.Run("no module present errors nicely", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithWorkdir("/empty").
			With(daggerFunctions()).
			Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "No functions found.")
	})
}

func (CLISuite) TestModuleLoadErrors(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("normal context dir", func(ctx context.Context, t *testctx.T) {
		modGen := goGitBase(t, c).
			WithNewFile("dagger.json", `{"name": "broke", "engineVersion": "v100.0.0", "sdk": 666}`)

		_, err := modGen.With(daggerFunctions("-m", ".")).Stdout(ctx)
		requireErrOut(t, err, `module requires dagger v100.0.0`)
	})

	t.Run("fallback context dir", func(ctx context.Context, t *testctx.T) {
		modGen := daggerCliBase(t, c).
			WithNewFile("dagger.json", `{"name": "broke", "engineVersion": "v100.0.0", "sdk": 666}`)

		_, err := modGen.With(daggerFunctions("-m", ".")).Stdout(ctx)
		requireErrOut(t, err, `module requires dagger v100.0.0`)
	})
}

func (CLISuite) TestModuleWithoutSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(withWorkspaceFixture(t, c, "/work/test", "workspaces/module-without-sdk"))

	testCtr := base.WithWorkdir("/work/test")

	daggerJSON, err := testCtr.File("dagger.json").Contents(ctx)
	require.NoError(t, err)
	require.NotContains(t, daggerJSON, `"sdk"`)

	t.Run("functions with no SDK show just the headers", func(ctx context.Context, t *testctx.T) {
		out, err := testCtr.With(daggerFunctions("-m", ".")).Stdout(ctx)
		require.NoError(t, err)

		lines := strings.Split(strings.TrimSpace(out), "\n")
		require.LessOrEqual(t, len(lines), 2, "Should only show headers or be empty")

		if len(lines) > 0 {
			for _, line := range lines {
				if strings.TrimSpace(line) == "" {
					continue
				}
				require.Contains(t, line, "Name", "Should only contain header")
				require.Contains(t, line, "Description", "Should only contain header")
			}
		}
	})

	t.Run("call a module without sdk", func(ctx context.Context, t *testctx.T) {
		_, err := testCtr.WithWorkdir("/work/test/nosdk").With(daggerCallAt(".")).Stdout(ctx)
		require.NoError(t, err)
	})
}
