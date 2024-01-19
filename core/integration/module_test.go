package core

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/idproto"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core/modules"
)

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
			With(daggerExec("mod", "init", "-m=./child", "--name=child", "--sdk=go")).
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
			}).
			WithWorkdir("/work/child")

		generated := modGen.
			// explicitly sync to see whether it makes a go.mod
			With(daggerExec("mod", "sync")).
			Directory(".")

		logGen(ctx, t, generated)

		out, err := modGen.
			With(daggerQuery(`{child{root{entries}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"child":{"root":{"entries":["child","dagger.json","foo.go","go.mod","go.sum"]}}}`, out)

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
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "-m=./child", "--name=child", "--sdk=go")).
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
			}).
			WithWorkdir("/work/child")

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
		require.JSONEq(t, `{"child":{"root":{"entries":["child","dagger.json","foo.go","go.mod"]}}}`, out)

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
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "-m=child", "--name=bare", "--sdk=python"))

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
				Contents: `from dagger import function

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

	t.Run("uses expected field casing", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=hello-world", "--sdk=python")).
			With(sdkSource("python", `from dagger import field, function, object_type

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
		sdk               string
		gitGeneratedFiles []string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			gitGeneratedFiles: []string{
				"/dagger.gen.go",
				"/querybuilder/**",
			},
		},
		{
			sdk: "python",
			gitGeneratedFiles: []string{
				"/sdk/**",
			},
		},
		{
			sdk: "typescript",
			gitGeneratedFiles: []string{
				"/sdk/**",
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

			t.Run("configures .gitattributes", func(t *testing.T) {
				ignore, err := modGen.File(".gitattributes").Contents(ctx)
				require.NoError(t, err)
				for _, fileName := range tc.gitGeneratedFiles {
					require.Contains(t, ignore, fmt.Sprintf("%s linguist-generated=true\n", fileName))
				}
			})
		})
	}
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
		out, err := modGen.With(daggerQuery(`{minimal{echoes(msgs: ["hello"])}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoes":["hello...hello...hello..."]}}`, out)
	})

	t.Run("func EchoesVariadic(...string) string", func(t *testing.T) {
		t.Parallel()
		out, err := modGen.With(daggerQuery(`{minimal{echoesVariadic(msgs: ["hello"])}}`)).Stdout(ctx)
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

	t.Run("func EchoOptsPragmas(string, string, int) error", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOptsPragmas(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsPragmas":"hi...hi...hi..."}}`, out)
	})
}

//go:embed testdata/modules/typescript/minimal/index.ts
var tsSignatures string

func TestModuleTypescriptSignatures(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=typescript")).
		WithNewFile("src/index.ts", dagger.ContainerWithNewFileOpts{
			Contents: tsSignatures,
		})

	t.Run("hello(): string", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"hello":"hello"}}`, out)
	})

	t.Run("echoes(msgs: string[]): string[]", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoes(msgs: ["hello"])}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoes":["hello...hello...hello..."]}}`, out)
	})

	t.Run("echoOptional(msg = 'default'): string", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOptional(msg: "hello")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptional":"hello...hello...hello..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptional}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptional":"default...default...default..."}}`, out)
	})

	t.Run("echoesVariadic(...msgs: string[]): string", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoesVariadic(msgs: ["hello"])}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoesVariadic":"hello...hello...hello..."}}`, out)
	})

	t.Run("echo(msg: string): string", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echo(msg: "hello")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echo":"hello...hello...hello..."}}`, out)
	})

	t.Run("echoOptionalSlice(msg = ['foobar']): string", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOptionalSlice(msg: ["hello", "there"])}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalSlice":"hello+there...hello+there...hello+there..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptionalSlice}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalSlice":"foobar...foobar...foobar..."}}`, out)
	})

	t.Run("helloVoid(): void", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{helloVoid}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloVoid":null}}`, out)
	})

	t.Run("echoOpts(msg: string, suffix: string = '', times: number = 1): string", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi!hi!"}}`, out)

		t.Run("execute with unordered args", func(t *testing.T) {
			out, err = modGen.With(daggerQuery(`{minimal{echoOpts(times: 2, msg: "order", suffix: "?")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"minimal":{"echoOpts":"order?order?"}}`, out)
		})
	})

	t.Run("echoMaybe(msg: string, isQuestion = false): string", func(t *testing.T) {
		t.Parallel()

		out, err := modGen.With(daggerQuery(`{minimal{echoMaybe(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoMaybe":"hi...hi...hi..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoMaybe(msg: "hi", isQuestion: true)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoMaybe":"hi?...hi?...hi?..."}}`, out)

		t.Run("execute with unordered args", func(t *testing.T) {
			out, err = modGen.With(daggerQuery(`{minimal{echoMaybe(isQuestion: false, msg: "hi")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"minimal":{"echoMaybe":"hi...hi...hi..."}}`, out)
		})
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

	var logs safeBuffer
	c, ctx := connect(t, dagger.WithLogOutput(&logs))

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

	require.Equal(t, 1, len(objs.Array()))
	minimal := objs.Get(`0.asObject`)
	require.Equal(t, "Minimal", minimal.Get("name").String())

	modGen = c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Minimal struct {}

type Foo struct {}

type bar struct {}

func (m *Minimal) Hello(name string) Foo {
	return Foo{}
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

	out, err = modGen.With(inspectModule).Stdout(ctx)
	require.NoError(t, err)
	objs = gjson.Get(out, "host.directory.asModule.objects")

	require.Equal(t, 2, len(objs.Array()))
	minimal = objs.Get(`0.asObject`)
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

func (m *Minimal) Hello(name string) Foo {
	return Foo{}
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
	require.NoError(t, c.Close())
	require.Contains(t, logs.String(), "cannot code-generate unexported type bar")
}

func TestModuleGoSignaturesMixMatch(t *testing.T) {
	t.Parallel()

	var logs safeBuffer
	c, ctx := connect(t, dagger.WithLogOutput(&logs))

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
	require.NoError(t, c.Close())
	require.Contains(t, logs.String(), "nested structs are not supported")
}

func TestModuleGoSignaturesNameConflict(t *testing.T) {
	t.Parallel()

	var logs safeBuffer
	c, ctx := connect(t, dagger.WithLogOutput(&logs))

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Minimal struct {
	Foo Foo
	Bar Bar
	Baz Baz
}

type Foo struct {}
type Bar struct {}
type Baz struct {}

func (m *Foo) Hello(name string) string {
	return name
}

func (f *Bar) Hello(name string, name2 string) string {
	return name + name2
}

func (b *Baz) Hello() (string, error) {
	return "", nil
}
`,
		})
	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(inspectModule).Stdout(ctx)
	require.NoError(t, err)
	objs := gjson.Get(out, "host.directory.asModule.objects")

	require.Equal(t, 4, len(objs.Array()))

	obj := objs.Get(`0.asObject`)
	require.Equal(t, "Minimal", obj.Get("name").String())
	obj = objs.Get(`1.asObject`)
	require.Equal(t, "MinimalFoo", obj.Get("name").String())
	obj = objs.Get(`2.asObject`)
	require.Equal(t, "MinimalBar", obj.Get("name").String())
	obj = objs.Get(`3.asObject`)
	require.Equal(t, "MinimalBaz", obj.Get("name").String())
}

var inspectModule = daggerQuery(`
query {
  host {
    directory(path: ".") {
      asModule {
        objects {
          asObject {
            name
            description
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

	// test the arg-based form (with pragmas)
	echoOpts = obj.Get(`functions.#(name="echoOptsPragmas")`)
	require.Equal(t, "echoOptsPragmas", echoOpts.Get("name").String())
	require.Len(t, echoOpts.Get("args").Array(), 3)
	require.Equal(t, "msg", echoOpts.Get("args.0.name").String())
	require.Equal(t, "", echoOpts.Get("args.0.defaultValue").String())
	require.Equal(t, "suffix", echoOpts.Get("args.1.name").String())
	require.Equal(t, "String to append to the echoed message", echoOpts.Get("args.1.description").String())
	require.Equal(t, "\"...\"", echoOpts.Get("args.1.defaultValue").String())
	require.Equal(t, "times", echoOpts.Get("args.2.name").String())
	require.Equal(t, "3", echoOpts.Get("args.2.defaultValue").String())
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

// Minimal is a thing
type Minimal struct {
	// X is this
	X, Y string  // Y is not this

	// +private
	Z string
}

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
	require.Equal(t, "Minimal is a thing", obj.Get("description").String())

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

	require.Len(t, obj.Get(`fields`).Array(), 2)
	prop := obj.Get(`fields.#(name="x")`)
	require.Equal(t, "x", prop.Get("name").String())
	require.Equal(t, "X is this", prop.Get("description").String())
	prop = obj.Get(`fields.#(name="y")`)
	require.Equal(t, "y", prop.Get("name").String())
	require.Equal(t, "", prop.Get("description").String())
}

func TestModuleGoWeirdFields(t *testing.T) {
	// these are all cases that used to panic due to the disparity in the type spec and the ast

	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Z string

type Minimal struct {
	// field with single (normal) name
	W string

	// field with multiple names
	X, Y string

	// field with no names
	Z
}

func New() Minimal {
	return Minimal{
		W: "-",
		X: "-",
		Y: "-",
		Z: Z("-"),
	}
}

// struct with no fields
type Bar struct{}

func (m *Minimal) Say(
	// field with single (normal) name
	a string,
	// field with multiple names
	b, c string,
	// field with no names (not included, mixed names not allowed)
	// string
) string {
	return a + " " + b + " " + c
}

func (m *Minimal) Hello(
	// field with no names
	string,
) string {
	return "hello"
}

func (m *Minimal) SayOpts(opts struct{
	// field with single (normal) name
	A string
	// field with multiple names
	B, C string
	// field with no names (not included because of above)
	// string
}) string {
	return opts.A + " " + opts.B + " " + opts.C
}

func (m *Minimal) HelloOpts(opts struct{
	// field with no names
	string
}) string {
	return "hello"
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	out, err := modGen.With(daggerQuery(`{minimal{w, x, y, z}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal": {"w": "-", "x": "-", "y": "-", "z": "-"}}`, out)

	for _, name := range []string{"say", "sayOpts"} {
		out, err := modGen.With(daggerQuery(`{minimal{%s(a: "hello", b: "world", c: "!")}}`, name)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"minimal": {"%s": "hello world !"}}`, name), out)
	}

	for _, name := range []string{"hello", "helloOpts"} {
		out, err := modGen.With(daggerQuery(`{minimal{%s(string: "")}}`, name)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"minimal": {"%s": "hello"}}`, name), out)
	}
}

func TestModuleGoOptionalMustBeNil(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Minimal struct {}

func (m *Minimal) Foo(x *Optional[*string]) string {
	if v, _ := x.Get(); v != nil {
		panic("uh oh")
	}
	return ""
}

func (m *Minimal) Bar(opts struct {
	x *Optional[*string]
}) string {
	if v, _ := opts.x.Get(); v != nil {
		panic("uh oh")
	}
	return ""
}

func (m *Minimal) Baz(
	// +optional
	x *string,
) string {
	if x != nil {
		panic("uh oh")
	}
	return ""
}

func (m *Minimal) Qux(opts struct {
	// +optional
	x *string
}) string {
	if opts.x != nil {
		panic("uh oh")
	}
	return ""
}
`,
		})

	logGen(ctx, t, modGen.Directory("."))

	for _, name := range []string{"foo", "bar", "baz", "qux"} {
		out, err := modGen.With(daggerQuery(`{minimal{%s}}`, name)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"minimal": {"%s": ""}}`, name), out)
	}
}

func TestModuleGoFieldMustBeNil(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "fmt"

type Minimal struct {
	Src *Directory
	Name *string
}

func New() *Minimal {
	return &Minimal{}
}

func (m *Minimal) IsEmpty() bool {
	if m.Name != nil {
		panic(fmt.Sprintf("name should be nil but is %v", m.Name))
	}
	if m.Src != nil {
		panic(fmt.Sprintf("src should be nil but is %v", m.Src))
	}
	return true
}
`,
		})

	out, err := modGen.With(daggerQuery(`{minimal{isEmpty}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal": {"isEmpty": true}}`, out)
}

func TestModulePrivateField(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	for _, tc := range []struct {
		sdk    string
		source string
	}{
		{
			sdk: "go",
			source: `package main

type Minimal struct {
	Foo string

	Bar string // +private
}

func (m *Minimal) Set(foo string, bar string) *Minimal {
	m.Foo = foo
	m.Bar = bar
	return m
}

func (m *Minimal) Hello() string {
	return m.Foo + m.Bar
}
`,
		},
		{
			sdk: "python",
			source: `from dagger import field, function, object_type

@object_type
class Minimal:
    foo: str = field(default="")
    bar: str = ""

    @function
    def set(self, foo: str, bar: str) -> "Minimal":
        self.foo = foo
        self.bar = bar
        return self

    @function
    def hello(self) -> str:
        return self.foo + self.bar
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=minimal", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(inspectModule).Stdout(ctx)
			require.NoError(t, err)
			obj := gjson.Get(out, "host.directory.asModule.objects.0.asObject")
			require.Equal(t, "Minimal", obj.Get("name").String())
			require.Len(t, obj.Get(`fields`).Array(), 1)
			prop := obj.Get(`fields.#(name="foo")`)
			require.Equal(t, "foo", prop.Get("name").String())

			out, err = modGen.With(daggerQuery(`{minimal{set(foo: "abc", bar: "xyz"){hello}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"minimal":{"set":{"hello": "abcxyz"}}}`, out)

			out, err = modGen.With(daggerQuery(`{minimal{set(foo: "abc", bar: "xyz"){foo}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"minimal":{"set":{"foo": "abc"}}}`, out)

			_, err = modGen.With(daggerQuery(`{minimal{set(foo: "abc", bar: "xyz"){bar}}}`)).Stdout(ctx)
			require.ErrorContains(t, err, `Minimal has no such field: "bar"`)
		})
	}
}

// this is no longer allowed, but verify the SDK errors out
func TestModuleGoExtendCore(t *testing.T) {
	t.Parallel()

	var logs safeBuffer
	c, ctx := connect(t, dagger.WithLogOutput(&logs))

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=container", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "context"

func (c *Container) Echo(ctx context.Context, msg string) (string, error) {
	return c.WithExec([]string{"echo", msg}).Stdout(ctx)
}
`,
		}).
		With(daggerQuery(`{container{from(address:"` + alpineImage + `"){echo(msg:"echo!"){stdout}}}}`)).
		Sync(ctx)
	require.Error(t, err)
	require.NoError(t, c.Close())
	t.Log(logs.String())
	require.Contains(t, logs.String(), "cannot define methods on objects from outside this module")
}

func TestModuleCustomTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import "strings"

type Test struct{}

func (m *Test) Repeater(msg string, times int) *Repeater {
	return &Repeater{
		Message: msg,
		Times:   times,
	}
}

type Repeater struct {
	Message string
	Times   int
}

func (t *Repeater) Render() string {
	return strings.Repeat(t.Message, t.Times)
}
`,
		},
		{
			sdk: "python",
			source: `from dagger import field, function, object_type

@object_type
class Repeater:
    message: str = field(default="")
    times: int = field(default=0)

    @function
    def render(self) -> str:
        return self.message * self.times

@function
def repeater(msg: str, times: int) -> Repeater:
    return Repeater(message=msg, times=times)
`,
		},
	} {
		tc := tc

		t.Run(fmt.Sprintf("custom %s types", tc.sdk), func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=test", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{test{repeater(msg:"echo!", times: 3){render}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"repeater":{"render":"echo!echo!echo!"}}}`, out)
		})
	}
}

func TestModuleReturnTypeDetection(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

type Foo struct {}

type X struct {
	Message string ` + "`json:\"message\"`" + `
}

func (m *Foo) MyFunction() X {
	return X{Message: "foo"}
}
`,
		},
		{
			sdk: "python",
			source: `from dagger import field, function, object_type

@object_type
class X:
    message: str = field(default="")

@function
def my_function() -> X:
    return X(message="foo")
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{foo{myFunction{message}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"myFunction":{"message":"foo"}}}`, out)
		})
	}
}

func TestModuleReturnObject(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

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
		},
		{
			sdk: "python",
			source: `from dagger import field, function, object_type

@object_type
class X:
    message: str = field(default="")
    when: str = field(default="", name="Timestamp")
    to: str = field(default="", name="recipient")
    from_: str = field(default="", name="from")

@object_type
class Foo:
    @function
    def my_function(self) -> X:
        return X(message="foo", when="now", to="user", from_="admin")
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{foo{myFunction{message, recipient, from, timestamp}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"myFunction":{"message":"foo", "recipient":"user", "from":"admin", "timestamp":"now"}}}`, out)
		})
	}
}

