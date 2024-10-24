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
			With(daggerExec("init", "--source=.")).
			With(daggerExec("develop", "--sdk=python", "--source=.")).
			With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("doesn't create files in develop with existing pyproject.toml", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := daggerCliBase(t, c).
			With(daggerExec("init", "--source=.")).
			With(pyprojectExtra(nil, "")).
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

	t.Run("init module in .dagger if files present in current dir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("hello.py", `
                from dagger import field, function, object_type

                @object_type
                class HelloWorld:
                    my_name: str = field(default="World")

                    @function
                    def message(self) -> str:
                        return f"Hello, {self.my_name}!"
            `).
			With(daggerExec("init", "--name=bare", "--sdk=python"))

		daggerDirEnts, err := modGen.Directory("/work/.dagger").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerDirEnts, "pyproject.toml", "sdk", "src", "uv.lock")

		out, err := modGen.
			WithWorkdir("/work").
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("init module when current dir only has hidden dirs", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"mkdir", "-p", ".git"}).
			With(daggerExec("init", "--name=bare", "--sdk=python"))

		daggerDirEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerDirEnts, "pyproject.toml", "sdk", "src", "uv.lock")

		out, err := modGen.
			WithWorkdir("/work").
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})
}

func (PythonSuite) TestProjectLayout(ctx context.Context, t *testctx.T) {
	// NB: This is testing uv integration with different build backends,
	// **not** different package managers.

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
dependencies = ["dagger-io"]

[tool.uv]
package = true

[tool.uv.sources]
dagger-io = { path = "sdk", editable = true }
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
dependencies = ["dagger-io"]

[tool.uv]
package = true

[tool.uv.sources]
dagger-io = { path = "sdk", editable = true }
`,
		},
		{
			name: "setuptools",
			path: "main/__init__.py",
			conf: `
[project]
name = "main"
version = "0.0.0"
dependencies = ["dagger-io"]

[tool.uv]
package = true

[tool.uv.sources]
dagger-io = { path = "sdk", editable = true }

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
dependencies = ["dagger-io"]

[tool.uv]
package = true

[tool.uv.sources]
dagger-io = { path = "sdk", editable = true }

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
dependencies = ["dagger-io"]

[tool.uv.sources]
dagger-io = { path = "sdk", editable = true }

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
dependencies = ["dagger-io"]

[tool.uv.sources]
dagger-io = { path = "sdk", editable = true }

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
dependencies = ["dagger-io"]

[tool.uv.sources]
dagger-io = { path = "sdk", editable = true }

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
dependencies = ["dagger-io"]

[tool.uv.sources]
dagger-io = { path = "sdk", editable = true }

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

		t.Run(fmt.Sprintf("%s/%s", tc.name, tc.path), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := daggerCliBase(t, c).
				With(fileContents(tc.path, fmt.Sprintf(`
import anyio
import dagger

@dagger.object_type
class Test:
    @dagger.function
    async def whoami(self) -> str:
        path = await anyio.Path(__file__).absolute()
        return f"%s: {path}"
`, tc.name),
				)).
				With(fileContents("pyproject.toml", tc.conf+"\n# "+tc.path)).
				With(func(ctr *dagger.Container) *dagger.Container {
					// For poetry projects, uv will fail to build due to missing
					// [project] table in pyproject.toml. Support is possible
					// via `uv pip` and requirements.lock though.
					if tc.name != "poetry" {
						return ctr.WithEnvVariable("TEST_LOCK_FILE", "uv.lock")
					}
					return pipLockMod(t, c, []string{"requirements.lock"})(
						ctr.WithEnvVariable("TEST_LOCK_FILE", "requirements.lock"),
					)
				}).
				With(daggerInitPython()).
				WithExec([]string{"sh", "-c", "grep dagger-io $TEST_LOCK_FILE"}).
				With(daggerCall("whoami")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, tc.name)
			require.Contains(t, out, tc.path)
		})
	}
}

func (PythonSuite) TestVersion(ctx context.Context, t *testctx.T) {
	// NB: All "pinned" and "relaxed" tests intentionally choose patch versions that
	// are not the latest, and major versions that aren't the default in the runtime.

	source := pythonSource(`
import sys
import dagger

@dagger.object_type
class Test:
    @dagger.function
    def pinned(self) -> str:
        v = sys.version_info
        return f"{v.major}.{v.minor}.{v.micro}"

    @dagger.function
    def relaxed(self) -> str:
        v = sys.version_info
        return f"{v.major}.{v.minor}"
`,
	)

	t.Run("relaxed requires-python", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(nil, `requires-python = ">=3.11"`)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.11", out)
	})

	t.Run("pinned requires-python", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			// Space after `==` is intentional.
			With(pyprojectExtra(nil, `requires-python = "== 3.11.6"`)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("pinned")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.11.6", out)
	})

	t.Run("relaxed .python-version", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(source).
			With(pyprojectExtra(nil, `requires-python = ">=3.10"`)).
			With(fileContents(".python-version", "3.11")).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.11", out)
	})

	t.Run("pinned .python-version", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(fileContents(".python-version", "3.12.1")).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("pinned")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.12.1", out)
	})

	t.Run(".python-version takes precedence", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pyprojectExtra(nil, `requires-python = ">=3.10"`)).
			With(fileContents(".python-version", "3.11")).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.11", out)
	})

	t.Run("pinned base image", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			// base image takes precedence over .python-version
			// warning: uv will fail if these don't match, just testing the
			// the runtime's version discovery
			With(fileContents(".python-version", "3.12.2")).
			With(pyprojectExtra(nil, `
                [tool.dagger]
                use-uv = false
                base-image = "python:3.12.1-slim@sha256:a64ac5be6928c6a94f00b16e09cdf3ba3edd44452d10ffa4516a58004873573e"
            `)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("pinned")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.12.1", out)
	})

	t.Run("default", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("relaxed")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.12", out)
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
			With(pyprojectExtra(nil, `
requires-python = ">=3.11"

[tool.dagger]
use-uv = false
`)).
			With(pythonSource(`
import sys
import dagger

@dagger.object_type
class Test:
    @dagger.function
    def version(self) -> str:
        v = sys.version_info
        return f"{v.major}.{v.minor}"
`,
			)).
			With(daggerExec("init", "--sdk=../extended", "--name=test", "--source=.")).
			// use-uv = false should be ignored
			WithExec([]string{"test", "-f", "uv.lock"}).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "3.12", out)
	})
}

func (PythonSuite) TestUv(ctx context.Context, t *testctx.T) {
	t.Run("disabled", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr, err := daggerCliBase(t, c).
			With(pyprojectExtra(nil, `
                [tool.dagger]
                use-uv = false
            `)).
			With(daggerInitPython()).
			// Only uv creates a lock
			WithExec([]string{"test", "!", "-f", "uv.lock"}).
			WithExec([]string{"test", "!", "-f", "requirements.lock"}).
			Sync(ctx)

		require.NoError(t, err)

		// Should still work with pip though
		out, err := ctr.
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
			With(pyprojectExtra(nil, `
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
import dagger

@dagger.object_type
class Test:
    @dagger.function
    async def version(self) -> str:
        try:
            r = await anyio.run_process(["uv", "version"])
        except FileNotFoundError:
            return "n/d"

        # example output: uv 0.4.7 (a178051e8 2024-09-07)
        parts = r.stdout.decode().split(" ")
        return parts[1].strip()
`,
		)

		out, err := daggerCliBase(t, c).
			// Intentionally using a version that's older than the runtime's default.
			// Can't lag too far behind because the runtime may depend on some
			// newer feature.
			With(pyprojectExtra(nil, `
                [tool.dagger]
                uv-version = "0.4.5"
            `)).
			With(source).
			With(daggerInitPython()).
			With(daggerCall("version")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "0.4.5", out)
	})

	t.Run("index-url", func(ctx context.Context, t *testctx.T) {
		source := pythonSource(`
import contextlib
import os

import dagger

@dagger.object_type
class Test:
    @dagger.function
    def urls(self) -> list[str]:
        res = []
        with contextlib.suppress(KeyError):
            res.append(os.environ["UV_INDEX_URL"])
        with contextlib.suppress(KeyError):
            res.append(os.environ["UV_EXTRA_INDEX_URL"])
        return res
`,
		)

		t.Run("with", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := daggerCliBase(t, c).
				With(source).
				With(pyprojectExtra(nil, `
                    [[tool.uv.index]]
                    url = "https://test.pypi.org/simple"
                    default = true

                    [[tool.uv.index]]
                    url = "https://pypi.org/simple"
                `)).
				With(daggerInitPython()).
				With(daggerCall("urls")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Equal(t, "https://test.pypi.org/simple\nhttps://pypi.org/simple\n", out)
		})

		t.Run("without", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			out, err := daggerCliBase(t, c).
				With(source).
				With(daggerInitPython()).
				With(daggerCall("urls", "--json")).
				Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t, "[]", out)
		})

		t.Run("error", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			_, err := daggerCliBase(t, c).
				With(source).
				With(pyprojectExtra(nil, `
                    [[tool.uv.index]]
                    url = "https://pypi.example.com/simple"
                    default = true
                `)).
				With(daggerInitPython()).
				Sync(ctx)

			require.ErrorContains(t, err, "Failed to prepare distributions")
		})
	})
}

func (PythonSuite) TestPipLock(ctx context.Context, t *testctx.T) {
	t.Run("can run existing module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pipLockMod(t, c, nil)).
			With(daggerCall("--json")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "Test", gjson.Get(out, "_type").String())
	})

	t.Run("no uv.lock on develop", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := daggerCliBase(t, c).
			With(pipLockMod(t, c, nil)).
			With(daggerExec("develop")).
			WithExec([]string{"test", "!", "-f", "uv.lock"}).
			With(daggerCall()).
			Sync(ctx)

		require.NoError(t, err)
	})

	t.Run("no uv.lock on init", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := daggerCliBase(t, c).
			With(pipLockMod(t, c, []string{"pyproject.toml", "requirements.lock"})).
			With(daggerInitPython()).
			WithExec([]string{"test", "!", "-f", "uv.lock"}).
			Sync(ctx)

		require.NoError(t, err)
	})

	t.Run("no locks on develop", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := daggerCliBase(t, c).
			With(pipLockMod(t, c, []string{"**", "!requirements.lock"})).
			With(daggerExec("develop")).
			WithExec([]string{"test", "!", "-f", "uv.lock"}).
			WithExec([]string{"test", "!", "-f", "requirements.lock"}).
			Sync(ctx)

		require.NoError(t, err)
	})

	t.Run("does not update pinned dependency", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pipLockMod(t, c, nil)).
			With(daggerExec("develop")).
			With(daggerCall("versions", "--names=platformdirs")).
			Stdout(ctx)

		require.NoError(t, err)

		// this pinned version of platformdirs is known to not be the latest one
		require.Equal(t, "platformdirs==4.2.0\n", out)
	})

	t.Run("new dependency", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		out, err := daggerCliBase(t, c).
			With(pipLockMod(t, c, nil)).
			WithExec([]string{"sh", "-c", `echo 'dependencies = ["packaging<24.0"]' >> pyproject.toml`}).
			With(daggerExec("develop")).
			WithExec([]string{"grep", "packaging==23.2", "requirements.lock"}).
			With(daggerCall("versions", "--names=platformdirs,packaging")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "platformdirs==4.2.0\npackaging==23.2\n", out)
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

        from dagger import Name, field, function, object_type

        @object_type
        class Test:
            field_: str = field(name="field")

            @function(name="func")
            def func_(self, arg_: Annotated[str, Name(name="arg")] = "") -> str:
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

func pyprojectExtra(dependencies []string, contents string) dagger.WithContainerFunc {
	dependencies = append([]string{"dagger-io"}, dependencies...)
	depLine := `dependencies = ["` + strings.Join(dependencies, `", "`) + `"]`
	base := `
[build-system]
requires = ["hatchling"]
build-backend = "hatchling.build"

[tool.uv.sources]
dagger-io = { path = "sdk", editable = true }

[project]
name = "main"
version = "0.0.0"
`
	return fileContents("pyproject.toml", base+depLine+"\n"+contents)
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

func pipLockMod(t *testctx.T, c *dagger.Client, inc []string) dagger.WithContainerFunc {
	t.Helper()
	modSrc, err := filepath.Abs("./testdata/modules/python/pip-lock")
	require.NoError(t, err)
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithDirectory("", c.Host().Directory(modSrc, dagger.HostDirectoryOpts{
			Include: inc,
		}))
	}
}
