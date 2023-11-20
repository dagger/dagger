package core

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core/modules"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"
)

/* TODO: add coverage for
* dagger mod use
* dagger mod sync
* that the codegen of the testdata envs are up to date (or incorporate that into a cli command)
* if a dependency changes, then checks should re-run
 */

func TestModuleGoInit(t *testing.T) {
	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=bare", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("reserved go.mod name", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=go", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{go{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"go":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("uses expected Go module name, camel-cases Dagger module name", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=My-Module", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{myModule{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"myModule":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		generated, err := modGen.File("go.mod").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, generated, "module main")
	})

	t.Run("creates go.mod beneath an existing go.mod if root points beneath it", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/go.mod", dagger.ContainerWithNewFileOpts{
				Contents: "module example.com/test\n",
			}).
			WithNewFile("/work/foo.go", dagger.ContainerWithNewFileOpts{
				Contents: "package foo\n",
			}).
			WithWorkdir("/work/ci").
			With(daggerExec("mod", "init", "--name=beneathGoMod", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{beneathGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"beneathGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("names Go module after Dagger module", func(t *testing.T) {
			generated, err := modGen.File("go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "module main")
		})
	})

	t.Run("respects existing go.mod", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "mod", "init", "example.com/test"}).
			With(daggerExec("mod", "init", "--name=hasGoMod", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{hasGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("preserves module name", func(t *testing.T) {
			generated, err := modGen.File("go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "module example.com/test")
		})
	})

	t.Run("respects parent go.mod if root points to it", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "mod", "init", "example.com/test"}).
			WithNewFile("/work/foo.go", dagger.ContainerWithNewFileOpts{
				Contents: "package foo\n",
			}).
			WithWorkdir("/work/child").
			With(daggerExec("mod", "init", "--name=child", "--sdk=go", "--root=..")).
			WithNewFile("/work/child/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `
					package main

					import "os"

					type Child struct{}

					func (m *Child) Root() *Directory {
						wd, err := os.Getwd()
						if err != nil {
							panic(err)
						}
						return dag.Host().Directory(wd+"/..")
					}
				`,
			})

		generated := modGen.
			// explicitly sync to see whether it makes a go.mod
			With(daggerExec("mod", "sync")).
			Directory(".")

		logGen(ctx, t, generated)

		out, err := modGen.
			With(daggerQuery(`{child{root{entries}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"child":{"root":{"entries":["child","foo.go","go.mod","go.sum"]}}}`, out)

		childEntries, err := generated.Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, childEntries, "go.mod")

		t.Run("preserves parent module name", func(t *testing.T) {
			generated, err := modGen.File("/work/go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "module example.com/test")
		})
	})

	t.Run("respects existing go.mod even if root points to parent that has go.mod", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "mod", "init", "example.com/test"}).
			WithNewFile("/work/foo.go", dagger.ContainerWithNewFileOpts{
				Contents: "package foo\n",
			}).
			WithWorkdir("/work/child").
			WithExec([]string{"go", "mod", "init", "my-mod"}).
			With(daggerExec("mod", "init", "--name=child", "--sdk=go", "--root=..")).
			WithNewFile("/work/child/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `
					package main

					import "os"

					type Child struct{}

					func (m *Child) Root() *Directory {
						wd, err := os.Getwd()
						if err != nil {
							panic(err)
						}
						return dag.Host().Directory(wd+"/..")
					}
				`,
			})

		generated := modGen.
			// explicitly sync to see whether it makes a go.mod
			With(daggerExec("mod", "sync")).
			Directory(".")

		logGen(ctx, t, generated)

		out, err := modGen.
			With(daggerQuery(`{child{root{entries}}}`)).
			Stdout(ctx)
		require.NoError(t, err)

		// no go.sum
		require.JSONEq(t, `{"child":{"root":{"entries":["child","foo.go","go.mod"]}}}`, out)

		childEntries, err := generated.Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, childEntries, "go.mod")
		require.Contains(t, childEntries, "go.sum")
	})

	t.Run("respects existing main.go", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `
					package main

					type HasMainGo struct {}

					func (m *HasMainGo) Hello() string { return "Hello, world!" }
				`,
			}).
			With(daggerExec("mod", "init", "--name=hasMainGo", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{hasMainGo{hello}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasMainGo":{"hello":"Hello, world!"}}`, out)
	})

	t.Run("respects existing main.go even if it uses types not generated yet", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `
					package main

					type HasDaggerTypes struct {}

					func (m *HasDaggerTypes) Hello() *Container {
						return dag.Container().
							From("` + alpineImage + `").
							WithExec([]string{"echo", "Hello, world!"})
					}
				`,
			}).
			With(daggerExec("mod", "init", "--name=hasDaggerTypes", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{hasDaggerTypes{hello{stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasDaggerTypes":{"hello":{"stdout":"Hello, world!\n"}}}`, out)
	})

	t.Run("respects existing package without creating main.go", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/notmain.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

type HasNotMainGo struct {}

func (m *HasNotMainGo) Hello() string { return "Hello, world!" }
`,
			}).
			With(daggerExec("mod", "init", "--name=hasNotMainGo", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.
			With(daggerQuery(`{hasNotMainGo{hello}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasNotMainGo":{"hello":"Hello, world!"}}`, out)
	})
}

func TestModuleInitLICENSE(t *testing.T) {
	t.Run("bootstraps Apache-2.0 LICENSE file if none found", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=licensed-to-ill", "--sdk=go"))

		content, err := modGen.File("LICENSE").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "Apache License, Version 2.0")
	})

	t.Run("creates LICENSE file in the directory specified by -m", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "-m", "./mymod", "--name=licensed-to-ill", "--sdk=go"))

		content, err := modGen.File("mymod/LICENSE").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "Apache License, Version 2.0")
	})

	t.Run("does not bootstrap LICENSE file if it exists in the parent dir", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/LICENSE", dagger.ContainerWithNewFileOpts{
				Contents: "doesnt matter",
			}).
			WithWorkdir("/work/sub").
			With(daggerExec("mod", "init", "--name=licensed-to-ill", "--sdk=go"))

		_, err := modGen.File("LICENSE").Contents(ctx)
		require.Error(t, err)
	})

	t.Run("bootstraps a LICENSE file when requested, even if it exists in the parent dir", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/LICENSE", dagger.ContainerWithNewFileOpts{
				Contents: "doesnt matter",
			}).
			WithWorkdir("/work/sub").
			With(daggerExec("mod", "init", "--name=licensed-to-ill", "--sdk=go", "--license=MIT"))

		content, err := modGen.File("LICENSE").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "MIT License")
	})
}

func TestModuleGit(t *testing.T) {
	t.Parallel()

	type testCase struct {
		sdk        string
		gitignores []string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			gitignores: []string{
				"/dagger.gen.go\n",
				"/querybuilder/\n",
			},
		},
		{
			sdk: "python",
			gitignores: []string{
				"/sdk\n",
			},
		},
	} {
		tc := tc
		t.Run(fmt.Sprintf("module %s git", tc.sdk), func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			modGen := goGitBase(t, c).
				With(daggerExec("mod", "init", "--name=bare", "--sdk="+tc.sdk))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.
				With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
				Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)

			t.Run("configures .gitignore", func(t *testing.T) {
				ignore, err := modGen.File(".gitignore").Contents(ctx)
				require.NoError(t, err)
				for _, gitignore := range tc.gitignores {
					require.Contains(t, ignore, gitignore)
				}
			})
		})
	}
}