func TestModuleReturnNestedObject(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

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
		},
		{
			sdk: "python",
			source: `from dagger import field, function, object_type

@object_type
class Bar:
    msg: str = field()

@object_type
class Foo:
    msg_container: Bar = field()

@object_type
class Playground:
    @function
    def my_function(self) -> Foo:
        return Foo(msg_container=Bar(msg="hello world"))
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=playground", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{playground{myFunction{msgContainer{msg}}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"playground":{"myFunction":{"msgContainer":{"msg": "hello world"}}}}`, out)
		})
	}
}

func TestModuleReturnCompositeCore(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

type Playground struct{}

func (m *Playground) MySlice() []*Container {
	return []*Container{dag.Container().From("alpine:latest").WithExec([]string{"echo", "hello world"})}
}

type Foo struct {
	Con *Container
	// verify fields can remain nil w/out error too
	UnsetFile *File
}

func (m *Playground) MyStruct() *Foo {
	return &Foo{Con: dag.Container().From("alpine:latest").WithExec([]string{"echo", "hello world"})}
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import dag, field, function, object_type

@object_type
class Foo:
    con: dagger.Container = field()
    unset_file: dagger.File | None = field(default=None)

@object_type
class Playground:
    @function
    def my_slice(self) -> list[dagger.Container]:
        return [dag.container().from_("alpine:latest").with_exec(["echo", "hello world"])]

    @function
    def my_struct(self) -> Foo:
        return Foo(con=dag.container().from_("alpine:latest").with_exec(["echo", "hello world"]))
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=playground", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{playground{mySlice{stdout}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"playground":{"mySlice":[{"stdout":"hello world\n"}]}}`, out)

			out, err = modGen.With(daggerQuery(`{playground{myStruct{con{stdout}}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"playground":{"myStruct":{"con":{"stdout":"hello world\n"}}}}`, out)
		})
	}
}

