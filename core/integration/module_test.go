package core

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/cenkalti/backoff/v4"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/dagger/testctx"
)

type ModuleSuite struct{}

func TestModule(t *testing.T) {
	testctx.Run(testCtx, t, ModuleSuite{}, Middleware()...)
}

func (ModuleSuite) TestInvalidSDK(ctx context.Context, t *testctx.T) {
	t.Run("invalid sdk returns readable error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=foo-bar"))

		_, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), `The "foo-bar" SDK does not exist.`)
	})

	t.Run("specifying version with either of go/python/typescript sdk returns error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=go@main"))

		_, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), `the go sdk does not currently support selecting a specific version`)
	})
}

func (ModuleSuite) TestDescription(ctx context.Context, t *testctx.T) {
	type source struct {
		file     string
		contents string
	}

	for i, tc := range []struct {
		sdk     string
		sources []source
	}{
		{
			sdk: "go",
			sources: []source{
				{
					file: "main.go",
					contents: `
// Test module, short description
//
// Long description, with full sentences.

package main

// Test object, short description
type Test struct {
	// +default="foo"
	Foo string
}
`,
				},
			},
		},
		{
			sdk: "go",
			sources: []source{
				{
					file: "a.go",
					contents: `
// First, but not main
package main

type Foo struct {}
`,
				},
				{
					file: "z.go",
					contents: `
// Test module, short description
//
// Long description, with full sentences.

package main

// Test object, short description
	type Test struct {
}

func (*Test) Foo() Foo {
	return Foo{}
}
`,
				},
			},
		},
		{
			sdk: "python",
			sources: []source{
				{
					file: "src/main/__init__.py",
					contents: `
"""Test module, short description

Long description, with full sentences.
"""

from dagger import field, object_type

@object_type
class Test:
    """Test object, short description"""

    foo: str = field(default="foo")
`,
				},
			},
		},
		{
			sdk: "python",
			sources: []source{
				{
					file: "src/main/foo.py",
					contents: `
"""Not the main file"""

from dagger import field, object_type

@object_type
class Foo:
    bar: str = field(default="bar")
`,
				},
				{
					file: "src/main/__init__.py",
					contents: `
"""Test module, short description

Long description, with full sentences.
"""

from dagger import function, object_type

from .foo import Foo

@object_type
class Test:
    """Test object, short description"""

    foo = function(Foo)
`,
				},
			},
		},
		{
			sdk: "typescript",
			sources: []source{
				{
					file: "src/index.ts",
					contents: `
/**
 * Test module, short description
 *
 * Long description, with full sentences.
 */
import { object, func } from '@dagger.io/dagger'

/**
 * Test object, short description
 */
@object()
export class Test {
    @func()
    foo: string = "foo"
}
`,
				},
			},
		},
		{
			sdk: "typescript",
			sources: []source{
				{
					file: "src/foo.ts",
					contents: `
/**
 * Not the main file
 */
import { object, func } from '@dagger.io/dagger'

@object()
export class Foo {
    @func()
    bar = "bar"
}
`,
				},
				{
					file: "src/index.ts",
					contents: `
/**
 * Test module, short description
 *
 * Long description, with full sentences.
 */
import { object, func } from '@dagger.io/dagger'
import { Foo } from "./foo"

/**
 * Test object, short description
 */
@object()
export class Test {
    @func()
    foo(): Foo {
        return new Foo()
    }
}
`,
				},
			},
		},
	} {
		tc := tc

		t.Run(fmt.Sprintf("%s with %d files (#%d)", tc.sdk, len(tc.sources), i+1), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work")

			for _, src := range tc.sources {
				src := src
				modGen = modGen.WithNewFile(src.file, heredoc.Doc(src.contents))
			}

			mod := inspectModule(ctx, t,
				modGen.With(daggerExec("init", "--source=.", "--name=test", "--sdk="+tc.sdk)))

			require.Equal(t,
				"Test module, short description\n\nLong description, with full sentences.",
				mod.Get("description").String(),
			)
			require.Equal(t,
				"Test object, short description",
				mod.Get("objects.#.asObject|#(name=Test).description").String(),
			)
		})
	}
}

func (ModuleSuite) TestPrivateField(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		sdk    string
		source string
	}{
		{
			sdk: "go",
			source: `package main

type Minimal struct {
	Foo string

	Bar string // +private
}

func (m *Minimal) Set(foo string, bar string) *Minimal {
	m.Foo = foo
	m.Bar = bar
	return m
}

func (m *Minimal) Hello() string {
	return m.Foo + m.Bar
}
`,
		},
		{
			sdk: "python",
			source: `from dagger import field, function, object_type

@object_type
class Minimal:
    foo: str = field(default="")
    bar: str = ""

    @function
    def set(self, foo: str, bar: str) -> "Minimal":
        self.foo = foo
        self.bar = bar
        return self

    @function
    def hello(self) -> str:
        return self.foo + self.bar
`,
		},
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
export class Minimal {
  @func()
  foo: string

  bar?: string

  constructor(foo?: string, bar?: string) {
    this.foo = foo
    this.bar = bar
  }

  @func()
  set(foo: string, bar: string): Minimal {
    this.foo = foo
    this.bar = bar
    return this
  }

  @func()
  hello(): string {
    return this.foo + this.bar
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=minimal", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			obj := inspectModuleObjects(ctx, t, modGen).Get("0")
			require.Equal(t, "Minimal", obj.Get("name").String())
			require.Len(t, obj.Get(`fields`).Array(), 1)
			prop := obj.Get(`fields.#(name="foo")`)
			require.Equal(t, "foo", prop.Get("name").String())

			out, err := modGen.With(daggerQuery(`{minimal{set(foo: "abc", bar: "xyz"){hello}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"minimal":{"set":{"hello": "abcxyz"}}}`, out)

			out, err = modGen.With(daggerQuery(`{minimal{set(foo: "abc", bar: "xyz"){foo}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"minimal":{"set":{"foo": "abc"}}}`, out)

			_, err = modGen.With(daggerQuery(`{minimal{set(foo: "abc", bar: "xyz"){bar}}}`)).Stdout(ctx)
			require.ErrorContains(t, err, `Minimal has no such field: "bar"`)
		})
	}
}

func (ModuleSuite) TestOptionalDefaults(ctx context.Context, t *testctx.T) {
	// Test expressiveness for following schema:
	//   a: String!
	//   b: String
	//   c: String! = "foo"
	//   d: String = null
	//   e: String = "bar"

	for _, tc := range []struct {
		sdk      string
		source   string
		expected string
	}{
		{
			sdk: "go",
			source: `package main

import "fmt"

type Test struct{ }

func (m *Test) Foo(
	a string,
	// +optional
	b *string,
	// +default="foo"
	c string,
	// +default=null
	d *string,
	// +default="bar"
	e *string,
) string {
	return fmt.Sprintf("%+v, %+v, %+v, %+v, %+v", a, b, c, d, *e)
}
`,
			expected: "test, <nil>, foo, <nil>, bar",
		},
		{
			sdk: "python",
			source: `from dagger import field, function, object_type

@object_type
class Test:
    @function
    def foo(
        self,
        a: str,
        b: str | None,
        c: str = "foo",
        d: str | None = None,
        e: str | None = "bar",
    ) -> str:
        return ", ".join(repr(x) for x in (a, b, c, d, e))
`,
			expected: "'test', None, 'foo', None, 'bar'",
		},
		{
			sdk: "typescript",
			source: `import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  foo(
    a: string,
    b?: string,
    c: string = "foo",
    d: string | null = null,
    e: string | null = "bar",
  ): string {
    return [a, b, c, d, e].map(v => JSON.stringify(v)).join(", ")
  }
}
`,
			expected: "\"test\", , \"foo\", null, \"bar\"",
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := modInit(t, c, tc.sdk, tc.source)

			q := heredoc.Doc(`
                query {
                    __type(name: "Test") {
                        fields {
                            name
                            args {
                                name
                                type {
                                    name
                                    kind
                                    ofType {
                                        name
                                        kind
                                    }
                                }
                                defaultValue
                            }
                        }
                    }
                }
            `)

			out, err := modGen.With(daggerQuery(q)).Stdout(ctx)
			require.NoError(t, err)
			args := gjson.Get(out, "__type.fields.#(name=foo).args")

			t.Run("a: String!", func(ctx context.Context, t *testctx.T) {
				// required, i.e., non-null and no default
				arg := args.Get("#(name=a)")
				require.Equal(t, "NON_NULL", arg.Get("type.kind").String())
				require.Equal(t, "SCALAR", arg.Get("type.ofType.kind").String())
				require.Nil(t, arg.Get("defaultValue").Value())
			})

			t.Run("b: String", func(ctx context.Context, t *testctx.T) {
				// GraphQL implicitly sets default to null for nullable types
				arg := args.Get("#(name=b)")
				require.Equal(t, "SCALAR", arg.Get("type.kind").String())
				require.Nil(t, arg.Get("defaultValue").Value())
			})

			t.Run(`c: String! = "foo"`, func(ctx context.Context, t *testctx.T) {
				// non-null, with default
				arg := args.Get("#(name=c)")
				require.Equal(t, "NON_NULL", arg.Get("type.kind").String())
				require.Equal(t, "SCALAR", arg.Get("type.ofType.kind").String())
				require.JSONEq(t, `"foo"`, arg.Get("defaultValue").String())
			})

			t.Run("d: String = null", func(ctx context.Context, t *testctx.T) {
				// nullable, with explicit null default; same as b in practice
				arg := args.Get("#(name=d)")
				require.Equal(t, "SCALAR", arg.Get("type.kind").String())
				require.JSONEq(t, "null", arg.Get("defaultValue").String())
			})

			t.Run(`e: String = "bar"`, func(ctx context.Context, t *testctx.T) {
				// nullable, with non-null default
				arg := args.Get("#(name=e)")
				require.Equal(t, "SCALAR", arg.Get("type.kind").String())
				require.JSONEq(t, `"bar"`, arg.Get("defaultValue").String())
			})

			t.Run("default values", func(ctx context.Context, t *testctx.T) {
				out, err = modGen.With(daggerCall("foo", "--a=test")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, tc.expected, out)
			})
		})
	}
}

func (ModuleSuite) TestCodegenOptionals(ctx context.Context, t *testctx.T) {
	// Same code as TestOptionalDefaults since it guarantees this is being
	// registered correctly and equally by all SDKs.
	src := `package main

import "fmt"

type Dep struct {}

func (m *Dep) Ctl(
	a string,
	// +optional
	b *string,
	// +default="foo"
	c string,
	// +default=null
	d *string,
	// +default="bar"
	e *string,
) string {
	return fmt.Sprintf("%+v, %+v, %+v, %+v, %+v", a, b, c, d, *e)
}
`
	expected := "foo, <nil>, foo, <nil>, bar"

	for _, tc := range []struct {
		sdk    string
		source string
	}{
		{
			sdk: "go",
			source: `package main

import "context"

type Test struct {}

func (m *Test) Test(ctx context.Context) (string, error) {
	return dag.Dep().Ctl(ctx, "foo")
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
    async def test(self) -> str:
        return await dag.dep().ctl("foo")
`,
		},
		{
			sdk: "typescript",
			source: `import { dag, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  async test(): Promise<string> {
    return await dag.dep().ctl("foo")
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := modInit(t, c, tc.sdk, tc.source).
				With(withModInitAt("./dep", "go", src)).
				With(daggerExec("install", "./dep")).
				With(daggerCall("test")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, expected, out)
		})
	}
}

func (ModuleSuite) TestGlobalVarDAG(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import "context"

type Foo struct {}

var someDefault = dag.Container().From("` + alpineImage + `")

func (m *Foo) Fn(ctx context.Context) (string, error) {
	return someDefault.WithExec([]string{"echo", "foo"}).Stdout(ctx)
}
`,
		},
		{
			sdk: "python",
			source: `from dagger import dag, function, object_type

SOME_DEFAULT = dag.container().from_("` + alpineImage + `")

@object_type
class Foo:
    @function
    async def fn(self) -> str:
        return await SOME_DEFAULT.with_exec(["echo", "foo"]).stdout()
`,
		},
		{
			sdk: "typescript",
			source: `
import { dag, object, func } from "@dagger.io/dagger"

var someDefault = dag.container().from("` + alpineImage + `")

@object()
export class Foo {
  @func()
  async fn(): Promise<string> {
    return someDefault.withExec(["echo", "foo"]).stdout()
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			out, err := modGen.With(daggerQuery(`{foo{fn}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"fn":"foo\n"}}`, out)
		})
	}
}

func (ModuleSuite) TestConflictingSameNameDeps(ctx context.Context, t *testctx.T) {
	// A -> B -> Dint
	// A -> C -> Dstr
	// where Dint and Dstr are modules with the same name and same object names but conflicting types
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
		)

	ctr = ctr.
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
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
		)

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
		)

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
		)

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
		)

	out, err := ctr.With(daggerQuery(`{a{fn}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"a":{"fn": "foo123"}}`, out)

	// verify that no types from (transitive) deps show up
	types := currentSchema(ctx, t, ctr).Types
	require.NotNil(t, types.Get("A"))
	require.Nil(t, types.Get("B"))
	require.Nil(t, types.Get("C"))
	require.Nil(t, types.Get("D"))
}

func (ModuleSuite) TestSelfAPICall(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"context"

	"github.com/Khan/genqlient/graphql"
)

type Test struct{}

func (m *Test) FnA(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{test{fnB}}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["test"].(map[string]any)["fnB"].(string), nil
}

func (m *Test) FnB() string {
	return "hi from b"
}
`,
		).
		With(daggerQuery(`{test{fnA}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"fnA": "hi from b"}}`, out)
}

var useInner = `package main

type Dep struct{}

func (m *Dep) Hello() string {
	return "hello"
}
`

var useGoOuter = `package main

import "context"

type Use struct{}

func (m *Use) UseHello(ctx context.Context) (string, error) {
	return dag.Dep().Hello(ctx)
}
`

var usePythonOuter = `from dagger import dag, function

@function
def use_hello() -> str:
    return dag.dep().hello()
`

var useTSOuter = `
import { dag, object, func } from '@dagger.io/dagger'

@object()
export class Use {
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
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=use", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			out, err := modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

			// cannot use transitive dependency directly
			_, err = modGen.With(daggerQuery(`{dep{hello}}`)).Stdout(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, `Query has no such field: "dep"`)
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
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=use", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			out, err := modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

			// make back-incompatible change to dep
			newInner := strings.ReplaceAll(useInner, `Hello()`, `Hellov2()`)
			modGen = modGen.
				WithWorkdir("/work/dep").
				With(sdkSource("go", newInner)).
				WithWorkdir("/work").
				With(daggerExec("develop"))

			codegenContents, err := modGen.File(sdkCodegenFile(t, tc.sdk)).Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, codegenContents, tc.expected)

			modGen = modGen.With(sdkSource(tc.sdk, tc.changed))

			out, err = modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)
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
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=use", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			modGen = modGen.With(daggerQuery(`{use{useHello}}`))
			out, err := modGen.Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

			newInner := strings.ReplaceAll(useInner, `"hello"`, `"goodbye"`)
			modGen = modGen.
				WithWorkdir("/work/dep").
				With(sdkSource("go", newInner)).
				WithWorkdir("/work").
				With(daggerExec("develop"))

			out, err = modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"goodbye"}}`, out)
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

