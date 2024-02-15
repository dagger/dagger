package core

import (
	"context"
	"fmt"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
)

func TestModulePythonInit(t *testing.T) {
	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=python")).
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("with different root", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=python", "child")).
			With(daggerQueryAt("child", `{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("respects existing pyproject.toml", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("pyproject.toml", dagger.ContainerWithNewFileOpts{
				Contents: heredoc.Doc(`
                    [project]
                    name = "has-pyproject"
                    version = "0.0.0"
                `),
			}).
			With(daggerExec("init", "--name=hasPyproject", "--sdk=python"))

		out, err := modGen.
			With(daggerQuery(`{hasPyproject{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasPyproject":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("preserves module name", func(t *testing.T) {
			generated, err := modGen.File("pyproject.toml").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, `name = "has-pyproject"`)
		})
	})

	t.Run("respects existing main.py", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/src/main/__init__.py", dagger.ContainerWithNewFileOpts{
				Contents: "from . import notmain\n",
			}).
			WithNewFile("/work/src/main/notmain.py", dagger.ContainerWithNewFileOpts{
				Contents: heredoc.Doc(`
                    from dagger import function

                    @function
                    def hello() -> str:
                        return "Hello, world!"
                `),
			}).
			With(daggerExec("init", "--source=.", "--name=hasMainPy", "--sdk=python")).
			With(daggerQuery(`{hasMainPy{hello}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"hasMainPy":{"hello":"Hello, world!"}}`, out)
	})

	t.Run("uses expected field casing", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=hello-world", "--sdk=python")).
			With(pythonSource(`
                from dagger import field, function, object_type

                @object_type
                class HelloWorld:
                    my_name: str = field(default="World")

                    @function
                    def message(self) -> str:
                        return f"Hello, {self.my_name}!"
            `)).
			With(daggerQuery(`{helloWorld(myName: "Monde"){message}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"helloWorld":{"message":"Hello, Monde!"}}`, out)
	})
}

func TestModulePythonSignatures(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := pythonModInit(ctx, t, c, `
        from collections.abc import Sequence
        from typing import Optional

        from dagger import field, function, object_type

        @object_type
        class Test:
            @function
            def hello(self) -> str:
                return "hello"

            @function
            def hello_none(self) -> None:
                ...

            @function
            def hello_void(self):
                ...

            @function
            def echo(self, msg: str) -> str:
                return msg

            @function
            def echo_default(self, msg: str = "hello") -> str:
                return msg

            @function
            def echo_old_optional(self, msg: Optional[str] = None) -> str:
                return "hello" if msg is None else msg

            @function
            def echo_optional(self, msg: str | None = None) -> str:
                return "hello" if msg is None else msg

            @function
            def echo_sequence(self, msg: Sequence[str]) -> str:
               return self.echo("+".join(msg))

            @function
            def echo_tuple(self, msg: tuple[str, ...]) -> str:
                return self.echo_sequence(msg)

            @function
            def echo_list(self, msg: list[str]) -> str:
                return self.echo_sequence(msg)

            @function
            def echo_opts(self, msg: str, suffix: str = "", times: int = 1) -> str:
                return (msg + suffix) * times
    `)

	for _, tc := range []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "def () -> str",
			query:    `{test{hello}}`,
			expected: `{"test":{"hello":"hello"}}`,
		},
		{
			name:     "def () -> None",
			query:    `{test{helloNone}}`,
			expected: `{"test":{"helloNone":null}}`,
		},
		{
			name:     "def ()",
			query:    `{test{helloVoid}}`,
			expected: `{"test":{"helloVoid":null}}`,
		},
		{
			name:     "def (str) -> str",
			query:    `{test{echo(msg:"world")}}`,
			expected: `{"test":{"echo":"world"}}`,
		},
		{
			name:     "def (str = 'hello') -> str",
			query:    `{test{echoDefault}}`,
			expected: `{"test":{"echoDefault":"hello"}}`,
		},
		{
			name:     "def (str = 'hello') -> str: (bonjour)",
			query:    `{test{echoDefault(msg:"bonjour")}}`,
			expected: `{"test":{"echoDefault":"bonjour"}}`,
		},
		{
			name:     "def (str | None = None) -> str",
			query:    `{test{echoOptional}}`,
			expected: `{"test":{"echoOptional":"hello"}}`,
		},
		{
			name:     "def (Optional[str] = None) -> str",
			query:    `{test{echoOldOptional}}`,
			expected: `{"test":{"echoOldOptional":"hello"}}`,
		},
		{
			name:     "def (str | None = None) -> str: (bonjour)",
			query:    `{test{echoOptional(msg:"bonjour")}}`,
			expected: `{"test":{"echoOptional":"bonjour"}}`,
		},
		{
			name:     "sequence abc",
			query:    `{test{echoSequence(msg:["a", "b", "c"])}}`,
			expected: `{"test":{"echoSequence":"a+b+c"}}`,
		},
		{
			name:     "tuple",
			query:    `{test{echoTuple(msg:["a", "b", "c"])}}`,
			expected: `{"test":{"echoTuple":"a+b+c"}}`,
		},
		{
			name:     "list",
			query:    `{test{echoList(msg:["a", "b", "c"])}}`,
			expected: `{"test":{"echoList":"a+b+c"}}`,
		},
		{
			name:     "def (str, str, int) -> str",
			query:    `{test{echoOpts(msg:"hello", suffix:"!", times:3)}}`,
			expected: `{"test":{"echoOpts":"hello!hello!hello!"}}`,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := modGen.With(daggerQuery(tc.query)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, out)
		})
	}
}

func TestModulePythonSignaturesBuiltinTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := pythonModInit(ctx, t, c, `
        import dagger
        from dagger import field, function, object_type

        @object_type
        class Test:
            @function
            async def read(self, dir: dagger.Directory) -> str:
                return await dir.file("foo").contents()

            @function
            async def read_list(self, dir: list[dagger.Directory]) -> str:
                return await dir[0].file("foo").contents()

            @function
            async def read_optional(self, dir: dagger.Directory | None = None) -> str:
                return "" if dir is None else await dir.file("foo").contents()
    `)

	out, err := modGen.With(daggerQuery(`{directory{withNewFile(path: "foo", contents: "bar"){id}}}`)).Stdout(ctx)
	require.NoError(t, err)
	dirID := gjson.Get(out, "directory.withNewFile.id").String()

	for _, tc := range []struct {
		name     string
		query    string
		expected string
	}{
		{
			name:     "read",
			query:    fmt.Sprintf(`{test{read(dir: %q)}}`, dirID),
			expected: `{"test":{"read":"bar"}}`,
		},
		{
			name:     "read list",
			query:    fmt.Sprintf(`{test{readList(dir: [%q])}}`, dirID),
			expected: `{"test":{"readList":"bar"}}`,
		},
		{
			name:     "read optional",
			query:    fmt.Sprintf(`{test{readOptional(dir: %q)}}`, dirID),
			expected: `{"test":{"readOptional":"bar"}}`,
		},
		{
			name:     "read optional (default)",
			query:    `{test{readOptional}}`,
			expected: `{"test":{"readOptional":""}}`,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := modGen.With(daggerQuery(tc.query)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, out)
		})
	}
}

