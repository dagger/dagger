package core

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
)

type ModuleSuite struct{}

func TestModule(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleSuite{})
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
		requireErrOut(t, err, `The "foo-bar" SDK does not exist.`)
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
		requireErrOut(t, err, `the go sdk does not currently support selecting a specific version`)
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
					file: "src/test/__init__.py",
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
					file: "src/test/foo.py",
					contents: `
"""Not the main file"""

from dagger import field, object_type

@object_type
class Foo:
    bar: str = field(default="bar")
`,
				},
				{
					file: "src/test/__init__.py",
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
		t.Run(fmt.Sprintf("%s with %d files (#%d)", tc.sdk, len(tc.sources), i+1), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work")

			for _, src := range tc.sources {
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

type Test struct {
	Foo string

	Bar string // +private
}

func (m *Test) Set(foo string, bar string) *Test {
	m.Foo = foo
	m.Bar = bar
	return m
}

func (m *Test) Hello() string {
	return m.Foo + m.Bar
}
`,
		},
		{
			sdk: "python",
			source: `import dagger

@dagger.object_type
class Test:
    foo: str = dagger.field(default="")
    bar: str = ""

    @dagger.function
    def set(self, foo: str, bar: str) -> "Test":
        self.foo = foo
        self.bar = bar
        return self

    @dagger.function
    def hello(self) -> str:
        return self.foo + self.bar
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

  bar?: string

  constructor(foo?: string, bar?: string) {
    this.foo = foo
    this.bar = bar
  }

  @func()
  set(foo: string, bar: string): Test {
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
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := modInit(t, c, tc.sdk, tc.source)

			obj := inspectModuleObjects(ctx, t, modGen).Get("0")
			require.Equal(t, "Test", obj.Get("name").String())
			require.Len(t, obj.Get(`fields`).Array(), 1)
			prop := obj.Get(`fields.#(name="foo")`)
			require.Equal(t, "foo", prop.Get("name").String())

			out, err := modGen.With(daggerQuery(`{test{set(foo: "abc", bar: "xyz"){hello}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"set":{"hello": "abcxyz"}}}`, out)

			out, err = modGen.With(daggerQuery(`{test{set(foo: "abc", bar: "xyz"){foo}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"set":{"foo": "abc"}}}`, out)

			_, err = modGen.With(daggerQuery(`{test{set(foo: "abc", bar: "xyz"){bar}}}`)).Stdout(ctx)
			requireErrOut(t, err, `Cannot query field \"bar\" on type \"Test\"`)
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

type Test struct {}

var someDefault = dag.Container().From("` + alpineImage + `")

func (m *Test) Fn(ctx context.Context) (string, error) {
	return someDefault.WithExec([]string{"echo", "foo"}).Stdout(ctx)
}
`,
		},
		{
			sdk: "python",
			source: `from dagger import dag, function, object_type

SOME_DEFAULT = dag.container().from_("` + alpineImage + `")

@object_type
class Test:
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
export class Test {
  @func()
  async fn(): Promise<string> {
    return someDefault.withExec(["echo", "foo"]).stdout()
  }
}
`,
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := modInit(t, c, tc.sdk, tc.source).
				With(daggerQuery(`{test{fn}}`)).Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"fn":"foo\n"}}`, out)
		})
	}
}

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
		)

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

	types := currentSchema(ctx, t, ctr).Types
	require.NotNil(t, types.Get("A"))
	require.NotNil(t, types.Get("B"))
	require.NotNil(t, types.Get("C"))

	// verify that no types from transitive deps show up (only direct ones)
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

			out, err := modGen.With(daggerQuery(`{test{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"useHello":"hello"}}`, out)

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

			out, err := modGen.With(daggerQuery(`{test{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"useHello":"hello"}}`, out)

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

			out, err = modGen.With(daggerQuery(`{test{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"useHello":"hello"}}`, out)
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

			modGen = modGen.With(daggerQuery(`{test{useHello}}`))
			out, err := modGen.Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"useHello":"hello"}}`, out)

			newInner := strings.ReplaceAll(useInner, `"hello"`, `"goodbye"`)
			modGen = modGen.
				WithWorkdir("/work/dep").
				With(sdkSource("go", newInner)).
				WithWorkdir("/work").
				With(daggerExec("develop"))

			out, err = modGen.With(daggerQuery(`{test{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"useHello":"goodbye"}}`, out)
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

			out, err := modGen.With(daggerQuery(`{test{names}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"names":["foo", "bar"]}}`, out)
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
		require.Contains(t, out, `"source": "python"`)

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
		require.Contains(t, out, `"source": "typescript"`)

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
					fmt.Sprintf(`{test{container{echo(msg:%q){unwrap{stdout}}}}}`, id),
				)).
				Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t,
				fmt.Sprintf(`{"test":{"container":{"echo":{"unwrap":{"stdout":%q}}}}}`, id),
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
		With(daggerQuery(`{test{fn(s:"yo")}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"fn":["*dagger.Sub1Obj made 1:yo", "*dagger.Sub2Obj made 2:yo"]}}`, out)
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
				t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)

					_, err := c.Container().From(golangImage).
						WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
						WithWorkdir("/work").
						With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
						With(sdkSource(tc.sdk, tc.source)).
						With(daggerQuery(`{test{fn{id}}}`)).
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
						With(daggerQuery(`{test{id}}`)).
						Sync(ctx)

					requireErrOut(t, err, "cannot define function with reserved name \"id\"")
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
			requireErrOut(t, err, `workdir path "../rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-file-abs", "contents")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "/rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-dir", "entries")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "../foo" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-dir-abs", "entries")).
				Stdout(ctx)
			requireErrOut(t, err, `workdir path "/foo" escapes workdir`)
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
	"context"
	"encoding/json"

	"dagger/cool-sdk/internal/dagger"
)

type CoolSdk struct {}

func (m *CoolSdk) ModuleTypes(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File, outputFilePath string) (*dagger.Container, error) {
	mod := modSource.WithSDK("go").AsModule()
	modID, err := mod.ID(ctx)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(modID)
	if err != nil {
		return nil, err
	}
	return dag.Container().
		From("alpine").
		WithNewFile(outputFilePath, string(b)).
		WithEntrypoint([]string{
			"sh", "-c", "",
		}), nil
}

func (m *CoolSdk) ModuleRuntime(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *CoolSdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("go").AsModule().GeneratedContextDirectory())
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
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			ctr := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				With(privateSetup).
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

	t.Run("module initialization", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// verify that SDKs can successfully:
		// - create an exec during module initialization
		// - call CurrentModule().Source
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/coolsdk").
			With(daggerExec("init", "--source=.", "--name=cool-sdk", "--sdk=go")).
			WithNewFile("main.go", `package main

import (
	"context"
	"encoding/json"

	"dagger/cool-sdk/internal/dagger"
)

type CoolSdk struct {}


func (m *CoolSdk) ModuleTypes(ctx context.Context, modSource *dagger.ModuleSource, introspectionJSON *dagger.File, outputFilePath string) (*dagger.Container, error) {
	// return hardcoded typedefs; this module will thus only work during init, but that's all we're testing here
	mod := dag.Module().WithObject(dag.TypeDef().
		WithObject("Test").
		WithFunction(dag.Function("CoolFn", dag.TypeDef().WithKind(dagger.TypeDefKindVoidKind).WithOptional(true))))
	modID, err := mod.ID(ctx)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(modID)
	if err != nil {
		return nil, err
	}
	return dag.Container().
		From("alpine").
		WithNewFile(outputFilePath, string(b)).
		WithEntrypoint([]string{
			"sh", "-c", "",
		}), nil
}

func (m *CoolSdk) ModuleRuntime(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *CoolSdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("go").AsModule().GeneratedContextDirectory())
}
`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=coolsdk")).
			WithNewFile("main.go", `package main

type Test struct {}
`,
			)

		out, err := ctr.
			With(daggerFunctions()).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `cool-fn`) // hardcoded typedef
	})
}

// TestUnbundleSDK verifies that you can implement a SDK without
// having to implements the full interface but only the ones you want.
// cc: https://github.com/dagger/dagger/issues/7707
func (ModuleSuite) TestUnbundleSDK(ctx context.Context, t *testctx.T) {
	t.Run("only codegen", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory("/work/sdk", c.Host().Directory("./testdata/sdks/only-codegen")).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=./sdk", "--source=."))

		t.Run("can run dagger develop", func(ctx context.Context, t *testctx.T) {
			generatedFile, err := ctr.With(daggerExec("develop")).File("/work/hello.txt").Contents(ctx)

			require.NoError(t, err)
			require.Equal(t, "Hello, world!", generatedFile)
		})

		t.Run("explicit error on dagger call", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerExec("call", "foo")).Sync(ctx)

			requireErrOut(t, err, `"./sdk" SDK does not support defining and executing functions`)
		})

		t.Run("explicit error on dagger functions", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerFunctions()).Sync(ctx)

			requireErrOut(t, err, `"./sdk" SDK does not support defining and executing functions`)
		})
	})

	t.Run("only runtime", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory("/work/sdk", c.Host().Directory("./testdata/sdks/only-runtime")).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=./sdk", "--source=."))

		t.Run("can run dagger develop without failing", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(daggerExec("develop")).Sync(ctx)

			require.NoError(t, err)
		})

		t.Run("can run dagger functions", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerFunctions()).Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "hello-world")
		})

		t.Run("can run dagger call", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.With(daggerCall("hello-world")).Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "Hello world")
		})
	})
}

// TestHostError verifies the host api is not exposed to modules
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
	requireErrOut(t, err, "dag.Host undefined")
}

// TestEngineError verifies the engine api is not exposed to modules
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
 				_, _ = dag.Engine().LocalCache().EntrySet().Entries(ctx)
				return nil
 			}
 			`,
		).
		With(daggerCall("fn")).
		Sync(ctx)
	requireErrOut(t, err, "dag.Engine undefined")
}

func (ModuleSuite) TestDaggerListen(ctx context.Context, t *testctx.T) {
	t.Run("with mod", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		addr := "127.0.0.1:12456"
		listenCmd := hostDaggerCommand(ctx, t, modDir, "listen", "--listen", addr)
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

		callCmd := hostDaggerCommand(ctx, t, modDir, "call", "container-echo", "--string-arg=hi", "stdout")
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
			_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
			require.NoError(t, err)

			listenCmd := hostDaggerCommand(ctx, t, modDir, "listen", "--disable-host-read-write", "--listen", "127.0.0.1:12457")
			listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")
			require.NoError(t, listenCmd.Start())

			var out []byte
			for range limitTicker(time.Second, 60) {
				callCmd := hostDaggerCommand(ctx, t, modDir, "query")
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

			listenCmd := hostDaggerCommand(ctx, t, tmpdir, "listen", "--disable-host-read-write", "--listen", "127.0.0.1:12458")
			listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")
			require.NoError(t, listenCmd.Start())

			var out []byte
			var err error
			for range limitTicker(time.Second, 60) {
				callCmd := hostDaggerCommand(ctx, t, tmpdir, "query")
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

		t.Run("double nested and called repeatedly", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

			// Set up the base generator module
			ctr = ctr.
				WithWorkdir("/work/keychain/generator").
				With(daggerExec("init", "--name=generator", "--sdk=go", "--source=.")).
				WithNewFile("main.go", `package main