type Use struct {}

func (m *Use) Names(ctx context.Context) ([]string, error) {
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
			source: `from dagger import dag, function

@function
async def names() -> list[str]:
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
export class Use {
	@func()
	async names(): Promise<string[]> {
		return [await dag.foo().name(), await dag.bar().name()]
	}
}
`,
		},
	} {
		tc := tc

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
				With(daggerExec("init", "--name=use", "--sdk="+tc.sdk)).
				With(daggerExec("install", "./foo")).
				With(daggerExec("install", "./bar")).
				With(sdkSource(tc.sdk, tc.source)).
				WithEnvVariable("BUST", identity.NewID()) // NB(vito): hmm...

			out, err := modGen.With(daggerQuery(`{use{names}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"names":["foo", "bar"]}}`, out)
		})
	}
}

func (ModuleSuite) TestConstructor(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

func New(
	ctx context.Context,
	foo string,
	bar *int, // +optional
	baz []string,
	dir *dagger.Directory,
) *Test {
	bar2 := 42
	if bar != nil {
		bar2 = *bar
	}
	return &Test{
		Foo: foo,
		Bar: bar2,
		Baz: baz,
		Dir: dir,
	}
}

type Test struct {
	Foo string
	Bar int
	Baz []string
	Dir *dagger.Directory
	NeverSetDir *dagger.Directory
}

func (m *Test) GimmeFoo() string {
	return m.Foo
}

func (m *Test) GimmeBar() int {
	return m.Bar
}

func (m *Test) GimmeBaz() []string {
	return m.Baz
}

func (m *Test) GimmeDirEnts(ctx context.Context) ([]string, error) {
	return m.Dir.Entries(ctx)
}
`,
			},
			{
				sdk: "python",
				source: `import dagger
from dagger import field, function, object_type

@object_type
class Test:
    foo: str = field()
    dir: dagger.Directory = field()
    bar: int = field(default=42)
    baz: list[str] = field(default=list)
    never_set_dir: dagger.Directory | None = field(default=None)

    @function
    def gimme_foo(self) -> str:
        return self.foo

    @function
    def gimme_bar(self) -> int:
        return self.bar

    @function
    def gimme_baz(self) -> list[str]:
        return self.baz

    @function
    async def gimme_dir_ents(self) -> list[str]:
        return await self.dir.entries()
`,
			},
			{
				sdk: "typescript",
				source: `
import { Directory, object, func } from '@dagger.io/dagger';

@object()
export class Test {
	@func()
	foo: string

	@func()
	dir: Directory

	@func()
	bar: number

	@func()
	baz: string[]

	@func()
	neverSetDir?: Directory

	constructor(foo: string, dir: Directory, bar = 42, baz: string[] = []) {
		this.foo = foo;
		this.dir = dir;
		this.bar = bar;
		this.baz = baz;
	}

	@func()
	gimmeFoo(): string {
		return this.foo;
	}

	@func()
	gimmeBar(): number {
		return this.bar;
	}

	@func()
	gimmeBaz(): string[] {
		return this.baz;
	}

	@func()
	async gimmeDirEnts(): Promise<string[]> {
		return this.dir.entries();
	}
}
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				ctr := modInit(t, c, tc.sdk, tc.source)

				out, err := ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "foo")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "abc")

				out, err = ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "gimme-foo")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "abc")

				out, err = ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "42")

				out, err = ctr.With(daggerCall("--foo=abc", "--baz=x,y,z", "--dir=.", "gimme-bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "42")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "123")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "gimme-bar")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "123")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "baz")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "x\ny\nz")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "gimme-baz")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "x\ny\nz")

				out, err = ctr.With(daggerCall("--foo=abc", "--bar=123", "--baz=x,y,z", "--dir=.", "gimme-dir-ents")).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, strings.TrimSpace(out), "dagger.json")
			})
		}
	})

	t.Run("fields only", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"context"
)

func New(ctx context.Context) (Test, error) {
	v, err := dag.Container().From("%s").File("/etc/alpine-release").Contents(ctx)
	if err != nil {
		return Test{}, err
	}
	return Test{
		AlpineVersion: v,
	}, nil
}

type Test struct {
	AlpineVersion string
}
`,
			},
			{
				sdk: "python",
				source: `from dagger import dag, field, function, object_type

@object_type
class Test:
    alpine_version: str = field()

    @classmethod
    async def create(cls) -> "Test":
        return cls(alpine_version=await (
            dag.container()
            .from_("%s")
            .file("/etc/alpine-release")
            .contents()
        ))
`,
			},
			{
				sdk: "typescript",
				source: `
import { dag, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  alpineVersion: string

  // NOTE: this is not standard to do async operations in the constructor.
  // This is only for testing purpose but it shouldn't be done in real usage.
  constructor() {
    return (async () => {
      this.alpineVersion = await dag.container().from("%s").file("/etc/alpine-release").contents()

      return this; // Return the newly-created instance
    })();
  }
}
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				ctr := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/test").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
					With(sdkSource(tc.sdk, fmt.Sprintf(tc.source, alpineImage)))

				out, err := ctr.With(daggerCall("alpine-version")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, distconsts.AlpineVersion, strings.TrimSpace(out))
			})
		}
	})

	t.Run("return error", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"fmt"
)

func New() (*Test, error) {
	return nil, fmt.Errorf("too bad: %s", "so sad")
}

type Test struct {
	Foo string
}
`,
			},
			{
				sdk: "python",
				source: `from dagger import object_type, field

@object_type
class Test:
    foo: str = field()

    def __init__(self):
        raise ValueError("too bad: " + "so sad")
`,
			},
			{
				sdk: "typescript",
				source: `
import { object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  foo: string

  constructor() {
    throw new Error("too bad: " + "so sad")
  }
}
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				var logs safeBuffer
				c := connect(ctx, t, dagger.WithLogOutput(&logs))

				ctr := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/test").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
					With(sdkSource(tc.sdk, tc.source))

				_, err := ctr.With(daggerCall("foo")).Stdout(ctx)
				require.Error(t, err)

				require.NoError(t, c.Close())

				t.Log(logs.String())
				require.Regexp(t, "too bad: so sad", logs.String())
			})
		}
	})

	t.Run("python: with default factory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		content := identity.NewID()

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/test").
			With(daggerExec("init", "--name=test", "--sdk=python")).
			With(sdkSource("python", fmt.Sprintf(`import dagger
from dagger import dag, object_type, field

@object_type
class Test:
    foo: dagger.File = field(default=lambda: (
        dag.directory()
        .with_new_file("foo.txt", "%s")
        .file("foo.txt")
    ))
    bar: list[str] = field(default=list)
`, content),
			))

		out, err := ctr.With(daggerCall("foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("--foo=dagger.json", "foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"sdk": "python"`)

		_, err = ctr.With(daggerCall("bar")).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("typescript: with default factory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		content := identity.NewID()

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/test").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", fmt.Sprintf(`
import { dag, File, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  foo: File = dag.directory().withNewFile("foo.txt", "%s").file("foo.txt")

  @func()
  bar: string[] = []

  // Allow foo to be set through the constructor
  constructor(foo?: File) {
    if (foo) {
      this.foo = foo
    }
  }
}
`, content),
			))

		out, err := ctr.With(daggerCall("foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("--foo=dagger.json", "foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"sdk": "typescript"`)

		_, err = ctr.With(daggerCall("bar")).Sync(ctx)
		require.NoError(t, err)
	})
}

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
	"dagger/wrapper/internal/dagger"
)

type Wrapper struct{}

func (m *Wrapper) Container() *WrappedContainer {
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
from dagger import dag, field, function, object_type

@object_type
class WrappedContainer:
    unwrap: dagger.Container = field()

    @function
    def echo(self, msg: str) -> Self:
        return WrappedContainer(unwrap=self.unwrap.with_exec(["echo", "-n", msg]))

@object_type
class Wrapper:
    @function
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
export class Wrapper {
  @func()
  container(): WrappedContainer {
    return new WrappedContainer(dag.container().from("` + alpineImage + `"))
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=wrapper", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			id := identity.NewID()
			out, err := modGen.With(daggerQuery(
				fmt.Sprintf(`{wrapper{container{echo(msg:%q){unwrap{stdout}}}}}`, id),
			)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t,
				fmt.Sprintf(`{"wrapper":{"container":{"echo":{"unwrap":{"stdout":%q}}}}}`, id),
				out)
		})
	}
}

func (ModuleSuite) TestLotsOfFunctions(ctx context.Context, t *testctx.T) {
	const funcCount = 100

	t.Run("go sdk", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		mainSrc := `
		package main

		type PotatoSack struct {}
		`

		for i := 0; i < funcCount; i++ {
			mainSrc += fmt.Sprintf(`
			func (m *PotatoSack) Potato%d() string {
				return "potato #%d"
			}
			`, i, i)
		}

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/main.go", mainSrc).
			With(daggerExec("init", "--source=.", "--name=potatoSack", "--sdk=go"))

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					With(daggerCall(fmt.Sprintf("potato-%d", i))).
					Sync(ctx)
				return err
			})
		}
		require.NoError(t, eg.Wait())
	})

	t.Run("python sdk", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		mainSrc := `from dagger import function
		`

		for i := 0; i < funcCount; i++ {
			mainSrc += fmt.Sprintf(`
@function
def potato_%d() -> str:
    return "potato #%d"
`, i, i)
		}

		modGen := pythonModInit(t, c, mainSrc)

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					With(daggerCall(fmt.Sprintf("potato-%d", i))).
					Sync(ctx)
				return err
			})
		}
		require.NoError(t, eg.Wait())
	})

	t.Run("typescript sdk", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		mainSrc := `
		import { object, func } from "@dagger.io/dagger"

@object()
export class PotatoSack {
		`

		for i := 0; i < funcCount; i++ {
			mainSrc += fmt.Sprintf(`
  @func()
  potato_%d(): string {
    return "potato #%d"
  }
			`, i, i)
		}

		mainSrc += "\n}"

		modGen := c.
			Container().
			From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(sdkSource("typescript", mainSrc)).
			With(daggerExec("init", "--name=potatoSack", "--sdk=typescript"))

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					With(daggerCall(fmt.Sprintf("potato-%d", i))).
					Sync(ctx)
				return err
			})
		}
		require.NoError(t, eg.Wait())
	})
}

