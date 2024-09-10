package core

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
)

const pythonSourcePath = "src/main/__init__.py"

// Group all tests that are specific to Python only.
type PythonSuite struct{}

func TestPython(t *testing.T) {
	testctx.Run(testCtx, t, PythonSuite{}, Middleware()...)
}

func (PythonSuite) TestInit(ctx context.Context, t *testctx.T) {
	t.Run("from scratch", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerInitPython()).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("with different root", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerInitPythonAt("child")).
			With(daggerCallAt("child", "container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("on develop", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerExec("init")).
			With(daggerExec("develop", "--sdk=python", "--source=.")).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("doesn't create files in develop with existing pyproject.toml", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := daggerCliBase(t, c).
			With(daggerExec("init")).
			With(fileContents("pyproject.toml", `
[project]
name = "main"
version = "0.0.0"
`,
			)).
			With(daggerExec("develop", "--sdk=python", "--source=.")).
			Sync(ctx)

		require.ErrorContains(t, err, "no python files found")
	})

	t.Run("uses expected field casing", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerInitPython("--name=hello-world")).
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

	t.Run("fail if --merge is specified", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := daggerCliBase(t, c).
			With(daggerInitPython("--name=hello-world", "--merge")).
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

		require.ErrorContains(t, err, "merge is only supported")
	})
}

func (PythonSuite) TestProjectLayout(ctx context.Context, t *testctx.T) {
	testCases := []struct {
		name string
		path string
		conf string
	}{
		{
			name: "setuptools",
			path: "src/main/__init__.py",
			conf: `
[project]
name = "main"
version = "0.0.0"
`,
		},
		{
			// This is the old template layout.
			name: "setuptools",
			path: "src/main.py",
			conf: `
[project]
name = "main"
version = "0.0.0"
`,
		},
		{
			name: "setuptools",
			path: "main/__init__.py",
			conf: `
[project]
name = "main"
version = "0.0.0"

[tool.setuptools]
packages = ["main"]
`,
		},
		{
			name: "setuptools",
			path: "main.py",
			conf: `
[project]
name = "main"
version = "0.0.0"

[tool.setuptools]
py-modules = ["main"]
`,
		},
		{
			// This is the **current** template layout.
			name: "hatch",
			path: "src/main/__init__.py",
			conf: `
[project]
name = "main"
version = "0.0.0"

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"
`,
		},
		{
			name: "hatch",
			path: "src/main.py",
			conf: `
[project]
name = "main"
version = "0.0.0"

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[tool.hatch.build.targets.wheel]
packages = ["src/main.py"]
`,
		},
		{
			name: "hatch",
			path: "main/__init__.py",
			conf: `
[project]
name = "main"
version = "0.0.0"

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[tool.hatch.build.targets.wheel]
packages = ["main"]
`,
		},
		{
			name: "hatch",
			path: "main.py",
			conf: `
[project]
name = "main"
version = "0.0.0"

[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[tool.hatch.build.targets.wheel]
packages = ["main.py"]
`,
		},
		{
			name: "poetry",
			path: "src/main/__init__.py",
			conf: `
[tool.poetry]
name = "main"
version = "0.0.0"
authors = []
description = ""

[build-system]
requires = ["poetry-core>=1.0.0"]
build-backend = "poetry.core.masonry.api"
`,
		},
		{
			name: "poetry",
			path: "src/main.py",
			conf: `
[tool.poetry]
name = "main"
version = "0.0.0"
authors = []
description = ""

[build-system]
requires = ["poetry-core>=1.0.0"]
build-backend = "poetry.core.masonry.api"
`,
		},
		{
			name: "poetry",
			path: "main/__init__.py",
			conf: `
[tool.poetry]
name = "main"
version = "0.0.0"
authors = []
description = ""

[build-system]
requires = ["poetry-core>=1.0.0"]
build-backend = "poetry.core.masonry.api"
`,
		},
		{
			name: "poetry",
			path: "main.py",
			conf: `
[tool.poetry]
name = "main"
version = "0.0.0"
authors = []
description = ""

[build-system]
requires = ["poetry-core>=1.0.0"]
build-backend = "poetry.core.masonry.api"
`,
		},
	}

	for i, tc := range testCases {
		tc := tc

		t.Run(fmt.Sprintf("%s/%s", tc.name, tc.path), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := daggerCliBase(t, c).
				// uv caches a project's metadata by its absolute path so we can't change
				// build backends and package location on the same `pyproject.toml` path
				// concurrently because uv may return the cached metadata from another subtest.
				WithWorkdir(fmt.Sprintf("/work/%s-%d", tc.name, i)).
				With(fileContents("pyproject.toml", tc.conf)).
				With(fileContents(tc.path, `
from dagger import function

@function
def hello() -> str:
    return "Hello, world!"
`,
				)).
				With(daggerInitPython()).
				With(daggerCall("hello")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, "Hello, world!", out)
		})
	}
}