func TestModulePythonDocs(t *testing.T) {
	t.Parallel()

	t.Run("basic", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := pythonModInit(ctx, t, c, `
            from typing import Annotated

            from dagger.mod import Doc, function, object_type

            @object_type
            class Test:
                """Object docstring.

                Multiline.
                """

                @function
                def undoc(self, msg: str) -> str:
                    return msg

                @function
                def echo(self, msg: Annotated[str, Doc("the message to echo")] = "marco") -> str:
                    """Function docstring.

                    Multiline.
                    """
                    return msg

                @function(doc="overridden description")
                def over(self) -> str:
                    """Code-only docstring."""
                    return ""
        `)

		obj := inspectModuleObjects(ctx, t, modGen).Get("0")

		// NB: Should not end in a new line.
		require.Equal(t, "Object docstring.\n\nMultiline.", obj.Get("description").String())

		// test undocumented function
		undoc := obj.Get("functions.#(name=undoc)")
		require.Empty(t, undoc.Get("description").String())
		require.Empty(t, undoc.Get("args.0.description").String())
		require.Empty(t, undoc.Get("args.0.defaultValue").String())

		// test documented function
		echo := obj.Get("functions.#(name=echo)")
		require.Equal(t, "Function docstring.\n\nMultiline.", echo.Get("description").String())
		require.Equal(t, "msg", echo.Get("args.0.name").String())
		require.Equal(t, "the message to echo", echo.Get("args.0.description").String())
		require.Equal(t, "marco", echo.Get("args.0.defaultValue.@fromstr").String())

		// test function description override
		over := obj.Get("functions.#(name=over)")
		require.Equal(t, "overridden description", over.Get("description").String())
	})

	t.Run("autogenerated constructor", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := pythonModInit(ctx, t, c, `
            from dataclasses import field as datafield
            from typing import Annotated

            from dagger.mod import Doc, function, object_type, field

            @object_type
            class Test:
                """Object docstring."""
                undoc: str = field(default="")
                private: str = datafield(default=True, init=False)
                exposed: Annotated[str, Doc("field and init")] = field(default="hello")
                only_field: Annotated[bool, Doc("only field")] = field(default=True, init=False)
                only_init: Annotated[bool, Doc("only init")] = True
        `)

		obj := inspectModuleObjects(ctx, t, modGen).Get("0")

		expectedFields := []any{"undoc", "exposed", "onlyField"}
		expectedInitArgs := []any{"undoc", "exposed", "onlyInit"}

		require.EqualValues(t, expectedFields, obj.Get("fields.#.name").Value())
		require.EqualValues(t, expectedInitArgs, obj.Get("constructor.args.#.name").Value())

		require.Equal(t, "Object docstring.", obj.Get("description").String())
		require.Equal(t, "Object docstring.", obj.Get("constructor.description").String())

		require.Empty(t, obj.Get("fields.#(name=undoc).description").String())
		require.Empty(t, obj.Get("constructor.args.#(name=undoc).description").String())

		require.Equal(t, "field and init", obj.Get("fields.#(name=exposed).description").String())
		require.Equal(t, "field and init", obj.Get("constructor.args.#(name=exposed).description").String())
		require.Equal(t, "hello", obj.Get("constructor.args.#(name=exposed).defaultValue.@fromstr").String())

		require.Equal(t, "only field", obj.Get("fields.#(name=onlyField).description").String())
		require.Equal(t, "only init", obj.Get("constructor.args.#(name=onlyInit).description").String())

		require.True(t, obj.Get("constructor.args.#(name=onlyInit).defaultValue").Bool())
	})

	t.Run("alternative constructor", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := pythonModInit(ctx, t, c, `
            from typing import Annotated, Self

            from dagger.mod import Doc, function, object_type

            @object_type
            class Test:
                """the main object"""
                foo: str = ""

                @classmethod
                def create(cls, bar: Annotated[str, Doc("not foo")]) -> Self:
                    """factory constructor"""
                    return cls(foo=bar)
        `)

		cns := inspectModuleObjects(ctx, t, modGen).Get("0.constructor")

		require.EqualValues(t, []any{"bar"}, cns.Get("args.#.name").Value())
		require.Equal(t, "not foo", cns.Get("args.0.description").String())
		require.Equal(t, "factory constructor", cns.Get("description").String())
	})

	t.Run("external constructor", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := pythonModInit(ctx, t, c, `
            from typing import Annotated, Self

            from dagger.mod import Doc, function, object_type

            @object_type
            class External:
                """external docstring"""

                foo: Annotated[str, Doc("a foo walks into a bar")] = "bar"

                @function
                def bar(self) -> str:
                    return self.foo

            @object_type
            class Test:
                external = function(External)
                alternative = function(doc="still external")(External)
        `)

		obj := inspectModuleObjects(ctx, t, modGen).Get("#(name=Test)")

		require.Equal(t, "external docstring", obj.Get("functions.#(name=external).description").String())
		require.Equal(t, "still external", obj.Get("functions.#(name=alternative).description").String())

		// all functions point to the same constructor, with the same arguments
		obj.Get("functions.#.args|@flatten").ForEach(func(key, value gjson.Result) bool {
			require.Equal(t, "foo", value.Get("name").String())
			require.Equal(t, "a foo walks into a bar", value.Get("description").String())
			require.Equal(t, "bar", value.Get("defaultValue.@fromstr").String())
			return true
		})
	})

	t.Run("external alternative constructor", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := pythonModInit(ctx, t, c, `
            from typing import Annotated, Self

            from dagger.mod import Doc, function, object_type

            @object_type
            class External:
                """an object"""

                @classmethod
                def create(cls) -> Self:
                    """factory constructor"""
                    return cls()

            @object_type
            class Test:
                external = function(External)
            `)

		obj := inspectModuleObjects(ctx, t, modGen).Get("#(name=Test)")

		require.Equal(t, "factory constructor", obj.Get("functions.#(name=external).description").String())
	})

	t.Run("inheritance", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := pythonModInit(ctx, t, c, `
            from typing import Annotated, Self

            from dagger.mod import Doc, function, object_type

            class Base:
                """What's the object-oriented way to become wealthy?"""

                @classmethod
                def create(cls) -> Self:
                    """Inheritance."""
                    return cls()

            @object_type
            class Test(Base):
                ...
        `)

		obj := inspectModuleObjects(ctx, t, modGen).Get("#(name=Test)")

		require.Equal(t, "Inheritance.", obj.Get("constructor.description").String())
	})
}