func (ModuleSuite) TestLotsOfDeps(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work")

	modCount := 0

	getModMainSrc := func(name string, depNames []string) string {
		t.Helper()
		mainSrc := fmt.Sprintf(`package main
	import "context"

	type %s struct {}

	func (m *%s) Fn(ctx context.Context) (string, error) {
		s := "%s"
		var depS string
		_ = depS
		var err error
		_ = err
	`, strcase.ToCamel(name), strcase.ToCamel(name), name)
		for _, depName := range depNames {
			mainSrc += fmt.Sprintf(`
	depS, err = dag.%s().Fn(ctx)
	if err != nil {
		return "", err
	}
	s += depS
	`, strcase.ToCamel(depName))
		}
		mainSrc += "return s, nil\n}\n"
		fmted, err := format.Source([]byte(mainSrc))
		require.NoError(t, err)
		return string(fmted)
	}

	// need to construct dagger.json directly in order to avoid excessive
	// `dagger mod use` calls while constructing the huge DAG of deps
	var rootCfg modules.ModuleConfig

	addModulesWithDeps := func(newMods int, depNames []string) []string {
		t.Helper()

		var newModNames []string
		for i := 0; i < newMods; i++ {
			name := fmt.Sprintf("mod%d", modCount)
			modCount++
			newModNames = append(newModNames, name)
			modGen = modGen.
				WithWorkdir("/work/"+name).
				WithNewFile("./main.go", getModMainSrc(name, depNames))

			var depCfgs []*modules.ModuleConfigDependency
			for _, depName := range depNames {
				depCfgs = append(depCfgs, &modules.ModuleConfigDependency{
					Name:   depName,
					Source: filepath.Join("..", depName),
				})
			}
			modGen = modGen.With(configFile(".", &modules.ModuleConfig{
				Name:         name,
				SDK:          "go",
				Dependencies: depCfgs,
			}))
		}
		return newModNames
	}

	// Create a base module, then add 6 layers of deps, where each layer has one more module
	// than the previous layer and each module within the layer has a dep on each module
	// from the previous layer. Finally add a single module at the top that depends on all
	// modules from the last layer and call that.
	// Basically, this creates a quadratically growing DAG of modules and verifies we
	// handle it efficiently enough to be callable.
	curDeps := addModulesWithDeps(1, nil)
	for i := 0; i < 6; i++ {
		curDeps = addModulesWithDeps(len(curDeps)+1, curDeps)
	}
	addModulesWithDeps(1, curDeps)

	modGen = modGen.With(configFile("..", &rootCfg))

	_, err := modGen.With(daggerCall("fn")).Sync(ctx)
	require.NoError(t, err)
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
		With(daggerQuery(`{test{fn(s:"yo")}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"fn":["*dagger.Sub1Obj made 1:yo", "*dagger.Sub2Obj made 2:yo"]}}`, out)
}

func (ModuleSuite) TestLoops(ctx context.Context, t *testctx.T) {
	// verify circular module dependencies result in an error

	c := connect(ctx, t)

	_, err := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		With(daggerExec("init", "--name=depA", "--sdk=go", "depA")).
		With(daggerExec("init", "--name=depB", "--sdk=go", "depB")).
		With(daggerExec("init", "--name=depC", "--sdk=go", "depC")).
		With(daggerExec("install", "-m=depC", "./depB")).
		With(daggerExec("install", "-m=depB", "./depA")).
		With(daggerExec("install", "-m=depA", "./depC")).
		Sync(ctx)
	require.ErrorContains(t, err, `local module at "/work/depA" has a circular dependency`)
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
				tc := tc

				t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					out, err := modInit(t, c, tc.sdk, tc.source).
						With(daggerQuery(`{test{fn(id:"YES!!!!")}}`)).
						Stdout(ctx)
					require.NoError(t, err)
					require.JSONEq(t, `{"test":{"fn":"YES!!!!"}}`, out)
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
				tc := tc

				t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					_, err := c.Container().From(golangImage).
						WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
						WithWorkdir("/work").
						With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
						With(sdkSource(tc.sdk, tc.source)).
						With(daggerQuery(`{test{fn{id}}}`)).
						Sync(ctx)

					require.ErrorContains(t, err, "cannot define field with reserved name \"id\"")
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
				tc := tc

				t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					_, err := c.Container().From(golangImage).
						WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
						WithWorkdir("/work").
						With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
						With(sdkSource(tc.sdk, tc.source)).
						With(daggerQuery(`{test{id}}`)).
						Sync(ctx)

					require.ErrorContains(t, err, "cannot define function with reserved name \"id\"")
				})
			}
		})
	})
}

func (ModuleSuite) TestExecError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=playground", "--sdk=go")).
		WithNewFile("main.go", `
package main

import (
	"context"
	"errors"
)

type Playground struct{}

func (p *Playground) DoThing(ctx context.Context) error {
	_, err := dag.Container().From("`+alpineImage+`").WithExec([]string{"sh", "-c", "exit 5"}).Sync(ctx)
	var e *ExecError
	if errors.As(err, &e) {
		if e.ExitCode == 5 {
			return nil
		}
	}
	panic("yikes")
}
`,
		)

	_, err := modGen.
		With(daggerQuery(`{playground{doThing}}`)).
		Stdout(ctx)
	require.NoError(t, err)
}

