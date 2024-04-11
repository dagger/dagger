package core

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
)

const pythonSourcePath = "src/main/__init__.py"

func TestModulePythonInit(t *testing.T) {
	t.Parallel()

	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := daggerCliBase(t, c).
			With(daggerInitPython()).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("with different root", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := daggerCliBase(t, c).
			With(daggerInitPythonAt("child")).
			With(daggerCallAt("child", "container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("on develop", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := daggerCliBase(t, c).
			With(daggerExec("init")).
			With(daggerExec("develop", "--sdk=python", "--source=.")).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("doesn't create files in develop with existing pyproject.toml", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	t.Run("uses expected field casing", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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
}

func TestModulePythonProjectLayout(t *testing.T) {
	t.Parallel()

	var testCases = []struct {
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

	for _, tc := range testCases {
		tc := tc

		t.Run(fmt.Sprintf("%s/%s", tc.name, tc.path), func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			out, err := daggerCliBase(t, c).
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

func TestModulePythonVersion(t *testing.T) {
	t.Parallel()

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

	t.Run("relaxed requires-python", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(`requires-python = ">=3.10"`)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.10", out)
	})

	t.Run("pinned requires-python", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	t.Run("relaxed .python-version", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := daggerCliBase(t, c).
			With(fileContents(".python-version", "3.12")).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.12", out)
	})

	t.Run("pinned .python-version", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := daggerCliBase(t, c).
			With(fileContents(".python-version", "3.12.1")).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.12.1", out)
	})

	t.Run(".python-version takes precedence", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	t.Run("pinned base image", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	t.Run("default", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := daggerCliBase(t, c).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.11", out)
	})
}

func TestModulePythonAltRuntime(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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

	t.Run("git dependency", func(t *testing.T) {
		out, err := base.
			WithMountedDirectory("/work/git-dep", c.Host().Directory(moduleSrcPath)).
			WithWorkdir("/work/git-dep").
			With(daggerCall("hello")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, "git version")
	})

	t.Run("disabled custom config", func(t *testing.T) {
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

func TestModulePythonUv(t *testing.T) {
	t.Parallel()

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

	t.Run("disable", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(`
                [tool.dagger]
                use-uv = false
            `)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "n/d", out)
	})

	t.Run("pinned version", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(`
                [tool.dagger]
                uv-version = "==0.1.25"
            `)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "0.1.25", out)
	})

	t.Run("upper bound", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(`
                [tool.dagger]
                uv-version = "<0.1.26"
            `)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "0.1.25", out)
	})
}

func TestModulePythonLock(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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

func TestModulePythonLockHashes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	base := daggerCliBase(t, c).With(daggerInitPython())

	out, err := base.File("requirements.lock").Contents(ctx)
	require.NoError(t, err)

	// Replace hashes for platformdirs with an invalid one.
	// The lock file has the following format:
	//
	// httpx==0.27.0 \
	//     --hash=sha256:71d5465162c13681bff01ad59b2cc68dd838ea1f10e51574bac27103f00c91a5 \
	//     --hash=sha256:a0cb88a46f32dc874e04ee956e4c2764aba2aa228f650b06788ba6bda2962ab5
	//     # via gql
	// platformdirs==4.2.0 \
	//     --hash=sha256:0614df2a2f37e1a662acbd8e2b25b92ccf8632929bc6d43467e17fe89c75e068 \
	//     --hash=sha256:ef0cc731df711022c174543cb70a9b5bd22e5a9337c8624ef2c2ceb8ddad8768
	// pygments==2.17.2 \
	//     --hash=sha256:b27c2826c47d0f3219f29554824c30c5e8945175d888647acd804ddd04af846c \
	//     --hash=sha256:da46cec9fd2de5be3a8a784f434e4c4ab670b4ff54d605c4c2717e9d49c4c367
	//     # via rich

	var lock strings.Builder
	replaceHashes := false
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "platformdirs==") {
			replaceHashes = true
			lock.WriteString(
				fmt.Sprintf("%s\n    --hash=sha256:%s\n", line, strings.Repeat("1", 64)),
			)
			continue
		}
		if replaceHashes {
			if strings.HasPrefix(strings.TrimSpace(line), "--hash") {
				continue
			} else {
				replaceHashes = false
			}
		}
		lock.WriteString(line)
		lock.WriteString("\n")
	}

	requirements := lock.String()
	t.Logf("requirements.lock:\n%s", requirements)

	t.Run("uv", func(t *testing.T) {
		_, err := base.
			With(fileContents("requirements.lock", requirements)).
			With(daggerExec("develop")).
			Sync(ctx)

		// TODO: uv doesn't support hash verification yet.
		// require.ErrorContains(t, err, "hash mismatch")
		require.NoError(t, err)
	})

	t.Run("pip", func(t *testing.T) {
		_, err := base.
			With(fileContents("requirements.lock", requirements)).
			With(pyprojectExtra(`
                [tool.dagger]
                use-uv = false
            `)).
			With(daggerExec("develop")).
			Sync(ctx)

		require.ErrorContains(t, err, "DO NOT MATCH THE HASHES")
		require.ErrorContains(t, err, "Expected sha256 1111111")
	})
}

func TestModulePythonLockAddedDep(t *testing.T) {
	t.Parallel()

	source := pythonSource(`
from importlib import metadata
from dagger import function

@function
def version() -> str:
    return metadata.version("packaging")
`,
	)

	t.Run("new module", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	t.Run("existing module", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	t.Run("sdk overrides local changes", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

func TestModulePythonSignatures(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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

	t.Run("autogenerated constructor", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	t.Run("alternative constructor", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	t.Run("external constructor", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	t.Run("external alternative constructor", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

	t.Run("inheritance", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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

func TestModulePythonNameOverrides(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := pythonModInit(t, c, `
        from typing import Annotated

        from dagger import Arg, Doc, field, function, object_type

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

func TestModulePythonWithOtherModuleTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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

func TestModulePythonScalarKind(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	_, err := pythonModInit(t, c, `
        import dagger
        from dagger import dag, function, object_type

        @object_type
        class Test:
            @function
            def foo(self, platform: dagger.Platform) -> dagger.Container:
                return dag.container(platform=platform)
        `).
		With(daggerCall("foo", "--platform", "linux/arm64")).
		Sync(ctx)

	require.ErrorContains(t, err, "not supported yet")
}

func TestModulePythonEnumKind(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	_, err := pythonModInit(t, c, `
        import dagger
        from dagger import dag, function, object_type

        @object_type
        class Test:
            @function
            def foo(self, protocol: dagger.NetworkProtocol) -> dagger.Container:
                return dag.container().with_exposed_port(8000, protocol=protocol)
        `).
		With(daggerCall("foo", "--protocol", "UDP")).
		Sync(ctx)

	require.ErrorContains(t, err, "not supported yet")
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