func TestModuleReturnComplexThing(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

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
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import dag, field, function, object_type

@object_type
class ScanReport:
    contents: str = field()
    authors: list[str] = field()

@object_type
class ScanResult:
    containers: list[dagger.Container] = field(name="targets")
    report: ScanReport = field()

@object_type
class Playground:
    @function
    def scan(self) -> ScanResult:
        return ScanResult(
            containers=[
                dag.container().from_("alpine:latest").with_exec(["echo", "hello world"]),
            ],
            report=ScanReport(
                contents="hello world",
                authors=["foo", "bar"],
            ),
        )
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=playground", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{playground{scan{targets{stdout},report{contents,authors}}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"playground":{"scan":{"targets":[{"stdout":"hello world\n"}],"report":{"contents":"hello world","authors":["foo","bar"]}}}}`, out)
		})
	}
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

from dagger import field, function, object_type

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

func TestModuleGlobalVarDAG(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

import "context"

type Foo struct {}

var someDefault = dag.Container().From("alpine:latest")

func (m *Foo) Fn(ctx context.Context) (string, error) {
	return someDefault.WithExec([]string{"echo", "foo"}).Stdout(ctx)
}
`,
		},
		{
			sdk: "python",
			source: `from dagger import dag, function, object_type

SOME_DEFAULT = dag.container().from_("alpine:latest")

@object_type
class Foo:
    @function
    async def fn(self) -> str:
        return await SOME_DEFAULT.with_exec(["echo", "foo"]).stdout()
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{foo{fn}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"fn":"foo\n"}}`, out)
		})
	}
}

func TestModuleIDableType(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

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
		},
		{
			sdk: "python",
			source: `from typing import Self