func (ModuleSuite) TestCurrentModuleAPI(ctx context.Context, t *testctx.T) {
	t.Run("name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=WaCkY", "--sdk=go")).
			WithNewFile("/work/main.go", `package main

			import "context"

			type WaCkY struct {}

			func (m *WaCkY) Fn(ctx context.Context) (string, error) {
				return dag.CurrentModule().Name(ctx)
			}
			`,
			).
			With(daggerCall("fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "WaCkY", strings.TrimSpace(out))
	})

	t.Run("source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/subdir/coolfile.txt", "nice").
			WithNewFile("/work/main.go", `package main

			import (
				"context"
				"dagger/test/internal/dagger"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) *dagger.File {
				return dag.CurrentModule().Source().File("subdir/coolfile.txt")
			}
			`,
			).
			With(daggerCall("fn", "contents")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "nice", strings.TrimSpace(out))
	})

	t.Run("workdir", func(ctx context.Context, t *testctx.T) {
		t.Run("dir", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", `package main

			import (
				"context"
				"os"
				"dagger/test/internal/dagger"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (*dagger.Directory, error) {
				if err := os.MkdirAll("subdir/moresubdir", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("subdir/moresubdir/coolfile.txt", []byte("nice"), 0644); err != nil {
					return nil, err
				}
				return dag.CurrentModule().Workdir("subdir/moresubdir"), nil
			}
			`,
				).
				With(daggerCall("fn", "file", "--path=coolfile.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", `package main

			import (
				"context"
				"os"
				"dagger/test/internal/dagger"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (*dagger.File, error) {
				if err := os.MkdirAll("subdir/moresubdir", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("subdir/moresubdir/coolfile.txt", []byte("nice"), 0644); err != nil {
					return nil, err
				}
				return dag.CurrentModule().WorkdirFile("subdir/moresubdir/coolfile.txt"), nil
			}
			`,
				).
				With(daggerCall("fn", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("error on escape", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", `package main

			import (
				"context"
				"os"
				"dagger/test/internal/dagger"
			)

			func New() (*Test, error) {
				if err := os.WriteFile("/rootfile.txt", []byte("notnice"), 0644); err != nil {
					return nil, err
				}
				if err := os.MkdirAll("/foo", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("/foo/foofile.txt", []byte("notnice"), 0644); err != nil {
					return nil, err
				}

				return &Test{}, nil
			}

			type Test struct {}

			func (m *Test) EscapeFile(ctx context.Context) *dagger.File {
				return dag.CurrentModule().WorkdirFile("../rootfile.txt")
			}

			func (m *Test) EscapeFileAbs(ctx context.Context) *dagger.File {
				return dag.CurrentModule().WorkdirFile("/rootfile.txt")
			}

			func (m *Test) EscapeDir(ctx context.Context) *dagger.Directory {
				return dag.CurrentModule().Workdir("../foo")
			}

			func (m *Test) EscapeDirAbs(ctx context.Context) *dagger.Directory {
				return dag.CurrentModule().Workdir("/foo")
			}
			`,
				)

			_, err := ctr.
				With(daggerCall("escape-file", "contents")).
				Stdout(ctx)
			require.ErrorContains(t, err, `workdir path "../rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-file-abs", "contents")).
				Stdout(ctx)
			require.ErrorContains(t, err, `workdir path "/rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-dir", "entries")).
				Stdout(ctx)
			require.ErrorContains(t, err, `workdir path "../foo" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-dir-abs", "entries")).
				Stdout(ctx)
			require.ErrorContains(t, err, `workdir path "/foo" escapes workdir`)
		})
	})
}

func (ModuleSuite) TestCustomSDK(ctx context.Context, t *testctx.T) {
	t.Run("local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/coolsdk").
			With(daggerExec("init", "--source=.", "--name=cool-sdk", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"dagger/cool-sdk/internal/dagger"
)

type CoolSdk struct {}

func (m *CoolSdk) ModuleRuntime(modSource *dagger.ModuleSource, introspectionJson string) *dagger.Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *CoolSdk) Codegen(modSource *dagger.ModuleSource, introspectionJson string) *dagger.GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("go").AsModule().GeneratedContextDirectory())
}

func (m *CoolSdk) RequiredPaths() []string {
	return []string{
		"**/go.mod",
		"**/go.sum",
		"**/go.work",
		"**/go.work.sum",
		"**/vendor/",
		"**/*.go",
	}
}
`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=coolsdk")).
			WithNewFile("main.go", `package main

import "os"

type Test struct {}

func (m *Test) Fn() string {
	return os.Getenv("COOL")
}
`,
			)

		out, err := ctr.
			With(daggerCall("fn")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "true", strings.TrimSpace(out))
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("git", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
			defer cleanup()

			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				With(mountedSocket).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk="+testGitModuleRef(tc, "cool-sdk"))).
				WithNewFile("main.go", `package main

import "os"

type Test struct {}

func (m *Test) Fn() string {
	return os.Getenv("COOL")
}
`,
				)

			out, err := ctr.
				With(daggerCall("fn")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, "true", strings.TrimSpace(out))
		})
	})
}

// TestModuleHostError verifies the host api is not exposed to modules
func (ModuleSuite) TestHostError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/work/main.go", `package main
 			import (
 				"context"
				"dagger/test/internal/dagger"
 			)
 			type Test struct {}
 			func (m *Test) Fn(ctx context.Context) *dagger.Directory {
 				return dag.Host().Directory(".")
 			}
 			`,
		).
		With(daggerCall("fn")).
		Sync(ctx)
	require.ErrorContains(t, err, "dag.Host undefined")
}

// TestModuleEngineError verifies the engine api is not exposed to modules
func (ModuleSuite) TestEngineError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/work/main.go", `package main
 			import (
 				"context"
 			)
 			type Test struct {}
 			func (m *Test) Fn(ctx context.Context) error {
 				_, _ = dag.DaggerEngine().LocalCache().EntrySet().Entries(ctx)
				return nil
 			}
 			`,
		).
		With(daggerCall("fn")).
		Sync(ctx)
	require.ErrorContains(t, err, "dag.DaggerEngine undefined")
}

func (ModuleSuite) TestDaggerListen(ctx context.Context, t *testctx.T) {
	t.Run("with mod", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		_, err := hostDaggerExec(ctx, t, modDir, "--debug", "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		addr := "127.0.0.1:12456"
		listenCmd := hostDaggerCommand(ctx, t, modDir, "--debug", "listen", "--listen", addr)
		listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")
		listenCmd.Stdout = testutil.NewTWriter(t)
		listenCmd.Stderr = testutil.NewTWriter(t)
		require.NoError(t, listenCmd.Start())

		backoff.Retry(func() error {
			c, err := net.Dial("tcp", addr)
			t.Log("dial", addr, c, err)
			if err != nil {
				return err
			}
			return c.Close()
		}, backoff.NewExponentialBackOff(
			backoff.WithMaxElapsedTime(time.Minute),
		))

		callCmd := hostDaggerCommand(ctx, t, modDir, "--debug", "call", "container-echo", "--string-arg=hi", "stdout")
		callCmd.Env = append(callCmd.Env, "DAGGER_SESSION_PORT=12456", "DAGGER_SESSION_TOKEN=lol")
		callCmd.Stderr = testutil.NewTWriter(t)
		out, err := callCmd.Output()
		require.NoError(t, err)
		lines := strings.Split(string(out), "\n")
		lastLine := lines[len(lines)-2]
		require.Equal(t, "hi", lastLine)
	})

	t.Run("disable read write", func(ctx context.Context, t *testctx.T) {
		t.Run("with mod", func(ctx context.Context, t *testctx.T) {
			// mod load fails but should still be able to query base api

			modDir := t.TempDir()
			_, err := hostDaggerExec(ctx, t, modDir, "--debug", "init", "--source=.", "--name=test", "--sdk=go")
			require.NoError(t, err)

			listenCmd := hostDaggerCommand(ctx, t, modDir, "--debug", "listen", "--disable-host-read-write", "--listen", "127.0.0.1:12457")
			listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")
			require.NoError(t, listenCmd.Start())

			var out []byte
			for range limitTicker(time.Second, 60) {
				callCmd := hostDaggerCommand(ctx, t, modDir, "--debug", "query")
				callCmd.Stdin = strings.NewReader(fmt.Sprintf(`query{container{from(address:"%s"){file(path:"/etc/alpine-release"){contents}}}}`, alpineImage))
				callCmd.Stderr = testutil.NewTWriter(t)
				callCmd.Env = append(callCmd.Env, "DAGGER_SESSION_PORT=12457", "DAGGER_SESSION_TOKEN=lol")
				out, err = callCmd.Output()
				if err == nil {
					require.Contains(t, string(out), distconsts.AlpineVersion)
					return
				}
				time.Sleep(1 * time.Second)
			}
			t.Fatalf("failed to call query: %s err: %v", string(out), err)
		})

		t.Run("without mod", func(ctx context.Context, t *testctx.T) {
			tmpdir := t.TempDir()

			listenCmd := hostDaggerCommand(ctx, t, tmpdir, "--debug", "listen", "--disable-host-read-write", "--listen", "127.0.0.1:12458")
			listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")
			require.NoError(t, listenCmd.Start())

			var out []byte
			var err error
			for range limitTicker(time.Second, 60) {
				callCmd := hostDaggerCommand(ctx, t, tmpdir, "--debug", "query")
				callCmd.Stdin = strings.NewReader(fmt.Sprintf(`query{container{from(address:"%s"){file(path:"/etc/alpine-release"){contents}}}}`, alpineImage))
				callCmd.Stderr = testutil.NewTWriter(t)
				callCmd.Env = append(callCmd.Env, "DAGGER_SESSION_PORT=12458", "DAGGER_SESSION_TOKEN=lol")
				out, err = callCmd.Output()
				if err == nil {
					require.Contains(t, string(out), distconsts.AlpineVersion)
					return
				}
				time.Sleep(1 * time.Second)
			}
			t.Fatalf("failed to call query: %s err: %v", string(out), err)
		})
	})
}

func (ModuleSuite) TestSecretNested(ctx context.Context, t *testctx.T) {
	t.Run("pass secrets between modules", func(ctx context.Context, t *testctx.T) {
		// check that we can pass valid secret objects between functions in
		// different modules

		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/secreter").
			With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/secreter/internal/dagger"
)

type Secreter struct {}

func (_ *Secreter) Make() *dagger.Secret {
	return dag.SetSecret("FOO", "inner")
}

func (_ *Secreter) Get(ctx context.Context, secret *dagger.Secret) (string, error) {
	return secret.Plaintext(ctx)
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./secreter")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) TryReturn(ctx context.Context) error {
	text, err := dag.Secreter().Make().Plaintext(ctx)
	if err != nil {
		return err
	}
	if text != "inner" {
		return fmt.Errorf("expected \"inner\", but got %q", text)
	}
	return nil
}

func (t *Toplevel) TryArg(ctx context.Context) error {
	text, err := dag.Secreter().Get(ctx, dag.SetSecret("BAR", "outer"))
	if err != nil {
		return err
	}
	if text != "outer" {
		return fmt.Errorf("expected \"outer\", but got %q", text)
	}
	return nil
}
`,
			)

		t.Run("can pass secrets", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{toplevel{tryArg}}`)).Stdout(ctx)
			require.NoError(t, err)
		})

		t.Run("can return secrets", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{toplevel{tryReturn}}`)).Stdout(ctx)
			require.NoError(t, err)
		})
	})

	t.Run("dockerfiles in modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("/input/Dockerfile", `FROM `+alpineImage+`
RUN --mount=type=secret,id=my-secret test "$(cat /run/secrets/my-secret)" = "barbar"
`).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
}

func (t *Test) Ctr(src *dagger.Directory) *dagger.Container {
	secret := dag.SetSecret("my-secret", "barbar")
	return src.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Secrets: []*dagger.Secret{secret},
		}).
		WithExec([]string{"true"}) // needed to avoid "no command set" error
}

func (t *Test) Evaluated(ctx context.Context, src *dagger.Directory) error {
	secret := dag.SetSecret("my-secret", "barbar")
	_, err := src.
		DockerBuild(dagger.DirectoryDockerBuildOpts{
			Secrets: []*dagger.Secret{secret},
		}).
		WithExec([]string{"true"}).
		Sync(ctx)
	return err
}
`)

		_, err := ctr.
			With(daggerCall("ctr", "--src", "/input", "stdout")).
			Sync(ctx)
		require.NoError(t, err)

		_, err = ctr.
			With(daggerCall("evaluated", "--src", "/input")).
			Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("pass embedded secrets between modules", func(ctx context.Context, t *testctx.T) {
		// check that we can pass valid secret objects between functions in
		// different modules when the secrets are embedded in containers rather than
		// passed directly

		t.Run("embedded in returns", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"context"
	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (*Dep) GetEncoded(ctx context.Context) *dagger.Container {
	secret := dag.SetSecret("FOO", "shhh")
	return dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"})
}

func (*Dep) GetCensored(ctx context.Context) *dagger.Container {
	secret := dag.SetSecret("BAR", "fdjsklajakldjfl")
	return dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET"})
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	return dag.Dep().GetEncoded().Stdout(ctx)
}

func (t *Test) GetCensored(ctx context.Context) (string, error) {
	return dag.Dep().GetCensored().Stdout(ctx)
}
`,
				)

			encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
			require.NoError(t, err)
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
			require.NoError(t, err)
			require.Equal(t, "shhh\n", string(decoded))

			censoredOut, err := ctr.With(daggerCall("get-censored")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "***\n", censoredOut)
		})

		t.Run("embedded in args", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"context"
	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (*Dep) Get(ctx context.Context, ctr *dagger.Container) (string, error) {
	return ctr.Stdout(ctx)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	secret := dag.SetSecret("FOO", "shhh")
	ctr := dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"})
	return dag.Dep().Get(ctx, ctr)
}

func (t *Test) GetCensored(ctx context.Context) (string, error) {
	secret := dag.SetSecret("BAR", "fdlaskfjdlsajfdkasl")
	ctr := dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret).
		WithExec([]string{"sh", "-c", "echo $SECRET"})
	return dag.Dep().Get(ctx, ctr)
}
`,
				)

			encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
			require.NoError(t, err)
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
			require.NoError(t, err)
			require.Equal(t, "shhh\n", string(decoded))

			censoredOut, err := ctr.With(daggerCall("get-censored")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "***\n", censoredOut)
		})

		t.Run("embedded through struct field", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct {}

type SecretMount struct {
	Secret *dagger.Secret
	Path string
}

func (m *Dep) SecretMount(path string) *SecretMount {
	return &SecretMount{
		Secret: dag.SetSecret("foo", "hello from foo"),
		Path:   path,
	}
}

func (m *SecretMount) Mount(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedSecret(m.Path, m.Secret)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (m *Test) Test(ctx context.Context) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", "cat /mnt/secret | tr [a-z] [A-Z]"}).
		Stdout(ctx)
}
`,
				)

			out, err := ctr.With(daggerCall("test")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "HELLO FROM FOO", out)
		})

		t.Run("embedded through private struct field", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct {}

type SecretMount struct {
	// +private
	Secret *dagger.Secret
	// +private
	Path string
}

func (m *Dep) SecretMount(path string) *SecretMount {
	return &SecretMount{
		Secret: dag.SetSecret("foo", "hello from foo"),
		Path:   path,
	}
}

func (m *SecretMount) Mount(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedSecret(m.Path, m.Secret)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (m *Test) Test(ctx context.Context) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", "cat /mnt/secret | tr [a-z] [A-Z]"}).
		Stdout(ctx)
}
`,
				)

			out, err := ctr.With(daggerCall("test")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "HELLO FROM FOO", out)
		})

		t.Run("cached", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			ctr = ctr.
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct {}

type SecretMount struct {
	Secret *dagger.Secret
	Path string
}

func (m *Dep) SecretMount(path string) *SecretMount {
	return &SecretMount{
		Secret: dag.SetSecret("foo", "hello from mount"),
		Path:   path,
	}
}

func (m *SecretMount) Mount(ctr *dagger.Container) *dagger.Container {
	return ctr.WithMountedSecret(m.Path, m.Secret)
}
`,
				)

			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./dep")).
				WithNewFile("main.go", `package main

import (
	"context"
  "fmt"
)

type Test struct {}

func (m *Test) Foo(ctx context.Context) (string, error) {
  return m.impl(ctx, "foo")
}

func (m *Test) Bar(ctx context.Context) (string, error) {
  return m.impl(ctx, "bar")
}

func (m *Test) impl(ctx context.Context, name string) (string, error) {
	mount := dag.Dep().SecretMount("/mnt/secret")
	return dag.Container().
		From("alpine").
		With(mount.Mount).
		WithExec([]string{"sh", "-c", fmt.Sprintf("(echo %s && cat /mnt/secret) | tr [a-z] [A-Z]", name)}).
		Stdout(ctx)
}
`,
				)

			out, err := ctr.With(daggerQuery("{test{foo,bar}}")).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test": {"foo": "FOO\nHELLO FROM MOUNT", "bar": "BAR\nHELLO FROM MOUNT"}}`, out)
		})
	})

	t.Run("parent fields", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
	Ctr *dagger.Container
}

func (t *Test) FnA() *Test {
	secret := dag.SetSecret("FOO", "omg")
	t.Ctr = dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret)
	return t
}

func (t *Test) FnB(ctx context.Context) (string, error) {
	return t.Ctr.
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"}).
		Stdout(ctx)
}
`,
			)

		encodedOut, err := ctr.With(daggerCall("fn-a", "fn-b")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omg\n", string(decoded))
	})

	t.Run("private parent fields", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
	// +private
	Ctr *dagger.Container
}

func (t *Test) FnA() *Test {
	secret := dag.SetSecret("FOO", "omg")
	t.Ctr = dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret)
	return t
}

func (t *Test) FnB(ctx context.Context) (string, error) {
	return t.Ctr.
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"}).
		Stdout(ctx)
}
`,
			)

		encodedOut, err := ctr.With(daggerCall("fn-a", "fn-b")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omg\n", string(decoded))
	})

	t.Run("parent field set in constructor", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
	Ctr *dagger.Container
}

func New() *Test {
	t := &Test{}
	secret := dag.SetSecret("FOO", "omfg")
	t.Ctr = dag.Container().From("`+alpineImage+`").
		WithSecretVariable("SECRET", secret)
	return t
}