func TestModulePythonNameOverrides(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := pythonModInit(ctx, t, c, `
        from typing import Annotated

        from dagger.mod import Arg, Doc, field, function, object_type

        @object_type
        class Test:
            field_: str = field(name="field")

            @function(name="func")
            def func_(self, arg_: Annotated[str, Arg(name="arg")] = "") -> str:
                return ""
        `)

	obj := inspectModuleObjects(ctx, t, modGen).Get("0")

	require.Equal(t, "field", obj.Get("fields.0.name").String())
	require.Equal(t, "func", obj.Get("functions.0.name").String())
	require.Equal(t, "arg", obj.Get("functions.0.args.0.name").String())
	require.Equal(t, "field", obj.Get("constructor.args.0.name").String())
}

func TestModulePythonReturnSelf(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	out, err := pythonModInit(ctx, t, c, `
        from typing import Self

        from dagger import field, function, object_type

        @object_type
        class Test:
            message: str = field(default="")

            @function
            def foo(self) -> Self:
                self.message = "bar"
                return self
        `).
		With(daggerQuery(`{test{foo{message}}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"foo":{"message":"bar"}}}`, out)
}

func TestModulePythonWithOtherModuleTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--name=dep", "--sdk=python")).
		With(pythonSource(`
            from dagger import field, function, object_type

            @object_type
            class Obj:
                foo: str = field()

            @object_type
            class Dep:
                @function
                def fn(self) -> Obj:
                    return Obj(foo="foo")
        `)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=python", "test")).
		With(daggerExec("install", "-m=test", "./dep")).
		WithWorkdir("/work/test")

	t.Run("return as other module object", func(t *testing.T) {
		t.Run("direct", func(t *testing.T) {
			_, err := ctr.
				With(pythonSource(`
                    import dagger

                    @dagger.object_type
                    class Test:
                        @dagger.function
                        def fn(self) -> dagger.DepObj:
                            ...
                `)).
				With(daggerFunctions()).
				Sync(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q function %q cannot return external type from dependency module %q",
				"Test", "fn", "dep",
			))
		})

		t.Run("list", func(t *testing.T) {
			_, err := ctr.
				With(pythonSource(`
                    import dagger

                    @dagger.object_type
                    class Test:
                        @dagger.function
                        def fn(self) -> list[dagger.DepObj]:
                            ...
                `)).
				With(daggerFunctions()).
				Sync(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q function %q cannot return external type from dependency module %q",
				"Test", "fn", "dep",
			))
		})
	})

	t.Run("arg as other module object", func(t *testing.T) {
		t.Run("direct", func(t *testing.T) {
			_, err := ctr.With(pythonSource(`
                import dagger

                @dagger.object_type
                class Test:
                    @dagger.function
                    def fn(self, obj: dagger.DepObj):
                        ...
                `)).
				With(daggerFunctions()).
				Sync(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q function %q arg %q cannot reference external type from dependency module %q",
				"Test", "fn", "obj", "dep",
			))
		})

		t.Run("list", func(t *testing.T) {
			_, err := ctr.With(pythonSource(`
                import dagger

                @dagger.object_type
                class Test:
                    @dagger.function
                    def fn(self, obj: list[dagger.DepObj]):
                        ...
                `)).
				With(daggerFunctions()).
				Sync(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q function %q arg %q cannot reference external type from dependency module %q",
				"Test", "fn", "obj", "dep",
			))
		})
	})

	t.Run("field as other module object", func(t *testing.T) {
		t.Run("direct", func(t *testing.T) {
			_, err := ctr.
				With(pythonSource(`
                    import dagger

                    @dagger.object_type
                    class Obj:
                        foo: dagger.DepObj = dagger.field()

                    @dagger.object_type
                    class Test:
                        @dagger.function
                        def fn(self) -> Obj:
                            ...
                `)).
				With(daggerFunctions()).
				Sync(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q field %q cannot reference external type from dependency module %q",
				"Obj", "foo", "dep",
			))
		})

		t.Run("list", func(t *testing.T) {
			_, err := ctr.
				With(pythonSource(`
                    import dagger

                    @dagger.object_type
                    class Obj:
                        foo: list[dagger.DepObj] = dagger.field()

                    @dagger.object_type
                    class Test:
                        @dagger.function
                        def fn(self) -> list[Obj]:
                            ...
                `)).
				With(daggerFunctions()).
				Sync(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q field %q cannot reference external type from dependency module %q",
				"Obj", "foo", "dep",
			))
		})
	})
}

func pythonSource(contents string) dagger.WithContainerFunc {
	return sdkSource("python", contents)
}

func pythonModInit(ctx context.Context, t *testing.T, c *dagger.Client, source string) *dagger.Container {
	return modInit(ctx, t, c, "python", source)
}