func (PythonSuite) TestVersion(ctx context.Context, t *testctx.T) {
	source := pythonSource(`
import sys
from dagger import function

@function
def version() -> str:
    v = sys.version_info
    return f"{v.major}.{v.minor}.{v.micro}"

@function
def relaxed() -> str:
    v = sys.version_info
    return f"{v.major}.{v.minor}"
`,
	)

	t.Run("relaxed requires-python", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(`requires-python = ">=3.10"`)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.10", out)
	})

	t.Run("pinned requires-python", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			// NB: This is **not** the latest version.
			// Space after `==` is intentional.
			With(pyprojectExtra(`requires-python = "== 3.10.10"`)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.10.10", out)
	})

	t.Run("relaxed .python-version", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(fileContents(".python-version", "3.12")).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.12", out)
	})

	t.Run("pinned .python-version", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(fileContents(".python-version", "3.12.1")).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.12.1", out)
	})

	t.Run(".python-version takes precedence", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(`requires-python = ">=3.10"`)).
			With(fileContents(".python-version", "3.12")).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.12", out)
	})

	t.Run("pinned base image", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			// base image takes precedence over .python-version
			With(fileContents(".python-version", "3.12")).
			With(pyprojectExtra(`
                [tool.dagger]
                base-image = "python:3.10.13@sha256:d5b1fbbc00fd3b55620a9314222498bebf09c4bf606425bf464709ed6a79f202"
            `)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.10.13", out)
	})

	t.Run("default", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.11", out)
	})
}

func (PythonSuite) TestAltRuntime(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	runtimeSrcPath, err := filepath.Abs("../../sdk/python/runtime")
	require.NoError(t, err)

	extSrcPath, err := filepath.Abs("./testdata/modules/python/extended")
	require.NoError(t, err)

	moduleSrcPath, err := filepath.Abs("./testdata/modules/python/git-dep")
	require.NoError(t, err)

	base := goGitBase(t, c).
		WithMountedDirectory("/work/runtime", c.Host().Directory(runtimeSrcPath)).
		WithMountedDirectory("/work/extended", c.Host().Directory(extSrcPath)).
		WithExec([]string{"sed", "-i", "s#../../../../../sdk/python/##", "/work/extended/dagger.json"})

	t.Run("git dependency", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			WithMountedDirectory("/work/git-dep", c.Host().Directory(moduleSrcPath)).
			WithWorkdir("/work/git-dep").
			With(daggerCall("hello")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "git version")
	})

	t.Run("disabled custom config", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			WithWorkdir("/work/test").
			With(fileContents(".python-version", "3.12")).
			With(pythonSource(`
import sys
from dagger import function

@function
def version() -> str:
    v = sys.version_info
    return f"{v.major}.{v.minor}"
`,
			)).
			With(daggerExec("init", "--sdk=../extended", "--name=test", "--source=.")).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.11", out)
	})
}

func (PythonSuite) TestUv(ctx context.Context, t *testctx.T) {
	t.Run("disabled", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := daggerCliBase(t, c).
			With(pyprojectExtra(`
                [tool.dagger]
                use-uv = false
            `)).
			With(daggerInitPython())

		// Only uv creates a lock
		files, err := modGen.Directory("").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, files, "requirements.lock")

		// Should still work with pip
		out, err := modGen.
			With(daggerCall("container-echo", "--string-arg=hello", "stdout")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("disabled, check pip install", func(ctx context.Context, t *testctx.T) {
		// `pip check` fails if Requires-Python doesn't match the
		// Python version in the container

		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(`
                requires-python = "<3.11"

                [tool.dagger]
                use-uv = false
            `)).
			With(daggerInitPython()).
			With(daggerCall("container-echo", "--string-arg=hello", "stdout")).
			Stdout(ctx)

		t.Logf("out: %s", out)
		require.ErrorContains(t, err, "pip is looking at multiple versions of main")
		require.ErrorContains(t, err, "requires a different Python")
	})

	t.Run("pinned version", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		source := pythonSource(`
import anyio
from dagger import function

@function
async def version() -> str:
    try:
        r = await anyio.run_process(["uv", "version"])
    except FileNotFoundError:
        return "n/d"

    # example output: uv 0.1.22 (9afb36052 2024-03-18)
    parts = r.stdout.decode().split(" ")
    return parts[1].strip()
`,
		)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(`
                [tool.dagger]
                uv-version = "0.2.20"
            `)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "0.2.20", out)
	})
}