from dagger import field, function, object_type

@object_type
class Foo:
    data: str = ""

    @function
    def set(self, data: str) -> Self:
        self.data = data
        return self

    @function
    def get(self) -> str:
        return self.data
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			// sanity check
			out, err := modGen.With(daggerQuery(`{foo{set(data: "abc"){get}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"set":{"get": "abc"}}}`, out)

			out, err = modGen.With(daggerQuery(`{foo{set(data: "abc"){id}}}`)).Stdout(ctx)
			require.NoError(t, err)
			id := gjson.Get(out, "foo.set.id").String()

			var idp idproto.ID
			err = idp.Decode(id)
			require.NoError(t, err)
			require.Equal(t, idp.Display(), `foo.set(data: "abc"): Foo!`)

			out, err = modGen.With(daggerQuery(`{loadFooFromID(id: "%s"){get}}`, id)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"loadFooFromID":{"get": "abc"}}`, out)
		})
	}
}

func TestModuleArgOwnType(t *testing.T) {
	// Verify use of a module's own object as an argument type.
	// The server needs to specifically decode the input type from an ID into
	// the raw JSON, since the module doesn't understand it's own types as IDs

	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

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
}`,
		},
		{
			sdk: "python",
			source: `from dagger import field, function, object_type

@object_type
class Message:
    content: str = field()

@object_type
class Foo:
    @function
    def say_hello(self, name: str) -> Message:
        return Message(content=f"hello {name}")

    @function
    def upper(self, msg: Message) -> Message:
        msg.content = msg.content.upper()
        return msg

    @function
    def uppers(self, msg: list[Message]) -> list[Message]:
        for m in msg:
            m.content = m.content.upper()
        return msg
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{foo{sayHello(name: "world"){id}}}`)).Stdout(ctx)
			require.NoError(t, err)
			id := gjson.Get(out, "foo.sayHello.id").String()
			var idp idproto.ID
			err = idp.Decode(id)
			require.NoError(t, err)
			require.Equal(t, idp.Display(), `foo.sayHello(name: "world"): FooMessage!`)

			out, err = modGen.With(daggerQuery(`{foo{upper(msg:"%s"){content}}}`, id)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"upper":{"content": "HELLO WORLD"}}}`, out)

			out, err = modGen.With(daggerQuery(`{foo{uppers(msg:["%s", "%s"]){content}}}`, id, id)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"uppers":[{"content": "HELLO WORLD"}, {"content": "HELLO WORLD"}]}}`, out)
		})
	}
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
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "-m=c", "--name=c", "--sdk=go")).
		WithWorkdir("/work/c").
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
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "-m=b", "--name=b", "--sdk=go")).
		With(daggerExec("mod", "install", "-m=b", "./dint")).
		WithNewFile("/work/b/main.go", dagger.ContainerWithNewFileOpts{
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
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "-m=a", "--name=a", "--sdk=go")).
		WithWorkdir("/work/a").
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

func TestModuleSelfAPICall(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import (
	"context"

	"github.com/Khan/genqlient/graphql"
)

type Test struct{}

func (m *Test) FnA(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.c.MakeRequest(ctx, &graphql.Request{
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
		}).
		With(daggerQuery(`{test{fnA}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"fnA": "hi from b"}}`, out)
}

func TestModuleGoWithOtherModuleTypes(t *testing.T) {
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
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "-m=test", "--name=test", "--sdk=go")).
		With(daggerExec("mod", "install", "-m=test", "./dep")).
		WithWorkdir("/work/test")

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

func TestModulePythonWithOtherModuleTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("mod", "init", "--name=dep", "--sdk=python")).
		WithNewFile("src/main.py", dagger.ContainerWithNewFileOpts{
			Contents: `from dagger import field, function, object_type

@object_type
class Foo:
    ...

@object_type
class Obj:
    foo: str = field()

@object_type
class Dep:
    @function
    def fn(self) -> Obj:
        return Obj(foo="foo")
`,
		}).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "-m=test", "--name=test", "--sdk=python")).
		WithWorkdir("/work/test").
		With(daggerExec("mod", "install", "../dep"))

	t.Run("return as other module object", func(t *testing.T) {
		t.Parallel()

		t.Run("direct", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.
				WithNewFile("src/main.py", dagger.ContainerWithNewFileOpts{
					Contents: `import dagger

@dagger.object_type
class Test:
    @dagger.function
    def fn(self) -> dagger.DepObj:
        ...
`,
				}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+cannot\s+return\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "dep",
			), err.Error())
		})

		t.Run("list", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.
				WithNewFile("src/main.py", dagger.ContainerWithNewFileOpts{
					Contents: `import dagger

@dagger.object_type
class Test:
    @dagger.function
    def fn(self) -> list[dagger.DepObj]:
        ...
`,
				}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+cannot\s+return\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "dep",
			), err.Error())
		})
	})

	t.Run("arg as other module object", func(t *testing.T) {
		t.Parallel()

		t.Run("direct", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.WithNewFile("src/main.py", dagger.ContainerWithNewFileOpts{
				Contents: `import dagger

@dagger.object_type
class Test:
    @dagger.function
    def fn(self, obj: dagger.DepObj):
        ...
`,
			}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+arg\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "obj", "dep",
			), err.Error())
		})

		t.Run("list", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.WithNewFile("src/main.py", dagger.ContainerWithNewFileOpts{
				Contents: `import dagger

@dagger.object_type
class Test:
    @dagger.function
    def fn(self, obj: list[dagger.DepObj]):
        ...
`,
			}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+arg\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "obj", "dep",
			), err.Error())
		})
	})

	t.Run("field as other module object", func(t *testing.T) {
		t.Parallel()

		t.Run("direct", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.
				WithNewFile("src/main.py", dagger.ContainerWithNewFileOpts{
					Contents: `import dagger

@dagger.object_type
class Obj:
    foo: dagger.DepObj = dagger.field()

@dagger.object_type
class Test:
    @dagger.function
    def fn(self) -> Obj:
        ...
`,
				}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+field\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Obj", "foo", "dep",
			), err.Error())
		})

		t.Run("list", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.
				WithNewFile("src/main.py", dagger.ContainerWithNewFileOpts{
					Contents: `import dagger

@dagger.object_type
class Obj:
    foo: list[dagger.DepObj] = dagger.field()

@dagger.object_type
class Test:
    @dagger.function
    def fn(self) -> list[Obj]:
        ...
`,
				}).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+field\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Obj", "foo", "dep",
			), err.Error())
		})
	})
}

