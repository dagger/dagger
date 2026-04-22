package core

// Workspace alignment: mostly aligned; coverage targets post-workspace module definition semantics, but setup still relies on historical module helpers.
// Scope: SDK selection, module and object descriptions, field visibility, optional/default schema registration, codegen handling of optionals, and global dag references in module source.
// Intent: Keep core module authoring semantics stable while the historical umbrella suite is peeled into narrower module-owned files.

import (
	"context"
	"fmt"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func (ModuleSuite) TestInvalidSDK(ctx context.Context, t *testctx.T) {
	t.Run("invalid sdk returns readable error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=foo-bar"))

		_, err := modGen.
			With(daggerQuery(`{containerEcho(stringArg:"hello"){stdout}}`)).
			Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, `invalid SDK: "foo-bar"`)
	})

	t.Run("specifying version with either of go/python/typescript sdk returns error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=go@main"))

		_, err := modGen.
			With(daggerQuery(`{containerEcho(stringArg:"hello"){stdout}}`)).
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

			out, err := modGen.With(daggerQuery(`{set(foo: "abc", bar: "xyz"){hello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"set":{"hello": "abcxyz"}}`, out)

			out, err = modGen.With(daggerQuery(`{set(foo: "abc", bar: "xyz"){foo}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"set":{"foo": "abc"}}`, out)

			_, err = modGen.With(daggerQuery(`{set(foo: "abc", bar: "xyz"){bar}}`)).Stdout(ctx)
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
				With(daggerQuery(`{fn}`)).Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t, `{"fn":"foo\n"}`, out)
		})
	}
}