func TestModuleGoGitRemovesIgnored(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	committedModGen := goGitBase(t, c).
		With(daggerExec("mod", "init", "--name=bare", "--sdk=go")).
		WithExec([]string{"rm", ".gitignore"}).
		// simulate old path scheme to show we ignore it too to help transition
		WithExec([]string{"mkdir", "./internal"}).
		WithExec([]string{"cp", "-a", "./querybuilder", "./internal/querybuilder"}).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init with generated files"})

	changedAfterSync, err := committedModGen.
		With(daggerExec("mod", "sync")).
		WithExec([]string{"git", "diff"}). // for debugging
		WithExec([]string{"git", "status", "--short"}).
		Stdout(ctx)
	require.NoError(t, err)
	t.Logf("changed after sync:\n%s", changedAfterSync)
	require.Contains(t, changedAfterSync, "D  dagger.gen.go\n")
	require.Contains(t, changedAfterSync, "D  querybuilder/marshal.go\n")
	require.Contains(t, changedAfterSync, "D  querybuilder/querybuilder.go\n")
	require.Contains(t, changedAfterSync, "D  internal/querybuilder/marshal.go\n")
	require.Contains(t, changedAfterSync, "D  internal/querybuilder/querybuilder.go\n")
}

func TestModulePythonGitRemovesIgnored(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	committedModGen := goGitBase(t, c).
		With(daggerExec("mod", "init", "--name=bare", "--sdk=python")).
		WithExec([]string{"rm", ".gitignore"}).
		WithExec([]string{"git", "add", "."}).
		WithExec([]string{"git", "commit", "-m", "init with generated files"})

	changedAfterSync, err := committedModGen.
		With(daggerExec("mod", "sync")).
		WithExec([]string{"git", "diff"}). // for debugging
		WithExec([]string{"git", "status", "--short"}).
		Stdout(ctx)
	require.NoError(t, err)
	t.Logf("changed after sync:\n%s", changedAfterSync)
	require.Contains(t, changedAfterSync, "D  sdk/pyproject.toml\n")
	require.Contains(t, changedAfterSync, "D  sdk/src/dagger/__init__.py\n")
}

//go:embed testdata/modules/go/minimal/main.go
var goSignatures string

func TestModuleGoSignatures(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: goSignatures,
		})

	logGen(ctx, t, modGen.Directory("."))

	t.Run("func Hello() string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"hello":"hello"}}`, out)
	})

	t.Run("func Echo(string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echo(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echo":"hello...hello...hello..."}}`, out)
	})

	t.Run("func EchoPointer(*string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echoPointer(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoPointer":"hello...hello...hello..."}}`, out)
	})

	t.Run("func EchoPointerPointer(**string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echoPointerPointer(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoPointerPointer":"hello...hello...hello..."}}`, out)
	})

	t.Run("func EchoOptional(string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echoOptional(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptional":"hello...hello...hello..."}}`, out)
		out, err = modGen.With(daggerQuery(`{minimal{echoOptional}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptional":"default...default...default..."}}`, out)
	})

	t.Run("func EchoOptionalPointer(string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echoOptionalPointer(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalPointer":"hello...hello...hello..."}}`, out)
		out, err = modGen.With(daggerQuery(`{minimal{echoOptionalPointer}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalPointer":"default...default...default..."}}`, out)
	})

	t.Run("func EchoOptionalSlice([]string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echoOptionalSlice(msg: ["hello", "there"])}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalSlice":"hello+there...hello+there...hello+there..."}}`, out)
		out, err = modGen.With(daggerQuery(`{minimal{echoOptionalSlice}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalSlice":"foobar...foobar...foobar..."}}`, out)
	})

	t.Run("func Echoes([]string) []string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echoes(msgs: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoes":["hello...hello...hello..."]}}`, out)
	})

	t.Run("func EchoesVariadic(...string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echoesVariadic(msgs: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoesVariadic":"hello...hello...hello..."}}`, out)
	})

	t.Run("func HelloContext(context.Context) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{helloContext}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloContext":"hello context"}}`, out)
	})

	t.Run("func EchoContext(context.Context, string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echoContext(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoContext":"ctx.hello...ctx.hello...ctx.hello..."}}`, out)
	})

	t.Run("func HelloStringError() (string, error)", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{helloStringError}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloStringError":"hello i worked"}}`, out)
	})

	t.Run("func HelloVoid()", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{helloVoid}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloVoid":null}}`, out)
	})

	t.Run("func HelloVoidError() error", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{helloVoidError}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloVoidError":null}}`, out)
	})

	t.Run("func EchoOpts(string, string, int) error", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi!hi!"}}`, out)
	})

	t.Run("func EchoOptsInline(struct{string, string, int}) error", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOptsInline(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInline":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptsInline(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInline":"hi!hi!"}}`, out)
	})

	t.Run("func EchoOptsInlinePointer(*struct{string, string, int}) error", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOptsInlinePointer(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlinePointer":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptsInlinePointer(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlinePointer":"hi!hi!"}}`, out)
	})

	t.Run("func EchoOptsInlineCtx(ctx, struct{string, string, int}) error", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOptsInlineCtx(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlineCtx":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptsInlineCtx(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlineCtx":"hi!hi!"}}`, out)
	})

	t.Run("func EchoOptsInlineTags(struct{string, string, int}) error", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOptsInlineTags(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlineTags":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptsInlineTags(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlineTags":"hi!hi!"}}`, out)
	})
}

func TestModuleGoSignaturesBuiltinTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "context"

type Minimal struct {}

func (m *Minimal) Read(ctx context.Context, dir Directory) (string, error) {
	return dir.File("foo").Contents(ctx)
}

func (m *Minimal) ReadPointer(ctx context.Context, dir *Directory) (string, error) {
	return dir.File("foo").Contents(ctx)
}

func (m *Minimal) ReadSlice(ctx context.Context, dir []Directory) (string, error) {
	return dir[0].File("foo").Contents(ctx)
}

func (m *Minimal) ReadVariadic(ctx context.Context, dir ...Directory) (string, error) {
	return dir[0].File("foo").Contents(ctx)
}

func (m *Minimal) ReadOptional(ctx context.Context, dir Optional[Directory]) (string, error) {
	d, ok := dir.Get()
	if ok {
		return d.File("foo").Contents(ctx)
	}
	return "", nil
}
			`,
		})
	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{directory{withNewFile(path: "foo", contents: "bar"){id}}}`)).Stdout(ctx)
	require.NoError(t, err)
	dirID := gjson.Get(out, "directory.withNewFile.id").String()

	t.Run("func Read(ctx, Directory) (string, error)", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{read(dir: "%s")}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"read":"bar"}}`, out)
	})

	t.Run("func ReadPointer(ctx, *Directory) (string, error)", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readPointer(dir: "%s")}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readPointer":"bar"}}`, out)
	})

	t.Run("func ReadSlice(ctx, []Directory) (string, error)", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readSlice(dir: ["%s"])}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readSlice":"bar"}}`, out)
	})

	t.Run("func ReadVariadic(ctx, ...Directory) (string, error)", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readVariadic(dir: ["%s"])}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readVariadic":"bar"}}`, out)
	})

	t.Run("func ReadOptional(ctx, Optional[Directory]) (string, error)", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readOptional(dir: "%s")}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readOptional":"bar"}}`, out)
		out, err = modGen.With(daggerQuery(`{minimal{readOptional}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readOptional":""}}`, out)
	})
}

func TestModuleGoSignaturesUnexported(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Minimal struct {}

type Foo struct {}

type bar struct {}

func (m *Minimal) Hello(name string) string {
	return name
}

func (f *Foo) Hello(name string) string {
	return name
}

func (b *bar) Hello(name string) string {
	return name
}
`,
		})
	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(inspectModule).Stdout(ctx)
	require.NoError(t, err)
	objs := gjson.Get(out, "host.directory.asModule.objects")

	require.Equal(t, 2, len(objs.Array()))

	minimal := objs.Get(`0.asObject`)
	require.Equal(t, "Minimal", minimal.Get("name").String())
	foo := objs.Get(`1.asObject`)
	require.Equal(t, "MinimalFoo", foo.Get("name").String())

	modGen = c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Minimal struct {}

type Foo struct {
	Bar bar
}

type bar struct {}

func (m *Minimal) Hello(name string) string {
	return name
}

func (f *Foo) Hello(name string) string {
	return name
}

func (b *bar) Hello(name string) string {
	return name
}
`,
		})
	logGen(ctx, t, modGen.Directory("."))

	_, err = modGen.With(inspectModule).Stderr(ctx)
	require.Error(t, err)
}