var useInner = `package main

type Dep struct{}

func (m *Dep) Hello() string {
	return "hello"
}
`

var useGoOuter = `package main

import "context"

type Use struct{}

func (m *Use) UseHello(ctx context.Context) (string, error) {
	return dag.Dep().Hello(ctx)
}
`

var usePythonOuter = `from dagger import dag, function

@function
def use_hello() -> str:
    return dag.dep().hello()
`

func TestModuleUseLocal(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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
	} {
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=use", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("mod", "install", "./dep"))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

			// cannot use transitive dependency directly
			_, err = modGen.With(daggerQuery(`{dep{hello}}`)).Stdout(ctx)
			require.Error(t, err)
			require.ErrorContains(t, err, `Query has no such field: "dep"`)
		})
	}
}

func TestModuleCodegenOnDepChange(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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
	} {
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=use", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("mod", "install", "./dep"))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

			// make back-incompatible change to dep
			newInner := strings.ReplaceAll(useInner, `Hello()`, `Hellov2()`)
			modGen = modGen.
				WithWorkdir("/work/dep").
				With(sdkSource("go", newInner)).
				WithWorkdir("/work").
				With(daggerExec("mod", "sync"))

			codegenContents, err := modGen.File(sdkCodegenFile(t, tc.sdk)).Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, codegenContents, tc.expected)

			modGen = modGen.With(sdkSource(tc.sdk, tc.changed))

			out, err = modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)
		})
	}
}

func TestModuleSyncDeps(t *testing.T) {
	// verify that changes to deps result in a sync to the depender module
	t.Parallel()

	c, ctx := connect(t)

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
	} {
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=use", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("mod", "install", "./dep"))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			modGen = modGen.With(daggerQuery(`{use{useHello}}`))
			out, err := modGen.Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

			newInner := strings.ReplaceAll(useInner, `"hello"`, `"goodbye"`)
			modGen = modGen.
				WithWorkdir("/work/dep").
				With(sdkSource("go", newInner)).
				WithWorkdir("/work").
				With(daggerExec("mod", "sync"))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err = modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"goodbye"}}`, out)
		})
	}
}

func TestModuleUseLocalMulti(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

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
		},
		{
			sdk: "python",
			source: `from dagger import dag, function

@function
async def names() -> list[str]:
    return [
        await dag.foo().name(),
        await dag.bar().name(),
    ]
`,
		},
	} {
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(t *testing.T) {
			t.Parallel()

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
				With(daggerExec("mod", "init", "--name=use", "--sdk="+tc.sdk)).
				With(daggerExec("mod", "install", "./foo")).
				With(daggerExec("mod", "install", "./bar")).
				With(sdkSource(tc.sdk, tc.source)).
				WithEnvVariable("BUST", identity.NewID()) // NB(vito): hmm...

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			out, err := modGen.With(daggerQuery(`{use{names}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"names":["foo", "bar"]}}`, out)
		})
	}
}

func TestModuleConstructor(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}

	t.Run("basic", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"context"
)

func New(
	ctx context.Context,
	foo string,
	bar Optional[int],
	baz []string,
	dir *Directory,
) *Test {
	return &Test{
		Foo: foo,
		Bar: bar.GetOr(42),
		Baz: baz,
		Dir: dir,
	}
}

type Test struct {
	Foo string
	Bar int
	Baz []string
	Dir *Directory
	NeverSetDir *Directory
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
import { Directory, object, func, field } from '@dagger.io/dagger';

@object
class Test {
	@field
	foo: string
				
	@field
	dir: Directory
				
	@field
	bar: number
				
	@field
	baz: string[]
				
	@field
	neverSetDir?: Directory = undefined
				
	constructor(foo: string, dir: Directory, bar = 42, baz: string[] = []) {
		this.foo = foo;
		this.dir = dir;
		this.bar = bar;
		this.baz = baz;
	}
				
	@func
	gimmeFoo(): string {
		return this.foo;
	}
				
	@func
	gimmeBar(): number {
		return this.bar;
	}
				
	@func
	gimmeBaz(): string[] {
		return this.baz;
	}
				
	@func
	async gimmeDirEnts(): Promise<string[]> {
		return this.dir.entries();
	}
}					
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(t *testing.T) {
				t.Parallel()

				ctr := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/test").
					With(daggerExec("mod", "init", "--name=test", "--sdk="+tc.sdk)).
					With(sdkSource(tc.sdk, tc.source))

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

	t.Run("fields only", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"context"
)

func New(ctx context.Context) (Test, error) {
	v, err := dag.Container().From("alpine:3.18.4").File("/etc/alpine-release").Contents(ctx)
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
            .from_("alpine:3.18.4")
            .file("/etc/alpine-release")
            .contents()
        ))
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(t *testing.T) {
				t.Parallel()

				ctr := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/test").
					With(daggerExec("mod", "init", "--name=test", "--sdk="+tc.sdk)).
					With(sdkSource(tc.sdk, tc.source))

				out, err := ctr.With(daggerCall("alpine-version")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, strings.TrimSpace(out), "3.18.4")
			})
		}
	})

	t.Run("return error", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []testCase{
			{
				sdk: "go",
				source: `package main