func (t *Test) GetEncoded(ctx context.Context) (string, error) {
	return t.Ctr.
		WithExec([]string{"sh", "-c", "echo $SECRET | base64"}).
		Stdout(ctx)
}
`,
			)

		encodedOut, err := ctr.With(daggerCall("get-encoded")).Stdout(ctx)
		require.NoError(t, err)
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedOut))
		require.NoError(t, err)
		require.Equal(t, "omfg\n", string(decoded))
	})

	t.Run("duplicate secret names", func(ctx context.Context, t *testctx.T) {
		// check that each module has it's own segmented secret store, by
		// writing secrets with the same name

		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/maker").
			With(daggerExec("init", "--name=maker", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/maker/internal/dagger"
)

type Maker struct {}

func (_ *Maker) MakeSecret(ctx context.Context) (*dagger.Secret, error) {
	secret := dag.SetSecret("FOO", "inner")
	_, err := secret.ID(ctx)  // force the secret into the store
	if err != nil {
		return nil, err
	}
	return secret, nil
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./maker")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) Attempt(ctx context.Context) error {
	secret := dag.SetSecret("FOO", "outer")
	_, err := secret.ID(ctx)  // force the secret into the store
	if err != nil {
		return err
	}

	// this creates an inner secret "FOO", but it mustn't overwrite the outer one
	secret2 := dag.Maker().MakeSecret()

	plaintext, err := secret.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "outer" {
		return fmt.Errorf("expected \"outer\", but got %q", plaintext)
	}

	plaintext, err = secret2.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "inner" {
		return fmt.Errorf("expected \"inner\", but got %q", plaintext)
	}

	return nil
}
`,
			)

		_, err := ctr.With(daggerQuery(`{toplevel{attempt}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("separate secret stores", func(ctx context.Context, t *testctx.T) {
		// check that modules can't access each other's global secret stores,
		// by attempting to leak from each other

		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/leaker").
			With(daggerExec("init", "--name=leaker", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Leaker struct {}

func (l *Leaker) Leak(ctx context.Context) error {
	secret, _ := dag.Secret("mysecret").Plaintext(ctx)
	fmt.Println("trying to read secret:", secret)
	return nil
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel/leaker-build").
			With(daggerExec("init", "--name=leaker-build", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
	"strings"
)

type LeakerBuild struct {}

func (l *LeakerBuild) Leak(ctx context.Context) error {
	_, err := dag.Directory().
		WithNewFile("Dockerfile", "FROM alpine\nRUN --mount=type=secret,id=mysecret cat /run/secrets/mysecret || true").
		DockerBuild().
		Sync(ctx)
	if err == nil {
		return fmt.Errorf("expected error, but got nil")
	}
	if !strings.Contains(err.Error(), "secret not found: mysecret") {
		return fmt.Errorf("unexpected error: %v", err)
	}
	return nil
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./leaker")).
			With(daggerExec("install", "./leaker-build")).
			WithNewFile("main.go", `package main

import "context"

type Toplevel struct {}

func (t *Toplevel) Attempt(ctx context.Context, uniq string) error {
	// get the id of a secret to force the engine to eval it
	_, err := dag.SetSecret("mysecret", "asdf" + "asdf").ID(ctx)
	if err != nil {
		return err
	}
	err = dag.Leaker().Leak(ctx)
	if err != nil {
		return err
	}
	err = dag.LeakerBuild().Leak(ctx)
	if err != nil {
		return err
	}
	return nil
}
`,
			)

		_, err := ctr.With(daggerQuery(`{toplevel{attempt(uniq: %q)}}`, identity.NewID())).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
		require.NotContains(t, logs.String(), "asdfasdf")
	})

	t.Run("secret by id leak", func(ctx context.Context, t *testctx.T) {
		// check that modules can't access each other's global secret stores,
		// even when we know the underlying IDs

		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/leaker").
			With(daggerExec("init", "--name=leaker", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import (
	"context"

	"dagger/leaker/internal/dagger"
)

type Leaker struct {}

func (l *Leaker) Leak(ctx context.Context, target string) string {
	secret, _ := dag.LoadSecretFromID(dagger.SecretID(target)).Plaintext(ctx)
	return secret
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./leaker")).
			WithNewFile("main.go", `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) Attempt(ctx context.Context, uniq string) error {
	secretID, err := dag.SetSecret("mysecret", "asdfasdf").ID(ctx)
	if err != nil {
		return err
	}

	// loading secret-by-id in the same module should succeed
	plaintext, err := dag.LoadSecretFromID(secretID).Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "asdfasdf" {
		return fmt.Errorf("expected \"asdfasdf\", but got %q", plaintext)
	}

	// but getting a leaker module to do this should fail
	plaintext, err = dag.Leaker().Leak(ctx, string(secretID))
	if err != nil {
		return err
	}
	if plaintext != "" {
		return fmt.Errorf("expected \"\", but got %q", plaintext)
	}

	return nil
}
`,
			)

		_, err := ctr.With(daggerQuery(`{toplevel{attempt(uniq: %q)}}`, identity.NewID())).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("secrets cache normally", func(ctx context.Context, t *testctx.T) {
		// check that secrets cache as they would without nested modules,
		// which is essentially dependent on whether they have stable IDs

		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/secreter").
			With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
			WithNewFile("main.go", `package main

import "dagger/secreter/internal/dagger"

type Secreter struct {}

func (_ *Secreter) Make(uniq string) *dagger.Secret {
	return dag.SetSecret("MY_SECRET", uniq)
}
`,
			)

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./secreter")).
			WithNewFile("main.go", fmt.Sprintf(`package main

import (
	"context"
	"fmt"
	"dagger/toplevel/internal/dagger"
)

type Toplevel struct {}

func (_ *Toplevel) AttemptInternal(ctx context.Context) error {
	return diffSecret(
		ctx,
		dag.SetSecret("MY_SECRET", "foo"),
		dag.SetSecret("MY_SECRET", "bar"),
	)
}

func (_ *Toplevel) AttemptExternal(ctx context.Context) error {
	return diffSecret(
		ctx,
		dag.Secreter().Make("foo"),
		dag.Secreter().Make("bar"),
	)
}

func diffSecret(ctx context.Context, first, second *dagger.Secret) error {
	firstOut, err := dag.Container().
		From("%[1]s").
		WithSecretVariable("VAR", first).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	secondOut, err := dag.Container().
		From("%[1]s").
		WithSecretVariable("VAR", second).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	if firstOut != secondOut {
		return fmt.Errorf("%%q != %%q", firstOut, secondOut)
	}
	return nil
}
`, alpineImage),
			)

		t.Run("internal secrets cache", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{toplevel{attemptInternal}}`)).Stdout(ctx)
			require.NoError(t, err)
		})

		t.Run("external secrets cache", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerQuery(`{toplevel{attemptExternal}}`)).Stdout(ctx)
			require.NoError(t, err)
		})
	})
}

func (ModuleSuite) TestUnicodePath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/wórk/sub/").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/wórk/sub/main.go", `package main
 			import (
 				"context"
 			)
 			type Test struct {}
 			func (m *Test) Hello(ctx context.Context) string {
				return "hello"
 			}
 			`,
		).
		With(daggerQuery(`{test{hello}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"hello":"hello"}}`, out)
}