import (
    "context"
    "dagger/generator/internal/dagger"
)

type Generator struct {
    // +private
    Password *dagger.Secret
}

func New() *Generator {
    return &Generator{
        Password: dag.SetSecret("pass", "admin"),
    }
}

func (m *Generator) Gen(ctx context.Context, name string) error {
    _, err := m.Password.Plaintext(ctx)
    return err
}
`)

			// Set up the keychain module that depends on generator
			ctr = ctr.
				WithWorkdir("/work/keychain").
				With(daggerExec("init", "--name=keychain", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./generator")).
				WithNewFile("main.go", `package main

import (
    "context"
)

type Keychain struct{}

func (m *Keychain) Get(ctx context.Context, name string) error {
    return dag.Generator().Gen(ctx, name)
}
`)

			// Set up the main module that uses keychain
			ctr = ctr.
				WithWorkdir("/work").
				With(daggerExec("init", "--name=mymodule", "--sdk=go", "--source=.")).
				With(daggerExec("install", "./keychain")).
				WithNewFile("main.go", `package main

import (
    "context"
    "fmt"
)

type Mymodule struct{}

func (m *Mymodule) Issue(ctx context.Context) error {
    kc := dag.Keychain()

    err := kc.Get(ctx, "a")
    if err != nil {
        return fmt.Errorf("first get: %w", err)
    }

    err = kc.Get(ctx, "a")
    if err != nil {
        return fmt.Errorf("second get, same args: %w", err)
    }

    err = kc.Get(ctx, "b")
    if err != nil {
        return fmt.Errorf("third get: %w", err)
    }
    return nil
}
`)

			// Test that repeated calls work correctly
			_, err := ctr.With(daggerCall("issue")).Sync(ctx)
			require.NoError(t, err)
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

	t.Run("optional secret field on module object", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pythonSource(`
import base64
import dagger
from dagger import dag, field, function, object_type


@object_type
class Test:
    @function
    def getobj(self, *, top_secret: dagger.Secret | None = None) -> "Obj":
        return Obj(top_secret=top_secret)


@object_type
class Obj:
    top_secret: dagger.Secret | None = field(default=None)

    @function
    async def getSecret(self) -> str:
        plaintext = await self.top_secret.plaintext()
        return base64.b64encode(plaintext.encode()).decode()
`)).
			With(daggerInitPython()).
			WithEnvVariable("TOP_SECRET", "omg").
			With(daggerCall("getobj", "--top-secret", "env://TOP_SECRET", "get-secret")).
			Stdout(ctx)

		require.NoError(t, err)
		decodeOut, err := base64.StdEncoding.DecodeString(strings.TrimSpace(out))
		require.NoError(t, err)
		require.Equal(t, "omg", string(decodeOut))
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
			WithDefaultArgs([]string{"python", "-m", "http.server", "23457"}).
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
		AsService(dagger.ContainerAsServiceOpts{UseEntrypoint: true})

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

func (ModuleSuite) TestModulePreFilteringDirectory(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}

	t.Run("pre filtering directory on module call", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (t *Test) Call(
  // +ignore=[
  //   "foo.txt",
  //   "bar"
  // ]
  dir *dagger.Directory,
) *dagger.Directory {
 return dir
}`,
			},
			{
				sdk: "typescript",
				source: `import { object, func, Directory, argument } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  call(
    @argument({ ignore: ["foo.txt", "bar"] }) dir: Directory,
  ): Directory {
    return dir
  }
}`,
			},
			{
				sdk: "python",
				source: `from typing import Annotated

import dagger
from dagger import DefaultPath, Ignore, function, object_type


@object_type
class Test:
    @function
    async def call(
        self,
        dir: Annotated[dagger.Directory, Ignore(["foo.txt","bar"])],
    ) -> dagger.Directory:
        return dir
`,
			},
		} {
			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				modGen := goGitBase(t, c).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					// Add inputs
					WithDirectory("/work/input", c.
						Directory().
						WithNewFile("foo.txt", "foo").
						WithNewFile("bar.txt", "bar").
						WithDirectory("bar", c.Directory().WithNewFile("baz.txt", "baz"))).
					// Add dep
					WithWorkdir("/work/dep").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
					With(sdkSource(tc.sdk, tc.source)).
					// Setup test modules
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test-mod", "--sdk=go", "--source=.")).
					With(daggerExec("install", "./dep")).
					With(sdkSource("go", `package main

import (
	"dagger/test-mod/internal/dagger"
)

type TestMod struct {}

func (t *TestMod) Test(
  dir *dagger.Directory,
) *dagger.Directory {
 return dag.Test().Call(dir)
}`,
					))

				out, err := modGen.With(daggerCall("test", "--dir", "./input", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\n", out)
			})
		}
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
					require.Equal(t, ".git/\nbackend/\nci/\nfrontend/\nLICENSE\ndagger/\ndagger.json\n", out)
				})

				t.Run("dir ignore", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCallAt("ci", "dirs-ignore")).Stdout(ctx)
					require.NoError(t, err)
					require.Equal(t, "backend/\nfrontend/\ndagger/\n", out)
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
					require.Equal(t, ".git/\nLICENSE\nbackend/\ndagger/\ndagger.json\nfrontend/\n.git/\nLICENSE\nbackend/\ndagger/\ndagger.json\nfrontend/\n", out)
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
					requireErrOut(t, err, `path should be relative to the context directory`)
				})

				t.Run("too high relative context file path", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("too-high-relative-file-path")).Stdout(ctx)
					require.Empty(t, out)
					require.Error(t, err)
					requireErrOut(t, err, `path should be relative to the context directory`)
				})

				t.Run("non existing dir path", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("non-existing-path")).Stdout(ctx)
					require.Empty(t, out)
					require.Error(t, err)
					requireErrOut(t, err, "no such file or directory")
				})

				t.Run("non existing file", func(ctx context.Context, t *testctx.T) {
					out, err := modGen.With(daggerCall("non-existing-file")).Stdout(ctx)
					require.Empty(t, out)
					require.Error(t, err)
					requireErrOut(t, err, "no such file or directory")
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

		// now try calling from outside

		ctr = ctr.WithWorkdir("/")

		out, err = ctr.With(daggerCallAt("work", "get-dep-source", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)

		out, err = ctr.With(daggerCallAt("work", "get-rel-dep-source", "entries")).Stdout(ctx)
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
	// +defaultPath="."
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
	err := src.AsModule(dagger.DirectoryAsModuleOpts{SourceRootPath: "dep"}).Serve(ctx)
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
	err := src.AsModule(dagger.DirectoryAsModuleOpts{SourceRootPath: "dep"}).Serve(ctx)
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

		out, err := ctr.With(daggerCall("get-dep-source", "--src", ".", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)

		out, err = ctr.With(daggerCall("get-rel-dep-source", "--src", ".", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)
	})
}

func (ModuleSuite) TestContextDirectoryGit(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		for _, mod := range []string{"context-dir", "context-dir-user"} {
			t.Run(mod, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				mountedSocket, cleanup := privateRepoSetup(c, t, tc)
				defer cleanup()

				modRef := testGitModuleRef(tc, mod)
				modGen := goGitBase(t, c).
					WithWorkdir("/work").
					With(mountedSocket)

				out, err := modGen.With(daggerCallAt(modRef, "absolute-path", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, out, ".git/\n")
				require.Contains(t, out, "README.md\n")

				out, err = modGen.With(daggerCallAt(modRef, "absolute-path-subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, out, "root_data.txt\n")

				out, err = modGen.With(daggerCallAt(modRef, "relative-path", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, out, "dagger.json\n")
				require.Contains(t, out, "src/\n")

				out, err = modGen.With(daggerCallAt(modRef, "relative-path-subdir", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, out, "bar.txt\n")
			})
		}
	})
}

func (ModuleSuite) TestContextGit(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk    string
		source string
	}
	tcs := []testCase{
		{
			sdk: "go",
			source: `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) TestRepoLocal(
	ctx context.Context,
	// +defaultPath="./.git"
	git *dagger.GitRepository,
) (string, error) {
	return m.commitAndRef(ctx, git.Head())
}

func (m *Test) TestRepoLocalAbs(
	ctx context.Context,
	// +defaultPath="/"
	git *dagger.GitRepository,
) (string, error) {
	return m.commitAndRef(ctx, git.Head())
}

func (m *Test) TestRepoRemote(
	ctx context.Context,
	// +defaultPath="https://github.com/dagger/dagger.git"
	git *dagger.GitRepository,
) (string, error) {
	return m.commitAndRef(ctx, git.Tag("v0.18.2"))
}

func (m *Test) TestRefLocal(
	ctx context.Context,
	// +defaultPath="./.git"
	git *dagger.GitRef,
) (string, error) {
	return m.commitAndRef(ctx, git)
}

func (m *Test) TestRefRemote(
	ctx context.Context,
	// +defaultPath="https://github.com/dagger/dagger.git#v0.18.3"
	git *dagger.GitRef,
) (string, error) {
	return m.commitAndRef(ctx, git)
}

func (m *Test) commitAndRef(ctx context.Context, ref *dagger.GitRef) (string, error) {
	commit, err := ref.Commit(ctx)
	if err != nil {
		return "", err
	}
	reference, err := ref.Ref(ctx)
	if err != nil {
		return "", err
	}
	return reference + "@" + commit, nil
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
	async def test_repo_local(self, git: Annotated[dagger.GitRepository, DefaultPath("./.git")]) -> str:
		return await self.commit_and_ref(git.head())

	@function
	async def test_repo_local_abs(self, git: Annotated[dagger.GitRepository, DefaultPath("/")]) -> str:
		return await self.commit_and_ref(git.head())

	@function
	async def test_repo_remote(self, git: Annotated[dagger.GitRepository, DefaultPath("https://github.com/dagger/dagger.git")]) -> str:
		return await self.commit_and_ref(git.tag("v0.18.2"))

	@function
	async def test_ref_local(self, git: Annotated[dagger.GitRef, DefaultPath("./.git")]) -> str:
		return await self.commit_and_ref(git)

	@function
	async def test_ref_remote(self, git: Annotated[dagger.GitRef, DefaultPath("https://github.com/dagger/dagger.git#v0.18.3")]) -> str:
		return await self.commit_and_ref(git)

	async def commit_and_ref(self, ref: dagger.GitRef) -> str:
		commit = await ref.commit()
		reference = await ref.ref()
		return f"{reference}@{commit}"
`,
		},
		{
			sdk: "typescript",
			source: `import { GitRepository, GitRef, object, func, argument } from "@dagger.io/dagger"

@object()
export class Test {
	@func()
	async testRepoLocal(
		@argument({ defaultPath: "./.git" }) git: GitRepository,
	): Promise<string> {
		return await this.commitAndRef(git.head())
	}

	@func()
	async testRepoLocalAbs(
		@argument({ defaultPath: "/" }) git: GitRepository,
	): Promise<string> {
		return await this.commitAndRef(git.head())
	}

	@func()
	async testRepoRemote(
		@argument({ defaultPath: "https://github.com/dagger/dagger.git" }) git: GitRepository,
	): Promise<string> {
		return await this.commitAndRef(git.tag("v0.18.2"))
	}

	@func()
	async testRefLocal(
		@argument({ defaultPath: "./.git" }) git: GitRef,
	): Promise<string> {
		return await this.commitAndRef(git)
	}

	@func()
	async testRefRemote(
		@argument({ defaultPath: "https://github.com/dagger/dagger.git#v0.18.3" }) git: GitRef,
	): Promise<string> {
		return await this.commitAndRef(git)
	}

	async commitAndRef(git: GitRef): Promise<string> {
		const commit = await git.commit()
		const reference = await git.ref()
		return reference + "@" + commit
	}
}`,
		},
		{
			sdk: "java",
			source: `package io.dagger.modules.test;


import io.dagger.client.GitRef;
import io.dagger.client.GitRepository;
import io.dagger.client.exception.DaggerQueryException;
import io.dagger.module.annotation.DefaultPath;
import io.dagger.module.annotation.Function;
import io.dagger.module.annotation.Object;
import java.util.concurrent.ExecutionException;

@Object
public class Test {
    @Function
    public String testRepoLocal(@DefaultPath("./.git") GitRepository git) throws ExecutionException, DaggerQueryException, InterruptedException {
        return this.commitAndRef(git.head());
    }

    @Function
    public String testRepoLocalAbs(@DefaultPath("/") GitRepository git) throws ExecutionException, DaggerQueryException, InterruptedException {
        return this.commitAndRef(git.head());
    }

    @Function
    public String testRepoRemote(@DefaultPath("https://github.com/dagger/dagger.git") GitRepository git) throws ExecutionException, DaggerQueryException, InterruptedException {
        return this.commitAndRef(git.tag("v0.18.2"));
    }

    @Function
    public String testRefLocal(@DefaultPath("./.git") GitRef git) throws ExecutionException, DaggerQueryException, InterruptedException {
        return this.commitAndRef(git);
    }

    @Function
    public String testRefRemote(@DefaultPath("https://github.com/dagger/dagger.git#v0.18.3") GitRef git) throws ExecutionException, DaggerQueryException, InterruptedException {
        return this.commitAndRef(git);
    }

    private String commitAndRef(GitRef git) throws ExecutionException, DaggerQueryException, InterruptedException {
        var commit = git.commit();
        var reference = git.ref();
        return "%s@%s".formatted(reference, commit);
    }
}
`,
		},
	}
	for _, tc := range tcs {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := modInit(t, c, tc.sdk, tc.source).
				WithExec([]string{"sh", "-c", `git init && git add . && git commit -m "initial commit"`}).
				WithExec([]string{"git", "clean", "-fdx"})
			headCommit, err := modGen.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
			require.NoError(t, err)
			headCommit = strings.TrimSpace(headCommit)

			t.Run("repo local", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-repo-local")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "refs/heads/master@"+headCommit, out)
			})

			t.Run("repo local absolute", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-repo-local-abs")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "refs/heads/master@"+headCommit, out)
			})

			t.Run("repo remote", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-repo-remote")).Stdout(ctx)
				require.NoError(t, err)
				// dagger/dagger v0.18.2 => 0b46ea3c49b5d67509f67747742e5d8b24be9ef7
				require.Equal(t, "refs/tags/v0.18.2@0b46ea3c49b5d67509f67747742e5d8b24be9ef7", out)
			})

			t.Run("ref local", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-ref-local")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "refs/heads/master@"+headCommit, out)
			})

			t.Run("ref remote", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-ref-remote")).Stdout(ctx)
				require.NoError(t, err)
				// dagger/dagger v0.18.3 => 6f7af26f18061c6f575eda774f44aa7d314af4ce
				require.Equal(t, "refs/tags/v0.18.3@6f7af26f18061c6f575eda774f44aa7d314af4ce", out)
			})
		})
	}
}

