package core

import (
	"context"
	"encoding/json"
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

// An entrypoint's dependencies feed only the entrypoint: its calls reach
// them, but the module's own schema, which its code and generated bindings
// see, holds the core alone, and the module doesn't list them. So a Go
// module whose entrypoint builds it with dagger/go doesn't take dagger/go's
// names, such as go, into its own schema, or its callers'.
func (ModuleSuite) TestDangModuleEntrypointDependenciesFeedOnlyTheEntrypoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := goGitBase(t, c).
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
		WithNewFile(".dagger/modules/app/entrypoint/main.dang", fmt.Sprintf(dependencyEntrypoint, ""))

	out, err := ctr.With(daggerCallAt("app", "greet")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello from dep", strings.TrimSpace(out), "the entrypoint reaches its dependency")

	out, err = ctr.With(daggerQuery(`{moduleSource(refString:".dagger/modules/app"){introspectionSchemaJson{contents} asModule{dependencies{name}}}}`)).Stdout(ctx)
	require.NoError(t, err)
	var resp struct {
		ModuleSource struct {
			IntrospectionSchemaJSON struct {
				Contents string `json:"contents"`
			} `json:"introspectionSchemaJson"`
			AsModule struct {
				Dependencies []struct {
					Name string `json:"name"`
				} `json:"dependencies"`
			} `json:"asModule"`
		} `json:"moduleSource"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	var deps []string
	for _, dep := range resp.ModuleSource.AsModule.Dependencies {
		deps = append(deps, dep.Name)
	}
	// A Dang module lists itself among its dependencies.
	require.NotContains(t, deps, "dep", "the module doesn't list its entrypoint's dependency")
	types := parseSchemaContents(t, resp.ModuleSource.IntrospectionSchemaJSON.Contents).typeNames()
	require.Contains(t, types, "Container", "the module's schema holds the core")
	require.NotContains(t, types, "Dep", "the module's schema doesn't hold its entrypoint's dependency")
}