func TestModuleGoSignaturesMixMatch(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Minimal struct {}

func (m *Minimal) Hello(name string, opts struct{}, opts2 struct{}) string {
	return name
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	_, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)
	require.Error(t, err)
}

var inspectModule = daggerQuery(`
query {
  host {
    directory(path: ".") {
      asModule {
        objects {
          asObject {
            name
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
      }
    }
  }
}
`)

func TestModuleGoDocs(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: goSignatures,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(inspectModule).Stdout(ctx)
	require.NoError(t, err)
	obj := gjson.Get(out, "host.directory.asModule.objects.0.asObject")
	require.Equal(t, "Minimal", obj.Get("name").String())

	hello := obj.Get(`functions.#(name="hello")`)
	require.Equal(t, "hello", hello.Get("name").String())
	require.Empty(t, hello.Get("description").String())
	require.Empty(t, hello.Get("args").Array())

	// test the args-based form
	echoOpts := obj.Get(`functions.#(name="echoOpts")`)
	require.Equal(t, "echoOpts", echoOpts.Get("name").String())
	require.Equal(t, "EchoOpts does some opts things", echoOpts.Get("description").String())
	require.Len(t, echoOpts.Get("args").Array(), 3)
	require.Equal(t, "msg", echoOpts.Get("args.0.name").String())
	require.Equal(t, "the message to echo", echoOpts.Get("args.0.description").String())
	require.Equal(t, "suffix", echoOpts.Get("args.1.name").String())
	require.Equal(t, "String to append to the echoed message", echoOpts.Get("args.1.description").String())
	require.Equal(t, "times", echoOpts.Get("args.2.name").String())
	require.Equal(t, "Number of times to repeat the message", echoOpts.Get("args.2.description").String())

	// test the inline struct form
	echoOpts = obj.Get(`functions.#(name="echoOptsInline")`)
	require.Equal(t, "echoOptsInline", echoOpts.Get("name").String())
	require.Equal(t, "EchoOptsInline does some opts things", echoOpts.Get("description").String())
	require.Len(t, echoOpts.Get("args").Array(), 3)
	require.Equal(t, "msg", echoOpts.Get("args.0.name").String())
	require.Equal(t, "the message to echo", echoOpts.Get("args.0.description").String())
	require.Equal(t, "suffix", echoOpts.Get("args.1.name").String())
	require.Equal(t, "String to append to the echoed message", echoOpts.Get("args.1.description").String())
	require.Equal(t, "times", echoOpts.Get("args.2.name").String())
	require.Equal(t, "Number of times to repeat the message", echoOpts.Get("args.2.description").String())
}

func TestModuleGoDocsEdgeCases(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Minimal struct {}

// some docs
func (m *Minimal) Hello(foo string, bar string,
// hello
baz string, qux string, x string, // lol
) string {
	return foo + bar
}

func (m *Minimal) HelloMore(
	// foo here
	foo,
	// bar here
	bar string,
) string {
	return foo + bar
}

func (m *Minimal) HelloMoreInline(opts struct{
	// foo here
	foo, bar string
}) string {
	return opts.foo + opts.bar
}

func (m *Minimal) HelloAgain( // docs for helloagain
	foo string,
	bar string, // docs for bar
	baz string,
) string {
	return foo + bar
}

func (m *Minimal) HelloFinal(
	foo string) string { // woops
	return foo
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(inspectModule).Stdout(ctx)
	require.NoError(t, err)
	obj := gjson.Get(out, "host.directory.asModule.objects.0.asObject")
	require.Equal(t, "Minimal", obj.Get("name").String())

	hello := obj.Get(`functions.#(name="hello")`)
	require.Equal(t, "hello", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 5)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "", hello.Get("args.1.description").String())
	require.Equal(t, "baz", hello.Get("args.2.name").String())
	require.Equal(t, "hello", hello.Get("args.2.description").String())
	require.Equal(t, "qux", hello.Get("args.3.name").String())
	require.Equal(t, "", hello.Get("args.3.description").String())
	require.Equal(t, "x", hello.Get("args.4.name").String())
	require.Equal(t, "lol", hello.Get("args.4.description").String())

	hello = obj.Get(`functions.#(name="helloMore")`)
	require.Equal(t, "helloMore", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 2)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "foo here", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "bar here", hello.Get("args.1.description").String())

	hello = obj.Get(`functions.#(name="helloMoreInline")`)
	require.Equal(t, "helloMoreInline", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 2)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "foo here", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "", hello.Get("args.1.description").String())

	hello = obj.Get(`functions.#(name="helloAgain")`)
	require.Equal(t, "helloAgain", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 3)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "docs for bar", hello.Get("args.1.description").String())
	require.Equal(t, "baz", hello.Get("args.2.name").String())
	require.Equal(t, "", hello.Get("args.2.description").String())

	hello = obj.Get(`functions.#(name="helloFinal")`)
	require.Equal(t, "helloFinal", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 1)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "", hello.Get("args.0.description").String())
}

//go:embed testdata/modules/go/extend/main.go
var goExtend string

// this is no longer allowed, but verify the SDK errors out
func TestModuleGoExtendCore(t *testing.T) {
	t.Parallel()

	var logs safeBuffer
	c, ctx := connect(t, dagger.WithLogOutput(&logs))

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: goExtend,
		}).
		With(daggerExec("mod", "sync")).
		Sync(ctx)

	require.Error(t, err)
	require.NoError(t, c.Close())
	require.Contains(t, logs.String(), "cannot define methods on objects from outside this module")
}

//go:embed testdata/modules/go/custom-types/main.go
var goCustomTypes string

//go:embed testdata/modules/python/custom-types/main.py
var pythonCustomTypes string