func (ModuleSuite) TestContextGitRemote(ctx context.Context, t *testctx.T) {
	// pretty much exactly the same test as above, but calling a remote git repo instead

	c := connect(ctx, t)

	modGen := goGitBase(t, c)

	remoteModule := "github.com/dagger/dagger-test-modules"
	remoteRef := "context-git"
	g := c.Git(remoteModule).Ref(remoteRef)
	commit, err := g.Commit(ctx)
	require.NoError(t, err)
	fullref, err := g.Ref(ctx)
	require.NoError(t, err)

	modPath := "github.com/dagger/dagger-test-modules/context-git@" + remoteRef

	t.Run("repo local", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(modPath, "test-repo-local")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, fullref+"@"+commit, out)
	})

	t.Run("repo remote", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(modPath, "test-repo-remote")).Stdout(ctx)
		require.NoError(t, err)
		// dagger/dagger v0.18.2 => 0b46ea3c49b5d67509f67747742e5d8b24be9ef7
		require.Equal(t, "refs/tags/v0.18.2@0b46ea3c49b5d67509f67747742e5d8b24be9ef7", out)
	})

	t.Run("ref local", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(modPath, "test-ref-local")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, fullref+"@"+commit, out)
	})

	t.Run("ref remote", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(modPath, "test-ref-remote")).Stdout(ctx)
		require.NoError(t, err)
		// dagger/dagger v0.18.3 => 6f7af26f18061c6f575eda774f44aa7d314af4ce
		require.Equal(t, "refs/tags/v0.18.3@6f7af26f18061c6f575eda774f44aa7d314af4ce", out)
	})
}

func (ModuleSuite) TestContextGitRemoteDep(ctx context.Context, t *testctx.T) {
	// pretty much exactly the same test as above, but calling a remote git repo via a pinned dependency

	c := connect(ctx, t)

	remoteRepo := "github.com/dagger/dagger-test-modules"
	remoteModule := remoteRepo + "/context-git"

	// this commit is *not* the target of any version
	// so, this ends up repinning
	commit := "ed6bf431366bac652f807864e22ae49be9433bd5"

	for _, version := range []string{"", "main", "context-git", "v1.2.3"} {
		t.Run("version="+version, func(ctx context.Context, t *testctx.T) {
			g := c.Git(remoteRepo).Ref(cmp.Or(version, "HEAD"))
			fullref, err := g.Ref(ctx)
			require.NoError(t, err)
			require.Contains(t, fullref, version)

			if version != "" {
				version = "@" + version
			}

			// create a module that depends on the remote module
			modGen := goGitBase(t, c).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				WithNewFile("dagger.json", `{
			"name": "test",
	"source": ".",
	"sdk": "go",
	"dependencies": [
		{
			"name": "context-git",
			"source": "`+remoteModule+version+`",
			"pin": "`+commit+`"
		}
	]
	}`).
				With(sdkSource("go", `package main

	import (
		"context"
	)

	type Test struct{}

	func (m *Test) TestRepoLocal(ctx context.Context) (string, error) {
		return dag.ContextGit().TestRepoLocal(ctx)
	}

	func (m *Test) TestRepoRemote(ctx context.Context) (string, error) {
		return dag.ContextGit().TestRepoRemote(ctx)
	}

	func (m *Test) TestRefLocal(ctx context.Context) (string, error) {
		return dag.ContextGit().TestRefLocal(ctx)
	}

	func (m *Test) TestRefRemote(ctx context.Context) (string, error) {
		return dag.ContextGit().TestRefRemote(ctx)
	}
	`)).
				WithExec([]string{"sh", "-c", `git init && git add . && git commit -m "initial commit"`})

			t.Run("repo local", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-repo-local")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, fullref+"@"+commit, out)
			})

			t.Run("ref local", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-ref-local")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, fullref+"@"+commit, out)
			})

			t.Run("ref remote", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-ref-remote")).Stdout(ctx)
				require.NoError(t, err)
				// dagger/dagger v0.18.3 => 6f7af26f18061c6f575eda774f44aa7d314af4ce
				require.Equal(t, "refs/tags/v0.18.3@6f7af26f18061c6f575eda774f44aa7d314af4ce", out)
			})

			t.Run("repo remote", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-repo-remote")).Stdout(ctx)
				require.NoError(t, err)
				// dagger/dagger v0.18.2 => 0b46ea3c49b5d67509f67747742e5d8b24be9ef7
				require.Equal(t, "refs/tags/v0.18.2@0b46ea3c49b5d67509f67747742e5d8b24be9ef7", out)
			})
		})
	}
}

