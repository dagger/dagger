package core

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func TestModulePythonInit(t *testing.T) {
	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=python"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("with different root", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=python", "child"))

		out, err := modGen.
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
				Contents: `[project]
name = "has-pyproject"
version = "0.0.0"
`,
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

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/src/main/__init__.py", dagger.ContainerWithNewFileOpts{
				Contents: "from . import notmain\n",
			}).
			WithNewFile("/work/src/main/notmain.py", dagger.ContainerWithNewFileOpts{
				Contents: `from dagger import function

@function
def hello() -> str:
    return "Hello, world!"
`,
			}).
			With(daggerExec("init", "--name=hasMainPy", "--sdk=python"))

		out, err := modGen.
			With(daggerQuery(`{hasMainPy{hello}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasMainPy":{"hello":"Hello, world!"}}`, out)
	})

	t.Run("uses expected field casing", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=hello-world", "--sdk=python")).
			With(pythonSource(`from dagger import field, function, object_type

@object_type
class HelloWorld:
    my_name: str = field(default="World")

    @function
    def message(self) -> str:
        return f"Hello, {self.my_name}!"
`,
			))

		out, err := modGen.
			With(daggerQuery(`{helloWorld(myName: "Monde"){message}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"helloWorld":{"message":"Hello, Monde!"}}`, out)
	})
}

func TestModulePythonReturnSelf(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=foo", "--sdk=python")).
		With(pythonSource(`from typing import Self

from dagger import field, function, object_type

@object_type
class Foo:
    message: str = field(default="")

    @function
    def bar(self) -> Self:
        self.message = "foobar"
        return self
`,
		))

	out, err := modGen.With(daggerQuery(`{foo{bar{message}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"foo":{"bar":{"message":"foobar"}}}`, out)
}

func TestModulePythonWithOtherModuleTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--name=dep", "--sdk=python")).
		With(pythonSource(`from dagger import field, function, object_type

@object_type
class Obj:
    foo: str = field()

@object_type
class Dep:
    @function
    def fn(self) -> Obj:
        return Obj(foo="foo")
`,
		)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=python", "test")).
		With(daggerExec("install", "-m=test", "./dep")).
		WithWorkdir("/work/test")

	t.Run("return as other module object", func(t *testing.T) {
		t.Run("direct", func(t *testing.T) {
			_, err := ctr.
				With(pythonSource(`import dagger

@dagger.object_type
class Test:
    @dagger.function
    def fn(self) -> dagger.DepObj:
        ...
`,
				)).
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
				With(pythonSource(`import dagger

@dagger.object_type
class Test:
    @dagger.function
    def fn(self) -> list[dagger.DepObj]:
        ...
`,
				)).
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
			_, err := ctr.With(pythonSource(`import dagger

@dagger.object_type
class Test:
    @dagger.function
    def fn(self, obj: dagger.DepObj):
        ...
`,
			)).
				With(daggerFunctions()).
				Sync(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q function %q arg %q cannot reference external type from dependency module %q",
				"Test", "fn", "obj", "dep",
			))
		})

		t.Run("list", func(t *testing.T) {
			_, err := ctr.With(pythonSource(`import dagger

@dagger.object_type
class Test:
    @dagger.function
    def fn(self, obj: list[dagger.DepObj]):
        ...
`,
			)).
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
				With(pythonSource(`import dagger

@dagger.object_type
class Obj:
    foo: dagger.DepObj = dagger.field()

@dagger.object_type
class Test:
    @dagger.function
    def fn(self) -> Obj:
        ...
`,
				)).
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
				With(pythonSource(`import dagger

@dagger.object_type
class Obj:
    foo: list[dagger.DepObj] = dagger.field()

@dagger.object_type
class Test:
    @dagger.function
    def fn(self) -> list[Obj]:
        ...
`,
				)).
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