import (
	"fmt"
)

func New() (*Test, error) {
	return nil, fmt.Errorf("too bad")
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
        raise ValueError("too bad")
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(t *testing.T) {
				t.Parallel()

				var logs safeBuffer
				c, ctx := connect(t, dagger.WithLogOutput(&logs))

				ctr := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/test").
					With(daggerExec("mod", "init", "--name=test", "--sdk="+tc.sdk)).
					With(sdkSource(tc.sdk, tc.source))

				_, err := ctr.With(daggerCall("foo")).Stdout(ctx)
				require.Error(t, err)

				require.NoError(t, c.Close())

				t.Log(logs.String())
				require.Contains(t, logs.String(), "too bad")
			})
		}
	})

	t.Run("python: with default factory", func(t *testing.T) {
		t.Parallel()

		content := identity.NewID()

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/test").
			With(daggerExec("mod", "init", "--name=test", "--sdk=python")).
			With(sdkSource("python", fmt.Sprintf(`import dagger
from dagger import dag, object_type, field

@object_type
class Test:
    foo: dagger.File = field(default=lambda: (
        dag.directory()
        .with_new_file("foo.txt", contents="%s")
        .file("foo.txt")
    ))
    bar: list[str] = field(default=list)
`, content),
			))

		out, err := ctr.With(daggerCall("foo")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, strings.TrimSpace(out), content)

		out, err = ctr.With(daggerCall("--foo=dagger.json", "foo")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"sdk": "python"`)

		_, err = ctr.With(daggerCall("bar")).Sync(ctx)
		require.NoError(t, err)
	})
}

func TestModuleWrapping(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	type testCase struct {
		sdk    string
		source string
	}

	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

type Wrapper struct{}

func (m *Wrapper) Container() *WrappedContainer {
	return &WrappedContainer{
		dag.Container().From("alpine"),
	}
}

type WrappedContainer struct {
	Unwrap *Container` + "`" + `json:"unwrap"` + "`" + `
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
from dagger import dag, field, function, object_type

@object_type
class WrappedContainer:
    unwrap: dagger.Container = field()

    @function
    def echo(self, msg: str) -> Self:
        return WrappedContainer(unwrap=self.unwrap.with_exec(["echo", "-n", msg]))

@object_type
class Wrapper:
    @function
    def container(self) -> WrappedContainer:
        return WrappedContainer(unwrap=dag.container().from_("alpine"))

`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("mod", "init", "--name=wrapper", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			if tc.sdk == "go" {
				logGen(ctx, t, modGen.Directory("."))
			}

			id := identity.NewID()
			out, err := modGen.With(daggerQuery(
				fmt.Sprintf(`{wrapper{container{echo(msg:%q){unwrap{stdout}}}}}`, id),
			)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t,
				fmt.Sprintf(`{"wrapper":{"container":{"echo":{"unwrap":{"stdout":%q}}}}}`, id),
				out)
		})
	}
}

