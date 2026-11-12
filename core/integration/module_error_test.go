package core

// These tests cover error surfaces from module code. They verify execution
// error translation, hidden host/engine APIs, and large exec error output.
//
// See also:
// - module_call_test.go: successful module invocation flows.
// - module_validation_test.go: load-time validation errors.

import (
	"context"
	"fmt"
	"strconv"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestExecError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/exec-error")

	_, err := modGen.
		With(daggerQueryAt(".", `{doThing}`)).
		Stdout(ctx)
	require.NoError(t, err)
}

// TestHostError verifies the host api is not exposed to modules
func (ModuleSuite) TestHostError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := moduleFixture(t, c, "go/host-error").
		With(daggerCallAt(".", "fn")).
		Sync(ctx)
	requireErrOut(t, err, "dag.Host undefined")
}

// TestEngineError verifies the engine api is not exposed to modules
func (ModuleSuite) TestEngineError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := moduleFixture(t, c, "go/engine-error").
		With(daggerCallAt(".", "fn")).
		Sync(ctx)
	requireErrOut(t, err, "dag.Engine undefined")
}

// TestCurrentWorkspaceError verifies currentWorkspace is not exposed to module
// SDKs. Modules receive the current Workspace through declared arguments.
func (ModuleSuite) TestCurrentWorkspaceError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("go", func(ctx context.Context, t *testctx.T) {
		_, err := moduleFixture(t, c, "go/current-workspace-error").
			With(daggerCallAt(".", "fn")).
			Sync(ctx)
		requireErrOut(t, err, "dag.CurrentWorkspace undefined")
	})

	t.Run("python", func(ctx context.Context, t *testctx.T) {
		_, err := moduleFixture(t, c, "python/current-workspace-error").
			With(daggerCallAt(".", "fn")).
			Sync(ctx)
		requireErrOut(t, err, "current_workspace")
	})
}

// failingDangCalls are the bodies of two functions, %[2]s between them, that
// fail as an SDK entrypoint hands on its module process's failure: the first
// raises a Dagger Error with values, as it would raise the one the process
// built, loaded by ID; the second raises the GraphQL error of an exec it
// rescued, as it rescued it.
const failingDangCalls = `
      raise Dagger.error("the function failed")
        .withValue("_type", "\"EXEC_ERROR\"" :: Dagger.JSON!)
        .withValue("cmd", "[\"fail\"]" :: Dagger.JSON!)
        .withValue("exitCode", "42" :: Dagger.JSON!)
        .withValue("stdout", "\"some output\"" :: Dagger.JSON!)
        .withValue("stderr", "\"some error\"" :: Dagger.JSON!)
    %[2]s
      (container.from(%[1]s).withExec(["sh", "-c", "echo some output; echo some error >&2; exit 42"]).stdout) rescue {
        e: GraphQLError => raise e
      }
`

// A Dang function that raises a Dagger Error fails with that Error, values
// and all, as a module process that returns it with returnError does; one that
// raises the GraphQL error it rescued, with that error. Both are written in
// Dang 2.1: an entrypoint's default, and the module's by its engine version.
func (ModuleSuite) TestDangRaisedErrorValues(ctx context.Context, t *testctx.T) {
	calls := func(between string) string {
		return fmt.Sprintf(failingDangCalls, strconv.Quote(alpineImage), between)
	}
	for _, tc := range []struct {
		name string
		mod  func(*dagger.Client) *core.Directory
	}{
		{
			name: "entrypoint",
			mod: func(c *dagger.Client) *core.Directory {
				return core.NewQuery(c).Directory().
					WithNewFile("dagger-module.toml", `name = "failing"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
					WithNewFile("entrypoint/main.dang", `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    let str = typeDef.withKind(TypeDefKind.STRING_KIND)
    [
      typeDef
        .withObject("Failing")
        .withConstructor(function("", typeDef.withObject("Failing")))
        .withFunction(function("Raised", str))
        .withFunction(function("Rescued", str)),
    ]
  }

  pub call(workspace: Workspace!, receiverType: String!, receiverValue: JSON, fnName: String!, fnArgs: JSON!): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else if (fnName == "Raised") {`+calls(`} else {`)+`      ("{}" :: JSON!)
    }
  }
}
`)
			},
		},
		{
			name: "module",
			mod: func(c *dagger.Client) *core.Directory {
				return core.NewQuery(c).Directory().
					WithNewFile("dagger.json", `{"name":"failing","engineVersion":"v1.0.0-0","sdk":"dang"}`).
					WithNewFile("main.dang", `type Failing {
  pub raised: String! {`+calls("}\n  pub rescued: String! {")+`  }
}
`)
			},
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			require.NoError(t, tc.mod(c).AsModuleSource().AsModule().Serve(ctx))

			for _, fn := range []string{"raised", "rescued"} {
				t.Run(fn, func(ctx context.Context, t *testctx.T) {
					_, err := testutil.QueryWithClient[struct{ Failing struct{} }](c, t, "{failing{"+fn+"}}", nil)
					var execErr *core.ExecError
					require.ErrorAs(t, err, &execErr)
					require.Equal(t, 42, execErr.ExitCode)
					require.Equal(t, "some output", execErr.Stdout)
					require.Equal(t, "some error", execErr.Stderr)
					require.NotContains(t, err.Error(), "call module entrypoint")
					if fn == "raised" {
						require.Contains(t, err.Error(), "the function failed")
					}
				})
			}
		})
	}
}

func (ModuleSuite) TestLargeErrors(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "large-errors")

	c := connect(ctx, t)

	err := core.NewQuery(c).ModuleSource(modDir).AsModule().Serve(ctx)
	require.NoError(t, err)

	_, err = testutil.QueryWithClient[struct {
		Test struct {
			RunNoisy any
		}
	}](c, t, `{test{runNoisy}}`, nil)
	var execError *core.ExecError
	require.ErrorAs(t, err, &execError)

	// if we get `2` here, that means we're getting the less helpful error:
	// process "/runtime" did not complete successfully: exit code: 2
	require.Equal(t, 42, execError.ExitCode)
	require.Contains(t, execError.Stdout, "xxxxx")
	require.Contains(t, execError.Stderr, "yyyyy")
}