func (ModuleSuite) TestContextGitDetectDirty(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := modInit(t, c, "go", `
package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) IsDirty(
	ctx context.Context,
	// +defaultPath="./.git"
	git *dagger.GitRepository,
) (bool, error) {
	clean, err := git.Uncommitted().IsEmpty(ctx)
	return !clean, err
}
`).
		WithNewFile("somefile.txt", "some content").
		With(gitUserConfig).
		WithExec([]string{"sh", "-c", `git init && git add . && git commit -m "initial commit"`})

	out, err := modGen.With(daggerCall("is-dirty")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "false", out)

	out, err = modGen.WithNewFile("newfile.txt", "some new content").With(daggerCall("is-dirty")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "true", out)

	out, err = modGen.WithoutFile("somefile.txt").With(daggerCall("is-dirty")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "true", out)
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
  // +ignore=["internal/foo", "!internal/foo/bar.go"]
  // +defaultPath="./dagger"
  dir *dagger.Directory,
) *dagger.Directory {
  return dir
}`)).
		WithDirectory("./internal/foo", c.Directory().
			WithNewFile("bar.go", "package foo").
			WithNewFile("baz.go", "package foo"),
		).
		WithWorkdir("/work")

	t.Run("ignore with context directory", func(ctx context.Context, t *testctx.T) {
		t.Run("ignore all", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-all", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "", strings.TrimSpace(out))
		})

		t.Run("ignore all then reverse ignore all", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-then-reverse-ignore", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.Join([]string{
				".gitattributes",
				".gitignore",
				"dagger.gen.go",
				"go.mod",
				"go.sum",
				"internal/",
				"main.go",
			}, "\n"), strings.TrimSpace(out))
		})

		t.Run("ignore all then reverse ignore then exclude files", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-then-reverse-ignore-then-exclude-git-files", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.Join([]string{
				"dagger.gen.go",
				"go.mod",
				"go.sum",
				"internal/",
				"main.go",
			}, "\n"), strings.TrimSpace(out))
		})

		t.Run("ignore all then exclude files then reverse ignore", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-then-exclude-files-then-reverse-ignore", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.Join([]string{
				".gitattributes",
				".gitignore",
				"dagger.gen.go",
				"go.mod",
				"go.sum",
				"internal/",
				"main.go",
			}, "\n"), strings.TrimSpace(out))
		})

		t.Run("ignore dir", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-dir", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.Join([]string{
				".gitattributes",
				".gitignore",
				"dagger.gen.go",
				"go.mod",
				"go.sum",
				"main.go",
			}, "\n"), strings.TrimSpace(out))
		})

		t.Run("ignore everything but main.go", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-everything-but-main-go", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "main.go", strings.TrimSpace(out))
		})

		t.Run("no ignore", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("no-ignore", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.Join([]string{
				".gitattributes",
				".gitignore",
				"dagger.gen.go",
				"go.mod",
				"go.sum",
				"internal/",
				"main.go",
			}, "\n"), strings.TrimSpace(out))
		})

		t.Run("ignore every go files except main.go", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-every-go-file-except-main-go", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.Join([]string{
				".gitattributes",
				".gitignore",
				"go.mod",
				"go.sum",
				"internal/",
				"main.go",
			}, "\n"), strings.TrimSpace(out))

			// Verify the directories exist but files are correctly ignored (including the .gitiginore exclusion)
			out, err = modGen.With(daggerCall("ignore-every-go-file-except-main-go", "directory", "--path", "internal", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.Join([]string{
				"dagger/",
				"foo/",
				"querybuilder/",
				"telemetry/",
			}, "\n"), strings.TrimSpace(out))
		})

		t.Run("ignore dir but keep file in subdir", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-dir-but-keep-file-in-subdir", "directory", "--path", "internal/foo", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "bar.go", strings.TrimSpace(out))
		})
	})

	// We don't need to test all ignore patterns, just that it works with given directory instead of the context one and that
	// ignore is correctly applied.
	t.Run("ignore with argument directory", func(ctx context.Context, t *testctx.T) {
		t.Run("ignore all", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-all", "--dir", ".", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "", strings.TrimSpace(out))
		})

		t.Run("ignore all then reverse ignore all with different dir than the one set in context", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("ignore-then-reverse-ignore", "--dir", "/work", "entries")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.Join([]string{
				".git/",
				"LICENSE",
				"backend/",
				"dagger/",
				"dagger.json",
				"frontend/",
			}, "\n"), strings.TrimSpace(out))
		})
	})
}

func (ModuleSuite) TestGitignore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dagger").
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithDirectory("./backend", c.Directory().WithNewFile("foo.txt", "foo")).
		WithDirectory("./frontend", c.Directory().WithNewFile("bar.txt", "bar")).
		WithNewFile("./.gitignore", "frontend/*.txt\n").
		With(sdkSource("go", `
package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (t *Test) GetFile(ctx context.Context, filename string) (string, error) {
	return dag.CurrentModule().Source().File(filename).Contents(ctx)
}

func (t *Test) GetFileAt(ctx context.Context, filename string, dir *dagger.Directory) (string, error) {
	return dir.File(filename).Contents(ctx)
}

func (t *Test) GetFileContext(
	ctx context.Context,
	filename string,
	// +defaultPath="."
	dir *dagger.Directory,
) (string, error) {
	return dir.File(filename).Contents(ctx)
}
		`))

	t.Run("gitignore applies to loaded module", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("get-file", "--filename", "backend/foo.txt")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo", out)

		_, err = modGen.With(daggerCall("get-file", "--filename", "frontend/bar.txt")).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "no such file or directory")
	})

	t.Run("gitignore doesn't apply to manual args", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("get-file-at", "--dir", "./backend", "--filename", "foo.txt")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo", out)

		// NOTE: we disabled this in dagger/dagger#11017
		// args passed via function arguments do not automatically have gitignore applied
		out, err = modGen.With(daggerCall("get-file-at", "--dir", "./frontend", "--filename", "bar.txt")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", out)
	})

	t.Run("gitignore doesn't apply to context directory", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("get-file-context", "--filename", "backend/foo.txt")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo", out)

		// NOTE: we disabled this in dagger/dagger#11017
		// context arguments do not automatically have gitignore applied
		out, err = modGen.With(daggerCall("get-file-context", "--filename", "frontend/bar.txt")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar", out)
	})
}

func (ModuleSuite) TestFloat(ctx context.Context, t *testctx.T) {
	depSrc := `package main

type Dep struct{}

func (m *Dep) Dep(n float64) float32 {
	return float32(n)
}
`

	type testCase struct {
		sdk    string
		source string
	}

	testCases := []testCase{
		{
			sdk: "go",
			source: `package main

import "context"

type Test struct{}

func (m *Test) Test(n float64) float64 {
	return n
}

func (m *Test) TestFloat32(n float32) float32 {
	return n
}

func (m *Test) Dep(ctx context.Context, n float64) (float64, error) {
	return dag.Dep().Dep(ctx, n)
}`,
		},
		{
			sdk: "typescript",
			source: `import { dag, float, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  test(n: float): float {
    return n
  }

  @func()
  testFloat32(n: float): float {
    return n
  }

  @func()
  async dep(n: float): Promise<float> {
    return dag.dep().dep(n)
  }
}`,
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import dag

@dagger.object_type
class Test:
    @dagger.function
    def test(self, n: float) -> float:
        return n

    @dagger.function
    def testFloat32(self, n: float) -> float:
        return n

    @dagger.function
    async def dep(self, n: float) -> float:
        return await dag.dep().dep(n)
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go", "--source=.")).
				WithNewFile("/work/dep/main.go", depSrc).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			t.Run("float64", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test", "--n=3.14")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `3.14`, out)
			})

			t.Run("float32", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("test-float-32", "--n=1.73424")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `1.73424`, out)
			})

			t.Run("call dep with float64 to float32 conversion", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.With(daggerCall("dep", "--n=232.3454")).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `232.3454`, out)
			})
		})
	}
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
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("from high", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v100.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		_, err := work.
			File("dagger.json").
			Contents(ctx)

		// sadly, just no way to handle this :(
		// in the future, the format of dagger.json might change dramatically,
		// and so there's no real way to know from the older version how to
		// convert it back down
		require.Error(t, err)
		requireErrOut(t, err, `module requires dagger v100.0.0, but you have`)
	})

	t.Run("from missing", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("develop"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("to specified", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v0.0.0"}`)

		work = work.With(daggerExec("develop", "--compat=v0.9.9"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "v0.9.9", gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("skipped", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "engineVersion": "v0.9.9"}`)

		work = work.With(daggerExec("develop", "--compat"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "v0.9.9", gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("in install", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("install", "github.com/shykes/hello"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("in uninstall", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "dependencies": [{ "name": "hello", "source": "github.com/shykes/hello", "pin": "2d789671a44c4d559be506a9bc4b71b0ba6e23c9" }], "source": ".", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("uninstall", "hello"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})

	t.Run("in update", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		work := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.0.0"}`).
			WithNewFile("main.go", moduleSrc)

		work = work.With(daggerExec("update"))
		daggerJSON, err := work.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, engine.Version, gjson.Get(daggerJSON, "engineVersion").String())
	})
}

func (ModuleSuite) TestTypedefSourceMaps(ctx context.Context, t *testctx.T) {
	goBaseSrc := `package main

type Test struct {}
    `

	tsBaseSrc := `import { object, func } from "@dagger.io/dagger"

@object()
export class Test {}`

	type languageMatch struct {
		golang     []string
		typescript []string
	}

	tcs := []struct {
		sdk     string
		src     string
		matches languageMatch
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
			matches: languageMatch{
				golang: []string{
					// struct
					`\ntype Dep struct { // dep \(../../dep/main.go:5:6\)\n`,
					// struct field
					`\nfunc \(.* \*Dep\) FieldDef\(.* // dep \(../../dep/main.go:6:5\)\n`,
					// struct func
					`\nfunc \(.* \*Dep\) FuncDef\(.* // dep \(../../dep/main.go:9:1\)\n`,
					// struct func arg
					`\n\s*Arg2 string // dep \(../../dep/main.go:11:2\)\n`,

					// enum
					`\ntype DepMyEnum string // dep \(../../dep/main.go:16:6\)\n`,
					// enum value
					`\n\s*DepMyEnumA DepMyEnum = "MyEnumA" // dep \(../../dep/main.go:18:5\)\n`,

					// interface
					`\ntype DepMyInterface struct { // dep \(../../dep/main.go:22:6\)\n`,
					// interface func
					`\nfunc \(.* \*DepMyInterface\) Do\(.* // dep \(../../dep/main.go:24:4\)\n`,
				},
				typescript: []string{
					// struct
					`export class Dep extends BaseClient { // dep \(../../../dep/main.go:5:6\)`,
					// struct field
					`fieldDef = async \(\): Promise<string> => { // dep \(../../../dep/main.go:6:5\)`,
					// struct func
					`\s*funcDef = async \(.*\s*opts\?: .* \/\/ dep \(../../../dep/main.go:9:1\) *\s*.*\/\/ dep \(../../../dep/main.go:9:1\)`,
					// struct func arg
					`\s*arg2\?: string // dep \(../../../dep/main.go:11:2\)`,

					// enum
					`export enum DepMyEnum { // dep \(../../../dep/main.go:16:6\)`,
					// enum value
					`\s*A = "MyEnumA", // dep \(../../../dep/main.go:18:5\)`,
				},
			},
		},
		{
			sdk: "typescript",
			src: `import { object, func } from "@dagger.io/dagger"

export enum MyEnum {
  A = "MyEnumA",
	B = "MyEnumB",
}

@object()
export class Dep {
  @func()
  fieldDef: string

  @func()
  funcDef(arg1: string, arg2?: string): string {
    return ""
  }

	@func()
	async collect(enumValue: MyEnum): Promise<void> {}
}`,
			matches: languageMatch{
				golang: []string{
					// struct
					`\ntype Dep struct { // dep \(../../dep/src/index.ts:9:14\)\n`,
					// struct field
					`\nfunc \(.* \*Dep\) FieldDef\(.* // dep \(../../dep/src/index.ts:11:3\)\n`,
					// struct func
					`\nfunc \(.* \*Dep\) FuncDef\(.* // dep \(../../dep/src/index.ts:14:3\)\n`,
					// struct func arg
					`\n\s*Arg2 string // dep \(../../dep/src/index.ts:14:25\)\n`,

					// enum
					`\ntype DepMyEnum string // dep \(../../dep/src/index.ts:3:13\)\n`,
					// enum value
					`\n\s*DepMyEnumA DepMyEnum = "MyEnumA" // dep \(../../dep/src/index.ts:4:3\)\n`,
				},
				typescript: []string{
					// struct
					`export class Dep extends BaseClient { // dep \(../../../dep/src/index.ts:9:14\)`,
					// struct field
					`\s*fieldDef = async \(\): Promise<string> => { // dep \(../../../dep/src/index.ts:11:3\)`,
					// struct func
					`\s*funcDef = async \(.*\s*opts\?: .* \/\/ dep \(../../../dep/src/index.ts:14:3\) *\s*.*\/\/ dep \(../../../dep/src/index.ts:14:3\)`,
					// struct func arg
					`\s*arg2\?: string // dep \(../../../dep/src/index.ts:14:25\)`,

					// enum
					`export enum DepMyEnum { // dep \(../../../dep/src/index.ts:3:13\)`,
					// enum value
					`\s*A = "MyEnumA", // dep \(../../../dep/src/index.ts:4:3\)`,
				},
			},
		},
	}

	for _, tc := range tcs {
		t.Run(fmt.Sprintf("%s dep with go generation", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := modInit(t, c, "go", goBaseSrc).
				With(withModInitAt("./dep", tc.sdk, tc.src)).
				With(daggerExec("install", "./dep"))

			codegenContents, err := modGen.File(sdkCodegenFile(t, "go")).Contents(ctx)
			require.NoError(t, err)

			for _, match := range tc.matches.golang {
				matched, err := regexp.MatchString(match, codegenContents)
				require.NoError(t, err)
				require.Truef(t, matched, "%s did not match contents:\n%s", match, codegenContents)
			}
		})

		t.Run(fmt.Sprintf("%s dep with typescript generation", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := modInit(t, c, "typescript", tsBaseSrc).
				With(withModInitAt("./dep", tc.sdk, tc.src)).
				With(daggerExec("install", "./dep"))

			codegenContents, err := modGen.File(sdkCodegenFile(t, "typescript")).Contents(ctx)
			require.NoError(t, err)

			for _, match := range tc.matches.typescript {
				matched, err := regexp.MatchString(match, codegenContents)
				require.NoError(t, err)
				require.Truef(t, matched, "%s did not match contents:\n%s", match, codegenContents)
			}
		})
	}
}