func (ModuleSuite) TestStartServices(ctx context.Context, t *testctx.T) {
	// regression test for https://github.com/dagger/dagger/pull/6914
	t.Run("use service in multiple functions", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", fmt.Sprintf(`package main

	import (
		"context"
		"fmt"
		"dagger/test/internal/dagger"
	)

	type Test struct {
	}

	func (m *Test) FnA(ctx context.Context) (*Sub, error) {
		svc := dag.Container().
			From("python").
			WithMountedDirectory(
				"/srv/www",
				dag.Directory().WithNewFile("index.html", "hey there"),
			).
			WithWorkdir("/srv/www").
			WithExposedPort(23457).
			WithExec([]string{"python", "-m", "http.server", "23457"}).
			AsService()

		ctr := dag.Container().
			From("%s").
			WithServiceBinding("svc", svc).
			WithExec([]string{"wget", "-O", "-", "http://svc:23457"})

		out, err := ctr.Stdout(ctx)
		if err != nil {
			return nil, err
		}
		if out != "hey there" {
			return nil, fmt.Errorf("unexpected output: %%q", out)
		}
		return &Sub{Ctr: ctr}, nil
	}

	type Sub struct {
		Ctr *dagger.Container
	}

	func (m *Sub) FnB(ctx context.Context) (string, error) {
		return m.Ctr.
			WithExec([]string{"wget", "-O", "-", "http://svc:23457"}).
			Stdout(ctx)
	}
	`, alpineImage),
			).
			With(daggerCall("fn-a", "fn-b")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey there", strings.TrimSpace(out))
	})

	// regression test for https://github.com/dagger/dagger/issues/6951
	t.Run("service in multiple containers", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", fmt.Sprintf(`package main
import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {
}

func (m *Test) Fn(ctx context.Context) *dagger.Container {
	redis := dag.Container().
		From("redis").
		WithExposedPort(6379).
		AsService()
	cli := dag.Container().
		From("redis").
		WithoutEntrypoint().
		WithServiceBinding("redis", redis)

	ctrA := cli.WithExec([]string{"sh", "-c", "redis-cli -h redis info >> /tmp/out.txt"})

	file := ctrA.Directory("/tmp").File("/out.txt")

	ctrB := dag.Container().
		From("%s").
		WithFile("/out.txt", file)

	return ctrB.WithExec([]string{"cat", "/out.txt"})
}
	`, alpineImage),
			).
			With(daggerCall("fn", "stdout")).
			Sync(ctx)
		require.NoError(t, err)
	})
}

// regression test for https://github.com/dagger/dagger/issues/7334
// and https://github.com/dagger/dagger/pull/7336
func (ModuleSuite) TestCallSameModuleInParallel(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--name=dep", "--sdk=go")).
		With(sdkSource("go", `package main

import (
	"github.com/moby/buildkit/identity"
	"dagger/dep/internal/dagger"
)

type Dep struct {}

func (m *Dep) DepFn(s *dagger.Secret) string {
	return identity.NewID()
}
`)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go")).
		With(sdkSource("go", `package main

import (
	"context"
	"golang.org/x/sync/errgroup"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context) ([]string, error) {
	var eg errgroup.Group
	results := make([]string, 10)
	for i := 0; i < 10; i++ {
		i := i
		eg.Go(func() error {
			res, err := dag.Dep().DepFn(ctx, dag.SetSecret("foo", "bar"))
			if err != nil {
				return err
			}
			results[i] = res
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}
`)).
		With(daggerExec("install", "./dep")).
		With(daggerCall("fn"))

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	results := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, results, 10)
	expectedRes := results[0]
	for _, res := range results {
		require.Equal(t, expectedRes, res)
	}
}

func (ModuleSuite) TestLargeObjectFieldVal(ctx context.Context, t *testctx.T) {
	// make sure we don't hit any limits when an object field value is large

	c := connect(ctx, t)

	// put a timeout on this since failures modes could result in hangs
	t = t.WithTimeout(60 * time.Second)

	_, err := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go")).
		With(sdkSource("go", `package main

import "strings"

type Test struct {
	BigVal string
}

func New() *Test {
	return &Test{
		BigVal: strings.Repeat("a", 30*1024*1024),
	}
}

// add a func for returning the val in order to test mode codepaths that
// involve serializing and passing the object around
func (m *Test) Fn() string {
	return m.BigVal
}
`)).
		With(daggerCall("fn")).
		Sync(ctx)
	require.NoError(t, err)
}

func (ModuleSuite) TestReturnNilField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go")).
		With(sdkSource("go", `package main

type Test struct {
	A *Thing
	B *Thing
}

type Thing struct{}

func New() *Test {
	return &Test{
		A: &Thing{},
	}
}

func (m *Test) Hello() string {
	return "Hello"
}

`)).
		With(daggerCall("hello")).
		Sync(ctx)
	require.NoError(t, err)
}

func (ModuleSuite) TestGetEmptyField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("without constructor", func(ctx context.Context, t *testctx.T) {
		out, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go")).
			With(sdkSource("go", `package main

import "dagger/test/internal/dagger"

type Test struct {
	A string
	B int
	C *dagger.Container
	D dagger.ImageLayerCompression
	E dagger.Platform
}

`)).
			With(daggerQuery("{test{a,b}}")).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"test": {"a": "", "b": 0}}`, out)
		// NOTE:
		// - trying to get C will try and decode an empty ID
		// - trying to get D will fail to instantiate an empty enum
		// - trying to get E will fail to parse the platform
		// ...but, we should be able to get the other values (important for backwards-compat)
	})

	t.Run("with constructor", func(ctx context.Context, t *testctx.T) {
		out, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go")).
			With(sdkSource("go", `package main

import "dagger/test/internal/dagger"

type Test struct {
	A string
	B int
	C *dagger.Container
	// these aren't tested here, since we can't give them zero values in the constructor
	// D dagger.ImageLayerCompression
	// E dagger.Platform
}

func New() *Test {
	return &Test{}
}
`)).
			With(daggerQuery("{test{a,b}}")).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"test": {"a": "", "b": 0}}`, out)
		// NOTE:
		// - trying to get C will try and decode an empty ID
		// ...but, we should be able to get the other values (important for backwards-compat)
	})
}

func (ModuleSuite) TestModuleSchemaVersion(ctx context.Context, t *testctx.T) {
	t.Run("standalone", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)

		require.NotEmpty(t, gjson.Get(out, "__schemaVersion").String())
	})

	t.Run("standalone explicit", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v2.0.0").
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"__schemaVersion":"v2.0.0"}`, out)
	})

	t.Run("standalone explicit dev", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_VERSION", "v2.0.0-dev-123").
			WithWorkdir("/work")
		out, err := work.
			With(daggerQuery("{__schemaVersion}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"__schemaVersion":"v2.0.0"}`, out)
	})

	t.Run("module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=foo", "--sdk=go", "--source=.")).
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.11.0"}`).
			WithNewFile("main.go", `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Foo struct {}

func (m *Foo) GetVersion(ctx context.Context) (string, error) {
	return schemaVersion(ctx)
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}
`,
			)
		out, err := work.
			With(daggerQuery("{foo{getVersion}}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"foo":{"getVersion": "v0.11.0"}}`, out)

		out, err = work.
			With(daggerCall("get-version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "v0.11.0")
	})

	t.Run("module deps", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
			WithNewFile("dagger.json", `{"name": "dep", "sdk": "go", "source": ".", "engineVersion": "v0.11.0"}`).
			WithNewFile("main.go", `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Dep struct {}

func (m *Dep) GetVersion(ctx context.Context) (string, error) {
	return schemaVersion(ctx)
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}
`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=foo", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./dep")).
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.10.0", "dependencies": [{"name": "dep", "source": "dep"}]}`).
			WithNewFile("main.go", `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Foo struct {}

func (m *Foo) GetVersion(ctx context.Context) (string, error) {
	myVersion, err := schemaVersion(ctx)
	if err != nil {
		return "", err
	}
	depVersion, err := dag.Dep().GetVersion(ctx)
	if err != nil {
		return "", err
	}
	return myVersion + " " + depVersion, nil
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}
`,
			)

		out, err := work.
			With(daggerQuery("{foo{getVersion}}")).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"foo":{"getVersion": "v0.10.0 v0.11.0"}}`, out)

		out, err = work.
			With(daggerCall("get-version")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "v0.10.0 v0.11.0")
	})
}

func (ModuleSuite) TestContextDirectory(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	t.Run("load context inside git repo with module in a sub dir", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
  "context"
	"dagger/test/internal/dagger"
)

type Test struct {}

func (t *Test) Dirs(
  ctx context.Context,

  // +defaultPath="/"
  root *dagger.Directory,

  // +defaultPath="."
  relativeRoot *dagger.Directory,
) ([]string, error) {
  res, err := root.Entries(ctx)
  if err != nil {
    return nil, err
  }
  relativeRes, err := relativeRoot.Entries(ctx)
  if err != nil {
    return nil, err
  }
  return append(res, relativeRes...), nil
}


func (t *Test) DirsIgnore(
  ctx context.Context,

  // +defaultPath="/"
  // +ignore=["**", "!backend", "!frontend"]
  root *dagger.Directory,

  // +defaultPath="."
  // +ignore=["dagger.json", "LICENSE"]
  relativeRoot *dagger.Directory,
) ([]string, error) {
  res, err := root.Entries(ctx)
  if err != nil {
    return nil, err
  }
  relativeRes, err := relativeRoot.Entries(ctx)
  if err != nil {
    return nil, err
  }
  return append(res, relativeRes...), nil
}

func (t *Test) RootDirPath(
  ctx context.Context,

  // +defaultPath="/backend"
  backend *dagger.Directory,

  // +defaultPath="/frontend"
  frontend *dagger.Directory,

  // +defaultPath="/ci/dagger/sub"
  modSrcDir *dagger.Directory,
) ([]string, error) {
  backendFiles, err := backend.Entries(ctx)
  if err != nil {
    return nil, err
  }
  frontendFiles, err := frontend.Entries(ctx)
  if err != nil {
    return nil, err
  }
  modSrcDirFiles, err := modSrcDir.Entries(ctx)
  if err != nil {
    return nil, err
  }

	res := append(backendFiles, append(frontendFiles, modSrcDirFiles...)...)

  return res, nil
}

func (t *Test) RelativeDirPath(
  ctx context.Context,

  // +defaultPath="./dagger/sub"
  modSrcDir *dagger.Directory,

  // +defaultPath="../backend"
  backend *dagger.Directory,
) ([]string, error) {
  modSrcDirFiles, err := modSrcDir.Entries(ctx)
  if err != nil {
    return nil, err
  }
  backendFiles, err := backend.Entries(ctx)
  if err != nil {
    return nil, err
  }

  return append(modSrcDirFiles, backendFiles...), nil
}

func (t *Test) Files(
  ctx context.Context,

  // +defaultPath="/ci/LICENSE"
  license *dagger.File,

  // +defaultPath="./dagger/sub/sub.txt"
  index *dagger.File,
) ([]string, error) {
  licenseName, err := license.Name(ctx)
  if err != nil {
    return nil, err
  }
  indexName, err := index.Name(ctx)
  if err != nil {
    return nil, err
  }

  return []string{licenseName, indexName}, nil
}
`,
			},
			{
				sdk: "python",
				source: `from typing import Annotated

import dagger
from dagger import DefaultPath, Ignore, function, object_type


@object_type
class Test:
    @function
    async def dirs(
        self,
        root: Annotated[dagger.Directory, DefaultPath("/")],
        relativeRoot: Annotated[dagger.Directory, DefaultPath(".")],
    ) -> list[str]:
        return [
            *(await root.entries()),
            *(await relativeRoot.entries()),
       ]

    @function
    async def dirs_ignore(
        self,
        root: Annotated[dagger.Directory, DefaultPath("/"), Ignore(["**","!backend", "!frontend"])],
        relativeRoot: Annotated[dagger.Directory, DefaultPath("."), Ignore(["dagger.json", "LICENSE"])],
    ) -> list[str]:
        return [
            *(await root.entries()),
            *(await relativeRoot.entries()),
        ]

    @function
    async def root_dir_path(
        self,
        backend: Annotated[dagger.Directory, DefaultPath("/backend")],
        frontend: Annotated[dagger.Directory, DefaultPath("/frontend")],
        mod_src_dir: Annotated[dagger.Directory, DefaultPath("/ci/dagger/sub")],
    ) -> list[str]:
        return [
            *(await backend.entries()),
            *(await frontend.entries()),
            *(await mod_src_dir.entries()),
        ]

    @function
    async def relative_dir_path(
        self,
        mod_src_dir: Annotated[dagger.Directory, DefaultPath("./dagger/sub")],
        backend: Annotated[dagger.Directory, DefaultPath("../backend")],
    ) -> list[str]:
        return [
            *(await mod_src_dir.entries()),
            *(await backend.entries()),
        ]

    @function
    async def files(
        self,
        license: Annotated[dagger.File, DefaultPath("/ci/LICENSE")],
        index: Annotated[dagger.File, DefaultPath("./dagger/sub/sub.txt")],
    ) -> list[str]:
        return [
            await license.name(),
            await index.name(),
        ]
`,
			},
			{
				sdk: "typescript",
				source: `import { Directory, File, object, func, argument } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  async dirs(@argument({ defaultPath: "/" }) root: Directory, @argument({ defaultPath: "."}) relativeRoot: Directory): Promise<string[]> {
    const res = await root.entries()
    const relativeRes = await relativeRoot.entries()

    return [...res, ...relativeRes]
  }

  @func()
  async dirsIgnore(
    @argument({ defaultPath: "/", ignore: ["**", "!backend", "!frontend"] }) root: Directory,
    @argument({ defaultPath: ".", ignore: ["dagger.json", "LICENSE"] }) relativeRoot: Directory,
  ): Promise<string[]> {
    const res = await root.entries();
    const relativeRes = await relativeRoot.entries();

    return [...res, ...relativeRes];
  }

  @func()
  async rootDirPath(
    @argument({ defaultPath: "/backend" }) backend: Directory,
    @argument({ defaultPath: "/frontend" }) frontend: Directory,
    @argument({ defaultPath: "/ci/dagger/sub" }) modSrcDir: Directory,
  ): Promise<string[]> {
    const backendFiles = await backend.entries()
    const frontendFiles = await frontend.entries()
    const modSrcDirFiles = await modSrcDir.entries()

    return [...backendFiles, ...frontendFiles, ...modSrcDirFiles]
  }

  @func()
  async relativeDirPath(
    @argument({ defaultPath: "./dagger/sub" }) modSrcDir: Directory,
    @argument({ defaultPath: "../backend" }) backend: Directory,
  ): Promise<string[]> {
    const modSrcDirFiles = await modSrcDir.entries()
    const backendFiles = await backend.entries()

    return [...modSrcDirFiles, ...backendFiles]
  }

  @func()
  async files(
    @argument({ defaultPath: "/ci/LICENSE" }) license: File,
    @argument({ defaultPath: "./dagger/sub/sub.txt" }) index: File,
  ): Promise<string[]> {
    return [await license.name(), await index.name()]
  }
}
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := goGitBase(t, c).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					WithDirectory("/work/backend", c.Directory().WithNewFile("foo.txt", "foo")).
					WithDirectory("/work/frontend", c.Directory().WithNewFile("bar.txt", "bar")).
					WithWorkdir("/work/ci").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=dagger")).
					WithWorkdir("/work/ci/dagger").
					With(sdkSource(tc.sdk, tc.source)).
					WithDirectory("/work/ci/dagger/sub", c.Directory().WithNewFile("sub.txt", "sub")).
					WithWorkdir("/work")

				t.Run("absolute and relative root context dir", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCallAt("ci", "dirs")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, ".git\nbackend\nci\nfrontend\nLICENSE\ndagger\ndagger.json\n", out)
				})

				t.Run("dir ignore", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCallAt("ci", "dirs-ignore")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "backend\nfrontend\ndagger\n", out)
				})

				t.Run("absolute context dir subpath", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCallAt("ci", "root-dir-path")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "foo.txt\nbar.txt\nsub.txt\n", out)
				})

				t.Run("relative context dir subpath", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCallAt("ci", "relative-dir-path")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "sub.txt\nfoo.txt\n", out)
				})

				t.Run("files", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCallAt("ci", "files")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "LICENSE\nsub.txt\n", out)
				})
			})
		}
	})

	t.Run("load context inside git repo with module at the root of the repo", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
  "context"
	"dagger/test/internal/dagger"
)

type Test struct {}

func (t *Test) Dirs(
  ctx context.Context,

  // +defaultPath="/"
  root *dagger.Directory,

  // +defaultPath="."
  relativeRoot *dagger.Directory,
) ([]string, error) {
  res, err := root.Entries(ctx)
  if err != nil {
    return nil, err
  }
  relativeRes, err := relativeRoot.Entries(ctx)
  if err != nil {
    return nil, err
  }
  return append(res, relativeRes...), nil
}


func (t *Test) RootDirPath(
  ctx context.Context,

  // +defaultPath="/backend"
  backend *dagger.Directory,

  // +defaultPath="/frontend"
  frontend *dagger.Directory,

  // +defaultPath="/dagger/sub"
  modSrcDir *dagger.Directory,
) ([]string, error) {
  backendFiles, err := backend.Entries(ctx)
  if err != nil {
    return nil, err
  }
  frontendFiles, err := frontend.Entries(ctx)
  if err != nil {
    return nil, err
  }
  modSrcDirFiles, err := modSrcDir.Entries(ctx)
  if err != nil {
    return nil, err
  }

	res := append(backendFiles, append(frontendFiles, modSrcDirFiles...)...)

  return res, nil
}

func (t *Test) RelativeDirPath(
  ctx context.Context,

  // +defaultPath="./dagger/sub"
  modSrcDir *dagger.Directory,

  // +defaultPath="./backend"
  backend *dagger.Directory,
) ([]string, error) {
  modSrcDirFiles, err := modSrcDir.Entries(ctx)
  if err != nil {
    return nil, err
  }
  backendFiles, err := backend.Entries(ctx)
  if err != nil {
    return nil, err
  }

  return append(modSrcDirFiles, backendFiles...), nil
}

func (t *Test) Files(
  ctx context.Context,

  // +defaultPath="/LICENSE"
  license *dagger.File,

  // +defaultPath="./dagger.json"
  index *dagger.File,
) ([]string, error) {
  licenseName, err := license.Name(ctx)
  if err != nil {
    return nil, err
  }
  indexName, err := index.Name(ctx)
  if err != nil {
    return nil, err
  }

  return []string{licenseName, indexName}, nil
}
`,
			},
			{
				sdk: "python",
				source: `from typing import Annotated

import dagger
from dagger import DefaultPath, function, object_type

@object_type
class Test:
    @function
    async def dirs(
        self,
        root: Annotated[dagger.Directory, DefaultPath("/")],
        relative_root: Annotated[dagger.Directory, DefaultPath(".")],
    ) -> list[str]:
        return [
            *(await root.entries()),
            *(await relative_root.entries()),
        ]

    @function
    async def root_dir_path(
        self,
        backend: Annotated[dagger.Directory, DefaultPath("/backend")],
        frontend: Annotated[dagger.Directory, DefaultPath("/frontend")],
        mod_src_dir: Annotated[dagger.Directory, DefaultPath("/dagger/sub")],
    ) -> list[str]:
        return [
            *(await backend.entries()),
            *(await frontend.entries()),
            *(await mod_src_dir.entries()),
        ]

    @function
    async def relative_dir_path(
        self,
        mod_src_dir: Annotated[dagger.Directory, DefaultPath("./dagger/sub")],
        backend: Annotated[dagger.Directory, DefaultPath("./backend")],
    ) -> list[str]:
        return [
            *(await mod_src_dir.entries()),
            *(await backend.entries()),
        ]

    @function
    async def files(
        self,
        license: Annotated[dagger.File, DefaultPath("/LICENSE")],
        index: Annotated[dagger.File, DefaultPath("./dagger.json")],
    ) -> list[str]:
        return [
            await license.name(),
            await index.name(),
        ]
`,
			},
			{
				sdk: "typescript",
				source: `import { Directory, File, object, func, argument } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  async dirs(
    @argument({ defaultPath: "/" }) root: Directory,
    @argument({ defaultPath: "." }) relativeRoot: Directory,
  ): Promise<string[]> {
    const res = await root.entries()
    const relativeRes = await relativeRoot.entries()

    return [...res, ...relativeRes]
  }

  @func()
  async rootDirPath(
    @argument({ defaultPath: "/backend" }) backend: Directory,
    @argument({ defaultPath: "/frontend" }) frontend: Directory,
    @argument({ defaultPath: "/dagger/sub" }) modSrcDir: Directory,
  ): Promise<string[]> {
    const backendFiles = await backend.entries()
    const frontendFiles = await frontend.entries()
    const modSrcDirFiles = await modSrcDir.entries()

    return [...backendFiles, ...frontendFiles, ...modSrcDirFiles]
  }

  @func()
  async relativeDirPath(
    @argument({ defaultPath: "./dagger/sub" }) modSrcDir: Directory,
    @argument({ defaultPath: "./backend" }) backend: Directory,
  ): Promise<string[]> {
    const modSrcDirFiles = await modSrcDir.entries()
    const backendFiles = await backend.entries()

    return [...modSrcDirFiles, ...backendFiles]
  }

  @func()
  async files(
    @argument({ defaultPath: "/LICENSE" }) license: File,
  	@argument({ defaultPath: "./dagger.json" }) daggerConfig: File,
	): Promise<string[]> {
    return [await license.name(), await daggerConfig.name()]
  }
}
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := goGitBase(t, c).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					WithDirectory("/work/backend", c.Directory().WithNewFile("foo.txt", "foo")).
					WithDirectory("/work/frontend", c.Directory().WithNewFile("bar.txt", "bar")).
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=dagger")).
					WithDirectory("/work/dagger/sub", c.Directory().WithNewFile("sub.txt", "sub")).
					WithWorkdir("/work/dagger").
					With(sdkSource(tc.sdk, tc.source)).
					WithWorkdir("/work")

				t.Run("absolute and relative root context dir", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("dirs")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, ".git\nLICENSE\nbackend\ndagger\ndagger.json\nfrontend\n.git\nLICENSE\nbackend\ndagger\ndagger.json\nfrontend\n", out)
				})

				t.Run("absolute context dir subpath", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("root-dir-path")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "foo.txt\nbar.txt\nsub.txt\n", out)
				})

				t.Run("relative context dir subpath", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("relative-dir-path")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "sub.txt\nfoo.txt\n", out)
				})

				t.Run("files", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("files")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "LICENSE\ndagger.json\n", out)
				})
			})
		}
	})

	t.Run("load directory and files with invalid context path value", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {}

func (t *Test) TooHighRelativeDirPath(
	ctx context.Context,

	// +defaultPath="../../../"
	backend *dagger.Directory,
) ([]string, error) {
  // The engine should throw an error
	return []string{}, nil
}

func (t *Test) NonExistingPath(
	ctx context.Context,

	// +defaultPath="/invalid"
	dir *dagger.Directory,
) ([]string, error) {
  // The engine should throw an error
	return []string{}, nil
}

func (t *Test) TooHighRelativeFilePath(
	ctx context.Context,

	// +defaultPath="../../../file.txt"
	backend *dagger.File,
) (string, error) {
  // The engine should throw an error
	return "", nil
}

func (t *Test) NonExistingFile(
	ctx context.Context,

	// +defaultPath="/invalid"
	file *dagger.File,
) (string, error) {
  // The engine should throw an error
	return "", nil
}
`,
			},
			{
				sdk: "python",
				source: `from typing import Annotated

import dagger
from dagger import DefaultPath, function, object_type

@object_type
class Test:
    @function
    async def too_high_relative_dir_path(
        self,
        backend: Annotated[dagger.Directory, DefaultPath("../../../")],
    ) -> list[str]:
        # The engine should throw an error
        return []

    @function
    async def non_existing_path(
        self,
        dir: Annotated[dagger.Directory, DefaultPath("/invalid")],
    ) -> list[str]:
        # The engine should throw an error
        return []

    @function
    async def too_high_relative_file_path(
        self,
        backend: Annotated[dagger.File, DefaultPath("../../../file.txt")],
    ) -> str:
        # The engine should throw an error
        return ""

    @function
    async def non_existing_file(
        self,
        file: Annotated[dagger.File, DefaultPath("/invalid")],
    ) -> str:
        # The engine should throw an error
        return ""
`,
			},
			{
				sdk: "typescript",
				source: `import { Directory, File,object, func, argument } from "@dagger.io/dagger"
@object()
export class Test {
  @func()
  async tooHighRelativeDirPath(@argument({ defaultPath: "../../../" }) backend: Directory): Promise<string[]> {
    // The engine should throw an error
    return []
  }

  @func()
	async nonExistingPath(@argument({ defaultPath: "/invalid" }) dir: Directory): Promise<string[]> {
    // The engine should throw an error
    return []
  }

  @func()
	async tooHighRelativeFilePath(@argument({ defaultPath: "../../../file.txt" }) backend: File): Promise<string> {
    // The engine should throw an error
    return ""
  }

	@func() nonExistingFile(@argument({ defaultPath: "/invalid" }) file: File): Promise<string> {
    // The engine should throw an error
    return ""
  }
}
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := goGitBase(t, c).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=dagger")).
					WithWorkdir("/work/dagger").
					With(sdkSource(tc.sdk, tc.source)).
					WithWorkdir("/work")

				t.Run("too high relative context dir path", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("too-high-relative-dir-path")).Stdout(ctx)
					require.Empty(t, out)
					require.Error(t, err)
					require.ErrorContains(t, err, `path should be relative to the context directory`)
				})

				t.Run("too high relative context file path", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("too-high-relative-file-path")).Stdout(ctx)
					require.Empty(t, out)
					require.Error(t, err)
					require.ErrorContains(t, err, `path should be relative to the context directory`)
				})

				t.Run("non existing dir path", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("non-existing-path")).Stdout(ctx)
					require.Empty(t, out)
					require.Error(t, err)
					require.ErrorContains(t, err, "no such file or directory")
				})

				t.Run("non existing file", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("non-existing-file")).Stdout(ctx)
					require.Empty(t, out)
					require.Error(t, err)
					require.ErrorContains(t, err, "no such file or directory")
				})
			})
		}
	})

	t.Run("deps", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)

type Dep struct{}

func (m *Dep) GetSource(
	// +defaultPath="/dep"
	// +ignore=["**", "!yo"]
	source *dagger.Directory,
) *dagger.Directory {
	return source
}

func (m *Dep) GetRelSource(
  // +defaultPath="."
	// +ignore=["**", "!yo"]
	source *dagger.Directory,
) *dagger.Directory {
  return source 
}
`,
			).
			WithNewFile("yo", "yo")

		out, err := ctr.With(daggerCall("get-source", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)

		out, err = ctr.With(daggerCall("get-rel-source", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", "./dep")).
			WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) GetDepSource() *dagger.Directory {
	return dag.Dep().GetSource()
}

func (m *Test) GetRelDepSource() *dagger.Directory {
	return dag.Dep().GetRelSource()
}
`,
			)

		out, err = ctr.With(daggerCall("get-dep-source", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)

		out, err = ctr.With(daggerCall("get-rel-dep-source", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)
	})

	t.Run("as module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"dagger/dep/internal/dagger"
)
		
type Dep struct{}
		
func (m *Dep) GetSource(
	// +defaultPath="/dep"
	// +ignore=["**", "!yo"]
	source *dagger.Directory,
) *dagger.Directory {
	return source
}

func (m *Dep) GetRelSource(
	// +defaultPath="./dep"
	// +ignore=["**","!yo"]
	source *dagger.Directory,
) *dagger.Directory {
	return source 
}
		`).
			WithNewFile("yo", "yo")

		ctr = ctr.
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"

	"dagger/test/internal/dagger"
	"github.com/Khan/genqlient/graphql"
)
			
