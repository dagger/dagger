package core

import (
	"context"

	"github.com/stretchr/testify/require"

	"github.com/dagger/testctx"
)

func (ModuleSuite) TestBuiltinDangDependencyModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := goGitBase(t, c).
		WithWorkdir("/work").
		WithExec([]string{"mkdir", "-p", "gochild", "pychild", "tschild", "dangchild"}).
		With(withModInitAt("gochild", "go", `package main

type Gochild struct{}

func (m *Gochild) Value() string {
	return "go"
}
`)).
		With(withModInitAt("pychild", "python", `
from dagger import function, object_type


@object_type
class Pychild:
    @function
    def value(self) -> str:
        return "python"
`)).
		With(withModInitAt("tschild", "typescript", `
import { object, func } from "@dagger.io/dagger"

@object()
export class Tschild {
  @func()
  value(): string {
    return "typescript"
  }
}
`)).
		With(withModInitAt("dangchild", "dang", `
type Dangchild {
  pub value: String! {
    "dang"
  }
}
`)).
		With(withModInit("dang", `
type Test {
  pub local: String! {
    "local"
  }

  pub viaGo: String! {
    gochild.value
  }

  pub viaPython: String! {
    pychild.value
  }

  pub viaTypescript: String! {
    tschild.value
  }

  pub viaDang: String! {
    dangchild.value
  }
}
`)).
		With(daggerExec("install", "./gochild")).
		With(daggerExec("install", "./pychild")).
		With(daggerExec("install", "./tschild")).
		With(daggerExec("install", "./dangchild"))

	for _, tc := range []struct {
		name string
		call string
		want string
	}{
		{name: "local", call: "local", want: "local"},
		{name: "dang child", call: "via-dang", want: "dang"},
		{name: "python child", call: "via-python", want: "python"},
		{name: "go child", call: "via-go", want: "go"},
		{name: "typescript child", call: "via-typescript", want: "typescript"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall(tc.call)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.want, out)
		})
	}
}