func (ModuleSuite) TestSelfCalls(ctx context.Context, t *testctx.T) {
	tcs := []struct {
		sdk    string
		source string
	}{
		{
			sdk: "go",
			source: `package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) ContainerEcho(
	// +optional
	// +default="Hello Self Calls"
	stringArg string,
) *dagger.Container {
	return dag.Container().From("alpine:latest").WithExec([]string{"echo", stringArg})
}

func (m *Test) Print(ctx context.Context, stringArg string) (string, error) {
	return dag.Test().ContainerEcho(dagger.TestContainerEchoOpts{
		StringArg: stringArg,
	}).Stdout(ctx)
}

func (m *Test) PrintDefault(ctx context.Context) (string, error) {
	return dag.Test().ContainerEcho().Stdout(ctx)
}
`,
		},
		//		{
		//			sdk: "typescript",
		//			source: `import { dag, Container, object, func } from "@dagger.io/dagger"
		//
		// @object()
		// export class Test {
		//   /**
		//    * Returns a container that echoes whatever string argument is provided
		//    */
		//   @func()
		//   containerEcho(stringArg: string = "Hello Self Calls"): Container {
		//     return dag.container().from("alpine:latest").withExec(["echo", stringArg])
		//   }
		//
		//   @func()
		//   async print(stringArg: string): Promise<string> {
		//     return dag.test().containerEcho({stringArg}).stdout()
		//   }
		//
		//   @func()
		//   async printDefault(): Promise<string> {
		//     return dag.test().containerEcho().stdout()
		//   }
		// }
		// `,
		//		},
		//		{
		//			sdk: "python",
		//			source: `import dagger
		// from dagger import dag, function, object_type
		//
		// @object_type
		// class Test:
		//     @function
		//     def container_echo(self, string_arg: str = "Hello Self Calls") -> dagger.Container:
		//         return dag.container().from_("alpine:latest").with_exec(["echo", string_arg])
		//
		//     @function
		//     async def print(self, string_arg: str) -> str:
		//         return await dag.test().container_echo(string_arg=string_arg).stdout()
		//
		//     @function
		//     async def print_default(self) -> str:
		//         return await dag.test().container_echo().stdout()
		// `,
		//		},
	}

	for _, tc := range tcs {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := modInit(t, c, tc.sdk, tc.source, "--with-self-calls")

			t.Run("can call with arguments", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.
					With(daggerQuery(`{test{print(stringArg:"hello")}}`)).
					Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `{"test":{"print":"hello\n"}}`, out)
			})

			t.Run("can call with optional arguments", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.
					With(daggerQuery(`{test{printDefault}}`)).
					Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `{"test":{"printDefault":"Hello Self Calls\n"}}`, out)
			})
		})
	}
}

func (ModuleSuite) TestModuleDeprecationIntrospection(ctx context.Context, t *testctx.T) {
	type sdkCase struct {
		sdk        string
		writeFiles func(dir string) error
	}

	goSrc := `package main

import (
	"context"
)

// +deprecated="This module is deprecated and will be removed in future versions."
type Test struct {
	LegacyField string // +deprecated="This field is deprecated and will be removed in future versions."
}

// +deprecated="This type is deprecated and kept only for retro-compatibility."
type LegacyRecord struct {
	// +deprecated="This field is deprecated and will be removed in future versions."
	Note string
}

func (m *Test) EchoString(
	ctx context.Context,
	input *string, // +deprecated="Use 'other' instead of 'input'."
	other string,
) (string, error) {
	if input != nil {
		return *input, nil
	}
	return other, nil
}

// +deprecated="Prefer EchoString instead."
func (m *Test) LegacySummarize(note string) (LegacyRecord, error) {
	return LegacyRecord{Note: note}, nil
}

type Mode string

const (
	ModeAlpha Mode = "alpha" // +deprecated="alpha is deprecated; use zeta instead"
	// +deprecated="beta is deprecated; use zeta instead"
	ModeBeta Mode = "beta"
	ModeZeta Mode = "zeta"
)

// Reference the enum so it appears in the schema.
func (m *Test) UseMode(mode Mode) Mode {
	return mode
}

type Fooer interface {
	DaggerObject

	// +deprecated="Use Bar instead"
	Foo(ctx context.Context, value int) (string, error)

	Bar(ctx context.Context, value int) (string, error)
}

func (m *Test) CallFoo(ctx context.Context, foo Fooer, value int) (string, error) {
	return foo.Foo(ctx, value)
}`
	const tsSrc = `import { field, func, object } from "@dagger.io/dagger"

  /** @deprecated This module is deprecated and will be removed in future versions. */
  @object()
  export class Test {
    /** @deprecated This field is deprecated and will be removed in future versions. */
    @field()
    legacyField = "legacy"

    @func()
    async echoString(
	  other: string,
      /** @deprecated Use 'other' instead of 'input'. */
      input?: string,
    ): Promise<string> {
      return input ?? other
    }

    /** @deprecated Prefer EchoString instead. */
    @func()
    async legacySummarize(note: string): Promise<LegacyRecord> {
      return { note }
    }

    @func()
    useMode(mode: Mode): Mode {
      return mode
    }

	@func()
	async callFoo(foo: Fooer, value: number): Promise<string> {
		return foo.foo(value)
	}
  }

  /** @deprecated This type is deprecated and kept only for retro-compatibility. */
  export type LegacyRecord = {
    /** @deprecated This field is deprecated and will be removed in future versions. */
    note: string
  }

  export enum Mode {
    /** @deprecated alpha is deprecated; use zeta instead */
    Alpha = "alpha",
    /** @deprecated beta is deprecated; use zeta instead */
    Beta = "beta",
    Zeta = "zeta",
  }

  export interface Fooer {
    /** @deprecated Use Bar instead */
    foo(value: number): Promise<string>
    
    bar(value: number): Promise<string>
  }`

	const pySrc = `import enum
import typing
from typing import Annotated, Optional

import dagger

@dagger.object_type(
    deprecated="This module is deprecated and will be removed in future versions."
)
class Test:
    legacy_field: str = dagger.field(
        name="legacyField",
        deprecated="This field is deprecated and will be removed in future versions.",
    )

    @dagger.function(name="echoString")
    def echo_string(
        self,
        input: Annotated[
            Optional[str], dagger.Deprecated("Use 'other' instead of 'input'.")
        ],
        other: str,
    ) -> str:
        return input if input is not None else other

    @dagger.function(name="legacySummarize", deprecated="Prefer EchoString instead.")
    def legacy_summarize(self, note: str) -> "LegacyRecord":
        return LegacyRecord(note=note)

    @dagger.function(name="useMode")
    def use_mode(self, mode: "Mode") -> "Mode":
        return mode

    @dagger.function(name="callFoo")
    async def call_foo(self, foo: "Fooer", value: int) -> str:
        return await foo.foo(value)



@dagger.object_type(
    deprecated="This type is deprecated and kept only for retro-compatibility."
)
class LegacyRecord:
    note: str = dagger.field(
        deprecated="This field is deprecated and will be removed in future versions."
    )


@dagger.enum_type
class Mode(enum.Enum):
    """Mode is deprecated; use zeta instead."""

    ALPHA = "alpha"
    """Alpha mode.

    .. deprecated:: alpha is deprecated; use zeta instead
    """

    BETA = "beta"
    """Beta mode.

    .. deprecated:: beta is deprecated; use zeta instead
    """

    ZETA = "zeta"
    """ infos """

@dagger.interface
class Fooer(typing.Protocol):
    @dagger.function(deprecated="Use Bar instead")
    async def foo(self, value: int) -> str: ...

    @dagger.function()
    async def bar(self, value: int) -> str: ...
`

	cases := []sdkCase{
		{
			sdk: "go",
			writeFiles: func(dir string) error {
				return os.WriteFile(filepath.Join(dir, "main.go"), []byte(goSrc), 0o644)
			},
		},
		{
			sdk: "typescript",
			writeFiles: func(dir string) error {
				srcDir := filepath.Join(dir, "src")
				if err := os.MkdirAll(srcDir, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(srcDir, "index.ts"), []byte(tsSrc), 0o644)
			},
		},
		{
			sdk: "python",
			writeFiles: func(dir string) error {
				pyDir := filepath.Join(dir, "src", "test")
				if err := os.MkdirAll(pyDir, 0o755); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(pyDir, "__init__.py"), []byte(pySrc), 0o644)
			},
		},
	}

	type Arg struct {
		Name       string
		Deprecated string
	}
	type Fn struct {
		Name       string
		Deprecated string
		Args       []Arg
	}
	type Field struct {
		Name       string
		Deprecated string
	}
	type Obj struct {
		Name       string
		Deprecated string
		Functions  []Fn
		Fields     []Field
	}
	type EnumMember struct {
		Value      string
		Deprecated string
	}
	type Enum struct {
		Name    string
		Members []EnumMember
	}
	type Iface struct {
		Name      string
		Functions []Fn
	}
	type Resp struct {
		Host struct {
			Directory struct {
				AsModule struct {
					Objects    []struct{ AsObject Obj }
					Enums      []struct{ AsEnum Enum }
					Interfaces []struct{ AsInterface Iface }
				}
			}
		}
	}

	const introspect = `
query ModuleIntrospection($path: String!) {
  host {
    directory(path: $path) {
      asModule {
        objects {
          asObject {
            name
            deprecated
            functions { name deprecated args { name deprecated } }
            fields { name deprecated }
          }
        }
        enums { asEnum { name members { value deprecated } } }
        interfaces {
          asInterface {
            name
            functions { name deprecated args { name } }
          }
        }
      }
    }
  }
}`

	for _, tc := range cases {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			modDir := t.TempDir()

			_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk="+tc.sdk)
			require.NoError(t, err)
			require.NoError(t, tc.writeFiles(modDir))

			c := connect(ctx, t)

			res, err := testutil.QueryWithClient[Resp](c, t, introspect, &testutil.QueryOptions{
				Variables: map[string]any{"path": modDir},
			})
			require.NoError(t, err)

			var testObj, legacyObj *Obj
			for i := range res.Host.Directory.AsModule.Objects {
				o := &res.Host.Directory.AsModule.Objects[i].AsObject
				switch o.Name {
				case "Test":
					testObj = o
				case "TestLegacyRecord":
					legacyObj = o
				}
			}
			require.NotNil(t, testObj, "Test object must be present")
			require.Equal(t, "This module is deprecated and will be removed in future versions.", testObj.Deprecated, "Test object must be marked deprecated")

			legacyField := &testObj.Fields[0]
			require.NotNil(t, legacyField, "Test.LegacyField must be present")
			require.Equal(t, "This field is deprecated and will be removed in future versions.", legacyField.Deprecated, "Test.LegacyField must be marked deprecated")

			fnByName := map[string]Fn{}
			for _, f := range testObj.Functions {
				fnByName[f.Name] = f
			}

			ls, ok := fnByName["legacySummarize"]
			require.True(t, ok, "legacySummarize function must be present")
			require.Equal(t, "Prefer EchoString instead.", ls.Deprecated, "legacySummarize function must be marked deprecated")

			ech, ok := fnByName["echoString"]
			require.True(t, ok, "echoString function must be present")
			require.Empty(t, ech.Deprecated, "echoString function must not be deprecated")

			var inputArg, otherArg *Arg
			for i := range ech.Args {
				switch ech.Args[i].Name {
				case "input":
					inputArg = &ech.Args[i]
				case "other":
					otherArg = &ech.Args[i]
				}
			}
			require.NotNil(t, inputArg, "echoString should have arg 'input'")
			require.Equal(t, "Use 'other' instead of 'input'.", inputArg.Deprecated, "echoString.input should be marked deprecated")
			require.NotNil(t, otherArg, "echoString should have arg 'other'")
			require.Empty(t, otherArg.Deprecated, "echoString.other should NOT be deprecated")

			// Secondary object type + field deprecation: LegacyRecord.note
			require.NotNil(t, legacyObj, "LegacyRecord object must be present")
			require.Equal(t, "This type is deprecated and kept only for retro-compatibility.", legacyObj.Deprecated, "LegacyRecord must be marked deprecated")

			var noteField *Field
			for i := range legacyObj.Fields {
				if legacyObj.Fields[i].Name == "note" {
					noteField = &legacyObj.Fields[i]
					break
				}
			}
			require.NotNil(t, noteField, "LegacyRecord should have field 'note'")
			require.Equal(t, "This field is deprecated and will be removed in future versions.", noteField.Deprecated, "LegacyRecord.note must be marked deprecated")

			mode := &res.Host.Directory.AsModule.Enums[0]
			require.NotNil(t, mode)

			m := mode.AsEnum
			var alpha, beta, zeta *EnumMember
			for i := range m.Members {
				switch m.Members[i].Value {
				case "alpha":
					alpha = &m.Members[i]
				case "beta":
					beta = &m.Members[i]
				case "zeta":
					zeta = &m.Members[i]
				}
			}
			require.NotNil(t, alpha, "Mode should have member 'alpha'")
			require.Equal(t, "alpha is deprecated; use zeta instead", alpha.Deprecated, "Mode.alpha must be marked deprecated")
			require.NotNil(t, beta, "Mode should have member 'beta'")
			require.Equal(t, "beta is deprecated; use zeta instead", beta.Deprecated, "Mode.beta must be marked deprecated")
			require.NotNil(t, zeta, "Mode should have member 'zeta'")
			require.Empty(t, zeta.Deprecated, "Mode.zeta should NOT be deprecated")

			// Interface presence + deprecation on its method
			var fooer *Iface
			for i := range res.Host.Directory.AsModule.Interfaces {
				iFace := &res.Host.Directory.AsModule.Interfaces[i].AsInterface
				if iFace.Name == "TestFooer" {
					fooer = iFace
					break
				}
			}
			require.NotNil(t, fooer, "test interface must be present")

			fnByNameIface := map[string]Fn{}
			for _, f := range fooer.Functions {
				fnByNameIface[f.Name] = f
			}

			fooFn, ok := fnByNameIface["foo"]
			require.True(t, ok, "TestFooer.foo must be present")
			require.Equal(t, "Use Bar instead", fooFn.Deprecated, "TestFooer.foo must be marked deprecated")

			var valueArg *Arg
			for i := range fooFn.Args {
				if fooFn.Args[i].Name == "value" {
					valueArg = &fooFn.Args[i]
					break
				}
			}
			require.NotNil(t, valueArg, "TestFooer.foo must have arg 'value'")
		})
	}
}

