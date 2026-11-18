package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"dagger.io/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// handoffEntrypoint is a module entrypoint whose function fails as an SDK
// entrypoint hands on its module process's failure: the process, an exec
// with the nested session, builds a Dagger Error through its own client,
// prints the Error's ID on a marker line and exits; the entrypoint then loads
// the Error by that ID and raises it, as the Go SDK's entrypoint does. %[1]s
// is the image the process runs in.
const handoffEntrypoint = `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Handoff")
        .withConstructor(function("", typeDef.withObject("Handoff")))
        .withFunction(function("Fail", typeDef.withKind(TypeDefKind.STRING_KIND))),
    ]
  }

  pub call(workspace: Workspace!, receiverType: String!, receiverValue: JSON, fnName: String!, fnArgs: JSON!): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else {
      let out = container
        .from(%[1]s)
        .withNewFile("/query.json", JSON.encode({{query: "{error(message: \"the process failed\") {withValue(name: \"code\", value: \"42\") {id}}}"}}))
        .withExec(
          ["sh", "-c", "auth=$(printf '%%s:' \"$DAGGER_SESSION_TOKEN\" | base64 | tr -d '\\n'); wget -q -O /response.json --header \"Authorization: Basic $auth\" --header 'Content-Type: application/json' --post-file /query.json \"http://127.0.0.1:$DAGGER_SESSION_PORT/query\"; cat /response.json >&2; echo >&2; printf '::id::' >&2; sed -n 's/.*\"id\":\"\\([^\"]*\\)\".*/\\1/p' /response.json >&2; exit 1"],
          experimentalPrivilegedNesting: true,
        )
        .stdout rescue {
          err: GraphQLError => raiseProcessError(err)
        }
      (out :: JSON!)
    }
  }

  let raiseProcessError(err: GraphQLError!): String! {
    let stderr = ((JSON.decode(err.extensions) :: {{stderr: String}}).stderr ?? "") rescue ""
    let ids = stderr.split("\n").filter { line => line.hasPrefix("::id::") }
    if (ids.isEmpty) {
      raise err
    }
    raise node(id: ids.takeLast(1).join("").trimPrefix("::id::") :: ID!).{{... on Dagger.Error!}}
  }
}
`

// An Error that a module's process builds through its own client still
// loads by its ID after the process has exited, so an SDK entrypoint can
// raise it and the function's caller gets it, values and all. The process's
// client shares the function call's session, and the engine keeps the
// session's results until the session ends; if it released them when the
// process's client disconnected, the entrypoint couldn't load the Error and
// the caller would get the load's failure instead.
func (ModuleSuite) TestDangRaisedErrorFromModuleProcess(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	mod := core.NewQuery(c).Directory().
		WithNewFile("dagger-module.toml", `name = "handoff"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
		// Dang 2.1, as the Go SDK's generated entrypoint declares it.
		WithNewFile("entrypoint/dang.toml", "dang = \"2.1\"\n").
		WithNewFile("entrypoint/main.dang", fmt.Sprintf(handoffEntrypoint, strconv.Quote(alpineImage)))
	require.NoError(t, mod.AsModuleSource().AsModule().Serve(ctx))

	_, err := testutil.QueryWithClient[struct{ Handoff struct{} }](c, t, "{handoff{fail}}", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "the process failed")
	require.NotContains(t, err.Error(), "call module entrypoint")
	var extended interface{ Extensions() map[string]any }
	require.True(t, errors.As(err, &extended), "%#v", err)
	require.Equal(t, float64(42), extended.Extensions()["code"], "%v", extended.Extensions())
}