func TestModuleCustomTypes(t *testing.T) {
	t.Parallel()

	type testCase struct {
		sdk           string
		sourcePath    string
		sourceContent string
	}

	for _, tc := range []testCase{
		{
			sdk:           "go",
			sourcePath:    "main.go",
			sourceContent: goCustomTypes,
		},
		{
			sdk:           "python",
			sourcePath:    "src/main.py",
			sourceContent: pythonCustomTypes,
		},
	} {
		t.Run(fmt.Sprintf("custom %s types", tc.sdk), func(t *testing.T) {
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=test", "--sdk="+tc.sdk)).
				WithNewFile(tc.sourcePath, dagger.ContainerWithNewFileOpts{
					Contents: tc.sourceContent,
				})

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{test{repeater(msg:"echo!", times: 3){render}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"repeater":{"render":"echo!echo!echo!"}}}`, out)
		})
	}
}

func TestModuleGoReturnTypeDetection(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=foo", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Foo struct {}

type X struct {
	Message string ` + "`json:\"message\"`" + `
}

func (m *Foo) MyFunction() X {
	return X{Message: "foo"}
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{foo{myFunction{message}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"foo":{"myFunction":{"message":"foo"}}}`, out)
}

func TestModuleGoReturnStruct(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=foo", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Foo struct {}

type X struct {
	Message string ` + "`json:\"message\"`" + `
	When string ` + "`json:\"Timestamp\"`" + `
	To string ` + "`json:\"recipient\"`" + `
	From string
}

func (m *Foo) MyFunction() X {
	return X{Message: "foo", When: "now", To: "user", From: "admin"}
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{foo{myFunction{message, recipient, from, timestamp}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"foo":{"myFunction":{"message":"foo", "recipient":"user", "from":"admin", "timestamp":"now"}}}`, out)
}

func TestModuleGoReturnNestedStruct(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=playground", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Playground struct{}

type Foo struct {
	MsgContainer Bar
}

type Bar struct {
	Msg string
}

func (m *Playground) MyFunction() Foo {
	return Foo{MsgContainer: Bar{Msg: "hello world"}}
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{playground{myFunction{msgContainer{msg}}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"playground":{"myFunction":{"msgContainer":{"msg": "hello world"}}}}`, out)
}

func TestModuleGoReturnCompositeCore(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=playground", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Playground struct{}

func (m *Playground) MySlice() []*Container {
	return []*Container{dag.Container().From("alpine:latest").WithExec([]string{"echo", "hello world"})}
}

type Foo struct {
	Con *Container
}

func (m *Playground) MyStruct() *Foo {
	return &Foo{Con: dag.Container().From("alpine:latest").WithExec([]string{"echo", "hello world"})}
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{playground{mySlice{stdout}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"playground":{"mySlice":[{"stdout":"hello world\n"}]}}`, out)

	out, err = modGen.With(daggerQuery(`{playground{myStruct{con{stdout}}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"playground":{"myStruct":{"con":{"stdout":"hello world\n"}}}}`, out)
}

func TestModuleGoReturnComplexThing(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=playground", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Playground struct{}

type ScanResult struct {
	Containers	[]*Container ` + "`json:\"targets\"`" + `
	Report		ScanReport
}

type ScanReport struct {
	Contents string ` + "`json:\"contents\"`" + `
	Authors  []string ` + "`json:\"Authors\"`" + `
}

func (m *Playground) Scan() ScanResult {
	return ScanResult{
		Containers: []*Container{
			dag.Container().From("alpine:latest").WithExec([]string{"echo", "hello world"}),
		},
		Report: ScanReport{
			Contents: "hello world",
			Authors: []string{"foo", "bar"},
		},
	}
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{playground{scan{targets{stdout},report{contents,authors}}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"playground":{"scan":{"targets":[{"stdout":"hello world\n"}],"report":{"contents":"hello world","authors":["foo","bar"]}}}}`, out)
}

func TestModuleGoGlobalVarDAG(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=foo", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "context"

type Foo struct {}

var someDefault = dag.Container().From("alpine:latest")

func (m *Foo) Fn(ctx context.Context) (string, error) {
	return someDefault.WithExec([]string{"echo", "foo"}).Stdout(ctx)
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{foo{fn}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"foo":{"fn":"foo\n"}}`, out)
}

func TestModuleGoIDableType(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=foo", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Foo struct {
	Data string
}

func (m *Foo) Set(data string) *Foo {
	m.Data = data
	return m
}

func (m *Foo) Get() string {
	return m.Data
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	// sanity check
	out, err := modGen.With(daggerQuery(`{foo{set(data: "abc"){get}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"foo":{"set":{"get": "abc"}}}`, out)

	out, err = modGen.With(daggerQuery(`{foo{set(data: "abc"){id}}}`)).Stdout(ctx)
	require.NoError(t, err)
	id := gjson.Get(out, "foo.set.id").String()
	require.Contains(t, id, "moddata:")

	out, err = modGen.With(daggerQuery(`{loadFooFromID(id: "%s"){get}}`, id)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"loadFooFromID":{"get": "abc"}}`, out)
}

func TestModuleArgOwnType(t *testing.T) {
	// Verify use of a module's own object as an argument type.
	// The server needs to specifically decode the input type from an ID into
	// the raw JSON, since the module doesn't understand it's own types as IDs

	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=foo", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "strings"

type Foo struct{}

type Message struct {
	Content string
}

func (m *Foo) SayHello(name string) Message {
	return Message{Content: "hello " + name}
}

func (m *Foo) Upper(msg Message) Message {
	msg.Content = strings.ToUpper(msg.Content)
	return msg
}

func (m *Foo) Uppers(msg []Message) []Message {
	for i := range msg {
		msg[i].Content = strings.ToUpper(msg[i].Content)
	}
	return msg
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{foo{sayHello(name: "world"){id}}}`)).Stdout(ctx)
	require.NoError(t, err)
	id := gjson.Get(out, "foo.sayHello.id").String()
	require.Contains(t, id, "moddata:")

	out, err = modGen.With(daggerQuery(`{foo{upper(msg:"%s"){content}}}`, id)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"foo":{"upper":{"content": "HELLO WORLD"}}}`, out)

	out, err = modGen.With(daggerQuery(`{foo{uppers(msg:["%s", "%s"]){content}}}`, id, id)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"foo":{"uppers":[{"content": "HELLO WORLD"}, {"content": "HELLO WORLD"}]}}`, out)
}

func TestModuleConflictingSameNameDeps(t *testing.T) {
	// A -> B -> Dint
	// A -> C -> Dstr
	// where Dint and Dstr are modules with the same name and same object names but conflicting types
	t.Parallel()

	c, ctx := connect(t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dstr").
		With(daggerExec("mod", "init", "--name=d", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type D struct{}

type Obj struct {
	Foo string
}

func (m *D) Fn(foo string) Obj {
	return Obj{Foo: foo}
}
`,
		})

	ctr = ctr.
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dint").
		With(daggerExec("mod", "init", "--name=d", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type D struct{}

type Obj struct {
	Foo int
}

func (m *D) Fn(foo int) Obj {
	return Obj{Foo: foo}
}
`,
		})

	ctr = ctr.
		WithWorkdir("/work/c").
		With(daggerExec("mod", "init", "--name=c", "--sdk=go", "--root=..")).
		With(daggerExec("mod", "install", "../dstr")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import (
	"context"
)

type C struct{}

func (m *C) Fn(ctx context.Context, foo string) (string, error) {
	return dag.D().Fn(foo).Foo(ctx)
}
`,
		})

	ctr = ctr.
		WithWorkdir("/work/b").
		With(daggerExec("mod", "init", "--name=b", "--sdk=go", "--root=..")).
		With(daggerExec("mod", "install", "../dint")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import (
	"context"
)

type B struct{}

func (m *B) Fn(ctx context.Context, foo int) (int, error) {
	return dag.D().Fn(foo).Foo(ctx)
}
`,
		})

	ctr = ctr.
		WithWorkdir("/work/a").
		With(daggerExec("mod", "init", "--name=a", "--sdk=go", "--root=..")).
		With(daggerExec("mod", "install", "../b")).
		With(daggerExec("mod", "install", "../c")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

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
		})

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

func TestModuleWithOtherModuleTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Dep struct{}

type Obj struct {
	Foo string
}

func (m *Dep) Fn() Obj {
	return Obj{Foo: "foo"}
}
`,
		}).
		WithWorkdir("/work/test").
		With(daggerExec("mod", "init", "--name=test", "--sdk=go", "--root=..")).
		With(daggerExec("mod", "install", "../dep"))

	t.Run("return as other module object", func(t *testing.T) {
		t.Parallel()

		t.Run("direct", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

type Test struct{}

func (m *Test) Fn() (*DepObj, error) {
	return nil, nil
}
`,
				}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q function %q cannot return external type from dependency module %q",
				"Test", "Fn", "dep",
			))
		})

		t.Run("list", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

type Test struct{}

func (m *Test) Fn() ([]*DepObj, error) {
	return nil, nil
}
`,
				}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q function %q cannot return external type from dependency module %q",
				"Test", "Fn", "dep",
			))
		})
	})

	t.Run("arg as other module object", func(t *testing.T) {
		t.Parallel()

		t.Run("direct", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

type Test struct{}

func (m *Test) Fn(obj *DepObj) error {
	return nil
}
`,
			}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q function %q arg %q cannot reference external type from dependency module %q",
				"Test", "Fn", "obj", "dep",
			))
		})

		t.Run("list", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

type Test struct{}

func (m *Test) Fn(obj []*DepObj) error {
	return nil
}
`,
			}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q function %q arg %q cannot reference external type from dependency module %q",
				"Test", "Fn", "obj", "dep",
			))
		})
	})

	t.Run("field as other module object", func(t *testing.T) {
		t.Parallel()

		t.Run("direct", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

type Test struct{}

type Obj struct {
	Foo *DepObj
}

func (m *Test) Fn() (*Obj, error) {
	return nil, nil
}
`,
				}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q field %q cannot reference external type from dependency module %q",
				"Obj", "Foo", "dep",
			))
		})

		t.Run("list", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

type Test struct{}

type Obj struct {
	Foo []*DepObj
}

func (m *Test) Fn() (*Obj, error) {
	return nil, nil
}
`,
				}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, fmt.Sprintf(
				"object %q field %q cannot reference external type from dependency module %q",
				"Obj", "Foo", "dep",
			))
		})
	})
}