func (ModuleSuite) TestModuleDeprecationValidationErrors(ctx context.Context, t *testctx.T) {
	const introspect = `
query ModuleIntrospection($path: String!) {
  host {
    directory(path: $path) {
      asModule {
        objects {
          asObject {
            name
            deprecated
            functions { name deprecated args { name deprecated } }
            fields { name deprecated }
          }
        }
        enums { asEnum { name members { value deprecated } } }
        interfaces {
          asInterface {
            name
            functions { name deprecated args { name } }
          }
        }
      }
    }
  }
}`

	invalidCases := []struct {
		sdk        string
		contents   string
		errorMatch string
	}{
		{
			sdk: "go",
			contents: `package main

import "context"

type Test struct{}

func (m *Test) Legacy(
	ctx context.Context,
	input string, // +deprecated="Use other instead"
	other string,
) (string, error) {
	return input, nil
}
`,
			errorMatch: "argument \"input\" on Test.Legacy is required and cannot be deprecated",
		},
		{
			sdk: "typescript",
			contents: `import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  async legacy(
    /** @deprecated Use 'other' instead. */
    input: string,
    other: string,
  ): Promise<string> {
    return input
  }
}
`,
			errorMatch: "argument input is required and cannot be deprecated",
		},
		{
			sdk: "python",
			contents: `import dagger
from typing import Annotated


@dagger.object_type
class Test:
    @dagger.function
    def legacy(
        self,
        input: Annotated[str, dagger.Deprecated("Use other instead")],
        other: str,
    ) -> str:
        return input
`,
			errorMatch: "Can't deprecate required parameter 'input'",
		},
	}

	type Arg struct {
		Name       string
		Deprecated string
	}
	type Fn struct {
		Name       string
		Deprecated string
		Args       []Arg
	}
	type Field struct {
		Name       string
		Deprecated string
	}
	type Obj struct {
		Name       string
		Deprecated string
		Functions  []Fn
		Fields     []Field
	}
	type EnumMember struct {
		Value      string
		Deprecated string
	}
	type Enum struct {
		Name    string
		Members []EnumMember
	}
	type Iface struct {
		Name      string
		Functions []Fn
	}
	type Resp struct {
		Host struct {
			Directory struct {
				AsModule struct {
					Objects    []struct{ AsObject Obj }
					Enums      []struct{ AsEnum Enum }
					Interfaces []struct{ AsInterface Iface }
				}
			}
		}
	}

	for _, tc := range invalidCases {
		t.Run(fmt.Sprintf("%s rejects deprecated required arguments", tc.sdk), func(ctx context.Context, t *testctx.T) {
			modDir := t.TempDir()

			_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk="+tc.sdk)
			require.NoError(t, err)

			target := filepath.Join(modDir, sdkSourceFile(tc.sdk))
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
			require.NoError(t, os.WriteFile(target, []byte(tc.contents), 0o644))

			c := connect(ctx, t)

			_, err = testutil.QueryWithClient[Resp](c, t, introspect, &testutil.QueryOptions{
				Variables: map[string]any{"path": modDir},
			})
			require.Error(t, err)

			errMsg := err.Error()
			var execErr *dagger.ExecError
			if errors.As(err, &execErr) {
				errMsg = fmt.Sprintf("%s\nStdout: %s\nStderr: %s", err, execErr.Stdout, execErr.Stderr)
			}

			if strings.Contains(errMsg, "failed to run command [docker info]") ||
				strings.Contains(errMsg, "socket: operation not permitted") ||
				strings.Contains(errMsg, "permission denied while trying to connect to the Docker daemon") {
				t.Skipf("engine unavailable: %s", errMsg)
				return
			}

			require.Containsf(t, errMsg, tc.errorMatch, "unexpected error message: %s", errMsg)
		})
	}

	validCases := []struct {
		sdk      string
		contents string
	}{
		{
			sdk: "go",
			contents: `package main

import "context"

type Test struct{}

func (m *Test) Legacy(
	ctx context.Context,
	input string, // +default="\"legacy\"" +deprecated="Use other instead"
	other string,
) (string, error) {
	return input, nil
}
`,
		},
		{
			sdk: "go",
			contents: `package main

import "context"

type Test struct{}

func (m *Test) Legacy(
	ctx context.Context,
	input ...string, // +deprecated="Use other instead"
) (string, error) {
	if len(input) > 0 {
		return input[0], nil
	}
	return "", nil
}
`,
		},
		// todo(guillaume): re-enable once we have a way to resolve external libs default values in TS
		// https://github.com/dagger/dagger/pull/11319
		// 		{
		// 			sdk: "typescript",
		// 			contents: `import { func, object } from "@dagger.io/dagger"

		// @object()
		// export class Test {
		//   @func()
		//   async legacy(
		//     /** @deprecated Use 'other' instead. */
		//     input: string = "legacy",
		//     other: string,
		//   ): Promise<string> {
		//     return input
		//   }
		// }
		// `,
		// 		},
		{
			sdk: "typescript",
			contents: `import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  async legacy(
    /** @deprecated Prefer providing inputs via 'other'. */
    ...input: string[]
  ): Promise<string> {
    return input[0] ?? ""
  }
}
`,
		},
		{
			sdk: "python",
			contents: `import dagger
from typing import Annotated


@dagger.object_type
class Test:
    @dagger.function
    def legacy(
        self,
        input: Annotated[str, dagger.Deprecated("Use other instead")] = "legacy",
        other: str = "other",
    ) -> str:
        return input
`,
		},
	}

	for _, tc := range validCases {
		t.Run(fmt.Sprintf("%s allows deprecated optional arguments", tc.sdk), func(ctx context.Context, t *testctx.T) {
			modDir := t.TempDir()

			_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk="+tc.sdk)
			require.NoError(t, err)

			target := filepath.Join(modDir, sdkSourceFile(tc.sdk))
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
			require.NoError(t, os.WriteFile(target, []byte(tc.contents), 0o644))

			c := connect(ctx, t)

			_, err = testutil.QueryWithClient[Resp](c, t, introspect, &testutil.QueryOptions{
				Variables: map[string]any{"path": modDir},
			})
			if err != nil {
				errMsg := err.Error()
				if strings.Contains(errMsg, "failed to run command [docker info]") ||
					strings.Contains(errMsg, "socket: operation not permitted") ||
					strings.Contains(errMsg, "permission denied while trying to connect to the Docker daemon") {
					t.Skipf("engine unavailable: %s", errMsg)
					return
				}
			}
			require.NoError(t, err)
		})
	}
}

