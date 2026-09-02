package core

import (
	"context"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestBuiltinDangModuleEntrypoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.tiny]
source = ".dagger/modules/tiny"
`).
		WithNewFile(".dagger/modules/tiny/dagger-module.toml", `manifestVersion = 2
name = "tiny"

[entrypoint]
kind = "dang"
source = ".dagger/modules/tiny/entrypoint"
`).
		WithNewFile(".dagger/modules/tiny/entrypoint/main.dang", `type Entrypoint implements ModuleEntrypoint {
  pub types(workspace: Workspace!): [TypeDef!]! {
    [
      typeDef
        .withObject("Tiny")
        .withConstructor(function("", typeDef.withObject("Tiny")))
        .withFunction(
          function("Hello", typeDef.withKind(TypeDefKind.STRING_KIND)),
        ),
    ]
  }

  pub call(
    workspace: Workspace!,
    receiverType: String!,
    receiverValue: JSON,
    fnName: String!,
    fnArgs: JSON!,
  ): JSON! {
    if (receiverType != "Tiny") {
      raise "unknown receiver type: " + receiverType
    } else if (fnName == "") {
      ("{}" :: JSON!)
    } else if (fnName == "Hello") {
      ("\"hello\"" :: JSON!)
    } else {
      raise "unknown function: " + fnName
    }
  }
}
`).
		With(daggerCallAt("tiny", "hello"))

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))
}
