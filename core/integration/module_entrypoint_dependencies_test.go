package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// dependencyEntrypoint is a Dang entrypoint whose call returns a dependency's
// value, with an optional member spliced in with %s. Its types and call take
// a nullable workspace, which the engine passes only without sources.
const dependencyEntrypoint = `type Entrypoint implements ModuleEntrypoint {
%s
  pub types(workspace: Workspace): [TypeDef!]! {
    [
      typeDef
        .withObject("App")
        .withConstructor(function("", typeDef.withObject("App")))
        .withFunction(function("Greet", typeDef.withKind(TypeDefKind.STRING_KIND))),
    ]
  }

  pub call(
    workspace: Workspace,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
  ): JSON! {
    if (fnName == "") {
      ("{}" :: JSON!)
    } else if (fnName == "Greet") {
      (JSON.encode(dep.greeting) :: JSON!)
    } else {
      raise "unknown function: " + fnName
    }
  }
}
`

// A version 2 manifest lists the dependencies of its module's entrypoint,
// and the entrypoint's schema holds them, so its calls reach them: a Go
// module's generated entrypoint builds the module with dagger/go this way.
// With a sources member, the engine type-checks the whole entrypoint when it
// evaluates the sources, against a schema that holds them too.
func (ModuleSuite) TestDangModuleEntrypointDependencies(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name   string
		member string
	}{
		{name: "call"},
		{name: "with sources", member: `  pub sources(workspace: Workspace!): Directory! {
    directory.withDirectory(workspace.cwd, workspace.directory(".", gitignore: true))
  }
`},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			out, err := goGitBase(t, c).
				WithNewFile("dagger.toml", `[modules.app]
source = ".dagger/modules/app"
`).
				WithNewFile(".dagger/modules/dep/dagger-module.toml", `name = "dep"

[runtime]
source = "dang"
`).
				WithNewFile(".dagger/modules/dep/main.dang", `type Dep {
  pub greeting: String! {
    "hello from dep"
  }
}
`).
				WithNewFile(".dagger/modules/app/dagger-module.toml", `name = "app"

[[dependencies]]
name = "dep"
source = "../dep"

[entrypoint]
kind = "dang"
source = "./entrypoint"
`).
				WithNewFile(".dagger/modules/app/entrypoint/main.dang", fmt.Sprintf(dependencyEntrypoint, tc.member)).
				With(daggerCallAt("app", "greet")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello from dep", strings.TrimSpace(out))
		})
	}
}
