package core

// Workspace alignment: mostly aligned; coverage targets post-workspace module dependency runtime semantics, but setup still relies on historical module helpers.
// Scope: Dependency graph resolution, local module deps, codegen refresh after dep changes, dep sync behavior, and multi-dependency local wiring.
// Intent: Keep module dependency runtime behavior separate from explicit module dependency CLI mutation coverage.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestConflictingSameNameDeps(ctx context.Context, t *testctx.T) {
	// A -> B -> Dint
	// A -> C -> Dstr
	// where Dint and Dstr are modules with the same name and same object names but conflicting types

	// this test is often slow if you're running locally, skip if -short is specified
	if testing.Short() {
		t.SkipNow()
	}

	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dstr").
		With(daggerExec("init", "--source=.", "--name=d", "--sdk=go")).
		WithNewFile("main.go", `package main

type D struct{}

type Obj struct {
	Foo string
}

func (m *D) Fn(foo string) Obj {
	return Obj{Foo: foo}
}
`,
		).
		With(daggerExec("develop"))

	ctr = ctr.
		WithWorkdir("/work/dint").
		With(daggerExec("init", "--source=.", "--name=d", "--sdk=go")).
		WithNewFile("main.go", `package main

type D struct{}

type Obj struct {
	Foo int
}

func (m *D) Fn(foo int) Obj {
	return Obj{Foo: foo}
}
`,
		).
		With(daggerExec("develop"))

	ctr = ctr.
		WithWorkdir("/work").
		With(daggerExec("init", "--source=c", "--name=c", "--sdk=go", "c")).
		WithWorkdir("/work/c").
		With(daggerExec("install", "../dstr")).
		WithNewFile("main.go", `package main

import (
	"context"
)

type C struct{}

func (m *C) Fn(ctx context.Context, foo string) (string, error) {
	return dag.D().Fn(foo).Foo(ctx)
}
`,
		).
		With(daggerExec("develop"))

	ctr = ctr.
		WithWorkdir("/work").
		With(daggerExec("init", "--source=b", "--name=b", "--sdk=go", "b")).
		With(daggerExec("install", "-m=b", "./dint")).
		WithNewFile("/work/b/main.go", `package main

import (
	"context"
)

type B struct{}

func (m *B) Fn(ctx context.Context, foo int) (int, error) {
	return dag.D().Fn(foo).Foo(ctx)
}
`,
		).
		WithWorkdir("/work/b").
		With(daggerExec("develop")).
		WithWorkdir("/work")

	ctr = ctr.
		WithWorkdir("/work").
		With(daggerExec("init", "--source=a", "--name=a", "--sdk=go", "a")).
		WithWorkdir("/work/a").
		With(daggerExec("install", "../b")).
		With(daggerExec("install", "../c")).
		WithNewFile("main.go", `package main

import (
	"context"
	"strconv"
)

type A struct{}

func (m *A) Fn(ctx context.Context) (string, error) {
	fooStr, err := dag.C().Fn(ctx, "foo")
	if err != nil {
		return "", err
	}
	fooInt, err := dag.B().Fn(ctx, 123)
	if err != nil {
		return "", err
	}
	return fooStr + strconv.Itoa(fooInt), nil
}
`,
		).
		With(daggerExec("develop"))

	out, err := ctr.With(daggerQuery(`{fn}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"fn": "foo123"}`, out)

	types := currentSchema(ctx, t, ctr).Types
	require.NotNil(t, types.Get("A"))
	require.NotNil(t, types.Get("B"))
	require.NotNil(t, types.Get("C"))

	// verify that no types from transitive deps show up (only direct ones)
	require.Nil(t, types.Get("D"))
}

var useInner = `package main

type Dep struct{}

func (m *Dep) Hello() string {
	return "hello"
}
`

var useGoOuter = `package main

import "context"

type Test struct{}

func (m *Test) UseHello(ctx context.Context) (string, error) {
	return dag.Dep().Hello(ctx)
}
`

var usePythonOuter = `import dagger
from dagger import dag

@dagger.object_type
class Test:
    @dagger.function
    async def use_hello(self) -> str:
        return await dag.dep().hello()
`

var useTSOuter = `
import { dag, object, func } from '@dagger.io/dagger'

@object()
export class Test {
	@func()
	async useHello(): Promise<string> {
		return dag.dep().hello()
	}
}
`

func (ModuleSuite) TestUseLocal(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk:    "go",
			source: useGoOuter,
		},
		{
			sdk:    "python",
			source: usePythonOuter,
		},
		{
			sdk:    "typescript",
			source: useTSOuter,
		},
	} {
		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			out, err := modGen.With(daggerQuery(`{useHello}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"useHello":"hello"}`, out)

			// can use direct dependency directly
			out, err = modGen.With(daggerQuery(`{dep{hello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"dep":{"hello":"hello"}}`, out)
		})
	}
}

