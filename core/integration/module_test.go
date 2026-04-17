package core

// Workspace alignment: partially aligned; this historical umbrella suite is now down to a few leftover path/loading edge cases.
// Scope: Remaining module path/loading edge cases not yet split into narrower suites.
// Intent: Preserve confidence while incrementally extracting the last historical leftovers into precise module-owned files.
//
// Cleanup plan:
// 1. Done: exact-by-intent helpers live in module_helpers_test.go.
// 2. Done: legacy rewrite helpers live in module_legacy_helpers_test.go, visibly quarantined.
// 3. Done: workspace-owned command helpers live in workspace_test.go.
// 4. Next: decide whether these last leftovers deserve their own suites or should be absorbed into existing ownership boundaries.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ModuleSuite struct{}

func TestModule(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleSuite{})
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
		With(daggerQuery(`{hello}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"hello":"hello"}`, out)
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
					WithDirectory("/work/input", c.
						Directory().
						WithNewFile("foo.txt", "foo").
						WithNewFile("bar.txt", "bar").
						WithDirectory("bar", c.Directory().WithNewFile("baz.txt", "baz"))).
					WithWorkdir("/work/dep").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk, "--source=.")).
					With(sdkSource(tc.sdk, tc.source)).
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

func (ModuleSuite) TestLoadWhenNoModule(ctx context.Context, t *testctx.T) {
	// Verify that if a module is loaded from a directory with no module, we do
	// not load extra files.
	c := connect(ctx, t)

	tmpDir := t.TempDir()
	fileName := "foo"
	filePath := filepath.Join(tmpDir, fileName)
	require.NoError(t, os.WriteFile(filePath, []byte("foo"), 0o644))

	ents, err := c.ModuleSource(tmpDir).ContextDirectory().Entries(ctx)
	require.NoError(t, err)
	require.Empty(t, ents)
}
