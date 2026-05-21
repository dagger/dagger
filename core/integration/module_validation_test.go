package core

// These tests cover errors raised while turning module source into a Dagger
// schema. They verify wrapped object exposure, namespacing rules, dependency
// cycle errors, and reserved-word checks across SDKs.
//
// See also:
// - module_definition_test.go: valid API definitions.
// - module_runtime_behavior_test.go: behavior after a module loads.

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestWrapping(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk     string
		fixture string
	}

	for _, tc := range []testCase{
		{
			sdk:     "go",
			fixture: "go/wrapped-container",
		},
		{
			sdk:     "python",
			fixture: "python/wrapped-container",
		},
		{
			sdk:     "typescript",
			fixture: "typescript/wrapped-container",
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			id := identity.NewID()

			out, err := moduleFixture(t, c, tc.fixture).
				With(daggerQuery(
					fmt.Sprintf(`{container{echo(msg:%q){unwrap{stdout}}}}`, id),
				)).
				Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t,
				fmt.Sprintf(`{"container":{"echo":{"unwrap":{"stdout":%q}}}}`, id),
				out)
		})
	}
}

func (ModuleSuite) TestNamespacing(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	moduleSrcPath, err := filepath.Abs("./testdata/modules/go/namespacing")
	require.NoError(t, err)

	ctr := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithMountedDirectory("/work", c.Host().Directory(moduleSrcPath)).
		WithWorkdir("/work")

	out, err := ctr.
		With(daggerQueryAt(".", `{fn(s:"yo")}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"fn":["*dagger.Sub1Obj made 1:yo", "*dagger.Sub2Obj made 2:yo"]}`, out)
}

func (ModuleSuite) TestLoops(ctx context.Context, t *testctx.T) {
	// verify circular module dependencies result in an error

	// this test is often slow if you're running locally, skip if -short is specified
	if testing.Short() {
		t.SkipNow()
	}

	c := connect(ctx, t)

	_, err := goGitBase(t, c).
		With(withModuleFixture(t, c, ".", "go/circular-deps")).
		With(daggerCallAt("depA", "--help")).
		Sync(ctx)
	requireErrOut(t, err, `module "depA" has a circular dependency on itself through dependency "depC"`)
}

func (ModuleSuite) TestReservedWords(ctx context.Context, t *testctx.T) {
	// verify disallowed names are rejected

	type testCase struct {
		sdk     string
		fixture string
	}

	t.Run("id", func(ctx context.Context, t *testctx.T) {
		t.Run("arg", func(ctx context.Context, t *testctx.T) {
			// id used to be disallowed as an arg name, but is allowed now, test it works

			for _, tc := range []testCase{
				{
					sdk:     "go",
					fixture: "go/id/arg",
				},
				{
					sdk:     "python",
					fixture: "python/id/arg",
				},
				{
					sdk:     "typescript",
					fixture: "typescript/id/arg",
				},
			} {
				t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					out, err := moduleFixture(t, c, tc.fixture).
						With(daggerQuery(`{fn(id:"YES!!!!")}`)).
						Stdout(ctx)
					require.NoError(t, err)
					require.JSONEq(t, `{"fn":"YES!!!!"}`, out)
				})
			}
		})

		t.Run("field", func(ctx context.Context, t *testctx.T) {
			for _, tc := range []testCase{
				{
					sdk:     "go",
					fixture: "go/id/field",
				},
				{
					sdk:     "typescript",
					fixture: "typescript/id/field",
				},
			} {
				t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					_, err := moduleFixture(t, c, tc.fixture).
						With(daggerQuery(`{fn{id}}`)).
						Sync(ctx)

					requireErrOut(t, err, "cannot define field with reserved name \"id\"")
				})
			}
		})

		t.Run("fn", func(ctx context.Context, t *testctx.T) {
			for _, tc := range []testCase{
				{
					sdk:     "go",
					fixture: "go/id/fn",
				},
				{
					sdk:     "python",
					fixture: "python/id/fn",
				},
				{
					sdk:     "typescript",
					fixture: "typescript/id/fn",
				},
			} {
				t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					_, err := moduleFixture(t, c, tc.fixture).
						With(daggerQuery(`{id}`)).
						Sync(ctx)

					requireErrOut(t, err, "cannot define function with reserved name \"id\"")
				})
			}
		})
	})
}