//go:embed testdata/modules/go/use/dep/main.go
var useInner string

//go:embed testdata/modules/go/use/main.go
var useOuter string

func TestModuleUseLocal(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	t.Run("go uses go", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=use", "--sdk=go")).
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: useInner,
			}).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: useOuter,
			}).
			With(daggerExec("mod", "install", "./dep")).
			WithEnvVariable("BUST", identity.NewID()) // NB(vito): hmm...

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

		// cannot use transitive dependency directly
		_, err = modGen.With(daggerQuery(`{dep{hello}}`)).Stdout(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, `Cannot query field "dep" on type "Query".`)
	})

	t.Run("python uses go", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: useInner,
			}).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=use", "--sdk=python")).
			With(daggerExec("mod", "install", "./dep")).
			WithNewFile("/work/src/main.py", dagger.ContainerWithNewFileOpts{
				Contents: `from dagger.mod import function
import dagger

@function
def use_hello() -> str:
    return dagger.dep().hello()
`,
			})

		out, err := modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

		// cannot use transitive dependency directly
		_, err = modGen.With(daggerQuery(`{dep{hello}}`)).Stdout(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, `Cannot query field "dep" on type "Query".`)
	})
}

func TestModuleCodegenonDepChange(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	t.Run("go uses go", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=use", "--sdk=go")).
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: useInner,
			}).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: useOuter,
			}).
			With(daggerExec("mod", "install", "./dep"))

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

		// make back-incompatible change to dep
		newInner := strings.ReplaceAll(useInner, `Hello()`, `Hellov2()`)
		modGen = modGen.
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: newInner,
			}).
			WithExec([]string{"sh", "-c", "dagger mod sync"}, dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			})

		codegenContents, err := modGen.File("/work/dagger.gen.go").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, codegenContents, "Hellov2")

		newOuter := strings.ReplaceAll(useOuter, `Hello(ctx)`, `Hellov2(ctx)`)
		modGen = modGen.
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: newOuter,
			})

		out, err = modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)
	})

	t.Run("python uses go", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: useInner,
			}).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=use", "--sdk=python")).
			With(daggerExec("mod", "install", "./dep")).
			WithNewFile("/work/src/main.py", dagger.ContainerWithNewFileOpts{
				Contents: `from dagger.mod import function
import dagger

@function
def use_hello() -> str:
    return dagger.dep().hello()
`,
			})

		out, err := modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

		// make back-incompatible change to dep
		newInner := strings.ReplaceAll(useInner, `Hello()`, `Hellov2()`)
		modGen = modGen.
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: newInner,
			}).
			WithExec([]string{"sh", "-c", "dagger mod sync"}, dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			})

		codegenContents, err := modGen.File("/work/sdk/src/dagger/client/gen.py").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, codegenContents, "hellov2")

		modGen = modGen.
			WithNewFile("/work/src/main.py", dagger.ContainerWithNewFileOpts{
				Contents: `from dagger.mod import function
import dagger

@function
def use_hello() -> str:
    return dagger.dep().hellov2()
`,
			})

		out, err = modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)
	})
}

func TestModuleGoUseLocalMulti(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/foo").
		WithNewFile("/work/foo/main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Foo struct {}

func (m *Foo) Name() string { return "foo" }
`,
		}).
		With(daggerExec("mod", "init", "--name=foo", "--sdk=go")).
		WithWorkdir("/work/bar").
		WithNewFile("/work/bar/main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Bar struct {}

func (m *Bar) Name() string { return "bar" }
`,
		}).
		With(daggerExec("mod", "init", "--name=bar", "--sdk=go")).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=use", "--sdk=go")).
		With(daggerExec("mod", "install", "./foo")).
		With(daggerExec("mod", "install", "./bar")).
		WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

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
		}).
		WithEnvVariable("BUST", identity.NewID()) // NB(vito): hmm...

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{use{names}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"use":{"names":["foo", "bar"]}}`, out)
}

//go:embed testdata/modules/go/wrapper/main.go
var wrapper string

func TestModuleGoWrapping(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=wrapper", "--sdk=go")).
		WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
			Contents: wrapper,
		})

	logGen(ctx, t, modGen.Directory("."))

	id := identity.NewID()
	out, err := modGen.With(daggerQuery(
		fmt.Sprintf(`{wrapper{container{echo(msg:%q){unwrap{stdout}}}}}`, id),
	)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t,
		fmt.Sprintf(`{"wrapper":{"container":{"echo":{"unwrap":{"stdout":%q}}}}}`, id),
		out)
}

func TestModuleConfigAPI(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	moduleDir := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/subdir").
		With(daggerExec("mod", "init", "--name=test", "--sdk=go", "--root=..")).
		Directory("/work")

	cfg := c.ModuleConfig(moduleDir, dagger.ModuleConfigOpts{Subpath: "subdir"})

	name, err := cfg.Name(ctx)
	require.NoError(t, err)
	require.Equal(t, "test", name)

	sdk, err := cfg.SDK(ctx)
	require.NoError(t, err)
	require.Equal(t, "go", sdk)

	root, err := cfg.Root(ctx)
	require.NoError(t, err)
	require.Equal(t, "..", root)
}

func TestModulePythonInit(t *testing.T) {
	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=bare", "--sdk=python"))

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
			WithWorkdir("/work/child").
			With(daggerExec("mod", "init", "--name=bare", "--sdk=python", "--root=.."))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
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
			With(daggerExec("mod", "init", "--name=hasPyproject", "--sdk=python"))

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
				Contents: `from dagger.mod import function

@function
def hello() -> str:
    return "Hello, world!"
`,
			}).
			With(daggerExec("mod", "init", "--name=hasMainPy", "--sdk=python"))

		out, err := modGen.
			With(daggerQuery(`{hasMainPy{hello}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasMainPy":{"hello":"Hello, world!"}}`, out)
	})
}

func TestModulePythonReturnSelf(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=foo", "--sdk=python")).
		WithNewFile("src/main.py", dagger.ContainerWithNewFileOpts{
			Contents: `from typing import Self

from dagger.mod import field, function, object_type

@object_type
class Foo:
    message: str = field(default="")

    @function
    def bar(self) -> Self:
        self.message = "foobar"
        return self
`,
		})

	out, err := modGen.With(daggerQuery(`{foo{bar{message}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"foo":{"bar":{"message":"foobar"}}}`, out)
}

func TestModuleLotsOfFunctions(t *testing.T) {
	t.Parallel()

	const funcCount = 100

	t.Run("go sdk", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

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
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: mainSrc,
			}).
			With(daggerExec("mod", "init", "--name=potatoSack", "--sdk=go"))

		logGen(ctx, t, modGen.Directory("."))

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					With(daggerCall(fmt.Sprintf("potato%d", i))).
					Sync(ctx)
				return err
			})
		}
		require.NoError(t, eg.Wait())
	})

	t.Run("python sdk", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		mainSrc := `from dagger.mod import function
		`

		for i := 0; i < funcCount; i++ {
			mainSrc += fmt.Sprintf(`
@function
def potato_%d() -> str:
    return "potato #%d"
`, i, i)
		}

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("./src/main.py", dagger.ContainerWithNewFileOpts{
				Contents: mainSrc,
			}).
			With(daggerExec("mod", "init", "--name=potatoSack", "--sdk=python"))

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					With(daggerCall(fmt.Sprintf("potato%d", i))).
					Sync(ctx)
				return err
			})
		}
		require.NoError(t, eg.Wait())
	})
}