func (ModuleSuite) TestLoadWhenNoModule(ctx context.Context, t *testctx.T) {
	// verify that if a module is loaded from a directory w/ no module we don't
	// load extra files
	c := connect(ctx, t)

	tmpDir := t.TempDir()
	fileName := "foo"
	filePath := filepath.Join(tmpDir, fileName)
	require.NoError(t, os.WriteFile(filePath, []byte("foo"), 0o644))

	ents, err := c.ModuleSource(tmpDir).ContextDirectory().Entries(ctx)
	require.NoError(t, err)
	require.Empty(t, ents)
}

func (ModuleSuite) TestSSHAgentConnection(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("ConcurrentSetupAndCleanup", func(ctx context.Context, t *testctx.T) {
			var wg sync.WaitGroup
			for range 100 {
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
	tc := getVCSTestCase(t, "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git")

	t.Run("SSH auth with home expansion and symlink", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		privateSetup, cleanup := privateRepoSetup(c, t, tc)
		defer cleanup()

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(privateSetup).
			WithExec([]string{"mkdir", "-p", "/home/dagger"}).
			WithEnvVariable("HOME", "/home/dagger").
			WithExec([]string{"ln", "-s", "/sock/unix-socket", "/home/dagger/.ssh-sock"}).
			WithEnvVariable("SSH_AUTH_SOCK", "~/.ssh-sock")

		out, err := ctr.
			WithWorkdir("/work/some/subdir").
			WithExec([]string{"mkdir", "-p", "/home/dagger"}).
			WithExec([]string{"sh", "-c", "cd", "/work/some/subdir"}).
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")
	})

	t.Run("SSH auth from different relative paths", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		privateSetup, cleanup := privateRepoSetup(c, t, tc)
		defer cleanup()

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			With(privateSetup).
			WithExec([]string{"mkdir", "-p", "/work/subdir"})

		// Test from same directory as the socket
		out, err := ctr.
			WithWorkdir("/sock").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")

		// Test from a subdirectory
		out, err = ctr.
			WithWorkdir("/work/subdir").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines = strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")

		// Test from parent directory
		out, err = ctr.
			WithWorkdir("/").
			With(daggerFunctions("-m", tc.gitTestRepoRef)).
			Stdout(ctx)
		require.NoError(t, err)
		lines = strings.Split(out, "\n")
		require.Contains(t, lines, "fn     -")
	})
}

func (ModuleSuite) TestPrivateDeps(ctx context.Context, t *testctx.T) {
	t.Run("golang", func(ctx context.Context, t *testctx.T) {
		privateDepCode := `package main

import (
	"github.com/dagger/dagger-test-modules/privatedeps/pkg/cooldep"
)

type Foo struct{}

// Returns a container that echoes whatever string argument is provided
func (m *Foo) HowCoolIsDagger() string {
	return cooldep.HowCoolIsThat
}
`

		daggerjson := `{
  "name": "foo",
  "engineVersion": "v0.16.2",
  "sdk": {
    "source": "go",
    "config": {
      "goprivate": "github.com/dagger/dagger-test-modules"
    }
  }
}`

		c := connect(ctx, t)
		sockPath, cleanup := setupPrivateRepoSSHAgent(t)
		defer cleanup()

		socket := c.Host().UnixSocket(sockPath)

		// This is simulating a user's setup where they have
		// 1. ssh auth sock setup
		// 2. gitconfig file with insteadOf directive
		// 3. a dagger module that requires a dependency (NOT a dagger dependency) from a remote private repo.
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithExec([]string{"apk", "add", "git", "openssh", "openssl"}).
			WithUnixSocket("/sock/unix-socket", socket).
			WithEnvVariable("SSH_AUTH_SOCK", "/sock/unix-socket").
			WithWorkdir("/work").
			WithNewFile("/root/.gitconfig", `
[url "ssh://git@github.com/"]
	insteadOf = https://github.com/
`).
			With(daggerExec("init", "--name=foo", "--sdk=go", "--source=.")).
			WithNewFile("main.go", privateDepCode).
			WithNewFile("dagger.json", daggerjson)

		howCoolIsDagger, err := modGen.
			With(daggerExec("call", "how-cool-is-dagger")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "ubercool", howCoolIsDagger)
	})
}

func (ModuleSuite) TestDefaultPathNoCache(ctx context.Context, t *testctx.T) {
	t.Run("sources are reloaded when changed with defaultPath", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()

		_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		initialContent := "initial content"
		testFilePath := filepath.Join(modDir, "test-file.txt")
		err = os.WriteFile(testFilePath, []byte(initialContent), 0o644)
		require.NoError(t, err)

		moduleSrc := `package main

import (
       "context"
       "dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) ReadFile(
       ctx context.Context,
       // +defaultPath="."
       dir *dagger.Directory,
) (string, error) {
       return dir.File("test-file.txt").Contents(ctx)
}
`
		err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(moduleSrc), 0o644)
		require.NoError(t, err)

		// it's critical that we re-use a single session here like shell/prompt
		c := connect(ctx, t)

		err = c.ModuleSource(modDir).AsModule().Serve(ctx)
		require.NoError(t, err)

		res1, err := testutil.QueryWithClient[struct {
			Test struct {
				ReadFile string
			}
		}](c, t, `{test{readFile}}`, nil)
		require.NoError(t, err)
		require.Equal(t, initialContent, res1.Test.ReadFile)

		newContent := "updated content"
		err = os.WriteFile(testFilePath, []byte(newContent), 0o644)
		require.NoError(t, err)

		res2, err := testutil.QueryWithClient[struct {
			Test struct {
				ReadFile string
			}
		}](c, t, `{test{readFile}}`, nil)
		require.NoError(t, err)
		require.Equal(t, newContent, res2.Test.ReadFile)
	})
}

func (ModuleSuite) TestLargeErrors(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()

	_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
	require.NoError(t, err)

	moduleSrc := `package main

import (
  "context"
)

type Test struct{}

func (m *Test) RunNoisy(ctx context.Context) error {
	_, err := dag.Container().
		From("` + alpineImage + `").
		WithExec([]string{"sh", "-c", ` + "`" + `
			for i in $(seq 100); do
				for j in $(seq 1024); do
					echo -n x
					echo -n y >/dev/stderr
				done
				echo
			done
			exit 42
		` + "`" + `}).
		Sync(ctx)
	return err
}
`
	err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(moduleSrc), 0o644)
	require.NoError(t, err)

	c := connect(ctx, t)

	err = c.ModuleSource(modDir).AsModule().Serve(ctx)
	require.NoError(t, err)

	_, err = testutil.QueryWithClient[struct {
		Test struct {
			RunNoisy any
		}
	}](c, t, `{test{runNoisy}}`, nil)
	var execError *dagger.ExecError
	require.ErrorAs(t, err, &execError)

	// if we get `2` here, that means we're getting the less helpful error:
	// process "/runtime" did not complete successfully: exit code: 2
	require.Equal(t, 42, execError.ExitCode)
	require.Contains(t, execError.Stdout, "xxxxx")
	require.Contains(t, execError.Stderr, "yyyyy")
}

func (ModuleSuite) TestReturnNil(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {
}

func (m *Test) Nothing() (*dagger.Directory, error) {
	return nil, nil
}
`,
		)

	_, err := modGen.With(daggerQuery(`{test{nothing{id}}}`)).Stdout(ctx)
	require.NoError(t, err)
}

func (ModuleSuite) TestFunctionCacheControl(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		sdk    string
		source string
	}{
		{
			// TODO: add test that function doc strings still get parsed correctly, don't include //+ etc.
			sdk: "go",
			source: `package main

import (
	"crypto/rand"
)

type Test struct{}

// My cool doc on TestTtl
// +cache="40s"
func (m *Test) TestTtl() string {
	return rand.Text()
}

// My dope doc on TestCachePerSession
// +cache="session"
func (m *Test) TestCachePerSession() string {
	return rand.Text()
}

// My darling doc on TestNeverCache
// +cache="never"
func (m *Test) TestNeverCache() string {
	return rand.Text()
}

// My rad doc on TestAlwaysCache
func (m *Test) TestAlwaysCache() string {
	return rand.Text()
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
import random
import string

@dagger.object_type
class Test:
		@dagger.function(cache="40s")
		def test_ttl(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))

		@dagger.function(cache="session")
		def test_cache_per_session(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))

		@dagger.function(cache="never")
		def test_never_cache(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))

		@dagger.function
		def test_always_cache(self) -> str:
				return ''.join(random.choices(string.ascii_lowercase + string.digits, k=10))
`,
		},

		{
			sdk: "typescript",
			source: `
import crypto from "crypto"

import {  object, func } from "@dagger.io/dagger"

@object()
export class Test {
	@func({ cache: "40s"})
	testTtl(): string {
		return crypto.randomBytes(16).toString("hex")
	}

	@func({ cache: "session" })
	testCachePerSession(): string {
		return crypto.randomBytes(16).toString("hex")
	}

	@func({ cache: "never" })
	testNeverCache(): string {
		return crypto.randomBytes(16).toString("hex")
	}

	@func()
	testAlwaysCache(): string {
		return crypto.randomBytes(16).toString("hex")
	}
}