func (ModuleSuite) TestCodegenOnDepChange(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk      string
		source   string
		changed  string
		expected string
	}

	for _, tc := range []testCase{
		{
			sdk:      "go",
			source:   useGoOuter,
			expected: "Hellov2",
			changed:  strings.ReplaceAll(useGoOuter, `Hello(ctx)`, `Hellov2(ctx)`),
		},
		{
			sdk:      "python",
			source:   usePythonOuter,
			expected: "hellov2",
			changed:  strings.ReplaceAll(usePythonOuter, `.hello()`, `.hellov2()`),
		},
		{
			sdk:      "typescript",
			source:   useTSOuter,
			expected: "hellov2",
			changed:  strings.ReplaceAll(useTSOuter, `.hello()`, `.hellov2()`),
		},
	} {
		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			out, err := modGen.With(daggerQuery(`{useHello}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"useHello":"hello"}`, out)

			// make back-incompatible change to dep
			newInner := strings.ReplaceAll(useInner, `Hello()`, `Hellov2()`)
			modGen = modGen.
				WithWorkdir("/work/dep").
				With(sdkSource("go", newInner)).
				WithWorkdir("/work").
				With(daggerExec("develop"))

			var codegenContents string
			if tc.sdk == "go" {
				// If go, the changes will be in `dep.gen.go`
				codegenContents, err = modGen.File("internal/dagger/dep.gen.go").Contents(ctx)
			} else {
				codegenContents, err = modGen.File(sdkCodegenFile(t, tc.sdk)).Contents(ctx)
			}

			require.NoError(t, err)
			require.Contains(t, codegenContents, tc.expected)

			modGen = modGen.With(sdkSource(tc.sdk, tc.changed))

			out, err = modGen.With(daggerQuery(`{useHello}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"useHello":"hello"}`, out)
		})
	}
}

func (ModuleSuite) TestSyncDeps(ctx context.Context, t *testctx.T) {
	// verify that changes to deps result in a develop to the depender module
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk:    "go",
			source: useGoOuter,
		},
		{
			sdk:    "python",
			source: usePythonOuter,
		},
		{
			sdk:    "typescript",
			source: useTSOuter,
		},
	} {
		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			modGen = modGen.With(daggerQuery(`{useHello}`))
			out, err := modGen.Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"useHello":"hello"}`, out)

			newInner := strings.ReplaceAll(useInner, `"hello"`, `"goodbye"`)
			modGen = modGen.
				WithWorkdir("/work/dep").
				With(sdkSource("go", newInner)).
				WithWorkdir("/work").
				With(daggerExec("develop"))

			out, err = modGen.With(daggerQuery(`{useHello}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"useHello":"goodbye"}`, out)
		})
	}
}

func (ModuleSuite) TestUseLocalMulti(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import "context"
import "fmt"

type Test struct {}

func (m *Test) Names(ctx context.Context) ([]string, error) {
	fooName, err := dag.Foo().Name(ctx)
	if err != nil {
		return nil, fmt.Errorf("foo.name: %w", err)
	}
	barName, err := dag.Bar().Name(ctx)
	if err != nil {
		return nil, fmt.Errorf("bar.name: %w", err)
	}
	return []string{fooName, barName}, nil
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import dag

@dagger.object_type
class Test:
    @dagger.function
    async def names(self) -> list[str]:
        return [
            await dag.foo().name(),
            await dag.bar().name(),
        ]
`,
		},
		{
			sdk: "typescript",
			source: `
import { dag, object, func } from '@dagger.io/dagger'

@object()
export class Test {
	@func()
	async names(): Promise<string[]> {
		return [await dag.foo().name(), await dag.bar().name()]
	}
}
`,
		},
	} {
		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/foo").
				WithNewFile("/work/foo/main.go", `package main

        type Foo struct {}

        func (m *Foo) Name() string { return "foo" }
        `,
				).
				With(daggerExec("init", "--source=.", "--name=foo", "--sdk=go")).
				WithWorkdir("/work/bar").
				WithNewFile("/work/bar/main.go", `package main

        type Bar struct {}

        func (m *Bar) Name() string { return "bar" }
        `,
				).
				With(daggerExec("init", "--source=.", "--name=bar", "--sdk=go")).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
				With(daggerExec("install", "./foo")).
				With(daggerExec("install", "./bar")).
				With(sdkSource(tc.sdk, tc.source)).
				WithEnvVariable("BUST", identity.NewID()) // NB(vito): hmm...

			out, err := modGen.With(daggerQuery(`{names}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"names":["foo", "bar"]}`, out)
		})
	}
}