func TestModuleLotsOfDeps(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
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
	getModDaggerConfig := func(name string, depNames []string) string {
		t.Helper()

		var depVals []string
		for _, depName := range depNames {
			depVals = append(depVals, "../"+depName)
		}
		cfg := modules.Config{
			Name:         name,
			Root:         "..",
			SDK:          "go",
			Dependencies: depVals,
		}
		bs, err := json.Marshal(cfg)
		require.NoError(t, err)
		return string(bs)
	}

	addModulesWithDeps := func(newMods int, depNames []string) []string {
		t.Helper()

		var newModNames []string
		for i := 0; i < newMods; i++ {
			name := fmt.Sprintf("mod%d", modCount)
			modCount++
			newModNames = append(newModNames, name)
			modGen = modGen.
				WithWorkdir("/work/"+name).
				WithNewFile("./main.go", dagger.ContainerWithNewFileOpts{
					Contents: getModMainSrc(name, depNames),
				}).
				WithNewFile("./dagger.json", dagger.ContainerWithNewFileOpts{
					Contents: getModDaggerConfig(name, depNames),
				})
		}
		return newModNames
	}

	// Create a base module, then add 6 layers of deps, where each layer has one more module
	// than the last of modules as the previous layer and each module within the layer has a
	// dep on each module from the previous layer. Finally add a single module at the top that
	// depends on all modules from the last layer and call that.
	// Basically, this creates a quadratically growing DAG of modules and verifies we
	// handle it efficiently enough to be callable.
	curDeps := addModulesWithDeps(1, nil)
	for i := 0; i < 6; i++ {
		curDeps = addModulesWithDeps(len(curDeps)+1, curDeps)
	}
	addModulesWithDeps(1, curDeps)

	_, err := modGen.With(daggerCall("fn")).Stdout(ctx)
	require.NoError(t, err)
}

func TestModuleNamespacing(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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
	require.JSONEq(t, `{"test":{"fn":"1:yo 2:yo"}}`, out)
}

func TestModuleRoots(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	moduleSrcPath, err := filepath.Abs("./testdata/modules/go/roots")
	require.NoError(t, err)

	entries, err := os.ReadDir(moduleSrcPath)
	require.NoError(t, err)

	ctr := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithMountedDirectory("/work", c.Host().Directory(moduleSrcPath))

	for _, entry := range entries {
		entry := entry
		t.Run(entry.Name(), func(t *testing.T) {
			t.Parallel()

			ctr := ctr.WithWorkdir("/work/" + entry.Name())

			out, err := ctr.
				With(daggerQuery(fmt.Sprintf(`{%s{hello}}`, strcase.ToLowerCamel(entry.Name())))).
				Stdout(ctx)
			if strings.HasPrefix(entry.Name(), "good-") {
				require.NoError(t, err)
				require.JSONEq(t, fmt.Sprintf(`{"%s":{"hello": "hello"}}`, strcase.ToLowerCamel(entry.Name())), out)
			} else if strings.HasPrefix(entry.Name(), "bad-") {
				require.Error(t, err)
				require.Regexp(t, "is not under( module)? root", err.Error())
			}
		})
	}
}

func TestModuleDaggerCall(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	t.Run("service args", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main
import (
	"context"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context, svc *Service) (string, error) {
	return dag.Container().From("alpine:3.18").WithExec([]string{"apk", "add", "curl"}).
		WithServiceBinding("daserver", svc).
		WithExec([]string{"curl", "http://daserver:8000"}).
		Stdout(ctx)
}
`,
			})

		logGen(ctx, t, modGen.Directory("."))

		httpServer, _ := httpService(ctx, t, c, "im up")
		endpoint, err := httpServer.Endpoint(ctx)
		require.NoError(t, err)

		out, err := modGen.
			WithServiceBinding("testserver", httpServer).
			With(daggerCall("fn", "--svc", "tcp://"+endpoint)).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(out), "im up")
	})

	t.Run("list args", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
			WithNewFile("foo.txt", dagger.ContainerWithNewFileOpts{
				Contents: "bar",
			}).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main
import (
	"context"
	"strings"
)

type Minimal struct {}

func (m *Minimal) Hello(msgs []string) string {
	return strings.Join(msgs, "+")
}

func (m *Minimal) Reads(ctx context.Context, files []File) (string, error) {
	var contents []string
	for _, f := range files {
		content, err := f.Contents(ctx)
		if err != nil {
			return "", err
		}
		contents = append(contents, content)
	}
	return strings.Join(contents, "+"), nil
}
`,
			})

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.With(daggerCall("hello", "--msgs", "yo", "--msgs", "my", "--msgs", "friend")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(out), "yo+my+friend")

		out, err = modGen.With(daggerCall("reads", "--files=foo.txt", "--files=foo.txt")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(out), "bar+bar")
	})

	t.Run("return list objects", func(t *testing.T) {
		t.Parallel()

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main
type Minimal struct {}

type Foo struct {
	Bar int ` + "`" + `json:"bar"` + "`" + `
}

func (m *Minimal) Fn() []*Foo {
	var foos []*Foo
	for i := 0; i < 3; i++ {
		foos = append(foos, &Foo{Bar: i})
	}
	return foos
}
`,
			})

		logGen(ctx, t, modGen.Directory("."))

		out, err := modGen.With(daggerCall("fn", "bar")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(out), "0\n1\n2")
	})

	t.Run("directory arg inputs", func(t *testing.T) {
		t.Parallel()

		t.Run("local dir", func(t *testing.T) {
			t.Parallel()
			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
				WithNewFile("/dir/subdir/foo.txt", dagger.ContainerWithNewFileOpts{
					Contents: "foo",
				}).
				WithNewFile("/dir/subdir/bar.txt", dagger.ContainerWithNewFileOpts{
					Contents: "bar",
				}).
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main
type Test struct {}

func (m *Test) Fn(dir *Directory) *Directory {
	return dir
}
	`,
				})

			out, err := modGen.With(daggerCall("fn", "--dir", "/dir/subdir")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.TrimSpace(out), "bar.txt\nfoo.txt")

			out, err = modGen.With(daggerCall("fn", "--dir", "file:///dir/subdir")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, strings.TrimSpace(out), "bar.txt\nfoo.txt")
		})

		t.Run("git dir", func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main
type Test struct {}

func (m *Test) Fn(dir *Directory, subpath Optional[string]) *Directory {
	return dir.Directory(subpath.GetOr("."))
}
	`,
				})

			for _, tc := range []struct {
				baseURL string
				subpath string
			}{
				{
					baseURL: "https://github.com/dagger/dagger",
				},
				{
					baseURL: "https://github.com/dagger/dagger",
					subpath: ".changes",
				},
				{
					baseURL: "https://github.com/dagger/dagger.git",
				},
				{
					baseURL: "https://github.com/dagger/dagger.git",
					subpath: ".changes",
				},
			} {
				tc := tc
				t.Run(fmt.Sprintf("%s:%s", tc.baseURL, tc.subpath), func(t *testing.T) {
					t.Parallel()
					url := tc.baseURL + "#v0.9.1"
					if tc.subpath != "" {
						url += ":" + tc.subpath
					}

					args := []string{"fn", "--dir", url}
					if tc.subpath == "" {
						args = append(args, "--subpath", ".changes")
					}
					out, err := modGen.With(daggerCall(args...)).Stdout(ctx)
					require.NoError(t, err)

					require.Contains(t, out, "v0.9.1.md")
					require.NotContains(t, out, "v0.9.2.md")
				})
			}
		})
	})
}

func TestModuleGoSyncDeps(t *testing.T) {
	// verify that changes to deps result in a sync to the depender module
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=use", "--sdk=go")).
		WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
			Contents: useInner,
		}).
		WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
			Contents: useOuter,
		}).
		With(daggerExec("mod", "install", "./dep"))

	logGen(ctx, t, modGen.Directory("."))

	modGen = modGen.With(daggerQuery(`{use{useHello}}`))
	out, err := modGen.Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

	newInner := strings.ReplaceAll(useInner, `"hello"`, `"goodbye"`)
	modGen = modGen.
		WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
			Contents: newInner,
		}).
		With(daggerExec("mod", "sync"))

	logGen(ctx, t, modGen.Directory("."))

	out, err = modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"use":{"useHello":"goodbye"}}`, out)
}

