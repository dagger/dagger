package core

// Workspace alignment: mostly aligned; coverage targets post-workspace module validation and API-shape semantics, but setup still relies on historical module helpers.
// Scope: Wrapped object exposure, namespacing, dependency cycle validation, and reserved-word validation across SDKs.
// Intent: Keep module validation and API-shape rules explicit and separate from the remaining runtime and current-module API coverage in the historical umbrella suite.

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestWrapping(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) Container() *WrappedContainer {
	return &WrappedContainer{
		dag.Container().From("` + alpineImage + `"),
	}
}

type WrappedContainer struct {
	Unwrap *dagger.Container` + "`" + `json:"unwrap"` + "`" + `
}

func (c *WrappedContainer) Echo(msg string) *WrappedContainer {
	return &WrappedContainer{
		c.Unwrap.WithExec([]string{"echo", "-n", msg}),
	}
}
`,
		},
		{
			sdk: "python",
			source: `from typing import Self

import dagger
from dagger import dag

@dagger.object_type
class WrappedContainer:
    unwrap: dagger.Container = dagger.field()

    @dagger.function
    def echo(self, msg: str) -> Self:
        return WrappedContainer(unwrap=self.unwrap.with_exec(["echo", "-n", msg]))

@dagger.object_type
class Test:
    @dagger.function
    def container(self) -> WrappedContainer:
        return WrappedContainer(unwrap=dag.container().from_("` + alpineImage + `"))

`,
		},
		{
			sdk: "typescript",
			source: `
import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
export class WrappedContainer {
  @func()
  unwrap: Container

  constructor(unwrap: Container) {
    this.unwrap = unwrap
  }

  @func()
  echo(msg: string): WrappedContainer {
    return new WrappedContainer(this.unwrap.withExec(["echo", "-n", msg]))
  }
}

@object()
export class Test {
  @func()
  container(): WrappedContainer {
    return new WrappedContainer(dag.container().from("` + alpineImage + `"))
  }
}
`,
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			id := identity.NewID()

			out, err := modInit(t, c, tc.sdk, tc.source).
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
		With(daggerQuery(`{fn(s:"yo")}`)).
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
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		With(daggerExec("init", "--name=depA", "--sdk=go", "depA")).
		With(daggerExec("init", "--name=depB", "--sdk=go", "depB")).
		With(daggerExec("init", "--name=depC", "--sdk=go", "depC")).
		With(daggerExec("install", "-m=depC", "./depB")).
		With(daggerExec("install", "-m=depB", "./depA")).
		With(daggerExec("install", "-m=depA", "./depC")).
		With(daggerCallAt("depA", "--help")).
		Sync(ctx)
	requireErrOut(t, err, `module "depA" has a circular dependency on itself through dependency "depC"`)
}

//go:embed testdata/modules/go/id/arg/main.go
var goodIDArgGoSrc string

//go:embed testdata/modules/python/id/arg/main.py
var goodIDArgPySrc string

//go:embed testdata/modules/typescript/id/arg/index.ts
var goodIDArgTSSrc string

//go:embed testdata/modules/go/id/field/main.go
var badIDFieldGoSrc string

//go:embed testdata/modules/typescript/id/field/index.ts
var badIDFieldTSSrc string

//go:embed testdata/modules/go/id/fn/main.go
var badIDFnGoSrc string

//go:embed testdata/modules/python/id/fn/main.py
var badIDFnPySrc string

//go:embed testdata/modules/typescript/id/fn/index.ts
var badIDFnTSSrc string

func (ModuleSuite) TestReservedWords(ctx context.Context, t *testctx.T) {
	// verify disallowed names are rejected

	type testCase struct {
		sdk    string
		source string
	}

	t.Run("id", func(ctx context.Context, t *testctx.T) {
		t.Run("arg", func(ctx context.Context, t *testctx.T) {
			// id used to be disallowed as an arg name, but is allowed now, test it works

			for _, tc := range []testCase{
				{
					sdk:    "go",
					source: goodIDArgGoSrc,
				},
				{
					sdk:    "python",
					source: goodIDArgPySrc,
				},
				{
					sdk:    "typescript",
					source: goodIDArgTSSrc,
				},
			} {
				t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					out, err := modInit(t, c, tc.sdk, tc.source).
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
					sdk:    "go",
					source: badIDFieldGoSrc,
				},
				{
					sdk:    "typescript",
					source: badIDFieldTSSrc,
				},
			} {
				t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					_, err := c.Container().From(golangImage).
						WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
						WithWorkdir("/work").
						With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
						With(sdkSource(tc.sdk, tc.source)).
						With(daggerQuery(`{fn{id}}`)).
						Sync(ctx)

					requireErrOut(t, err, "cannot define field with reserved name \"id\"")
				})
			}
		})

		t.Run("fn", func(ctx context.Context, t *testctx.T) {
			for _, tc := range []testCase{
				{
					sdk:    "go",
					source: badIDFnGoSrc,
				},
				{
					sdk:    "python",
					source: badIDFnPySrc,
				},
				{
					sdk:    "typescript",
					source: badIDFnTSSrc,
				},
			} {
				t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					_, err := c.Container().From(golangImage).
						WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
						WithWorkdir("/work").
						With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
						With(sdkSource(tc.sdk, tc.source)).
						With(daggerQuery(`{id}`)).
						Sync(ctx)

					requireErrOut(t, err, "cannot define function with reserved name \"id\"")
				})
			}
		})
	})
}
