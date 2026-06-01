package core

// These tests cover a module calling other installed modules from its own
// functions. They assume dependency entries already exist in config, then verify
// runtime calls and schema exposure.
//
// See also:
// - workspace_modules_test.go: workspace-level module installation/configuration.

import (
	"context"
	"fmt"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestConflictingSameNameTransitiveDeps covers two distinct dependency-graph
// contracts for A -> B -> Dint and A -> C -> Dstr, where both D modules have
// the same name and object names but incompatible field types.
func (ModuleSuite) TestConflictingSameNameTransitiveDeps(ctx context.Context, t *testctx.T) {
	// This setup is often slow locally; keep the two contracts below in one test
	// so they share the same dependency graph.
	if testing.Short() {
		t.SkipNow()
	}

	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		With(withTestdataFixture(t, c, ".", "modules/go/conflicting-transitive-deps")).
		WithWorkdir("/work/a")

	t.Run("runtime resolves conflicting transitive deps", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerQueryAt(".", `{fn}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"fn": "foo123"}`, out)
	})

	t.Run("schema exposes only direct deps", func(ctx context.Context, t *testctx.T) {
		types := currentSchema(ctx, t, ctr).Types
		require.NotNil(t, types.Get("A"))
		require.NotNil(t, types.Get("B"))
		require.NotNil(t, types.Get("C"))
		require.Nil(t, types.Get("D"))
	})
}

type localDepTestCase struct {
	sdk     string
	fixture string
}

var useLocalDepTestCases = []localDepTestCase{
	{
		sdk:     "go",
		fixture: "go/local-dep-parent",
	},
	{
		sdk:     "python",
		fixture: "python/local-dep-parent",
	},
	{
		sdk:     "typescript",
		fixture: "typescript/local-dep-parent",
	},
}

// TestUseLocalDependencyFromParentModule verifies the core local-dependency
// contract: a parent module installs a dependency by relative path, then client
// calls into the parent module can execute parent code that calls the dependency.
func (ModuleSuite) TestUseLocalDependencyFromParentModule(ctx context.Context, t *testctx.T) {
	for _, tc := range useLocalDepTestCases {
		t.Run(fmt.Sprintf("%s parent calls local dependency", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := testModuleWithLocalDep(t, c, tc.fixture)

			out, err := modGen.With(daggerQueryAt(".", `{useHello}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"useHello":"hello"}`, out)
		})
	}
}

// TestUseLocalDependencySchemaIsolation verifies that loading a parent module
// does not promote local dependency root fields onto the client Query schema.
// Parent-module use of the dependency is covered above.
func (ModuleSuite) TestUseLocalDependencySchemaIsolation(ctx context.Context, t *testctx.T) {
	for _, tc := range useLocalDepTestCases {
		t.Run(fmt.Sprintf("%s schema hides local dependency", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := testModuleWithLocalDep(t, c, tc.fixture)

			_, err := modGen.With(daggerQueryAt(".", `{dep{hello}}`)).Stdout(ctx)
			requireErrOut(t, err, `Cannot query field \"dep\" on type \"Query\"`)
		})
	}
}

func testModuleWithLocalDep(t *testctx.T, c *dagger.Client, fixture string) *dagger.Container {
	return moduleFixture(t, c, fixture)
}

// TestRuntimeDependencyDoesNotInheritWorkspace covers the A -> B runtime
// boundary: A receives the caller's contextual workspace, but dependency module
// B only receives it when A passes it explicitly.
func (ModuleSuite) TestRuntimeDependencyDoesNotInheritWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen := moduleFixture(t, c, "go/runtime-workspace-isolation").
		WithNewFile("marker.txt", "workspace marker")

	// A can still share its workspace with B when it passes the value explicitly.
	t.Run("explicit workspace pass succeeds", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{explicitWorkspaceArg}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"explicitWorkspaceArg":"workspace marker"}`, out)
	})

	// B must not receive A's contextual workspace through an omitted argument.
	t.Run("workspace argument is not auto-injected into dependency", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerQueryAt(".", `{implicitWorkspaceArg}`)).Stdout(ctx)
		requireErrOut(t, err, "workspace arguments are not inherited by module runtime calls; pass a Workspace explicitly")
	})

	// B must not receive A's contextual workspace through currentWorkspace.
	t.Run("currentWorkspace is not inherited by dependency", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.With(daggerQueryAt(".", `{currentWorkspaceFromDep}`)).Stdout(ctx)
		requireErrOut(t, err, "no current workspace")
	})
}

func (ModuleSuite) TestUseLocalMulti(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk     string
		fixture string
	}

	for _, tc := range []testCase{
		{
			sdk:     "go",
			fixture: "go/multi-dep-parent",
		},
		{
			sdk:     "python",
			fixture: "python/multi-dep-parent",
		},
		{
			sdk:     "typescript",
			fixture: "typescript/multi-dep-parent",
		},
	} {
		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := moduleFixture(t, c, tc.fixture).
				WithEnvVariable("BUST", identity.NewID()) // NB(vito): hmm...

			out, err := modGen.With(daggerQueryAt(".", `{names}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"names":["foo", "bar"]}`, out)
		})
	}
}