func (PythonSuite) TestLock(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := daggerCliBase(t, c).With(pythonSource(`
from importlib import metadata
import anyio
from dagger import function

@function
async def freeze() -> str:
    r = await anyio.run_process(["uv", "pip", "freeze"])
    return r.stdout.decode().strip()

@function
def version(name: str) -> str:
    return metadata.version(name)
`,
	))

	freeze, err := base.
		With(daggerInitPython()).
		With(daggerCall("freeze")).
		Stdout(ctx)

	require.NoError(t, err)

	var lock strings.Builder
	for _, line := range strings.Split(freeze, "\n") {
		// Freeze includes the editable installs, which are not a part of the lock file.
		if strings.HasPrefix(line, "-e") {
			continue
		}
		if strings.HasPrefix(line, "platformdirs==") {
			// Not the latest version so it's guaranteed to be different.
			lock.WriteString("platformdirs==4.1.0\n")
			continue
		}
		lock.WriteString(line)
		lock.WriteString("\n")
	}

	requirements := lock.String()
	t.Logf("requirements.lock:\n%s", requirements)

	out, err := base.
		With(fileContents("requirements.lock", requirements)).
		With(daggerInitPython()).
		With(daggerCall("version", "--name=platformdirs")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "4.1.0", out)
}

func (PythonSuite) TestLockAddedDep(ctx context.Context, t *testctx.T) {
	source := pythonSource(`
from importlib import metadata
from dagger import function

@function
def version() -> str:
    return metadata.version("packaging")
`,
	)

	t.Run("new module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(`dependencies = ["packaging<24.0"]`)).
			With(source).
			With(daggerInitPython()).
			WithExec([]string{"grep", "packaging==23.2", "requirements.lock"}).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "23.2", out)
	})

	t.Run("existing module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(daggerInitPython()).
			// Add dependency to pyproject.toml
			WithExec([]string{"sed", "-i", `/dependencies/ s/.*/dependencies = ["packaging<24.0"]/`, "pyproject.toml"}).
			With(source).
			With(daggerExec("develop")).
			WithExec([]string{"grep", "packaging==23.2", "requirements.lock"}).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "23.2", out)
	})

	t.Run("sdk overrides local changes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := daggerCliBase(t, c).
			With(daggerInitPython()).
			// Add dependency to sdk/pyproject.toml
			WithExec([]string{"sed", "-i", `/platformdirs>=/ a  = "packaging<24.0",/`, "sdk/pyproject.toml"}).
			With(source).
			With(daggerCall("version")).
			Sync(ctx)

		require.ErrorContains(t, err, "No package metadata was found for packaging")
	})
}

func (PythonSuite) TestSignatures(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := pythonModInit(t, c, `
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
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerQuery(tc.query)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, out)
		})
	}
}

func (PythonSuite) TestSignaturesBuiltinTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := pythonModInit(t, c, `
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
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerQuery(tc.query)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, tc.expected, out)
		})
	}
}

