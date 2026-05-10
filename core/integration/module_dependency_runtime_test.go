package core

// Workspace alignment: mostly aligned; coverage targets post-workspace module dependency runtime semantics, but setup still relies on historical module helpers.
// Scope: Dependency graph resolution, local module deps, codegen refresh after dep changes, dep sync behavior, and multi-dependency local wiring.
// Intent: Keep module dependency runtime behavior separate from explicit module dependency CLI mutation coverage.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// TestConflictingSameNameTransitiveDeps covers two distinct dependency-graph
// contracts for A -> B -> Dint and A -> C -> Dstr, where both D modules have
// the same name and object names but incompatible field types.
func (ModuleSuite) TestConflictingSameNameTransitiveDeps(ctx context.Context, t *testctx.T) {
	// This setup is often slow locally; keep the two contracts below in one test
	// so they share the same dependency graph.
	if testing.Short() {
		t.SkipNow()
	}

	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dstr").
		With(daggerExec("module", "init", "--source=.", "--sdk=go", "d", ".")).
		WithNewFile("main.go", `package main

type D struct{}

type Obj struct {
	Foo string
}

func (m *D) Fn(foo string) Obj {
	return Obj{Foo: foo}
}
`,
		)

	ctr = ctr.
		WithWorkdir("/work/dint").
		With(daggerExec("module", "init", "--source=.", "--sdk=go", "d", ".")).
		WithNewFile("main.go", `package main

type D struct{}

type Obj struct {
	Foo int
}

func (m *D) Fn(foo int) Obj {
	return Obj{Foo: foo}
}
`,
		)

	ctr = ctr.
		WithWorkdir("/work").
		With(daggerExec("module", "init", "--source=c", "--sdk=go", "c", "c")).
		WithWorkdir("/work/c").
		With(daggerExec("module", "install", "../dstr")).
		WithNewFile("main.go", `package main

import (
	"context"
)

type C struct{}

func (m *C) Fn(ctx context.Context, foo string) (string, error) {
	return dag.D().Fn(foo).Foo(ctx)
}
`,
		)

	ctr = ctr.
		WithWorkdir("/work").
		With(daggerExec("module", "init", "--source=b", "--sdk=go", "b", "b")).
		With(daggerExec("module", "install", "-m=b", "./dint")).
		WithNewFile("/work/b/main.go", `package main

import (
	"context"
)

type B struct{}

func (m *B) Fn(ctx context.Context, foo int) (int, error) {
	return dag.D().Fn(foo).Foo(ctx)
}
`,
		)

	ctr = ctr.
		WithWorkdir("/work").
		With(daggerExec("module", "init", "--source=a", "--sdk=go", "a", "a")).
		WithWorkdir("/work/a").
		With(daggerExec("module", "install", "../b")).
		With(daggerExec("module", "install", "../c")).
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
		)

	t.Run("runtime resolves conflicting transitive deps", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerQuery(`{fn}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"fn": "foo123"}`, out)
	})

	t.Run("schema exposes only direct deps", func(ctx context.Context, t *testctx.T) {
		types := currentSchema(ctx, t, ctr).Types
		require.NotNil(t, types.Get("A"))
		require.NotNil(t, types.Get("B"))
		require.NotNil(t, types.Get("C"))
		require.Nil(t, types.Get("D"))
	})
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

type localDepTestCase struct {
	sdk    string
	source string
}

var useLocalDepTestCases = []localDepTestCase{
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
}

// TestUseLocalDependencyFromParentModule verifies the core local-dependency
// contract: a parent module installs a dependency by relative path, then client
// calls into the parent module can execute parent code that calls the dependency.
func (ModuleSuite) TestUseLocalDependencyFromParentModule(ctx context.Context, t *testctx.T) {
	for _, tc := range useLocalDepTestCases {
		t.Run(fmt.Sprintf("%s parent calls local dependency", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := testModuleWithLocalDep(t, c, tc.sdk, tc.source)

			out, err := modGen.With(daggerQuery(`{useHello}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"useHello":"hello"}`, out)
		})
	}
}

// TestUseLocalDependencyDirectSchemaAccess documents current schema exposure:
// a client that loads the parent module can call the direct dependency's root
// field too. Workspace semantics are expected to change this; failures here are
// about dependency re-exporting, not parent module use of its dependency.
func (ModuleSuite) TestUseLocalDependencyDirectSchemaAccess(ctx context.Context, t *testctx.T) {
	for _, tc := range useLocalDepTestCases {
		t.Run(fmt.Sprintf("%s schema exposes local dependency", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := testModuleWithLocalDep(t, c, tc.sdk, tc.source)

			out, err := modGen.With(daggerQuery(`{dep{hello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"dep":{"hello":"hello"}}`, out)
		})
	}
}

func testModuleWithLocalDep(t *testctx.T, c *dagger.Client, sdk, source string) *dagger.Container {
	return goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("module", "init", "--sdk=go", "dep", ".")).
		With(sdkSource("go", useInner)).
		WithWorkdir("/work").
		With(daggerExec("module", "init", "test", "--sdk="+sdk, "--source=.", ".")).
		With(sdkSource(sdk, source)).
		With(daggerExec("module", "install", "./dep"))
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
				With(daggerExec("module", "init", "--sdk=go", "dep", ".")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("module", "init", "test", "--sdk="+tc.sdk, "--source=.", ".")).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("module", "install", "./dep"))

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
				With(daggerExec("module", "init", "--sdk=go", "dep", ".")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("module", "init", "test", "--sdk="+tc.sdk, "--source=.", ".")).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("module", "install", "./dep"))

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
				With(daggerExec("module", "init", "--source=.", "--sdk=go", "foo", ".")).
				WithWorkdir("/work/bar").
				WithNewFile("/work/bar/main.go", `package main

        type Bar struct {}

        func (m *Bar) Name() string { return "bar" }
        `,
				).
				With(daggerExec("module", "init", "--source=.", "--sdk=go", "bar", ".")).
				WithWorkdir("/work").
				With(daggerExec("module", "init", "test", "--sdk="+tc.sdk, "--source=.", ".")).
				With(daggerExec("module", "install", "./foo")).
				With(daggerExec("module", "install", "./bar")).
				With(sdkSource(tc.sdk, tc.source)).
				WithEnvVariable("BUST", identity.NewID()) // NB(vito): hmm...

			out, err := modGen.With(daggerQuery(`{names}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"names":["foo", "bar"]}`, out)
		})
	}
}
