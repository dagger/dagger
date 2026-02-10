package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	dagger "github.com/dagger/dagger/internal/testutil/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

)

type InterfaceSuite struct{}

func TestInterface(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(InterfaceSuite{})
}

func (InterfaceSuite) TestIfaceBasic(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk  string
		path string
	}

	for _, tc := range []testCase{
		{sdk: "go", path: "core/integration/testdata/modules/go/ifaces"},
		{sdk: "typescript", path: "core/integration/testdata/modules/typescript/ifaces"},
		{sdk: "python", path: "core/integration/testdata/modules/python/ifaces"},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			testdataPath, err := filepath.Abs(tc.path)
			require.NoError(t, err)

			_, err = c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithMountedDirectory("/work", c.Host().Directory(testdataPath)).
				WithWorkdir("/work").
				With(daggerCall("test")).
				Sync(ctx)
			require.NoError(t, err)
		})
	}
}

func (InterfaceSuite) TestIfaceGoSadPaths(ctx context.Context, t *testctx.T) {
	t.Run("no dagger object embed", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))

		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main
type Test struct {}

type BadIface interface {
	Foo(ctx context.Context) (string, error)
}

func (m *Test) Fn() BadIface {
	return nil
}
	`,
			).
			With(daggerFunctions()).
			Sync(ctx)
		require.Error(t, err)
		require.NoError(t, c.Close())
		require.Regexp(t, `missing method .* from DaggerObject interface, which must be embedded in interfaces used in Functions and Objects`, logs.String())
	})
}

func (InterfaceSuite) TestIfaceGoDanglingInterface(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main
type Test struct {}

func (test *Test) Hello() string {
	return "hello"
}

type DanglingObject struct {}

func (obj *DanglingObject) Hello(x DanglingIface) DanglingIface {
	return x
}

type DanglingIface interface {
	DoThing() (error)
}
	`,
		).
		Sync(ctx)
	require.NoError(t, err)

	out, err := modGen.
		With(daggerQuery(`{test{hello}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"hello":"hello"}}`, out)
}

func (InterfaceSuite) TestIfaceCall(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk        string
		depSource  string
		testSource string
	}

	tests := []testCase{
		{
			sdk: "go",
			depSource: `package main

type Mallard struct {}

func (m *Mallard) Quack() string {
	return "mallard quack"
}
			`,
			testSource: `package main

import (
	"context"
)

type Test struct {}

type Duck interface {
	DaggerObject
	Quack(ctx context.Context) (string, error)
}

func (m *Test) GetDuck() Duck {
	return dag.Mallard()
}`,
		},
		{
			sdk: "typescript",
			depSource: `import { object, func } from "@dagger.io/dagger"

@object()
export class Mallard {
  @func()
  quack(): string {
    return "mallard quack"
  }
}
			
`,
			testSource: `import { dag, object, func } from "@dagger.io/dagger"

export interface Duck {
  quack: () => Promise<string>
}

@object()
export class Test {
  @func()
  getDuck(): Duck {
    return dag.mallard()
  }
}
`,
		},
		{
			sdk: "python",
			depSource: `import dagger

@dagger.object_type
class Mallard:
    @dagger.function
    def quack(self) -> str: 
        return "mallard quack"
`,
			testSource: `import typing

import dagger
from dagger import dag

@dagger.interface
class Duck(typing.Protocol):
    @dagger.function
    async def quack(self) -> str: ...

@dagger.object_type
class Test:
    @dagger.function 
    def get_duck(self) -> Duck:
        return dag.mallard() 
`,
		},
	}

	for _, tc := range tests {
		for _, rtc := range tests {
			// No need for every permutation, just within the same SDK and
			// with Go as a reference implementation.
			if tc.sdk != "go" && rtc.sdk != "go" && tc.sdk != rtc.sdk {
				continue
			}

			t.Run(fmt.Sprintf("%s implementation defined in %s", tc.sdk, rtc.sdk), func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				out, err := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(withModInitAt("mallard", tc.sdk, tc.depSource)).
					With(daggerCallAt("mallard", "quack")).
					With(withModInit(rtc.sdk, rtc.testSource)).
					With(daggerExec("install", "./mallard")).
					With(daggerCall("get-duck", "quack")).
					Stdout(ctx)

				require.NoError(t, err)
				require.Equal(t, "mallard quack", strings.TrimSpace(out))
			})
		}
	}
}