func (PythonSuite) TestDocs(ctx context.Context, t *testctx.T) {
	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := pythonModInit(t, c, `
            from typing import Annotated

            from dagger import Doc, function, object_type

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

	t.Run("autogenerated constructor", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := pythonModInit(t, c, `
            from dataclasses import field as datafield
            from typing import Annotated

            from dagger import Doc, function, object_type, field

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

	t.Run("InitVar", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := pythonModInit(t, c, `
            import dataclasses
            from typing import Annotated
            from typing_extensions import Doc

            import dagger
            

            @dagger.object_type
            class Test:
                com_url: dataclasses.InitVar[Annotated[str, Doc("A .com URL")]] = "https://example.com"
                org_url: dataclasses.InitVar[Annotated[str, Doc("A .org URL")]] = "https://example.org"

                # NB: not a dagger.field() to force serialization/deserialization in urls function
                saved_urls: Annotated[list[str], Doc("List of URLs")] = dataclasses.field(init=False)

                def __post_init__(self, com_url: str, org_url: str):
                    self.saved_urls = [com_url, org_url]

                @dagger.function
                def urls(self) -> list[str]:
                    return self.saved_urls
        `)

		obj := inspectModuleObjects(ctx, t, modGen).Get("0")

		require.EqualValues(t, []any{"comUrl", "orgUrl"}, obj.Get("constructor.args.#.name").Value())
		require.Equal(t, "A .com URL", obj.Get("constructor.args.#(name=comUrl).description").String())
		require.Equal(t, "A .org URL", obj.Get("constructor.args.#(name=orgUrl).description").String())

		require.Equal(t, "https://example.com", obj.Get("constructor.args.#(name=comUrl).defaultValue.@fromstr").String())
		require.Equal(t, "https://example.org", obj.Get("constructor.args.#(name=orgUrl).defaultValue.@fromstr").String())

		// Sanity check
		out, err := modGen.With(daggerCall("urls", "--json")).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `["https://example.com", "https://example.org"]`, out)
	})

	t.Run("wrong InitVar syntax", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := pythonModInit(t, c, `
            import dataclasses
            from typing import Annotated
            from typing_extensions import Doc

            import dagger
            

            @dagger.object_type
            class Test:
                url: Annotated[dataclasses.InitVar[str], Doc("A URL")] = "https://example.com"

                def __post_init__(self, url: str):
                    ...
        `)

		_, err := modGen.With(daggerCall("--help")).Sync(ctx)
		require.ErrorContains(t, err, "InitVar[typing.Annotated[str, Doc('A URL')]]")
	})

	t.Run("alternative constructor", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := pythonModInit(t, c, `
            from typing import Annotated, Self

            from dagger import Doc, function, object_type

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

	t.Run("external constructor", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := pythonModInit(t, c, `
            from typing import Annotated, Self

            from dagger import Doc, function, object_type

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

	t.Run("external alternative constructor", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := pythonModInit(t, c, `
            from typing import Annotated, Self

            from dagger import Doc, function, object_type

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

	t.Run("inheritance", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := pythonModInit(t, c, `
            from typing import Annotated, Self

            from dagger import Doc, function, object_type

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

func (PythonSuite) TestNameConflicts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := pythonModInit(t, c, `
        from dagger import field, function, object_type

        @object_type
        class Test:
            from_: str = field(default="")

            @function
            def with_(self, import_: str = "") -> str:
                return import_
        `)

	out, err := modGen.With(daggerCall("--from=foo", "from")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "foo", out)

	out, err = modGen.With(daggerCall("with", "--import=bar")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "bar", out)
}

func (PythonSuite) TestNameOverrides(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := pythonModInit(t, c, `
        from typing import Annotated

        from dagger import Arg, field, function, object_type

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

func (PythonSuite) TestReturnSelf(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := pythonModInit(t, c, `
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

func (PythonSuite) TestWithOtherModuleTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithWorkdir("/work/dep").
		With(daggerInitPython("--name=dep")).
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
		WithWorkdir("/work/test").
		With(daggerInitPython()).
		With(daggerExec("install", "../dep"))

	t.Run("return as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
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

		t.Run("list", func(ctx context.Context, t *testctx.T) {
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

	t.Run("arg as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
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

		t.Run("list", func(ctx context.Context, t *testctx.T) {
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

	t.Run("field as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
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

		t.Run("list", func(ctx context.Context, t *testctx.T) {
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

func (PythonSuite) TestIgnoreConstructorArg(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := pythonModInit(t, c, `
        from typing import Annotated
        import dagger

        @dagger.object_type
        class Test:
            source: Annotated[
                dagger.Directory, 
                dagger.DefaultPath("/"),
                dagger.Ignore([".venv"]),
            ] = dagger.field()
        `).
		With(daggerCall("source", "entries", "--json")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, gjson.Parse(out).Value(), "dagger.json")
}

func pythonSource(contents string) dagger.WithContainerFunc {
	return pythonSourceAt("", contents)
}

func pythonSourceAt(modPath, contents string) dagger.WithContainerFunc {
	return fileContents(path.Join(modPath, pythonSourcePath), contents)
}

func pythonModInit(t testing.TB, c *dagger.Client, source string) *dagger.Container {
	t.Helper()
	return daggerCliBase(t, c).
		With(daggerInitPython()).
		With(pythonSource(source))
}

func daggerInitPython(args ...string) dagger.WithContainerFunc {
	return daggerInitPythonAt("", args...)
}

func pyprojectExtra(contents string) dagger.WithContainerFunc {
	base := `
[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[project]
name = "main"
version = "0.0.0"
`
	return fileContents("pyproject.toml", base+contents)
}

func daggerInitPythonAt(modPath string, args ...string) dagger.WithContainerFunc {
	execArgs := append([]string{"init", "--sdk=python"}, args...)
	if len(args) == 0 {
		execArgs = append(execArgs, "--name=test")
	}
	if modPath != "" {
		execArgs = append(execArgs, "--source="+modPath, modPath)
	} else {
		execArgs = append(execArgs, "--source=.")
	}
	return daggerExec(execArgs...)
}