//go:embed testdata/modules/go/id/arg/main.go
var badIDArgGoSrc string

//go:embed testdata/modules/python/id/arg/src/main.py
var badIDArgPySrc string

//go:embed testdata/modules/go/id/field/main.go
var badIDFieldGoSrc string

//go:embed testdata/modules/go/id/fn/main.go
var badIDFnGoSrc string

//go:embed testdata/modules/python/id/fn/src/main.py
var badIDFnPySrc string

func TestModuleReservedWords(t *testing.T) {
	// verify disallowed names are rejected

	t.Parallel()

	c, ctx := connect(t)

	t.Run("id", func(t *testing.T) {
		t.Parallel()

		t.Run("arg", func(t *testing.T) {
			t.Parallel()

			t.Run("go", func(t *testing.T) {
				t.Parallel()

				_, err := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
					WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
						Contents: badIDArgGoSrc,
					}).
					With(daggerQuery(`{test{fn(id:"no")}}`)).
					Sync(ctx)
				require.ErrorContains(t, err, "cannot define argument with reserved name \"id\"")
			})

			t.Run("python", func(t *testing.T) {
				t.Parallel()

				_, err := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(daggerExec("mod", "init", "--name=test", "--sdk=python")).
					WithNewFile("/work/src/main.py", dagger.ContainerWithNewFileOpts{
						Contents: badIDArgPySrc,
					}).
					With(daggerQuery(`{test{fn(id:"no")}}`)).
					Sync(ctx)
				require.ErrorContains(t, err, "cannot define argument with reserved name \"id\"")
			})
		})

		t.Run("field", func(t *testing.T) {
			t.Parallel()

			t.Run("go", func(t *testing.T) {
				t.Parallel()

				_, err := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
					WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
						Contents: badIDFieldGoSrc,
					}).
					With(daggerQuery(`{test{fn{id}}}`)).
					Sync(ctx)
				require.ErrorContains(t, err, "cannot define field with reserved name \"id\"")
			})
		})

		t.Run("fn", func(t *testing.T) {
			t.Parallel()

			t.Run("go", func(t *testing.T) {
				t.Parallel()

				_, err := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
					WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
						Contents: badIDFnGoSrc,
					}).
					With(daggerQuery(`{test{id}}`)).
					Sync(ctx)
				require.ErrorContains(t, err, "cannot define function with reserved name \"id\"")
			})

			t.Run("python", func(t *testing.T) {
				t.Parallel()

				_, err := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(daggerExec("mod", "init", "--name=test", "--sdk=python")).
					WithNewFile("/work/src/main.py", dagger.ContainerWithNewFileOpts{
						Contents: badIDFnPySrc,
					}).
					With(daggerQuery(`{test{fn(id:"no")}}`)).
					Sync(ctx)
				require.ErrorContains(t, err, "cannot define function with reserved name \"id\"")
			})
		})
	})
}

func TestEnvCmd(t *testing.T) {
	t.Skip("pending conversion to modules")

	t.Parallel()

	type testCase struct {
		environmentPath string
		expectedSDK     string
		expectedName    string
		expectedRoot    string
	}
	for _, tc := range []testCase{
		{
			environmentPath: "core/integration/testdata/environments/go/basic",
			expectedSDK:     "go",
			expectedName:    "basic",
			expectedRoot:    "../../../../../../",
		},
	} {
		tc := tc
		for _, testGitEnv := range []bool{false, true} {
			testGitEnv := testGitEnv
			testName := "local environment"
			if testGitEnv {
				testName = "git environment"
			}
			testName += "/" + tc.environmentPath
			t.Run(testName, func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				stderr, err := CLITestContainer(ctx, t, c).
					WithLoadedEnv(tc.environmentPath, testGitEnv).
					CallEnv().
					Stderr(ctx)
				require.NoError(t, err)
				require.Contains(t, stderr, fmt.Sprintf(`"root": %q`, tc.expectedRoot))
				require.Contains(t, stderr, fmt.Sprintf(`"name": %q`, tc.expectedName))
				require.Contains(t, stderr, fmt.Sprintf(`"sdk": %q`, tc.expectedSDK))
			})
		}
	}
}

func TestEnvCmdHelps(t *testing.T) {
	t.Skip("pending conversion to modules")

	t.Parallel()
	c, ctx := connect(t)

	baseCtr := CLITestContainer(ctx, t, c).WithHelpArg(true)

	// test with no env specified
	noEnvCtr := baseCtr
	// test with a valid local env
	validLocalEnvCtr := baseCtr.WithLoadedEnv("core/integration/testdata/environments/go/basic", false)
	// test with a broken local env (this helps ensure that we aren't actually running the entrypoints, if we did we'd get an error)
	brokenLocalEnvCtr := baseCtr.WithLoadedEnv("core/integration/testdata/environments/go/broken", false)

	for _, ctr := range []*DaggerCLIContainer{noEnvCtr, validLocalEnvCtr, brokenLocalEnvCtr} {
		type testCase struct {
			testName       string
			cmdCtr         *DaggerCLIContainer
			expectedOutput string
		}
		for _, tc := range []testCase{
			{
				testName:       "dagger env/" + ctr.EnvArg,
				cmdCtr:         ctr.CallEnv(),
				expectedOutput: "Usage:\n  dagger environment [flags]\n\nAliases:\n  environment, env",
			},
			{
				testName:       "dagger env init/" + ctr.EnvArg,
				cmdCtr:         ctr.CallEnvInit(),
				expectedOutput: "Usage:\n  dagger environment init",
			},
			{
				testName:       "dagger env sync/" + ctr.EnvArg,
				cmdCtr:         ctr.CallEnvSync(),
				expectedOutput: "Usage:\n  dagger environment sync",
			},
			{
				testName:       "dagger env extend/" + ctr.EnvArg,
				cmdCtr:         ctr.CallEnvExtend("./fake/dep"),
				expectedOutput: "Usage:\n  dagger environment extend",
			},
			{
				testName:       "dagger checks/" + ctr.EnvArg,
				cmdCtr:         ctr.CallChecks(),
				expectedOutput: "Usage:\n  dagger checks",
			},
		} {
			tc := tc
			t.Run(tc.testName, func(t *testing.T) {
				t.Parallel()
				stdout, err := tc.cmdCtr.Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, stdout, tc.expectedOutput)
			})
		}
	}
}

func TestEnvCmdInit(t *testing.T) {
	t.Skip("pending conversion to modules")

	t.Parallel()

	type testCase struct {
		testName             string
		environmentPath      string
		sdk                  string
		name                 string
		root                 string
		expectedErrorMessage string
	}
	for _, tc := range []testCase{
		{
			testName:        "explicit environment dir/go",
			environmentPath: "/var/testenvironment/subdir",
			sdk:             "go",
			name:            identity.NewID(),
			root:            "../",
		},
		{
			testName:        "explicit environment dir/python",
			environmentPath: "/var/testenvironment/subdir",
			sdk:             "python",
			name:            identity.NewID(),
			root:            "../..",
		},
		{
			testName:        "explicit environment file",
			environmentPath: "/var/testenvironment/subdir/dagger.json",
			sdk:             "python",
			name:            identity.NewID(),
		},
		{
			testName: "implicit environment",
			sdk:      "go",
			name:     identity.NewID(),
		},
		{
			testName:        "implicit environment with root",
			environmentPath: "/var/testenvironment",
			sdk:             "python",
			name:            identity.NewID(),
			root:            "..",
		},
		{
			testName:             "invalid sdk",
			environmentPath:      "/var/testenvironment",
			sdk:                  "c++--",
			name:                 identity.NewID(),
			expectedErrorMessage: "unsupported environment SDK",
		},
		{
			testName:             "error on git",
			environmentPath:      "git://github.com/dagger/dagger.git",
			sdk:                  "go",
			name:                 identity.NewID(),
			expectedErrorMessage: "environment init is not supported for git environments",
		},
	} {
		tc := tc
		t.Run(tc.testName, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)
			ctr := CLITestContainer(ctx, t, c).
				WithEnvArg(tc.environmentPath).
				WithSDKArg(tc.sdk).
				WithNameArg(tc.name).
				CallEnvInit()

			if tc.expectedErrorMessage != "" {
				_, err := ctr.Sync(ctx)
				require.ErrorContains(t, err, tc.expectedErrorMessage)
				return
			}

			expectedConfigPath := tc.environmentPath
			if !strings.HasSuffix(expectedConfigPath, "dagger.json") {
				expectedConfigPath = filepath.Join(expectedConfigPath, "dagger.json")
			}
			_, err := ctr.File(expectedConfigPath).Contents(ctx)
			require.NoError(t, err)

			// TODO: test rest of SDKs once custom codegen is supported
			if tc.sdk == "go" {
				codegenFile := filepath.Join(filepath.Dir(expectedConfigPath), "dagger.gen.go")
				_, err := ctr.File(codegenFile).Contents(ctx)
				require.NoError(t, err)
			}

			stderr, err := ctr.CallEnv().Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, stderr, fmt.Sprintf(`"name": %q`, tc.name))
			require.Contains(t, stderr, fmt.Sprintf(`"sdk": %q`, tc.sdk))
		})
	}

	t.Run("error on existing environment", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)
		_, err := CLITestContainer(ctx, t, c).
			WithLoadedEnv("core/integration/testdata/environments/go/basic", false).
			WithSDKArg("go").
			WithNameArg("foo").
			CallEnvInit().
			Sync(ctx)
		require.ErrorContains(t, err, "environment init config path already exists")
	})
}

