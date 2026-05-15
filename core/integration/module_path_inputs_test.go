package core

// These tests cover module arguments that receive filesystem or Git content.
// They verify how `+defaultPath`, `+ignore`, `.gitignore`, and explicit filters
// shape Directory, File, and GitRepository inputs before module code sees them.
//
// See also:
// - module_runtime_behavior_test.go: general module execution behavior.
// - module_loading_test.go: selecting module sources and entrypoints.

import (
	"cmp"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestContextDirectory(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk     string
		fixture string
		source  string
	}

	t.Run("load context inside git repo with module in a sub dir", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk:     "go",
				fixture: "go/path-context-directory-01",
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
				sdk:     "python",
				fixture: "python/path-context-directory-02",
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
				sdk:     "typescript",
				fixture: "typescript/path-context-directory-03",
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
					With(withModuleFixture(t, c, "/work/ci", tc.fixture)).
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
				sdk:     "go",
				fixture: "go/path-context-directory-04",
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
				sdk:     "python",
				fixture: "python/path-context-directory-05",
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
				sdk:     "typescript",
				fixture: "typescript/path-context-directory-06",
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
					With(withModuleFixture(t, c, "/work", tc.fixture)).
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
				sdk:     "go",
				fixture: "go/path-context-directory-07",
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
				sdk:     "python",
				fixture: "python/path-context-directory-08",
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
				sdk:     "typescript",
				fixture: "typescript/path-context-directory-09",
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
					With(withModuleFixture(t, c, "/work", tc.fixture)).
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
			With(withModuleFixture(t, c, "/work", "go/path-context-directory-deps")).
			WithWorkdir("/work/dep")

		out, err := ctr.With(daggerCall("get-source", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)

		out, err = ctr.With(daggerCall("get-rel-source", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)

		ctr = ctr.
			WithWorkdir("/work")

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
			With(withModuleFixture(t, c, "/work", "go/path-context-directory-as-module")).
			WithWorkdir("/work")

		out, err := ctr.With(daggerCall("get-dep-source", "--src", ".", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)

		out, err = ctr.With(daggerCall("get-rel-dep-source", "--src", ".", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo\n", out)
	})
}

func (ModuleSuite) TestDefaultPathAndIgnoreUseRemoteModuleSource(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	remoteModule := c.Directory().
		WithNewFile("source.txt", "module source").
		WithNewFile("filtered/keep.txt", "module keep").
		WithNewFile("filtered/drop.txt", "module drop").
		WithNewFile("dagger.json", `{"name":"reader","sdk":{"source":"go"}}`).
		WithNewFile("main.go", `package main

import (
	"context"

	"dagger/reader/internal/dagger"
)

type Reader struct{}

// ReadDefaultFile proves a File +defaultPath is resolved from this module source.
func (m *Reader) ReadDefaultFile(
	ctx context.Context,

	// +defaultPath="/source.txt"
	source *dagger.File,
) (string, error) {
	return source.Contents(ctx)
}

// ReadDefaultDirectoryFile proves a Directory +defaultPath is resolved from this module source.
func (m *Reader) ReadDefaultDirectoryFile(
	ctx context.Context,

	// +defaultPath="/filtered"
	filtered *dagger.Directory,
) (string, error) {
	return filtered.File("keep.txt").Contents(ctx)
}

// ListIgnoredDefaultDirectory proves +ignore applies to the defaultPath directory from this module source.
func (m *Reader) ListIgnoredDefaultDirectory(
	ctx context.Context,

	// +defaultPath="/filtered"
	// +ignore=["drop.txt"]
	filtered *dagger.Directory,
) ([]string, error) {
	return filtered.Entries(ctx)
}
`)

	gitSrv, _ := gitSmartHTTPServiceDirAuth(ctx, t, c, "", makeGitDir(c, remoteModule, "main"), "", nil)
	gitSrv, err := gitSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = gitSrv.Stop(ctx) })

	hostname, err := gitSrv.Hostname(ctx)
	require.NoError(t, err)
	remoteRef := "http://" + resolveServiceIP(ctx, t, c, hostname) + "/repo.git@main"

	ctr := workspaceBase(t, c).
		WithNewFile("source.txt", "workspace source should not be read").
		WithNewFile("filtered/keep.txt", "workspace keep should not be read").
		WithNewFile("filtered/drop.txt", "workspace drop should not be read").
		WithNewFile("filtered/workspace-only.txt", "workspace-only should not be listed").
		WithNewFile(".dagger/config.toml", `[modules.reader]
source = "`+remoteRef+`"
entrypoint = true
`)

	out, err := ctr.With(daggerCall("read-default-file")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "module source", out)

	out, err = ctr.With(daggerCall("read-default-directory-file")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "module keep", out)

	out, err = ctr.With(daggerCall("list-ignored-default-directory")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "keep.txt\n", out)

	t.Run("direct remote module from empty cwd", func(ctx context.Context, t *testctx.T) {
		emptyCtr := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/empty")

		out, err := emptyCtr.With(daggerCallAt(remoteRef, "read-default-file")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "module source", out)

		out, err = emptyCtr.With(daggerCallAt(remoteRef, "read-default-directory-file")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "module keep", out)

		out, err = emptyCtr.With(daggerCallAt(remoteRef, "list-ignored-default-directory")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep.txt\n", out)
	})

	t.Run("direct remote module from conflicting workspace cwd", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerCallAt(remoteRef, "read-default-file")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "module source", out)

		out, err = ctr.With(daggerCallAt(remoteRef, "read-default-directory-file")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "module keep", out)

		out, err = ctr.With(daggerCallAt(remoteRef, "list-ignored-default-directory")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "keep.txt\n", out)
	})
}

func resolveServiceIP(ctx context.Context, t *testctx.T, c *dagger.Client, hostname string) string {
	t.Helper()

	// The vcs URL parser (engine/vcs/vcs.go) requires at least one dot in the
	// host component. Auto-generated service hostnames are dot-less, but they
	// are registered in the session DNS via search-domain expansion. Resolve
	// them to an IP so the URL is both parser-compatible and reachable.
	getentOut, err := c.Container().From(alpineImage).
		WithExec([]string{"getent", "hosts", hostname}).
		Stdout(ctx)
	require.NoError(t, err, "could not resolve git service hostname %q", hostname)
	fields := strings.Fields(getentOut)
	require.NotEmpty(t, fields, "unexpected getent output: %q", getentOut)
	return fields[0]
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
		sdk     string
		fixture string
		source  string
	}
	tcs := []testCase{
		{
			sdk:     "go",
			fixture: "go/path-context-git-01",
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
			sdk:     "python",
			fixture: "python/path-context-git-02",
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
			sdk:     "typescript",
			fixture: "typescript/path-context-git-03",
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
		@argument({ defaultPath: "https://github.com/dagger/dagger.git#v0.18.3") }) git: GitRef,
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
			sdk:     "java",
			fixture: "java/path-context-git-04",
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

			modGen := moduleFixture(t, c, tc.fixture).
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

	// this commit is *not* the target of any version
	// so, this ends up repinning
	commit := "ed6bf431366bac652f807864e22ae49be9433bd5"

	for _, version := range []string{"", "main", "context-git", "v1.2.3"} {
		t.Run("version="+version, func(ctx context.Context, t *testctx.T) {
			g := c.Git(remoteRepo).Ref(cmp.Or(version, "HEAD"))
			fullref, err := g.Ref(ctx)
			require.NoError(t, err)
			require.Contains(t, fullref, version)

			fixture := map[string]string{
				"":            "go/path-context-git-remote-dep-default",
				"main":        "go/path-context-git-remote-dep-main",
				"context-git": "go/path-context-git-remote-dep-context-git",
				"v1.2.3":      "go/path-context-git-remote-dep-v1-2-3",
			}[version]
			modGen := goGitBase(t, c).
				WithWorkdir("/work").
				With(withModuleFixture(t, c, "/work", fixture)).
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

// Regression test for #11996. An unversioned dependency with a named pin must
// resolve that tag or branch rather than silently falling back to the default branch.
func (ModuleSuite) TestContextGitRemoteDepNamedPin(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	remoteRepo := "github.com/dagger/dagger-test-modules"

	// Use a tag pin — tags are immutable and exercise the same ref(name: ...)
	// code path as branches, without the risk of a branch being pruned.
	pin := "v1.2.3"

	g := c.Git(remoteRepo).Ref(pin)
	fullref, err := g.Ref(ctx)
	require.NoError(t, err)

	commit, err := g.Commit(ctx)
	require.NoError(t, err)

	modGen := goGitBase(t, c).
		WithWorkdir("/work").
		With(withModuleFixture(t, c, "/work", "go/path-context-git-remote-dep-named-pin")).
		WithExec([]string{"sh", "-c", `git init && git add . && git commit -m "initial commit"`})

	out, err := modGen.With(daggerCall("test-ref-local")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, fullref+"@"+commit, out)
}

func (ModuleSuite) TestContextGitDetectDirty(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/path-context-git-detect-dirty").
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
		With(withModuleFixture(t, c, "/work", "go/path-ignore")).
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

func (ModuleSuite) TestIgnorePrefiltersExplicitDirectoryArgs(ctx context.Context, t *testctx.T) {
	type testCase struct {
		sdk     string
		fixture string
		source  string
	}

	t.Run("pre filtering directory on module call", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []testCase{
			{
				sdk:     "go",
				fixture: "go/path-ignore-prefilters-explicit-directory-args-01",
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
				sdk:     "typescript",
				fixture: "typescript/path-ignore-prefilters-explicit-directory-args-02",
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
				sdk:     "python",
				fixture: "python/path-ignore-prefilters-explicit-directory-args-03",
				source: `from typing import Annotated

import dagger
from dagger import Ignore, function, object_type


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
					WithDirectory("/work/input", c.
						Directory().
						WithNewFile("foo.txt", "foo").
						WithNewFile("bar.txt", "bar").
						WithDirectory("bar", c.Directory().WithNewFile("baz.txt", "baz"))).
					With(withModuleFixture(t, c, "/work", tc.fixture))

				out, err := modGen.With(daggerCall("test", "--dir", "./input", "entries")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "bar.txt\n", out)
			})
		}
	})
}

func (ModuleSuite) TestGitignore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dagger").
		With(withModuleFixture(t, c, "/work/dagger", "go/path-gitignore"))

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

func (ModuleSuite) TestContextParallel(ctx context.Context, t *testctx.T) {
	c1 := connect(ctx, t)
	c2 := connect(ctx, t)

	getCtr := func(c *dagger.Client, r string) *dagger.Container {
		workdir := "/" + r
		return goGitBase(t, c).
			WithMountedDirectory(workdir, c.Host().Directory("../..")).
			WithWorkdir(workdir).
			WithoutDirectory(filepath.Join(workdir, ".dagger")).
			WithoutFile(filepath.Join(workdir, "dagger.json")).
			With(withModuleFixture(t, c, workdir, "go/path-context-parallel"))
	}

	rand1 := rand.Text()
	ctr1 := getCtr(c1, rand1)
	out, err := ctr1.With(daggerCall("fn", "--rand", rand1)).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "woo", out)

	rand2 := rand.Text()
	ctr2 := getCtr(c2, rand2)
	out, err = ctr2.With(daggerCall("fn", "--rand", rand2)).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "woo", out)
}

func (ModuleSuite) TestDefaultPathNoCache(ctx context.Context, t *testctx.T) {
	t.Run("sources are reloaded when changed with defaultPath", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()
		copyTestdataFixture(ctx, t, modDir, "modules", "go", "path-default-path-no-cache")

		initialContent := "initial content"
		testFilePath := filepath.Join(modDir, "test-file.txt")
		err := os.WriteFile(testFilePath, []byte(initialContent), 0o644)
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