type Test struct{}
			
func (m *Test) GetDepSource(ctx context.Context, src *dagger.Directory) (*dagger.Directory, error) {
	err := src.AsModule().Initialize().Serve(ctx)
	if err != nil {
		return nil, err
	}

	type DirectoryIDRes struct {
		Dep struct {
			GetSource struct {
				ID string
			}
		}
	}

	directoryIDRes := &DirectoryIDRes{}
	res := &graphql.Response{Data: directoryIDRes}

	err = dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{dep {getSource {id} } }",
	}, res)

	if err != nil {
		return nil, err
	}


	return dag.LoadDirectoryFromID(dagger.DirectoryID(directoryIDRes.Dep.GetSource.ID)), nil
}

func (m *Test) GetRelDepSource(ctx context.Context, src *dagger.Directory) (*dagger.Directory, error) {
  err := src.AsModule().Initialize().Serve(ctx)
	if err != nil {
		return nil, err
	}

	type DirectoryIDRes struct {
		Dep struct {
			GetRelSource struct {
				ID string
			}
		}
	}

	directoryIDRes := &DirectoryIDRes{}
	res := &graphql.Response{Data: directoryIDRes}

	err = dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{dep {getRelSource {id} } }",
	}, res)

	if err != nil {
		return nil, err
	}


	return dag.LoadDirectoryFromID(dagger.DirectoryID(directoryIDRes.Dep.GetRelSource.ID)), nil
}
			`,
			)

		out, err := ctr.With(daggerCall("get-dep-source", "--src", "./dep", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)

		out, err = ctr.With(daggerCall("get-rel-dep-source", "--src", "./dep", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)
	})
}

func (ModuleSuite) TestIgnore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithDirectory("/work/backend", c.Directory().WithNewFile("foo.txt", "foo").WithNewFile("bar.txt", "bar")).
		WithDirectory("/work/frontend", c.Directory().WithNewFile("bar.txt", "bar")).
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=dagger")).
		WithWorkdir("/work/dagger").
		With(sdkSource("go", `
package main

import (
  "dagger/test/internal/dagger"
)

type Test struct{}

func (t *Test) IgnoreAll(
  // +ignore=["**"]
  // +defaultPath="./dagger"
  dir *dagger.Directory,
) *dagger.Directory {
  return dir
}

func (t *Test) IgnoreThenReverseIgnore(
  // +ignore=["**", "!**"]
  // +defaultPath="./dagger"
  dir *dagger.Directory,
) *dagger.Directory {
  return dir
}

func (t *Test) IgnoreThenReverseIgnoreThenExcludeGitFiles(
  // +ignore=["**", "!**", "*.git*"]
  // +defaultPath="./dagger"
  dir *dagger.Directory,
) *dagger.Directory {
  return dir
}

func (t *Test) IgnoreThenExcludeFilesThenReverseIgnore(
  // +ignore=["**", "*.git*", "!**"]
  // +defaultPath="./dagger"
  dir *dagger.Directory,
) *dagger.Directory {
  return dir
}

func (t *Test) IgnoreDir(
  // +ignore=["internal"]
  // +defaultPath="./dagger"
  dir *dagger.Directory,
) *dagger.Directory {
  return dir
}

func (t *Test) IgnoreEverythingButMainGo(
  // +ignore=["**", "!main.go"]
  // +defaultPath="./dagger"
  dir *dagger.Directory,
) *dagger.Directory {
  return dir
}

func (t *Test) NoIgnore(
  // +ignore=["!main.go"]
  // +defaultPath="./dagger"
  dir *dagger.Directory,
) *dagger.Directory {
  return dir
}

func (t *Test) IgnoreEveryGoFileExceptMainGo(
  // +ignore=["**/*.go", "!main.go"]
  // +defaultPath="./dagger"
  dir *dagger.Directory,
) *dagger.Directory {
  return dir
}

func (t *Test) IgnoreDirButKeepFileInSubdir(
  // +ignore=["internal/telemetry", "!internal/telemetry/proxy.go"]
  // +defaultPath="./dagger"
  dir *dagger.Directory,
) *dagger.Directory {
  return dir
}`)).
		WithWorkdir("/work")

	t.Run("ignore with context directory", func(ctx context.Context, t *testctx.T) {
		t.Run("ignore all", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-all", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "", out)
		})

		t.Run("ignore all then reverse ignore all", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-then-reverse-ignore", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, ".gitattributes\n.gitignore\ndagger.gen.go\ngo.mod\ngo.sum\ninternal\nmain.go\n", out)
		})

		t.Run("ignore all then reverse ignore then exclude files", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-then-reverse-ignore-then-exclude-git-files", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "dagger.gen.go\ngo.mod\ngo.sum\ninternal\nmain.go\n", out)
		})

		t.Run("ignore all then exclude files then reverse ignore", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-then-exclude-files-then-reverse-ignore", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, ".gitattributes\n.gitignore\ndagger.gen.go\ngo.mod\ngo.sum\ninternal\nmain.go\n", out)
		})

		t.Run("ignore dir", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-dir", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, ".gitattributes\n.gitignore\ndagger.gen.go\ngo.mod\ngo.sum\nmain.go\n", out)
		})

		t.Run("ignore everything but main.go", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-everything-but-main-go", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "main.go\n", out)
		})

		t.Run("no ignore", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("no-ignore", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, ".gitattributes\n.gitignore\ndagger.gen.go\ngo.mod\ngo.sum\ninternal\nmain.go\n", out)
		})

		t.Run("ignore every go files except main.go", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-every-go-file-except-main-go", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, ".gitattributes\n.gitignore\ngo.mod\ngo.sum\ninternal\nmain.go\n", out)

			// Verify the directories exist but files are correctlyignored
			out, err = modGen.With(daggerCall("ignore-every-go-file-except-main-go", "directory", "--path", "internal", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "dagger\nquerybuilder\ntelemetry\n", out)

			out, err = modGen.With(daggerCall("ignore-every-go-file-except-main-go", "directory", "--path", "internal/telemetry", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "", out)
		})

		t.Run("ignore dir but keep file in subdir", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-dir-but-keep-file-in-subdir", "directory", "--path", "internal/telemetry", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "proxy.go\n", out)
		})
	})

	// We don't need to test all ignore pattenrs, just that it works with given directory instead of the context one and that
	// ignore is correctly applied.
	t.Run("ignore with argument directory", func(ctx context.Context, t *testctx.T) {
		t.Run("ignore all", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-all", "--dir", ".", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "", out)
		})

		t.Run("ignore all then reverse ignore all with different dir than the one set in context", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-then-reverse-ignore", "--dir", "/work", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, ".git\nLICENSE\nbackend\ndagger\ndagger.json\nfrontend\n", out)
		})
	})
}

func (ModuleSuite) TestModuleDevelopVersion(ctx context.Context, t *testctx.T) {
	moduleSrc := `package main

import (
	"context"
	"github.com/Khan/genqlient/graphql"
)

type Foo struct {}