func TestModuleTypescriptInit(t *testing.T) {
	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=bare", "--sdk=typescript"))

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
			With(daggerExec("mod", "init", "-m=child", "--name=bare", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQueryAt("child", `{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		out, err = modGen.
			WithWorkdir("/work/child").
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("camel-cases Dagger module name", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=My-Module", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{myModule{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"myModule":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("respect existing package.json", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/package.json", dagger.ContainerWithNewFileOpts{
				Contents: `{
  "name": "my-module",
  "version": "1.0.0",
  "description": "My module",
  "main": "index.js",
  "scripts": {
	"test": "echo \"Error: no test specified\" && exit 1"
  },
  "author": "John doe",
  "license": "MIT"
	}`,
			}).
			With(daggerExec("mod", "init", "--name=hasPkgJson", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{hasPkgJson{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasPkgJson":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("Add dagger dependencies to the existing package.json", func(t *testing.T) {
			pkgJSON, err := modGen.File("/work/package.json").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, pkgJSON, `"@dagger.io/dagger":`)
			require.Contains(t, pkgJSON, `"name": "my-module"`)
		})
	})

	t.Run("respect existing tsconfig.json", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/tsconfig.json", dagger.ContainerWithNewFileOpts{
				Contents: `{
	"compilerOptions": {
	  "target": "ES2022",
	  "moduleResolution": "Node",
	  "experimentalDecorators": true
	}
		}`,
			}).
			With(daggerExec("mod", "init", "--name=hasTsConfig", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{hasTsConfig{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasTsConfig":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("Add dagger paths to the existing tsconfig.json", func(t *testing.T) {
			tsConfig, err := modGen.File("/work/tsconfig.json").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, tsConfig, `"@dagger.io/dagger":`)
		})
	})

	t.Run("respect existing src/index.ts", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithDirectory("/work/src", c.Directory()).
			WithNewFile("/work/src/index.ts", dagger.ContainerWithNewFileOpts{
				Contents: `
				import { dag, Container, object, func } from "@dagger.io/dagger"

				@object
				// eslint-disable-next-line @typescript-eslint/no-unused-vars
				class ExistingSource {
				  /**
				   * example usage: "dagger call container-echo --string-arg yo"
				   */
				  @func
				  helloWorld(stringArg: string): Container {
					return dag.container().from("alpine:latest").withExec(["echo", stringArg])
				  }
				}
					
				`,
			}).
			With(daggerExec("mod", "init", "--name=existingSource", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{existingSource{helloWorld(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"existingSource":{"helloWorld":{"stdout":"hello\n"}}}`, out)
	})
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

		mainSrc := `from dagger import function
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
	var cfg modules.ModulesConfig

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
				})

			var depCfgs []*modules.ModuleConfigDependency
			for _, depName := range depNames {
				depCfgs = append(depCfgs, &modules.ModuleConfigDependency{
					Ref: depName,
				})
			}
			cfg.Modules = append(cfg.Modules, &modules.ModuleConfig{
				Name:         name,
				Source:       name,
				SDK:          "go",
				Dependencies: depCfgs,
			})
		}
		return newModNames
	}

	// Create a base module, then add 6 layers of deps, where each layer has one more module
	// than the previous layer and each module within the layer has a dep on each module
	// from the previous layer. Finally add a single module at the top that depends on all
	// modules from the last layer and call that.
	// Basically, this creates a quadratically growing DAG of modules and verifies we
	// handle it efficiently enough to be callable.
	curDeps := addModulesWithDeps(1, nil)
	for i := 0; i < 6; i++ {
		curDeps = addModulesWithDeps(len(curDeps)+1, curDeps)
	}
	addModulesWithDeps(1, curDeps)

	cfgBytes, err := json.Marshal(cfg)
	require.NoError(t, err)
	modGen = modGen.WithNewFile("../dagger.json", dagger.ContainerWithNewFileOpts{
		Contents: string(cfgBytes),
	})

	_, err = modGen.With(daggerCall("fn")).Stdout(ctx)
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
	require.JSONEq(t, `{"test":{"fn":["*main.Sub1Obj made 1:yo", "*main.Sub2Obj made 2:yo"]}}`, out)
}

func TestModuleSourceConfigs(t *testing.T) {
	// test dagger.json source configs that aren't inherently covered in other tests

	t.Parallel()
	c, ctx := connect(t)

	t.Run("upgrade from old config", func(t *testing.T) {
		t.Parallel()

		baseWithOldConfig := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
			WithNewFile("/work/dagger.json", dagger.ContainerWithNewFileOpts{
				Contents: `{"name": "test", "sdk": "go", "include": ["*"], "exclude": ["bar"], "dependencies": ["dep"]}`,
			})

		// verify sync updates config to new format
		confContents, err := baseWithOldConfig.With(daggerExec("mod", "sync")).File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.JSONEq(t,
			`{"modules":[{"name":"test","sdk":"go","source":".","include":["*"],"exclude":["bar"],"dependencies":[{"ref":"dep"}]}]}`,
			confContents,
		)

		// verify call works seamlessly even without explicit sync yet
		out, err := baseWithOldConfig.With(daggerCall("container-echo", "--string-arg", "hey")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey", strings.TrimSpace(out))
	})

	t.Run("old config with root fails", func(t *testing.T) {
		t.Parallel()

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go")).
			WithNewFile("/work/dagger.json", dagger.ContainerWithNewFileOpts{
				Contents: `{"name": "test", "sdk": "go", "root": ".."}`,
			}).
			With(daggerCall("container-echo", "--string-arg", "hey")).
			Stdout(ctx)
		require.Error(t, err)
		require.Contains(t, `Cannot load module config with legacy "root" setting`, out)
	})

	t.Run("dep has separate config", func(t *testing.T) {
		// Verify that if a local dep has its own dagger.json, that's used to load it correctly.
		t.Parallel()

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/subdir/dep").
			With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
			WithNewFile("/work/subdir/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			}).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "-m=test", "--name=test", "--sdk=go")).
			With(daggerExec("mod", "install", "-m=test", "./subdir/dep")).
			WithNewFile("/work/test/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) { return dag.Dep().DepFn(ctx, "hi dep") }
			`,
			})

		// try invoking it from a few different paths, just for more corner case coverage

		t.Run("from src dir", func(t *testing.T) {
			t.Parallel()
			out, err := base.WithWorkdir("test").With(daggerCall("fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from src root", func(t *testing.T) {
			t.Parallel()
			out, err := base.With(daggerCallAt("test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from root", func(t *testing.T) {
			t.Parallel()
			out, err := base.WithWorkdir("/").With(daggerCallAt("work/test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from dep parent", func(t *testing.T) {
			t.Parallel()
			out, err := base.WithWorkdir("/work/subdir").With(daggerCallAt("../test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from dep dir", func(t *testing.T) {
			t.Parallel()
			out, err := base.WithWorkdir("/work/subdir/dep").With(daggerCallAt("../../test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})
	})

	t.Run("install dep from weird places", func(t *testing.T) {
		t.Parallel()

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "-m=subdir/dep", "--name=dep", "--sdk=go")).
			WithNewFile("/work/subdir/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			}).
			With(daggerExec("mod", "init", "-m=test", "--name=test", "--sdk=go")).
			WithNewFile("/work/test/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) { return dag.Dep().DepFn(ctx, "hi dep") }
			`,
			})

		t.Run("from src dir", func(t *testing.T) {
			// sanity test normal case
			t.Parallel()
			out, err := base.
				WithWorkdir("/work/test").
				With(daggerExec("mod", "install", "../subdir/dep")).
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from root", func(t *testing.T) {
			t.Parallel()
			out, err := base.
				WithWorkdir("/").
				With(daggerExec("mod", "install", "-m=./work/test", "./work/subdir/dep")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from dep", func(t *testing.T) {
			t.Parallel()
			out, err := base.
				WithWorkdir("/work/subdir/dep").
				With(daggerExec("mod", "install", "-m=../../test", ".")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from random place", func(t *testing.T) {
			t.Parallel()
			out, err := base.
				WithWorkdir("/var").
				With(daggerExec("mod", "install", "-m=../work/test", "../work/subdir/dep")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})
	})

	t.Run("install out of tree dep fails", func(t *testing.T) {
		t.Parallel()

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
			WithWorkdir("/work/test").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go"))

		t.Run("from src dir", func(t *testing.T) {
			t.Parallel()
			_, err := base.
				WithWorkdir("/work/test").
				With(daggerExec("mod", "install", "../dep")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path escapes root: "../dep"`)
		})

		t.Run("from dep dir", func(t *testing.T) {
			t.Parallel()
			_, err := base.
				WithWorkdir("/work/dep").
				With(daggerExec("mod", "install", "-m=../test", ".")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path escapes root: "../dep"`)
		})

		t.Run("from root", func(t *testing.T) {
			t.Parallel()
			_, err := base.
				WithWorkdir("/").
				With(daggerExec("mod", "install", "-m=work/test", "work/dep")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path escapes root: "../dep"`)
		})
	})

	t.Run("malicious config", func(t *testing.T) {
		// verify a maliciously constructed dagger.json is still handled correctly
		t.Parallel()

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("mod", "init", "--name=dep", "--sdk=go")).
			WithWorkdir("/work").
			With(daggerExec("mod", "init", "--name=test", "--sdk=go"))

		t.Run("source points to root", func(t *testing.T) {
			t.Parallel()

			base := base.
				With(configFile(".", &modules.ModulesConfig{
					Modules: []*modules.ModuleConfig{{
						Name:   "evil",
						Source: "..",
						SDK:    "go",
					}},
				}))

			_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
			require.ErrorContains(t, err, ".. is not under the module configuration root")

			_, err = base.With(daggerExec("mod", "sync")).Sync(ctx)
			require.ErrorContains(t, err, ".. is not under the module configuration root")

			_, err = base.With(daggerExec("mod", "install", "./dep")).Sync(ctx)
			require.ErrorContains(t, err, ".. is not under the module configuration root")
		})

		t.Run("dep points to root", func(t *testing.T) {
			t.Parallel()

			base := base.
				With(configFile(".", &modules.ModulesConfig{
					Modules: []*modules.ModuleConfig{{
						Name:   "evil",
						Source: ".",
						SDK:    "go",
						Dependencies: []*modules.ModuleConfigDependency{{
							Ref: "..",
						}},
					}},
				}))

			_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
			require.ErrorContains(t, err, "local module source subpath points out of root")

			_, err = base.With(daggerExec("mod", "sync")).Sync(ctx)
			require.ErrorContains(t, err, "local module source subpath points out of root")

			_, err = base.With(daggerExec("mod", "install", "./dep")).Sync(ctx)
			require.ErrorContains(t, err, "local module source subpath points out of root")
		})
	})
}

func TestModuleLoops(t *testing.T) {
	// verify circular module dependencies result in an error
	t.Parallel()

	c, ctx := connect(t)

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		With(daggerExec("mod", "init", "-m=depA", "--name=depA", "--sdk=go")).
		With(daggerExec("mod", "init", "-m=depB", "--name=depB", "--sdk=go")).
		With(daggerExec("mod", "init", "-m=depC", "--name=depC", "--sdk=go")).
		With(daggerExec("mod", "install", "-m=depC", "./depB")).
		With(daggerExec("mod", "install", "-m=depB", "./depA")).
		With(daggerExec("mod", "install", "-m=depA", "./depC")).
		Sync(ctx)
	require.ErrorContains(t, err, "module depA has a circular dependency")
}

//go:embed testdata/modules/go/id/arg/main.go
var badIDArgGoSrc string

//go:embed testdata/modules/python/id/arg/main.py
var badIDArgPySrc string

//go:embed testdata/modules/go/id/field/main.go
var badIDFieldGoSrc string

//go:embed testdata/modules/go/id/fn/main.go
var badIDFnGoSrc string

//go:embed testdata/modules/python/id/fn/main.py
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

//go:embed testdata/modules/typescript/syntax/index.ts
var tsSyntax string

func TestModuleTypescriptSyntaxSupport(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=syntax", "--sdk=typescript")).
		WithNewFile("src/index.ts", dagger.ContainerWithNewFileOpts{
			Contents: tsSyntax,
		})

	t.Run("singleQuoteDefaultArgHello(msg: string = 'world'): string", func(t *testing.T) {
		t.Parallel()

		defaultOut, err := modGen.With(daggerQuery(`{syntax{singleQuoteDefaultArgHello}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"syntax":{"singleQuoteDefaultArgHello":"hello world"}}`, defaultOut)

		out, err := modGen.With(daggerQuery(`{syntax{singleQuoteDefaultArgHello(msg: "dagger")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"syntax":{"singleQuoteDefaultArgHello":"hello dagger"}}`, out)
	})

	t.Run("doubleQuotesDefaultArgHello(msg: string = \"world\"): string", func(t *testing.T) {
		t.Parallel()

		defaultOut, err := modGen.With(daggerQuery(`{syntax{doubleQuotesDefaultArgHello}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"syntax":{"doubleQuotesDefaultArgHello":"hello world"}}`, defaultOut)

		out, err := modGen.With(daggerQuery(`{syntax{doubleQuotesDefaultArgHello(msg: "dagger")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"syntax":{"doubleQuotesDefaultArgHello":"hello dagger"}}`, out)
	})
}

func TestModuleExecError(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("mod", "init", "--name=playground", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `
package main

import (
	"context"
	"errors"
)

type Playground struct{}

func (p *Playground) DoThing(ctx context.Context) error {
	_, err := dag.Container().From("alpine").WithExec([]string{"sh", "-c", "exit 5"}).Sync(ctx)
	var e *ExecError
	if errors.As(err, &e) {
		if e.ExitCode == 5 {
			return nil
		}
	}
	panic("yikes")
}
`})
	logGen(ctx, t, modGen.Directory("."))

	_, err := modGen.
		With(daggerQuery(`{playground{doThing}}`)).
		Stdout(ctx)
	require.NoError(t, err)
}

func daggerExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--debug"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func daggerQuery(query string, args ...any) dagger.WithContainerFunc {
	return daggerQueryAt("", query, args...)
}

func daggerQueryAt(modPath string, query string, args ...any) dagger.WithContainerFunc {
	query = fmt.Sprintf(query, args...)
	return func(c *dagger.Container) *dagger.Container {
		execArgs := []string{"dagger", "--debug", "query"}
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
		execArgs := []string{"dagger", "--debug", "call"}
		if modPath != "" {
			execArgs = append(execArgs, "-m", modPath)
		}
		return c.WithExec(append(execArgs, args...), dagger.ContainerWithExecOpts{
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

func configFile(dirPath string, cfg *modules.ModulesConfig) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		cfgPath := filepath.Join(dirPath, "dagger.json")
		cfgBytes, err := json.Marshal(cfg)
		if err != nil {
			panic(err)
		}
		return c.WithNewFile(cfgPath, dagger.ContainerWithNewFileOpts{
			Contents: string(cfgBytes),
		})
	}
}

// command for a dagger cli call direct on the host
func hostDaggerCommand(ctx context.Context, t testing.TB, workdir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(ctx, daggerCliPath(t), args...)
	cmd.Dir = workdir
	return cmd
}

// runs a dagger cli command directly on the host, rather than in an exec
func hostDaggerExec(ctx context.Context, t testing.TB, workdir string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := hostDaggerCommand(ctx, t, workdir, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s: %w", string(output), err)
	}
	return output, err
}

func sdkSource(sdk, contents string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		var sourcePath string
		switch sdk {
		case "go":
			sourcePath = "main.go"
		case "python":
			sourcePath = "src/main.py"
		case "typescript":
			sourcePath = "src/index.ts"
		default:
			return c
		}
		return c.WithNewFile(sourcePath, dagger.ContainerWithNewFileOpts{
			Contents: contents,
		})
	}
}

func sdkCodegenFile(t *testing.T, sdk string) string {
	t.Helper()
	switch sdk {
	case "go":
		return "dagger.gen.go"
	case "python":
		return "sdk/src/dagger/client/gen.py"
	default:
		return ""
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