`,
		},
	} {
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			t.Run("always cache", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				// TODO: this is gonna be flaky to cache prunes, might need an isolated engine

				out1, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()). // don't cache the nested execs themselves
					With(daggerCall("test-always-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-always-cache")).Stdout(ctx)
				require.NoError(t, err)

				require.Equal(t, out1, out2, "outputs should be equal since the result is always cached")
			})

			t.Run("cache per session", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				out1a, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				out1b, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, out1a, out1b, "outputs should be equal since they are from the same session")
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2a, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				out2b, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-cache-per-session")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, out2a, out2b, "outputs should be equal since they are from the same session")

				require.NotEqual(t, out1a, out2a, "outputs should not be equal since they are from different sessions")
			})

			t.Run("never cache", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				out1a, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				out1b, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out1a, out1b, "outputs should not be equal since they are never cached")
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2a, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				out2b, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-never-cache")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out2a, out2b, "outputs should not be equal since they are never cached")

				require.NotEqual(t, out1a, out2a, "outputs should not be equal since they are never cached")
			})

			// TODO: this is gonna be hella flaky probably, need isolated engine to combat pruning and probably more generous times...
			t.Run("cache ttl", func(ctx context.Context, t *testctx.T) {
				c1 := connect(ctx, t)
				modGen1 := modInit(t, c1, tc.sdk, tc.source)

				out1, err := modGen1.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c1.Close())

				c2 := connect(ctx, t)
				modGen2 := modInit(t, c2, tc.sdk, tc.source)

				out2, err := modGen2.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NoError(t, c2.Close())

				require.Equal(t, out1, out2, "outputs should be equal since the cache ttl has not expired")
				time.Sleep(41 * time.Second)

				c3 := connect(ctx, t)
				modGen3 := modInit(t, c3, tc.sdk, tc.source)

				out3, err := modGen3.
					WithEnvVariable("CACHE_BUST", rand.Text()).
					With(daggerCall("test-ttl")).Stdout(ctx)
				require.NoError(t, err)
				require.NotEqual(t, out1, out3, "outputs should not be equal since the cache ttl has expired")
			})
		})
	}

	// rest of tests are SDK agnostic so just test w/ go
	t.Run("setSecret invalidates cache", func(ctx context.Context, t *testctx.T) {
		const modSDK = "go"
		const modSrc = `package main

import (
	"crypto/rand"
	"dagger/test/internal/dagger"
)

type Test struct{}

func (m *Test) TestSetSecret() *dagger.Container {
	r := rand.Text()
	s := dag.SetSecret(r, r)
	return dag.Container().
		From("` + alpineImage + `").
		WithSecretVariable("TOP_SECRET", s)
}
`

		// in memory cache should be hit within a session, but
		// no cache hits across sessions should happen

		c1 := connect(ctx, t)
		modGen1 := modInit(t, c1, modSDK, modSrc)

		out1a, err := modGen1.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		out1b, err := modGen1.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out1a, out1b)
		require.NoError(t, c1.Close())

		c2 := connect(ctx, t)
		modGen2 := modInit(t, c2, modSDK, modSrc)

		out2a, err := modGen2.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		out2b, err := modGen2.
			WithEnvVariable("CACHE_BUST", rand.Text()).
			With(daggerCall("test-set-secret", "with-exec", "--args", `sh,-c,echo $TOP_SECRET | rev`)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, out2a, out2b)

		require.NotEqual(t, out1a, out2a)
	})

	t.Run("dependency contextual arg", func(ctx context.Context, t *testctx.T) {
		const modSDK = "go"
		const modSrc = `package main
import (
	"context"
	"dagger/test/internal/dagger"
)
type Test struct{}
func (m *Test) CallDep(ctx context.Context, cacheBust string) (*dagger.Directory, error) {
	return dag.Dep().Test().Sync(ctx)
}
func (m *Test) CallDepFile(ctx context.Context, cacheBust string) (*dagger.Directory, error) {
	return dag.Dep().TestFile().Sync(ctx)
}
`

		const depSrc = `package main
import (
	"dagger/dep/internal/dagger"
)
type Dep struct{}
func (m *Dep) Test() *dagger.Directory {
	return dag.Depdep().Test()
}
func (m *Dep) TestFile() *dagger.Directory {
	return dag.Depdep().TestFile()
}
`

		const depDepSrc = `package main
import (
	"crypto/rand"
	"dagger/depdep/internal/dagger"
)
type Depdep struct{}
func (m *Depdep) Test(
	// +defaultPath="."
	dir *dagger.Directory,
) *dagger.Directory {
	return dir.WithNewFile("rand.txt", rand.Text())
}
func (m *Depdep) TestFile(
	// +defaultPath="dagger.json"
	f *dagger.File,
) *dagger.Directory {
	return dag.Directory().
		WithFile("dagger.json", f).
		WithNewFile("rand.txt", rand.Text())
}
`

		getModGen := func(c *dagger.Client) *dagger.Container {
			return goGitBase(t, c).
				WithWorkdir("/work/depdep").
				With(daggerExec("init", "--name=depdep", "--sdk="+modSDK, "--source=.")).
				WithNewFile("/work/depdep/main.go", depDepSrc).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk="+modSDK, "--source=.")).
				With(daggerExec("install", "../depdep")).
				WithNewFile("/work/dep/main.go", depSrc).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk="+modSDK, "--source=.")).
				With(sdkSource(modSDK, modSrc)).
				With(daggerExec("install", "./dep"))
		}

		t.Run("dir", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			out1, err := getModGen(c1).
				With(daggerCall("call-dep", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.NoError(t, c1.Close())

			c2 := connect(ctx, t)
			out2, err := getModGen(c2).
				With(daggerCall("call-dep", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)

			require.Equal(t, out1, out2)
		})

		t.Run("file", func(ctx context.Context, t *testctx.T) {
			c1 := connect(ctx, t)
			out1, err := getModGen(c1).
				With(daggerCall("call-dep-file", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.NoError(t, c1.Close())

			c2 := connect(ctx, t)
			out2, err := getModGen(c2).
				With(daggerCall("call-dep-file", "--cache-bust", rand.Text(), "file", "--path", "rand.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)

			require.Equal(t, out1, out2)
		})
	})
}

func daggerExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerNonNestedExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.
			// Don't persist stable client id between runs. this matches the behavior
			// of actual nested execs. Stable client IDs on the filesystem don't work
			// when run inside layered containers that can branch off and run in parallel.
			WithEnvVariable("XDG_STATE_HOME", "/tmp").
			WithMountedTemp("/tmp").
			WithExec(append([]string{"dagger"}, args...), dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: false,
			})
	}
}

func daggerNonNestedRun(args ...string) dagger.WithContainerFunc {
	args = append([]string{"run"}, args...)

	return daggerNonNestedExec(args...)
}

func daggerClientInstall(generator string) dagger.WithContainerFunc {
	return daggerExec("client", "install", generator)
}

func daggerClientInstallAt(generator string, outputDirPath string) dagger.WithContainerFunc {
	return daggerExec("client", "install", generator, outputDirPath)
}

func daggerQuery(query string, args ...any) dagger.WithContainerFunc {
	return daggerQueryAt("", query, args...)
}

func daggerQueryAt(modPath string, query string, args ...any) dagger.WithContainerFunc {
	query = fmt.Sprintf(query, args...)
	return func(c *dagger.Container) *dagger.Container {
		execArgs := []string{"dagger", "query"}
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
		execArgs := []string{"dagger", "call"}
		if modPath != "" {
			execArgs = append(execArgs, "-m", modPath)
		}
		return c.WithExec(append(execArgs, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func privateRepoSetup(c *dagger.Client, t *testctx.T, tc vcsTestCase) (dagger.WithContainerFunc, func()) {
	var socket *dagger.Socket
	cleanup := func() {}
	if tc.sshKey {
		var sockPath string
		sockPath, cleanup = setupPrivateRepoSSHAgent(t)
		socket = c.Host().UnixSocket(sockPath)
	}

	return func(ctr *dagger.Container) *dagger.Container {
		if socket != nil {
			ctr = ctr.
				WithUnixSocket("/sock/unix-socket", socket).
				WithEnvVariable("SSH_AUTH_SOCK", "/sock/unix-socket")
		}
		if token := tc.token(); token != "" {
			ctr = ctr.
				WithNewFile("/tmp/git-config", makeGitCredentials("https://"+tc.expectedHost, "git", token)).
				WithEnvVariable("GIT_CONFIG_GLOBAL", "/tmp/git-config")
		}

		return ctr
	}, cleanup
}

func makeGitCredentials(url string, username string, token string) string {
	helper := fmt.Sprintf(`!f() { test "$1" = get && echo -e "password=%s\nusername=%s"; }; f`, token, username)

	contents := bytes.NewBuffer(nil)
	fmt.Fprintf(contents, "[credential %q]\n", url)
	fmt.Fprintf(contents, "\thelper = %q\n", helper)
	return contents.String()
}

func daggerFunctions(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "functions"}, args...), dagger.ContainerWithExecOpts{
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
	path := sdkSourceFile(sdk)
	if sdk == "python" && dir != "." && dir != "test" {
		path = strings.ReplaceAll(path, "test", dir)
	}
	return fileContents(filepath.Join(dir, path), contents)
}

func sdkSourceFile(sdk string) string {
	switch sdk {
	case "go":
		return "main.go"
	case "python":
		return "src/test/__init__.py"
	case "typescript":
		return "src/index.ts"
	case "java", "./sdk/java":
		return "src/main/java/io/dagger/modules/test/Test.java"
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
		return "sdk/client.gen.ts"
	default:
		panic(fmt.Errorf("unknown sdk %q", sdk))
	}
}

func modInit(t *testctx.T, c *dagger.Client, sdk, contents string, extra ...string) *dagger.Container {
	t.Helper()
	return goGitBase(t, c).
		With(func(ctr *dagger.Container) *dagger.Container {
			if sdk == "java" {
				// use the local SDK so that we can test non-released changes
				sdkSrc, err := filepath.Abs("../../sdk/java")
				require.NoError(t, err)
				ctr = ctr.WithMountedDirectory("sdk/java", c.Host().Directory(sdkSrc))
				sdk = "./sdk/java"
			}
			return ctr
		}).
		With(withModInit(sdk, contents, extra...))
}

func withModInit(sdk, contents string, extra ...string) dagger.WithContainerFunc {
	return withModInitAt(".", sdk, contents, extra...)
}

func withModInitAt(dir, sdk, contents string, extra ...string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		name := filepath.Base(dir)
		if name == "." {
			name = "test"
		}
		args := []string{"init", "--sdk=" + sdk, "--name=" + name, "--source=" + dir}
		args = append(args, extra...)
		args = append(args, dir)
		ctr = ctr.With(daggerExec(args...))
		if contents != "" {
			return ctr.With(sdkSourceAt(dir, sdk, contents))
		}
		return ctr
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
query { host { directory(path: ".") { asModule {
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
	interfaces {
	    asInterface {
			name
			description
			functions {
				name
				description
				args {
					name
					description
				}
            }
        }
    }
    enums {
        asEnum {
            name
            description
            members {
                name
				value
				description
			}
        }
    }
} } } }
`)

func inspectModule(ctx context.Context, t *testctx.T, ctr *dagger.Container) gjson.Result {
	t.Helper()
	out, err := ctr.With(moduleIntrospection).Stdout(ctx)
	require.NoError(t, err)
	result := gjson.Get(out, "host.directory.asModule")
	t.Logf("module introspection:\n%v", result.Raw)
	return result
}

func inspectModuleObjects(ctx context.Context, t *testctx.T, ctr *dagger.Container) gjson.Result {
	t.Helper()
	return inspectModule(ctx, t, ctr).Get("objects.#.asObject")
}

func inspectModuleInterfaces(ctx context.Context, t *testctx.T, ctr *dagger.Container) gjson.Result {
	t.Helper()
	return inspectModule(ctx, t, ctr).Get("interfaces.#.asInterface")
}

func goGitBase(t testing.TB, c *dagger.Client) *dagger.Container {
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
