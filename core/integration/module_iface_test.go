package core

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

type InterfaceSuite struct{}

func TestInterface(t *testing.T) {
	testctx.Run(testCtx, t, InterfaceSuite{}, Middleware()...)
}

func (InterfaceSuite) TestIfaceBasic(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk  string
		path string
	}

	for _, tc := range []testCase{
		{sdk: "go", path: "./testdata/modules/go/ifaces"},
		{sdk: "typescript", path: "./testdata/modules/typescript/ifaces"},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			_, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithMountedDirectory("/work", c.Host().Directory(tc.path)).
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
	}

	for _, tc := range tests {
		tc := tc

		for _, rtc := range tests {
			rtc := rtc

			t.Run(fmt.Sprintf("%s iface called from %s", tc.sdk, rtc.sdk), func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				out, err := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/mallard").
					With(daggerExec("init", "--source=.", "--name=mallard", fmt.Sprintf("--sdk=%s", tc.sdk))).
					With(sdkSource(tc.sdk, tc.depSource)).
					WithWorkdir("/work").
					With(daggerExec("init", "--source=.", "--name=test", fmt.Sprintf("--sdk=%s", rtc.sdk))).
					With(sdkSource(rtc.sdk, rtc.testSource)).
					With(daggerExec("install", "./mallard")).
					With(daggerCall("get-duck", "quack")).
					Stdout(ctx)

				require.NoError(t, err)
				require.Equal(t, "mallard quack", strings.TrimSpace(out))
			})
		}
	}
}