func (m *Foo) GetVersion(ctx context.Context) (string, error) {
	return schemaVersion(ctx)
}

func schemaVersion(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{__schemaVersion}",
	}, resp)
	if err != nil {
		return "", err
	}
	return resp.Data.(map[string]any)["__schemaVersion"].(string), nil
}`

	t.Run("from low", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "`+engine.Version+`"}`, daggerJSON)
	})

	t.Run("from high", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v100.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "`+engine.Version+`"}`, daggerJSON)
	})

	t.Run("from missing", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": "."}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "`+engine.Version+`"}`, daggerJSON)
	})

	t.Run("to specified", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.0.0"}`)

		work = work.With(daggerExec("develop", "--compat=v0.9.9"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.9.9"}`, daggerJSON)
	})

	t.Run("skipped", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.9.9"}`)

		work = work.With(daggerExec("develop", "--compat"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.9.9"}`, daggerJSON)
	})
}

func (ModuleSuite) TestTypedefSourceMaps(ctx context.Context, t *testctx.T) {
	baseSrc := `package main

type Test struct {}
    `

	tcs := []struct {
		sdk     string
		src     string
		matches []string
	}{
		{
			sdk: "go",
			src: `package main

import "context"

type Dep struct {
    FieldDef string
}

func (m *Dep) FuncDef(
	arg1 string,
	arg2 string, // +optional
) string {
    return ""
}

type MyEnum string
const (
    MyEnumA MyEnum = "MyEnumA"
    MyEnumB MyEnum = "MyEnumB"
)

type MyInterface interface {
	DaggerObject
	Do(ctx context.Context, val int) (string, error)
}

func (m *Dep) Collect(MyEnum, MyInterface) error {
    // force all the types here to be collected
    return nil
}
    `,
			matches: []string{
				// struct
				`\ntype Dep struct { // dep \(../../dep/main.go:5\)\n`,
				// struct field
				`\nfunc \(.* \*Dep\) FieldDef\(.* // dep \(../../dep/main.go:6\)\n`,
				// struct func
				`\nfunc \(.* \*Dep\) FuncDef\(.* // dep \(../../dep/main.go:9\)\n`,
				// struct func arg
				`\n\s*Arg2 string // dep \(../../dep/main.go:11\)\n`,

				// enum
				`\ntype DepMyEnum string // dep \(../../dep/main.go:16\)\n`,
				// enum value
				`\n\s*Myenuma DepMyEnum = "MyEnumA" // dep \(../../dep/main.go:18\)\n`,

				// interface
				`\ntype DepMyInterface struct { // dep \(../../dep/main.go:22\)\n`,
				// interface func
				`\nfunc \(.* \*DepMyInterface\) Do\(.* // dep \(../../dep/main.go:24\)\n`,
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := modInit(t, c, tc.sdk, baseSrc).
				With(withModInitAt("./dep", "go", tc.src)).
				With(daggerExec("install", "./dep"))

			codegenContents, err := modGen.File(sdkCodegenFile(t, tc.sdk)).Contents(ctx)
			require.NoError(t, err)

			for _, match := range tc.matches {
				matched, err := regexp.MatchString(match, codegenContents)
				require.NoError(t, err)
				require.Truef(t, matched, "%s did not match contents:\n%s", match, codegenContents)
			}
		})
	}
}

func daggerExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--debug"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerQuery(query string, args ...any) dagger.WithContainerFunc {
	return daggerQueryAt("", query, args...)
}

func daggerQueryAt(modPath string, query string, args ...any) dagger.WithContainerFunc {
	query = fmt.Sprintf(query, args...)
	return func(c *dagger.Container) *dagger.Container {
		execArgs := []string{"dagger", "--debug", "query"}
		if modPath != "" {
			execArgs = append(execArgs, "-m", modPath)
		}
		return c.WithExec(execArgs, dagger.ContainerWithExecOpts{
			Stdin:                         query,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerCall(args ...string) dagger.WithContainerFunc {
	return daggerCallAt("", args...)
}

func daggerCallAt(modPath string, args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		execArgs := []string{"dagger", "--debug", "call"}
		if modPath != "" {
			execArgs = append(execArgs, "-m", modPath)
		}
		return c.WithExec(append(execArgs, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func mountedPrivateRepoSocket(c *dagger.Client, t *testctx.T) (dagger.WithContainerFunc, func()) {
	sockPath, cleanup := setupPrivateRepoSSHAgent(t)

	return func(ctr *dagger.Container) *dagger.Container {
		sock := c.Host().UnixSocket(sockPath)
		if sock != nil {
			// Ensure that HOME env var is set, to ensure homePath expension in test suite
			homeDir, _ := os.UserHomeDir()
			ctr = ctr.WithEnvVariable("HOME", homeDir)

			ctr = ctr.WithUnixSocket("/sock/unix-socket", sock)
			ctr = ctr.WithEnvVariable("SSH_AUTH_SOCK", "/sock/unix-socket")
		}
		return ctr
	}, cleanup
}

func daggerFunctions(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--debug", "functions"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

// fileContents is syntax sugar for Container.WithNewFile.
func fileContents(path, contents string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithNewFile(path, heredoc.Doc(contents))
	}
}

func configFile(dirPath string, cfg *modules.ModuleConfig) dagger.WithContainerFunc {
	cfgPath := filepath.Join(dirPath, "dagger.json")
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return fileContents(cfgPath, string(cfgBytes))
}

// command for a dagger cli call direct on the host
func hostDaggerCommand(ctx context.Context, t testing.TB, workdir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(daggerCliPath(t), args...)
	cleanupExec(t, cmd)
	cmd.Env = append(os.Environ(), telemetry.PropagationEnv(ctx)...)
	cmd.Dir = workdir
	return cmd
}

// runs a dagger cli command directly on the host, rather than in an exec
func hostDaggerExec(ctx context.Context, t testing.TB, workdir string, args ...string) ([]byte, error) { //nolint: unparam
	t.Helper()
	cmd := hostDaggerCommand(ctx, t, workdir, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s: %w", string(output), err)
	}
	return output, err
}

func cleanupExec(t testing.TB, cmd *exec.Cmd) {
	t.Cleanup(func() {
		if cmd.Process == nil {
			t.Logf("never started: %v", cmd.Args)
			return
		}
		done := make(chan struct{})
		go func() {
			cmd.Wait()
			close(done)
		}()

		signals := []syscall.Signal{
			syscall.SIGINT,
			syscall.SIGTERM,
			syscall.SIGKILL,
		}
		doSignal := func() {
			if len(signals) == 0 {
				return
			}
			var signal syscall.Signal
			signal, signals = signals[0], signals[1:]
			t.Logf("sending %s: %v", signal, cmd.Args)
			cmd.Process.Signal(signal)
		}
		doSignal()

		for {
			select {
			case <-done:
				t.Logf("exited: %v", cmd.Args)
				return
			case <-time.After(30 * time.Second):
				if !t.Failed() {
					t.Errorf("process did not exit immediately")
				}

				// the process *still* isn't dead? try killing it harder.
				doSignal()
			}
		}
	})
}

func sdkSource(sdk, contents string) dagger.WithContainerFunc {
	return fileContents(sdkSourceFile(sdk), contents)
}

func sdkSourceAt(dir, sdk, contents string) dagger.WithContainerFunc {
	return fileContents(filepath.Join(dir, sdkSourceFile(sdk)), contents)
}

func sdkSourceFile(sdk string) string {
	switch sdk {
	case "go":
		return "main.go"
	case "python":
		return pythonSourcePath
	case "typescript":
		return "src/index.ts"
	default:
		panic(fmt.Errorf("unknown sdk %q", sdk))
	}
}

func sdkCodegenFile(t *testctx.T, sdk string) string {
	t.Helper()
	switch sdk {
	case "go":
		// FIXME: go codegen is split up into dagger/dagger.gen.go and
		// dagger/internal/dagger/dagger.gen.go
		return "internal/dagger/dagger.gen.go"
	case "python":
		return "sdk/src/dagger/client/gen.py"
	case "typescript":
		return "sdk/api/client.gen.ts"
	default:
		panic(fmt.Errorf("unknown sdk %q", sdk))
	}
}

func modInit(t *testctx.T, c *dagger.Client, sdk, contents string) *dagger.Container {
	t.Helper()
	return daggerCliBase(t, c).With(withModInit(sdk, contents))
}

func withModInit(sdk, contents string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(daggerExec("init", "--name=test", "--sdk="+sdk)).
			With(sdkSource(sdk, contents))
	}
}

func withModInitAt(dir, sdk, contents string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			With(daggerExec("init", "--name="+filepath.Base(dir), "--sdk="+sdk, dir)).
			With(sdkSourceAt(dir, sdk, contents))
	}
}

func currentSchema(ctx context.Context, t *testctx.T, ctr *dagger.Container) *introspection.Schema {
	t.Helper()
	out, err := ctr.With(daggerQuery(introspection.Query)).Stdout(ctx)
	require.NoError(t, err)
	var schemaResp introspection.Response
	err = json.Unmarshal([]byte(out), &schemaResp)
	require.NoError(t, err)
	return schemaResp.Schema
}

var moduleIntrospection = daggerQuery(`
query { host { directory(path: ".") { asModule { initialize {
    description
    objects {
        asObject {
            name
            description
            constructor {
                description
                args {
                    name
                    description
                    defaultValue
                }
            }
            functions {
                name
                description
                args {
                    name
                    description
                    defaultValue
                }
			}
            fields {
                name
                description
            }
        }
    }
    enums {
        asEnum {
            name
            description
            values {
                name
				description
			}
        }
    }
} } } } }
`)

func inspectModule(ctx context.Context, t *testctx.T, ctr *dagger.Container) gjson.Result {
	t.Helper()
	out, err := ctr.With(moduleIntrospection).Stdout(ctx)
	require.NoError(t, err)
	result := gjson.Get(out, "host.directory.asModule.initialize")
	t.Logf("module introspection:\n%v", result.Raw)
	return result
}

func inspectModuleObjects(ctx context.Context, t *testctx.T, ctr *dagger.Container) gjson.Result {
	t.Helper()
	return inspectModule(ctx, t, ctr).Get("objects.#.asObject")
}

func goGitBase(t *testctx.T, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"})
}

func logGen(ctx context.Context, t *testctx.T, modSrc *dagger.Directory) {
	t.Helper()
	generated, err := modSrc.File("dagger.gen.go").Contents(ctx)
	require.NoError(t, err)

	t.Cleanup(func() {
		t.Name()
		fileName := filepath.Join(
			os.TempDir(),
			t.Name(),
			fmt.Sprintf("dagger.gen.%d.go", time.Now().Unix()),
		)

		if err := os.MkdirAll(filepath.Dir(fileName), 0o755); err != nil {
			t.Logf("failed to create temp dir for generated code: %v", err)
			return
		}

		if err := os.WriteFile(fileName, []byte(generated), 0o644); err != nil {
			t.Logf("failed to write generated code to %s: %v", fileName, err)
		} else {
			t.Logf("wrote generated code to %s", fileName)
		}
	})
}

func (ModuleSuite) TestSSHAgentConnection(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("ConcurrentSetupAndCleanup", func(ctx context.Context, t *testctx.T) {
			var wg sync.WaitGroup
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, cleanup := setupPrivateRepoSSHAgent(t)
					time.Sleep(10 * time.Millisecond) // Simulate some work
					cleanup()
				}()
			}
			wg.Wait()
		})
	})
}

func (ModuleSuite) TestSSHAuthSockPathHandling(ctx context.Context, t *testctx.T) {
	repoURL := "git@gitlab.com:dagger-modules/private/test/more/dagger-test-modules-private.git"

	t.Run("SSH auth with home expansion and symlink", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
		defer cleanup()

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(mountedSocket).
			WithExec([]string{"mkdir", "-p", "/home/dagger"}).
			WithEnvVariable("HOME", "/home/dagger").
			WithExec([]string{"ln", "-s", "/sock/unix-socket", "/home/dagger/.ssh-sock"}).
			WithEnvVariable("SSH_AUTH_SOCK", "~/.ssh-sock")

		out, err := ctr.
			WithWorkdir("/work/some/subdir").
			WithExec([]string{"mkdir", "-p", "/home/dagger"}).
			WithExec([]string{"sh", "-c", "cd", "/work/some/subdir"}).
			With(daggerFunctions("-m", repoURL)).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")
	})

	t.Run("SSH auth from different relative paths", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
		defer cleanup()

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(mountedSocket).
			WithExec([]string{"mkdir", "-p", "/work/subdir"})

		// Test from same directory as the socket
		out, err := ctr.
			WithWorkdir("/sock").
			With(daggerFunctions("-m", repoURL)).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")

		// Test from a subdirectory
		out, err = ctr.
			WithWorkdir("/work/subdir").
			With(daggerFunctions("-m", repoURL)).
			Stdout(ctx)
		require.NoError(t, err)
		lines = strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")

		// Test from parent directory
		out, err = ctr.
			WithWorkdir("/").
			With(daggerFunctions("-m", repoURL)).
			Stdout(ctx)
		require.NoError(t, err)
		lines = strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")
	})
}