func TestEnvChecks(t *testing.T) {
	t.Skip("pending conversion to modules")

	t.Parallel()

	allChecks := []string{
		"cool-static-check",
		"sad-static-check",
		"cool-container-check",
		"sad-container-check",
		"cool-composite-check",
		"sad-composite-check",
		"another-cool-static-check",
		"another-sad-static-check",
		"cool-composite-check-from-explicit-dep",
		"sad-composite-check-from-explicit-dep",
		"cool-composite-check-from-dynamic-dep",
		"sad-composite-check-from-dynamic-dep",
		"cool-check-only-return",
		"cool-check-result-only-return",
		"cool-string-only-return",
		"cool-error-only-return",
		"sad-error-only-return",
		"cool-string-error-return",
		"sad-string-error-return",
	}
	compositeCheckToSubcheckNames := map[string][]string{
		"cool-composite-check": {
			"cool-subcheck-a",
			"cool-subcheck-b",
		},
		"sad-composite-check": {
			"sad-subcheck-a",
			"sad-subcheck-b",
		},
		"cool-composite-check-from-explicit-dep": {
			"another-cool-static-check",
			"another-cool-container-check",
			"another-cool-composite-check",
		},
		"sad-composite-check-from-explicit-dep": {
			"another-sad-static-check",
			"another-sad-container-check",
			"another-sad-composite-check",
		},
		"cool-composite-check-from-dynamic-dep": {
			"yet-another-cool-static-check",
			"yet-another-cool-container-check",
			"yet-another-cool-composite-check",
		},
		"sad-composite-check-from-dynamic-dep": {
			"yet-another-sad-static-check",
			"yet-another-sad-container-check",
			"yet-another-sad-composite-check",
		},
		"another-cool-composite-check": {
			"another-cool-subcheck-a",
			"another-cool-subcheck-b",
		},
		"another-sad-composite-check": {
			"another-sad-subcheck-a",
			"another-sad-subcheck-b",
		},
		"yet-another-cool-composite-check": {
			"yet-another-cool-subcheck-a",
			"yet-another-cool-subcheck-b",
		},
		"yet-another-sad-composite-check": {
			"yet-another-sad-subcheck-a",
			"yet-another-sad-subcheck-b",
		},
	}

	// should be aligned w/ `func checkOutput` in ./testdata/environments/go/basic/main.go
	checkOutput := func(name string) string {
		return "WE ARE RUNNING CHECK " + strcase.ToKebab(name)
	}

	type testCase struct {
		name            string
		environmentPath string
		selectedChecks  []string
		expectFailure   bool
	}
	for _, tc := range []testCase{
		{
			name:            "happy-path",
			environmentPath: "core/integration/testdata/environments/go/basic",
			selectedChecks: []string{
				"cool-static-check",
				"cool-container-check",
				"cool-composite-check",
				"another-cool-static-check",
				"cool-composite-check-from-explicit-dep",
				"cool-composite-check-from-dynamic-dep",
				"cool-check-only-return",
				"cool-check-result-only-return",
				"cool-string-only-return",
				"cool-error-only-return",
				"cool-string-error-return",
			},
		},
		{
			name:            "sad-path",
			expectFailure:   true,
			environmentPath: "core/integration/testdata/environments/go/basic",
			selectedChecks: []string{
				"sad-static-check",
				"sad-container-check",
				"sad-composite-check",
				"another-sad-static-check",
				"sad-composite-check-from-explicit-dep",
				"sad-composite-check-from-dynamic-dep",
				"sad-error-only-return",
				"sad-string-error-return",
			},
		},
		{
			name:            "mixed-path",
			expectFailure:   true,
			environmentPath: "core/integration/testdata/environments/go/basic",
			// run all checks, don't select any
		},
	} {
		tc := tc
		for _, testGitEnv := range []bool{false, true} {
			testGitEnv := testGitEnv
			testName := tc.name
			testName += "/gitenv=" + strconv.FormatBool(testGitEnv)
			testName += "/" + tc.environmentPath
			t.Run(testName, func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)
				stderr, err := CLITestContainer(ctx, t, c).
					WithLoadedEnv(tc.environmentPath, testGitEnv).
					CallChecks(tc.selectedChecks...).
					Stderr(ctx)
				if tc.expectFailure {
					require.Error(t, err)
					execErr := new(dagger.ExecError)
					require.True(t, errors.As(err, &execErr))
					stderr = execErr.Stderr
				} else {
					require.NoError(t, err)
				}

				selectedChecks := tc.selectedChecks
				if len(selectedChecks) == 0 {
					selectedChecks = allChecks
				}

				curChecks := selectedChecks
				for len(curChecks) > 0 {
					var nextChecks []string
					for _, checkName := range curChecks {
						subChecks, ok := compositeCheckToSubcheckNames[checkName]
						if ok {
							nextChecks = append(nextChecks, subChecks...)
						} else {
							// special case for successful error only check, doesn't have output
							if checkName == "cool-error-only-return" {
								continue
							}
							require.Contains(t, stderr, checkOutput(checkName))
						}
					}
					curChecks = nextChecks
				}
			})
		}
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
	query = fmt.Sprintf(query, args...)
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec([]string{"dagger", "--debug", "query"}, dagger.ContainerWithExecOpts{
			Stdin:                         query,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerCall(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--debug", "call"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerFunctions(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--debug", "functions"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func currentSchema(ctx context.Context, t *testing.T, ctr *dagger.Container) *introspection.Schema {
	t.Helper()
	out, err := ctr.With(daggerQuery(introspection.Query)).Stdout(ctx)
	require.NoError(t, err)
	var schemaResp introspection.Response
	err = json.Unmarshal([]byte(out), &schemaResp)
	require.NoError(t, err)
	return schemaResp.Schema
}

func goGitBase(t *testing.T, c *dagger.Client) *dagger.Container {
	t.Helper()
	return c.Container().From(golangImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "config", "--global", "user.email", "dagger@example.com"}).
		WithExec([]string{"git", "config", "--global", "user.name", "Dagger Tests"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		WithExec([]string{"git", "init"})
}

func logGen(ctx context.Context, t *testing.T, modSrc *dagger.Directory) {
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

		if err := os.WriteFile(fileName, []byte(generated), 0644); err != nil {
			t.Logf("failed to write generated code to %s: %v", fileName, err)
		} else {
			t.Logf("wrote generated code to %s", fileName)
		}
	})
}
