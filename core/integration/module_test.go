package core

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"go/format"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/iancoleman/strcase"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"golang.org/x/sync/errgroup"

	"dagger.io/dagger"
	"github.com/dagger/dagger/cmd/codegen/introspection"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/distconsts"
)

func TestModuleGoInit(t *testing.T) {
	t.Parallel()

	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=go"))

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
			With(daggerExec("init", "--name=go", "--sdk=go"))

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
			With(daggerExec("init", "--source=.", "--name=My-Module", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{myModule{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"myModule":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		generated, err := modGen.File("go.mod").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, generated, "module dagger/my-module")
	})

	t.Run("creates go.mod beneath an existing go.mod if context is beneath it", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		// don't use .git under /work so the context ends up being /work/ci
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
			With(daggerExec("init", "--name=beneathGoMod", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{beneathGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"beneathGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("names Go module after Dagger module", func(t *testing.T) {
			t.Parallel()
			generated, err := modGen.Directory("dagger").File("go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "module dagger/beneath-go-mod")
		})
	})

	t.Run("respects existing go.mod", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "mod", "init", "example.com/test"}).
			With(daggerExec("init", "--name=hasGoMod", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{hasGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("preserves module name", func(t *testing.T) {
			t.Parallel()
			generated, err := modGen.File("go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "module example.com/test")
		})

		t.Run("no new go.mod", func(t *testing.T) {
			t.Parallel()
			_, err := modGen.File("dagger/go.mod").Contents(ctx)
			require.ErrorContains(t, err, "no such file or directory")
		})
	})

	t.Run("respects existing go.work", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "work", "init"}).
			With(daggerExec("init", "--name=hasGoMod", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{hasGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("go.work is edited", func(t *testing.T) {
			t.Parallel()
			generated, err := modGen.File("go.work").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "use ./dagger\n")
		})
	})

	t.Run("respects existing go.work with existing module", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "mod", "init", "example.com/test"}).
			WithExec([]string{"go", "work", "init"}).
			WithExec([]string{"go", "work", "use", "."}).
			With(daggerExec("init", "--name=hasGoMod", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{hasGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("go.work is edited", func(t *testing.T) {
			t.Parallel()
			generated, err := modGen.File("go.work").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "use .\n")
		})
	})

	t.Run("respects go.work for subdir if git dir", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "work", "init"}).
			With(daggerExec("init", "--name=hasGoMod", "--sdk=go", "subdir"))

		out, err := modGen.
			WithWorkdir("./subdir").
			With(daggerQuery(`{hasGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("go.work is edited", func(t *testing.T) {
			t.Parallel()
			generated, err := modGen.File("go.work").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "use ./subdir/dagger\n")
		})
	})

	t.Run("ignores go.work for subdir", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "work", "init"}).
			With(daggerExec("init", "--name=hasGoMod", "--sdk=go", "subdir"))

		// we can't write to the go.work at the top-level so it should remain
		// unedited, but we should still be able to execute the module as
		// expected

		out, err := modGen.
			WithWorkdir("./subdir").
			With(daggerQuery(`{hasGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("go.work is unedited", func(t *testing.T) {
			t.Parallel()
			generated, err := modGen.File("go.work").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, generated, "use")
		})
	})

	t.Run("respects parent go.mod if root points to it", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		generated := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "mod", "init", "example.com/test"}).
			WithNewFile("/work/foo.go", dagger.ContainerWithNewFileOpts{
				Contents: "package foo\n",
			}).
			With(daggerExec("init", "--name=child", "--sdk=go", "./child")).
			WithWorkdir("/work/child").
			// explicitly develop to see whether it makes a go.mod
			With(daggerExec("develop")).
			Directory("/work")

		parentEntries, err := generated.Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{".git", "child", "foo.go", "go.mod", "go.sum"}, parentEntries)

		childEntries, err := generated.Directory("child").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, childEntries, "go.mod")

		t.Run("preserves parent module name", func(t *testing.T) {
			t.Parallel()
			goMod, err := generated.File("go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, goMod, "module example.com/test")
		})
	})

	t.Run("respects existing go.mod even if root points to parent that has go.mod", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		generated := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"git", "init"}).
			WithExec([]string{"go", "mod", "init", "example.com/test"}).
			WithNewFile("/work/foo.go", dagger.ContainerWithNewFileOpts{
				Contents: "package foo\n",
			}).
			WithWorkdir("/work/child").
			WithExec([]string{"go", "mod", "init", "my-mod"}).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=./child", "--name=child", "--sdk=go", "./child")).
			WithWorkdir("/work/child").
			// explicitly develop to see whether it makes a go.mod
			With(daggerExec("develop")).
			Directory("/work")

		parentEntries, err := generated.Entries(ctx)
		require.NoError(t, err)
		// no go.sum
		require.Equal(t, []string{".git", "child", "foo.go", "go.mod"}, parentEntries)

		childEntries, err := generated.Directory("child").Entries(ctx)
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
			With(daggerExec("init", "--name=hasMainGo", "--sdk=go", "--source=."))

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
			With(daggerExec("init", "--source=.", "--name=hasDaggerTypes", "--sdk=go"))

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
			With(daggerExec("init", "--source=.", "--name=hasNotMainGo", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{hasNotMainGo{hello}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasNotMainGo":{"hello":"Hello, world!"}}`, out)
	})

	t.Run("with source", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=go", "--source=some/subdir"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		sourceSubdirEnts, err := modGen.Directory("/work/some/subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, sourceSubdirEnts, "main.go")

		sourceRootEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, sourceRootEnts, "main.go")
	})

	t.Run("multiple modules in go.work", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "work", "init"}).
			With(daggerExec("init", "--sdk=go", "foo")).
			With(daggerExec("init", "--sdk=go", "bar"))

		generated, err := modGen.File("go.work").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, generated, "\t./foo/dagger\n")
		require.Contains(t, generated, "\t./bar/dagger\n")

		out, err := modGen.
			WithWorkdir("./foo").
			With(daggerQuery(`{foo{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"foo":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		out, err = modGen.
			WithWorkdir("./bar").
			With(daggerQuery(`{bar{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bar":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})
}

func TestModuleInitLICENSE(t *testing.T) {
	t.Run("bootstraps Apache-2.0 LICENSE file if none found", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=licensed-to-ill", "--sdk=go"))

		content, err := modGen.File("LICENSE").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "Apache License, Version 2.0")
	})

	t.Run("do not bootstrap LICENSE file if no sdk is specified", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=no-license"))

		content, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, content, "LICENSE")

		t.Run("bootstrap a license after sdk is set on dagger develop", func(t *testing.T) {
			modGen = modGen.With(daggerExec("develop", "--sdk=go"))

			content, err := modGen.File("LICENSE").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, content, "Apache License, Version 2.0")
		})
	})

	t.Run("creates LICENSE file in the directory specified by arg", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=licensed-to-ill", "--sdk=go", "./mymod"))

		content, err := modGen.File("mymod/LICENSE").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "Apache License, Version 2.0")
	})

	t.Run("does not bootstrap LICENSE file if it exists in the parent context", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/LICENSE", dagger.ContainerWithNewFileOpts{
				Contents: "doesnt matter",
			}).
			WithWorkdir("/work/sub").
			With(daggerExec("init", "--name=licensed-to-ill", "--sdk=go"))

		_, err := modGen.File("LICENSE").Contents(ctx)
		require.Error(t, err)
	})

	t.Run("does not bootstrap LICENSE file if it exists in an arbitrary parent dir", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/LICENSE", dagger.ContainerWithNewFileOpts{
				Contents: "doesnt matter",
			}).
			WithWorkdir("/work/sub").
			With(daggerExec("init", "--name=licensed-to-ill", "--sdk=go"))

		content, err := modGen.File("LICENSE").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "Apache License, Version 2.0")
	})

	t.Run("bootstraps a LICENSE file when requested, even if it exists in the parent dir", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/LICENSE", dagger.ContainerWithNewFileOpts{
				Contents: "doesnt matter",
			}).
			WithWorkdir("/work/sub").
			With(daggerExec("init", "--name=licensed-to-ill", "--sdk=go", "--license=MIT"))

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
		gitIgnoredFiles   []string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			gitGeneratedFiles: []string{
				"/dagger.gen.go",
				"/internal/dagger/**",
				"/internal/querybuilder/**",
				"/internal/telemetry/**",
			},
			gitIgnoredFiles: []string{
				"/dagger.gen.go",
				"/internal/dagger",
				"/internal/querybuilder",
				"/internal/telemetry",
			},
		},
		{
			sdk: "python",
			gitGeneratedFiles: []string{
				"/sdk/**",
			},
			gitIgnoredFiles: []string{
				"/sdk",
			},
		},
		{
			sdk: "typescript",
			gitGeneratedFiles: []string{
				"/sdk/**",
			},
			gitIgnoredFiles: []string{
				"/sdk",
			},
		},
	} {
		tc := tc
		t.Run(fmt.Sprintf("module %s git", tc.sdk), func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			modGen := goGitBase(t, c).
				With(daggerExec("init", "--name=bare", "--sdk="+tc.sdk))

			out, err := modGen.
				With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
				Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)

			t.Run("configures .gitattributes", func(t *testing.T) {
				t.Parallel()
				ignore, err := modGen.File("dagger/.gitattributes").Contents(ctx)
				require.NoError(t, err)
				for _, fileName := range tc.gitGeneratedFiles {
					require.Contains(t, ignore, fmt.Sprintf("%s linguist-generated\n", fileName))
				}
			})

			t.Run("configures .gitignore", func(t *testing.T) {
				t.Parallel()
				ignore, err := modGen.File("dagger/.gitignore").Contents(ctx)
				require.NoError(t, err)
				for _, fileName := range tc.gitIgnoredFiles {
					require.Contains(t, ignore, fileName)
				}
			})

			t.Run("does not configure .gitignore if disabled", func(t *testing.T) {
				t.Parallel()
				modGen := goGitBase(t, c).
					With(daggerExec("init", "--name=bare"))

				// TODO: use dagger config to set this once support is added there
				modCfgContents, err := modGen.File("dagger.json").Contents(ctx)
				require.NoError(t, err)
				modCfg := &modules.ModuleConfig{}
				require.NoError(t, json.Unmarshal([]byte(modCfgContents), modCfg))
				autoGitignore := false
				modCfg.Codegen = &modules.ModuleCodegenConfig{
					AutomaticGitignore: &autoGitignore,
				}
				modCfgBytes, err := json.Marshal(modCfg)
				require.NoError(t, err)

				modGen = modGen.WithNewFile("dagger.json", dagger.ContainerWithNewFileOpts{
					Contents: string(modCfgBytes),
				}).With(daggerExec("develop", "--sdk=go"))

				_, err = modGen.File("dagger/.gitignore").Contents(ctx)
				require.ErrorContains(t, err, "no such file or directory")
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
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: goSignatures,
		})

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

func TestModuleGoSignaturesBuiltinTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=minimal", "--sdk=go")).
		WithNewFile("dagger/main.go", dagger.ContainerWithNewFileOpts{
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

func (m *Minimal) ReadOptional(
	ctx context.Context,
	dir *Directory, // +optional
) (string, error) {
	if dir != nil {
		return dir.File("foo").Contents(ctx)
	}
	return "", nil
}
			`,
		})

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
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
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

	objs := inspectModuleObjects(ctx, t, modGen)
	require.Equal(t, 1, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("0.name").String())

	modGen = c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
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

	objs = inspectModuleObjects(ctx, t, modGen)
	require.Equal(t, 2, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("0.name").String())
	require.Equal(t, "MinimalFoo", objs.Get("1.name").String())

	modGen = c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
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

	_, err := modGen.With(moduleIntrospection).Stderr(ctx)
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
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Minimal struct {}

func (m *Minimal) Hello(name string, opts struct{}, opts2 struct{}) string {
	return name
}
`,
		})

	_, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)
	require.Error(t, err)
	require.NoError(t, c.Close())
	t.Log(logs.String())
	require.Contains(t, logs.String(), "nested structs are not supported")
}

func TestModuleGoSignaturesNameConflict(t *testing.T) {
	t.Parallel()

	var logs safeBuffer
	c, ctx := connect(t, dagger.WithLogOutput(&logs))

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
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

	objs := inspectModuleObjects(ctx, t, modGen)
	require.Equal(t, 4, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("0.name").String())
	require.Equal(t, "MinimalFoo", objs.Get("1.name").String())
	require.Equal(t, "MinimalBar", objs.Get("2.name").String())
	require.Equal(t, "MinimalBaz", objs.Get("3.name").String())
}

func TestModuleGoDocs(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: goSignatures,
		})

	logGen(ctx, t, modGen.Directory("."))

	obj := inspectModuleObjects(ctx, t, modGen).Get("0")
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
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
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

	obj := inspectModuleObjects(ctx, t, modGen).Get("0")
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
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
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

func TestModuleGoFieldMustBeNil(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
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

func TestModuleDescription(t *testing.T) {
	t.Parallel()

	type source struct {
		file     string
		contents string
	}

	for _, tc := range []struct {
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
					file: "src/main.py",
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
				{
					file: "pyproject.toml",
					contents: `
                        [project]
                        name = "main"
                        version = "0.0.0"
                        `,
				},
			},
		},
		{
			sdk: "python",
			sources: []source{
				{
					file: "src/main/foo.py",
					contents: `
"""Not the main file"""

from dagger import field, object_type

@object_type
class Foo:
    bar: str = field(default="bar")
`,
				},
				{
					file: "src/main/__init__.py",
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
class Test {
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
class Test {
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
		tc := tc

		t.Run(fmt.Sprintf("%s with %d files", tc.sdk, len(tc.sources)), func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work")

			for _, src := range tc.sources {
				src := src
				modGen = modGen.WithNewFile(src.file, dagger.ContainerWithNewFileOpts{
					Contents: heredoc.Doc(src.contents),
				})
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

func TestModulePrivateField(t *testing.T) {
	t.Parallel()

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
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
class Minimal {
  @func()
  foo: string

  bar?: string

  constructor(foo?: string, bar?: string) {
    this.foo = foo
    this.bar = bar
  }

  @func()
  set(foo: string, bar: string): Minimal {
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
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=minimal", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			obj := inspectModuleObjects(ctx, t, modGen).Get("0")
			require.Equal(t, "Minimal", obj.Get("name").String())
			require.Len(t, obj.Get(`fields`).Array(), 1)
			prop := obj.Get(`fields.#(name="foo")`)
			require.Equal(t, "foo", prop.Get("name").String())

			out, err := modGen.With(daggerQuery(`{minimal{set(foo: "abc", bar: "xyz"){hello}}}`)).Stdout(ctx)
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

func TestModuleOptionalDefaults(t *testing.T) {
	t.Parallel()

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
	// +optional
	// +default=null
	d *string,
	// +optional
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
class Test {
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
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

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

			t.Run("a: String!", func(t *testing.T) {
				t.Parallel()
				// required, i.e., non-null and no default
				arg := args.Get("#(name=a)")
				require.Equal(t, "NON_NULL", arg.Get("type.kind").String())
				require.Equal(t, "SCALAR", arg.Get("type.ofType.kind").String())
				require.Nil(t, arg.Get("defaultValue").Value())
			})

			t.Run("b: String", func(t *testing.T) {
				t.Parallel()
				// GraphQL implicitly sets default to null for nullable types
				arg := args.Get("#(name=b)")
				require.Equal(t, "SCALAR", arg.Get("type.kind").String())
				require.Nil(t, arg.Get("defaultValue").Value())
			})

			t.Run(`c: String! = "foo"`, func(t *testing.T) {
				t.Parallel()
				// non-null, with default
				arg := args.Get("#(name=c)")
				require.Equal(t, "NON_NULL", arg.Get("type.kind").String())
				require.Equal(t, "SCALAR", arg.Get("type.ofType.kind").String())
				require.JSONEq(t, `"foo"`, arg.Get("defaultValue").String())
			})

			t.Run("d: String = null", func(t *testing.T) {
				t.Parallel()
				// nullable, with explicit null default; same as b in practice
				arg := args.Get("#(name=d)")
				require.Equal(t, "SCALAR", arg.Get("type.kind").String())
				require.JSONEq(t, "null", arg.Get("defaultValue").String())
			})

			t.Run(`e: String = "bar"`, func(t *testing.T) {
				t.Parallel()
				// nullable, with non-null default
				arg := args.Get("#(name=e)")
				require.Equal(t, "SCALAR", arg.Get("type.kind").String())
				require.JSONEq(t, `"bar"`, arg.Get("defaultValue").String())
			})

			t.Run("default values", func(t *testing.T) {
				t.Parallel()
				out, err = modGen.With(daggerCall("foo", "--a=test")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, tc.expected, out)
			})
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
		With(daggerExec("init", "--source=.", "--name=container", "--sdk=go")).
		WithNewFile("internal/dagger/more.go", dagger.ContainerWithNewFileOpts{
			Contents: `package dagger

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

func TestModuleGoBadCtx(t *testing.T) {
	t.Parallel()

	var logs safeBuffer
	c, ctx := connect(t, dagger.WithLogOutput(&logs))

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=foo", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "context"

type Foo struct {}

func (f *Foo) Echo(ctx context.Context, ctx2 context.Context) (string, error) {
	return "", nil
}
`,
		}).
		With(daggerQuery(`{foo{echo}}`)).
		Sync(ctx)
	require.Error(t, err)
	require.NoError(t, c.Close())
	t.Log(logs.String())
	require.Contains(t, logs.String(), "unexpected context type")
}

func TestModuleCustomTypes(t *testing.T) {
	t.Parallel()

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
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
class Repeater {
  @func()
  message: string

  @func()
  times: number

  constructor(message: string, times: number) {
    this.message = message
    this.times = times
  }

  @func()
  render(): string {
    return this.message.repeat(this.times)
  }
}

@object()
class Test {
  @func()
  repeater(msg: string, times: number): Repeater {
    return new Repeater(msg, times)
  }
}
`,
		},
	} {
		tc := tc

		t.Run(fmt.Sprintf("custom %s types", tc.sdk), func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			out, err := modInit(t, c, tc.sdk, tc.source).
				With(daggerQuery(`{test{repeater(msg:"echo!", times: 3){render}}}`)).
				Stdout(ctx)

			require.NoError(t, err)
			require.JSONEq(t, `{"test":{"repeater":{"render":"echo!echo!echo!"}}}`, out)
		})
	}
}

func TestModuleReturnTypeDetection(t *testing.T) {
	t.Parallel()

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
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
class X {
  @func()
  message: string

  constructor(message: string) {
    this.message = message;
  }
}

@object()
class Foo {
  @func()
  myFunction(): X {
    return new X("foo");
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			out, err := modGen.With(daggerQuery(`{foo{myFunction{message}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"myFunction":{"message":"foo"}}}`, out)
		})
	}
}

func TestModuleReturnObject(t *testing.T) {
	t.Parallel()

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
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
class X {
  @func()
  message: string

  @func()
  timestamp: string

  @func()
  recipient: string

  @func()
  from: string

  constructor(message: string, timestamp: string, recipient: string, from: string) {
    this.message = message;
    this.timestamp = timestamp;
    this.recipient = recipient;
    this.from = from;
  }
}

@object()
class Foo {
  @func()
  myFunction(): X {
    return new X("foo", "now", "user", "admin");
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			out, err := modGen.With(daggerQuery(`{foo{myFunction{message, recipient, from, timestamp}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"myFunction":{"message":"foo", "recipient":"user", "from":"admin", "timestamp":"now"}}}`, out)
		})
	}
}

func TestModuleReturnNestedObject(t *testing.T) {
	t.Parallel()

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
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
class Bar {
  @func()
  msg: string;

  constructor(msg: string) {
    this.msg = msg;
  }
}

@object()
class Foo {
  @func()
  msgContainer: Bar;

  constructor(msgContainer: Bar) {
    this.msgContainer = msgContainer;
  }
}

@object()
class Playground {
  @func()
  myFunction(): Foo {
    return new Foo(new Bar("hello world"));
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=playground", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			out, err := modGen.With(daggerQuery(`{playground{myFunction{msgContainer{msg}}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"playground":{"myFunction":{"msgContainer":{"msg": "hello world"}}}}`, out)
		})
	}
}

func TestModuleReturnCompositeCore(t *testing.T) {
	t.Parallel()

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
	return []*Container{dag.Container().From("` + alpineImage + `").WithExec([]string{"echo", "hello world"})}
}

type Foo struct {
	Con *Container
	// verify fields can remain nil w/out error too
	UnsetFile *File
}

func (m *Playground) MyStruct() *Foo {
	return &Foo{Con: dag.Container().From("` + alpineImage + `").WithExec([]string{"echo", "hello world"})}
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
        return [dag.container().from_("` + alpineImage + `").with_exec(["echo", "hello world"])]

    @function
    def my_struct(self) -> Foo:
        return Foo(con=dag.container().from_("` + alpineImage + `").with_exec(["echo", "hello world"]))
`,
		},
		{
			sdk: "typescript",
			source: `
import { dag, Container, File, object, func } from "@dagger.io/dagger"

@object()
class Foo {
  @func()
  con: Container

  @func()
  unsetFile?: File

  constructor(con: Container, unsetFile?: File) {
    this.con = con
    this.unsetFile = unsetFile
  }
}

@object()
class Playground {
  @func()
  mySlice(): Container[] {
    return [
      dag.container().from("` + alpineImage + `").withExec(["echo", "hello world"])
    ]
  }

  @func()
  myStruct(): Foo {
    return new Foo(
      dag.container().from("` + alpineImage + `").withExec(["echo", "hello world"])
    )
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=playground", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

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
			dag.Container().From("` + alpineImage + `").WithExec([]string{"echo", "hello world"}),
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
                dag.container().from_("` + alpineImage + `").with_exec(["echo", "hello world"]),
            ],
            report=ScanReport(
                contents="hello world",
                authors=["foo", "bar"],
            ),
        )
`,
		},
		{
			sdk: "typescript",
			source: `
import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
class ScanReport {
  @func()
  contents: string

  @func()
  authors: string[]

  constructor(contents: string, authors: string[]) {
    this.contents = contents
    this.authors = authors
  }
}

@object()
class ScanResult {
  @func("targets")
  containers: Container[]

  @func()
  report: ScanReport

  constructor(containers: Container[], report: ScanReport) {
    this.containers = containers
    this.report = report
  }
}

@object()
class Playground {
  @func()
  async scan(): Promise<ScanResult> {
    return new ScanResult(
      [
        dag.container().from("` + alpineImage + `").withExec(["echo", "hello world"])
      ],
      new ScanReport("hello world", ["foo", "bar"])
    )
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=playground", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			out, err := modGen.With(daggerQuery(`{playground{scan{targets{stdout},report{contents,authors}}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"playground":{"scan":{"targets":[{"stdout":"hello world\n"}],"report":{"contents":"hello world","authors":["foo","bar"]}}}}`, out)
		})
	}
}

func TestModuleGlobalVarDAG(t *testing.T) {
	t.Parallel()

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

var someDefault = dag.Container().From("` + alpineImage + `")

func (m *Foo) Fn(ctx context.Context) (string, error) {
	return someDefault.WithExec([]string{"echo", "foo"}).Stdout(ctx)
}
`,
		},
		{
			sdk: "python",
			source: `from dagger import dag, function, object_type

SOME_DEFAULT = dag.container().from_("` + alpineImage + `")

@object_type
class Foo:
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
class Foo {
  @func()
  async fn(): Promise<string> {
    return someDefault.withExec(["echo", "foo"]).stdout()
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			out, err := modGen.With(daggerQuery(`{foo{fn}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"fn":"foo\n"}}`, out)
		})
	}
}

func TestModuleIDableType(t *testing.T) {
	t.Parallel()

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
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
class Foo {
  data: string = ""

  @func()
  set(data: string): Foo {
    this.data = data
    return this
  }

  @func()
  get(): string {
    return this.data
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			// sanity check
			out, err := modGen.With(daggerQuery(`{foo{set(data: "abc"){get}}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"foo":{"set":{"get": "abc"}}}`, out)

			out, err = modGen.With(daggerQuery(`{foo{set(data: "abc"){id}}}`)).Stdout(ctx)
			require.NoError(t, err)
			id := gjson.Get(out, "foo.set.id").String()

			var idp call.ID
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
		{
			sdk: "typescript",
			source: `
import { object, func } from "@dagger.io/dagger"

@object()
class Message {
  @func()
  content: string

  constructor(content: string) {
    this.content = content
  }
}

@object()
class Foo {
  @func()
  sayHello(name: string): Message {
    return new Message("hello " + name)
  }

  @func()
  upper(msg: Message): Message {
    msg.content = msg.content.toUpperCase()
    return msg
  }

  @func()
  uppers(msg: Message[]): Message[] {
    for (let i = 0; i < msg.length; i++) {
      msg[i].content = msg[i].content.toUpperCase()
    }
    return msg
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=foo", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

			out, err := modGen.With(daggerQuery(`{foo{sayHello(name: "world"){id}}}`)).Stdout(ctx)
			require.NoError(t, err)
			id := gjson.Get(out, "foo.sayHello.id").String()
			var idp call.ID
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

func TestModuleScalarType(t *testing.T) {
	t.Parallel()

	type testCase struct {
		sdk    string
		source string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

type Test struct{}

func (m *Test) FromPlatform(platform Platform) string {
	return string(platform)
}

func (m *Test) ToPlatform(platform string) Platform {
	return Platform(platform)
}

func (m *Test) FromPlatforms(platform []Platform) []string {
	result := []string{}
	for _, p := range platform {
		result = append(result, string(p))
	}
	return result
}

func (m *Test) ToPlatforms(platform []string) []Platform {
	result := []Platform{}
	for _, p := range platform {
		result = append(result, Platform(p))
	}
	return result
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import function, object_type

@object_type
class Test:
    @function
    def from_platform(self, platform: dagger.Platform) -> str:
        return str(platform)

    @function
    def to_platform(self, platform: str) -> dagger.Platform:
        return dagger.Platform(platform)

    @function
    def from_platforms(self, platform: list[dagger.Platform]) -> list[str]:
        return [str(p) for p in platform]

    @function
    def to_platforms(self, platform: list[str]) -> list[dagger.Platform]:
        return [dagger.Platform(p) for p in platform]
`,
		},
		{
			sdk: "typescript",
			source: `import { object, func, Platform } from "@dagger.io/dagger"

@object()
class Test {
	@func()
	fromPlatform(platform: Platform): string {
		return platform as string
	}

	@func()
	toPlatform(platform: string): Platform {
		return platform as Platform
	}

	@func()
	fromPlatforms(platform: Platform[]): string[] {
		return platform.map(p => p as string)
	}

	@func()
	toPlatforms(platform: string[]): Platform[] {
		return platform.map(p => p as Platform)
	}
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)
			modGen := modInit(t, c, tc.sdk, tc.source)

			out, err := modGen.With(daggerQuery(`{test{fromPlatform(platform: "linux/amd64")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "linux/amd64", gjson.Get(out, "test.fromPlatform").String())
			_, err = modGen.With(daggerQuery(`{test{fromPlatform(platform: "invalid")}}`)).Stdout(ctx)
			require.ErrorContains(t, err, "unknown operating system or architecture")

			out, err = modGen.With(daggerQuery(`{test{toPlatform(platform: "linux/amd64")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "linux/amd64", gjson.Get(out, "test.toPlatform").String())
			_, err = modGen.With(daggerQuery(`{test{toPlatform(platform: "invalid")}}`)).Sync(ctx)
			require.ErrorContains(t, err, "unknown operating system or architecture")

			out, err = modGen.With(daggerQuery(`{test{fromPlatforms(platform: ["linux/amd64"])}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, len(gjson.Get(out, "test.fromPlatforms").Array()))
			require.Equal(t, "linux/amd64", gjson.Get(out, "test.fromPlatforms.0").String())
			_, err = modGen.With(daggerQuery(`{test{fromPlatforms(platform: ["invalid"])}}`)).Stdout(ctx)
			require.ErrorContains(t, err, "unknown operating system or architecture")

			out, err = modGen.With(daggerQuery(`{test{toPlatforms(platform: ["linux/amd64"])}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, len(gjson.Get(out, "test.toPlatforms.0").Array()))
			require.Equal(t, "linux/amd64", gjson.Get(out, "test.toPlatforms.0").String())
			_, err = modGen.With(daggerQuery(`{test{toPlatforms(platform: ["invalid"])}}`)).Sync(ctx)
			require.ErrorContains(t, err, "unknown operating system or architecture")
		})
	}
}

func TestModuleEnumType(t *testing.T) {
	t.Parallel()

	type testCase struct {
		sdk    string
		source string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

type Test struct{}

func (m *Test) FromProto(proto NetworkProtocol) string {
	return string(proto)
}

func (m *Test) ToProto(proto string) NetworkProtocol {
	return NetworkProtocol(proto)
}
`,
		},
		{
			sdk: "python",
			source: `import dagger
from dagger import function, object_type

@object_type
class Test:
    @function
    def from_proto(self, proto: dagger.NetworkProtocol) -> str:
        return str(proto)

    @function
    def to_proto(self, proto: str) -> dagger.NetworkProtocol:
        # Doing "dagger.NetworkProtocol(proto)" will fail in Python, so mock
        # it to force sending the invalid value back to the server.
        from dagger.client.base import Enum

        class MockEnum(Enum):
            TCP = "TCP"
            INVALID = "INVALID"

        return MockEnum(proto)
`,
		},
		{
			sdk: "typescript",
			source: `import { object, func, NetworkProtocol } from "@dagger.io/dagger";

@object()
class Test {
  @func()
  fromProto(Proto: NetworkProtocol): string {
    return Proto as string;
  }

  @func()
  toProto(Proto: string): NetworkProtocol {
    return Proto as NetworkProtocol;
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)
			modGen := modInit(t, c, tc.sdk, tc.source)

			out, err := modGen.With(daggerQuery(`{test{fromProto(proto: "TCP")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "TCP", gjson.Get(out, "test.fromProto").String())

			_, err = modGen.With(daggerQuery(`{test{fromProto(proto: "INVALID")}}`)).Stdout(ctx)
			require.ErrorContains(t, err, "invalid enum value")

			out, err = modGen.With(daggerQuery(`{test{toProto(proto: "TCP")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "TCP", gjson.Get(out, "test.toProto").String())

			_, err = modGen.With(daggerQuery(`{test{toProto(proto: "INVALID")}}`)).Sync(ctx)
			require.ErrorContains(t, err, "invalid enum value")
		})
	}
}

func TestCustomModuleEnumType(t *testing.T) {
	t.Parallel()

	type testCase struct {
		sdk    string
		source string
	}
	for _, tc := range []testCase{
		{
			sdk: "go",
			source: `package main

// Enum for Status
type Status string

const (
	// Active status
	Active Status = "ACTIVE"

	// Inactive status
	Inactive Status = "INACTIVE"
)

type Test struct{}

func (m *Test) FromStatus(status Status) string {
	return string(status)
}

func (m *Test) ToStatus(status string) Status {
	return Status(status)
}
`,
		},
		{
			sdk: "python",
			source: `import dagger

@dagger.enum_type
class Status(dagger.Enum):
    """Enum for Status"""

    ACTIVE = "ACTIVE", "Active status"
    INACTIVE = "INACTIVE", "Inactive status"


@dagger.object_type
class Test:
    @dagger.function
    def from_status(self, status: Status) -> str:
        return str(status)

    @dagger.function
    def to_status(self, status: str) -> Status:
        # Doing "Status(proto)" will fail in Python, so mock
        # it to force sending the invalid value back to the server.
        class MockEnum(dagger.Enum):
            INACTIVE = "INACTIVE"
            INVALID = "INVALID"

        return MockEnum(status)
`,
		},
		{
			sdk: "typescript",
			source: `import { func, object, field, enumType } from "@dagger.io/dagger"

/**
 * Enum for Status
 */
@enumType()
class Status {
  /**
   * Active status
   */
  static readonly Active: string = "ACTIVE"

  /**
   * Inactive status
   */
  static readonly Inactive: string = "INACTIVE"
}

@object()
export class Test {
  @func()
  fromStatus(status: Status): string {
    return status as string
  }

  @func()
  toStatus(status: string): Status {
    return status as Status
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)
			modGen := modInit(t, c, tc.sdk, tc.source)

			out, err := modGen.With(daggerQuery(`{test{fromStatus(status: "ACTIVE")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "ACTIVE", gjson.Get(out, "test.fromStatus").String())

			_, err = modGen.With(daggerQuery(`{test{fromStatus(status: "INVALID")}}`)).Stdout(ctx)
			require.ErrorContains(t, err, "invalid enum value")

			out, err = modGen.With(daggerQuery(`{test{toStatus(status: "INACTIVE")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "INACTIVE", gjson.Get(out, "test.toStatus").String())

			_, err = modGen.With(daggerQuery(`{test{toStatus(status: "INVALID")}}`)).Sync(ctx)
			require.ErrorContains(t, err, "invalid enum value")

			mod := inspectModule(ctx, t, modGen)
			statusEnum := mod.Get("enums.#.asEnum|#(name=TestStatus)")
			require.Equal(t, "Enum for Status", statusEnum.Get("description").String())
			require.Len(t, statusEnum.Get("values").Array(), 2)
			require.Equal(t, "Active", statusEnum.Get("values.0.name").String())
			require.Equal(t, "Inactive", statusEnum.Get("values.1.name").String())
			require.Equal(t, "Active status", statusEnum.Get("values.0.description").String())
			require.Equal(t, "Inactive status", statusEnum.Get("values.1.description").String())
		})
	}
}

func TestModuleConflictingSameNameDeps(t *testing.T) {
	// A -> B -> Dint
	// A -> C -> Dstr
	// where Dint and Dstr are modules with the same name and same object names but conflicting types
	t.Parallel()

	c, ctx := connect(t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dstr").
		With(daggerExec("init", "--source=.", "--name=d", "--sdk=go")).
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
		With(daggerExec("init", "--source=.", "--name=d", "--sdk=go")).
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
		With(daggerExec("init", "--source=c", "--name=c", "--sdk=go", "c")).
		WithWorkdir("/work/c").
		With(daggerExec("install", "../dstr")).
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
		With(daggerExec("init", "--source=b", "--name=b", "--sdk=go", "b")).
		With(daggerExec("install", "-m=b", "./dint")).
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
		With(daggerExec("init", "--source=a", "--name=a", "--sdk=go", "a")).
		WithWorkdir("/work/a").
		With(daggerExec("install", "../b")).
		With(daggerExec("install", "../c")).
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
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import (
	"context"

	"github.com/Khan/genqlient/graphql"
)

type Test struct{}

func (m *Test) FnA(ctx context.Context) (string, error) {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
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

func TestModuleNoHostSocket(t *testing.T) {
	// verify that a sneaky module can't access host sockets with raw gql queries
	t.Parallel()

	c, ctx := connect(t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import (
	"context"

	"github.com/Khan/genqlient/graphql"
)

type Test struct{}

func (m *Test) Fn(ctx context.Context) string {
	resp := &graphql.Response{}
	err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
		Query: "{host{unixSocket(path:\"/some/sock\"){id}}}",
	}, resp)
	if err != nil {
		return err.Error()
	}
	panic("should not reach here")
}
`,
		}).
		With(daggerCall("fn")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "only the main client can access the host's unix sockets")
}

func TestModuleGoWithOtherModuleTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
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
		With(daggerExec("init", "--source=test", "--name=test", "--sdk=go", "test")).
		With(daggerExec("install", "-m=test", "./dep")).
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

func TestModuleGoUseDaggerTypesDirect(t *testing.T) {
	t.Parallel()

	var logs safeBuffer
	c, ctx := connect(t, dagger.WithLogOutput(&logs))

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "dagger/minimal/internal/dagger"

type Minimal struct{}

func (m *Minimal) Foo(dir *Directory) (*dagger.Directory) {
	return dir.WithNewFile("foo", "xxx")
}

func (m *Minimal) Bar(dir *dagger.Directory) (*Directory) {
	return dir.WithNewFile("bar", "yyy")
}

`,
		})

	out, err := modGen.With(daggerQuery(`{directory{id}}`)).Stdout(ctx)
	require.NoError(t, err)
	dirID := gjson.Get(out, "directory.id").String()

	out, err = modGen.With(daggerQuery(`{minimal{foo(dir: "%s"){file(path: "foo"){contents}}}}`, dirID)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal":{"foo":{"file":{"contents": "xxx"}}}}`, out)

	out, err = modGen.With(daggerQuery(`{minimal{bar(dir: "%s"){file(path: "bar"){contents}}}}`, dirID)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal":{"bar":{"file":{"contents": "yyy"}}}}`, out)
}

func TestModuleGoUtilsPkg(t *testing.T) {
	t.Parallel()

	var logs safeBuffer
	c, ctx := connect(t, dagger.WithLogOutput(&logs))

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import (
	"context"
	"dagger/minimal/utils"
)

type Minimal struct{}

func (m *Minimal) Hello(ctx context.Context) (string, error) {
	return utils.Foo().File("foo").Contents(ctx)
}

`,
		}).
		WithNewFile("utils/util.go", dagger.ContainerWithNewFileOpts{
			Contents: `package utils

import "dagger/minimal/internal/dagger"

func Foo() *dagger.Directory {
	return dagger.Connect().Directory().WithNewFile("/foo", "hello world")
}

`,
		})

	out, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal":{"hello":"hello world"}}`, out)
}

func TestModuleGoNameCase(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

	ctr = ctr.
		WithWorkdir("/toplevel/ssh").
		With(daggerExec("init", "--name=ssh", "--sdk=go", "--source=.")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Ssh struct {}

func (ssh *Ssh) SayHello() string {
        return "hello!"
}
`,
		})
	out, err := ctr.With(daggerQuery(`{ssh{sayHello}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"ssh":{"sayHello":"hello!"}}`, out)

	ctr = ctr.
		WithWorkdir("/toplevel").
		With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
		With(daggerExec("install", "./ssh")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "context"

type Toplevel struct {}

func (t *Toplevel) SayHello(ctx context.Context) (string, error) {
        return dag.SSH().SayHello(ctx)
}
`,
		})
	logGen(ctx, t, ctr.Directory("."))

	out, err = ctr.With(daggerQuery(`{toplevel{sayHello}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"toplevel":{"sayHello":"hello!"}}`, out)
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

var useTSOuter = `
import { dag, object, func } from '@dagger.io/dagger'

@object()
class Use {
	@func()
	async useHello(): Promise<string> {
		return dag.dep().hello()
	}
}
`

func TestModuleUseLocal(t *testing.T) {
	t.Parallel()

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
		{
			sdk:    "typescript",
			source: useTSOuter,
		},
	} {
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=use", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

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
		{
			sdk:      "typescript",
			source:   useTSOuter,
			expected: "hellov2",
			changed:  strings.ReplaceAll(useTSOuter, `.hello()`, `.hellov2()`),
		},
	} {
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=use", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			out, err := modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

			// make back-incompatible change to dep
			newInner := strings.ReplaceAll(useInner, `Hello()`, `Hellov2()`)
			modGen = modGen.
				WithWorkdir("/work/dep").
				With(sdkSource("go", newInner)).
				WithWorkdir("/work").
				With(daggerExec("develop"))

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
	// verify that changes to deps result in a develop to the depender module
	t.Parallel()

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
		{
			sdk:    "typescript",
			source: useTSOuter,
		},
	} {
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--name=dep", "--sdk=go")).
				With(sdkSource("go", useInner)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=use", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source)).
				With(daggerExec("install", "./dep"))

			modGen = modGen.With(daggerQuery(`{use{useHello}}`))
			out, err := modGen.Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"hello"}}`, out)

			newInner := strings.ReplaceAll(useInner, `"hello"`, `"goodbye"`)
			modGen = modGen.
				WithWorkdir("/work/dep").
				With(sdkSource("go", newInner)).
				WithWorkdir("/work").
				With(daggerExec("develop"))

			out, err = modGen.With(daggerQuery(`{use{useHello}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"useHello":"goodbye"}}`, out)
		})
	}
}

func TestModuleUseLocalMulti(t *testing.T) {
	t.Parallel()

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
		{
			sdk: "typescript",
			source: `
import { dag, object, func } from '@dagger.io/dagger'

@object()
class Use {
	@func()
	async names(): Promise<string[]> {
		return [await dag.foo().name(), await dag.bar().name()]
	}
}
`,
		},
	} {
		tc := tc

		t.Run(fmt.Sprintf("%s uses go", tc.sdk), func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/foo").
				WithNewFile("/work/foo/main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

        type Foo struct {}

        func (m *Foo) Name() string { return "foo" }
        `,
				}).
				With(daggerExec("init", "--source=.", "--name=foo", "--sdk=go")).
				WithWorkdir("/work/bar").
				WithNewFile("/work/bar/main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

        type Bar struct {}

        func (m *Bar) Name() string { return "bar" }
        `,
				}).
				With(daggerExec("init", "--source=.", "--name=bar", "--sdk=go")).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=use", "--sdk="+tc.sdk)).
				With(daggerExec("install", "./foo")).
				With(daggerExec("install", "./bar")).
				With(sdkSource(tc.sdk, tc.source)).
				WithEnvVariable("BUST", identity.NewID()) // NB(vito): hmm...

			out, err := modGen.With(daggerQuery(`{use{names}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"use":{"names":["foo", "bar"]}}`, out)
		})
	}
}

func TestModuleConstructor(t *testing.T) {
	t.Parallel()

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
	bar *int, // +optional
	baz []string,
	dir *Directory,
) *Test {
	bar2 := 42
	if bar != nil {
		bar2 = *bar
	}
	return &Test{
		Foo: foo,
		Bar: bar2,
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
import { Directory, object, func } from '@dagger.io/dagger';

@object()
class Test {
	@func()
	foo: string

	@func()
	dir: Directory

	@func()
	bar: number

	@func()
	baz: string[]

	@func()
	neverSetDir?: Directory

	constructor(foo: string, dir: Directory, bar = 42, baz: string[] = []) {
		this.foo = foo;
		this.dir = dir;
		this.bar = bar;
		this.baz = baz;
	}

	@func()
	gimmeFoo(): string {
		return this.foo;
	}

	@func()
	gimmeBar(): number {
		return this.bar;
	}

	@func()
	gimmeBaz(): string[] {
		return this.baz;
	}

	@func()
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
				c, ctx := connect(t)
				ctr := modInit(t, c, tc.sdk, tc.source)

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
	v, err := dag.Container().From("%s").File("/etc/alpine-release").Contents(ctx)
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
            .from_("%s")
            .file("/etc/alpine-release")
            .contents()
        ))
`,
			},
			{
				sdk: "typescript",
				source: `
import { dag, object, func } from "@dagger.io/dagger"

@object()
class Test {
  @func()
  alpineVersion: string

  // NOTE: this is not standard to do async operations in the constructor.
  // This is only for testing purpose but it shouldn't be done in real usage.
  constructor() {
    return (async () => {
      this.alpineVersion = await dag.container().from("%s").file("/etc/alpine-release").contents()

      return this; // Return the newly-created instance
    })();
  }
}
`,
			},
		} {
			tc := tc

			t.Run(tc.sdk, func(t *testing.T) {
				t.Parallel()
				c, ctx := connect(t)

				ctr := c.Container().From(golangImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/test").
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
					With(sdkSource(tc.sdk, fmt.Sprintf(tc.source, alpineImage)))

				out, err := ctr.With(daggerCall("alpine-version")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, distconsts.AlpineVersion, strings.TrimSpace(out))
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
	return nil, fmt.Errorf("too bad: %s", "so sad")
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
        raise ValueError("too bad: " + "so sad")
`,
			},
			{
				sdk: "typescript",
				source: `
import { object, func } from "@dagger.io/dagger"

@object()
class Test {
  @func()
  foo: string

  constructor() {
    throw new Error("too bad: " + "so sad")
  }
}
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
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
					With(sdkSource(tc.sdk, tc.source))

				_, err := ctr.With(daggerCall("foo")).Stdout(ctx)
				require.Error(t, err)

				require.NoError(t, c.Close())

				t.Log(logs.String())
				require.Regexp(t, "too bad: so sad", logs.String())
			})
		}
	})

	t.Run("python: with default factory", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		content := identity.NewID()

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/test").
			With(daggerExec("init", "--name=test", "--sdk=python")).
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

		out, err := ctr.With(daggerCall("foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("--foo=dagger.json", "foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"sdk": "python"`)

		_, err = ctr.With(daggerCall("bar")).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("typescript: with default factory", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		content := identity.NewID()

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/test").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", fmt.Sprintf(`
import { dag, File, object, func } from "@dagger.io/dagger"

@object()
class Test {
  @func()
  foo: File = dag.directory().withNewFile("foo.txt", "%s").file("foo.txt")

  @func()
  bar: string[] = []

  // Allow foo to be set through the constructor
  constructor(foo?: File) {
    if (foo) {
      this.foo = foo
    }
  }
}
`, content),
			))

		out, err := ctr.With(daggerCall("foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, content, strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("--foo=dagger.json", "foo", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"sdk": "typescript"`)

		_, err = ctr.With(daggerCall("bar")).Sync(ctx)
		require.NoError(t, err)
	})
}

func TestModuleGoEmbedded(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

	ctr = ctr.
		WithWorkdir("/playground").
		With(daggerExec("init", "--name=playground", "--sdk=go", "--source=.")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Playground struct {
	*Directory
}

func New() Playground {
	return Playground{Directory: dag.Directory()}
}

func (p *Playground) SayHello() string {
	return "hello!"
}
`,
		})

	out, err := ctr.With(daggerQuery(`{playground{sayHello, directory{entries}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"playground":{"sayHello":"hello!", "directory":{"entries": []}}}`, out)
}

func TestModuleWrapping(t *testing.T) {
	t.Parallel()

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
		dag.Container().From("` + alpineImage + `"),
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
        return WrappedContainer(unwrap=dag.container().from_("` + alpineImage + `"))

`,
		},
		{
			sdk: "typescript",
			source: `
import { dag, Container, object, func } from "@dagger.io/dagger"

@object()
class WrappedContainer {
  @func()
  unwrap: Container

  constructor(unwrap: Container) {
    this.unwrap = unwrap
  }

  @func()
  echo(msg: string): WrappedContainer {
    return new WrappedContainer(this.unwrap.withExec(["echo", "-n", msg]))
  }
}

@object()
class Wrapper {
  @func()
  container(): WrappedContainer {
    return new WrappedContainer(dag.container().from("` + alpineImage + `"))
  }
}
`,
		},
	} {
		tc := tc

		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			modGen := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=wrapper", "--sdk="+tc.sdk)).
				With(sdkSource(tc.sdk, tc.source))

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
			With(daggerExec("init", "--source=.", "--name=potatoSack", "--sdk=go"))

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					With(daggerCall(fmt.Sprintf("potato-%d", i))).
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

		modGen := pythonModInit(t, c, mainSrc)

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					With(daggerCall(fmt.Sprintf("potato-%d", i))).
					Sync(ctx)
				return err
			})
		}
		require.NoError(t, eg.Wait())
	})

	t.Run("typescript sdk", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		mainSrc := `
		import { object, func } from "@dagger.io/dagger"

@object()
class PotatoSack {
		`

		for i := 0; i < funcCount; i++ {
			mainSrc += fmt.Sprintf(`
  @func()
  potato_%d(): string {
    return "potato #%d"
  }
			`, i, i)
		}

		mainSrc += "\n}"

		modGen := c.
			Container().
			From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(sdkSource("typescript", mainSrc)).
			With(daggerExec("init", "--name=potatoSack", "--sdk=typescript"))

		var eg errgroup.Group
		for i := 0; i < funcCount; i++ {
			i := i
			// just verify a subset work
			if i%10 != 0 {
				continue
			}
			eg.Go(func() error {
				_, err := modGen.
					With(daggerCall(fmt.Sprintf("potato-%d", i))).
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

	modGen := goGitBase(t, c).
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
	var rootCfg modules.ModuleConfig

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
					Name:   depName,
					Source: filepath.Join("..", depName),
				})
			}
			modGen = modGen.With(configFile(".", &modules.ModuleConfig{
				Name:         name,
				SDK:          "go",
				Dependencies: depCfgs,
			}))
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

	modGen = modGen.With(configFile("..", &rootCfg))

	_, err := modGen.With(daggerCall("fn")).Sync(ctx)
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
	require.JSONEq(t, `{"test":{"fn":["*dagger.Sub1Obj made 1:yo", "*dagger.Sub2Obj made 2:yo"]}}`, out)
}

func TestModuleLoops(t *testing.T) {
	// verify circular module dependencies result in an error
	t.Parallel()

	c, ctx := connect(t)

	_, err := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		With(daggerExec("init", "--name=depA", "--sdk=go", "depA")).
		With(daggerExec("init", "--name=depB", "--sdk=go", "depB")).
		With(daggerExec("init", "--name=depC", "--sdk=go", "depC")).
		With(daggerExec("install", "-m=depC", "./depB")).
		With(daggerExec("install", "-m=depB", "./depA")).
		With(daggerExec("install", "-m=depA", "./depC")).
		Sync(ctx)
	require.ErrorContains(t, err, `local module at "/work/depA" has a circular dependency`)
}

//go:embed testdata/modules/go/id/arg/main.go
var goodIDArgGoSrc string

//go:embed testdata/modules/python/id/arg/main.py
var goodIDArgPySrc string

//go:embed testdata/modules/typescript/id/arg/index.ts
var goodIDArgTSSrc string

//go:embed testdata/modules/go/id/field/main.go
var badIDFieldGoSrc string

//go:embed testdata/modules/typescript/id/field/index.ts
var badIDFieldTSSrc string

//go:embed testdata/modules/go/id/fn/main.go
var badIDFnGoSrc string

//go:embed testdata/modules/python/id/fn/main.py
var badIDFnPySrc string

//go:embed testdata/modules/typescript/id/fn/index.ts
var badIDFnTSSrc string

func TestModuleReservedWords(t *testing.T) {
	// verify disallowed names are rejected

	t.Parallel()

	type testCase struct {
		sdk    string
		source string
	}

	t.Run("id", func(t *testing.T) {
		t.Parallel()

		t.Run("arg", func(t *testing.T) {
			// id used to be disallowed as an arg name, but is allowed now, test it works
			t.Parallel()

			for _, tc := range []testCase{
				{
					sdk:    "go",
					source: goodIDArgGoSrc,
				},
				{
					sdk:    "python",
					source: goodIDArgPySrc,
				},
				{
					sdk:    "typescript",
					source: goodIDArgTSSrc,
				},
			} {
				tc := tc

				t.Run(tc.sdk, func(t *testing.T) {
					t.Parallel()
					c, ctx := connect(t)

					out, err := modInit(t, c, tc.sdk, tc.source).
						With(daggerQuery(`{test{fn(id:"YES!!!!")}}`)).
						Stdout(ctx)
					require.NoError(t, err)
					require.JSONEq(t, `{"test":{"fn":"YES!!!!"}}`, out)
				})
			}
		})

		t.Run("field", func(t *testing.T) {
			t.Parallel()

			for _, tc := range []testCase{
				{
					sdk:    "go",
					source: badIDFieldGoSrc,
				},
				{
					sdk:    "typescript",
					source: badIDFieldTSSrc,
				},
			} {
				tc := tc

				t.Run(tc.sdk, func(t *testing.T) {
					t.Parallel()
					c, ctx := connect(t)

					_, err := c.Container().From(golangImage).
						WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
						WithWorkdir("/work").
						With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
						With(sdkSource(tc.sdk, tc.source)).
						With(daggerQuery(`{test{fn{id}}}`)).
						Sync(ctx)

					require.ErrorContains(t, err, "cannot define field with reserved name \"id\"")
				})
			}
		})

		t.Run("fn", func(t *testing.T) {
			t.Parallel()

			for _, tc := range []testCase{
				{
					sdk:    "go",
					source: badIDFnGoSrc,
				},
				{
					sdk:    "python",
					source: badIDFnPySrc,
				},
				{
					sdk:    "typescript",
					source: badIDFnTSSrc,
				},
			} {
				tc := tc

				t.Run(tc.sdk, func(t *testing.T) {
					t.Parallel()
					c, ctx := connect(t)

					_, err := c.Container().From(golangImage).
						WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
						WithWorkdir("/work").
						With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
						With(sdkSource(tc.sdk, tc.source)).
						With(daggerQuery(`{test{id}}`)).
						Sync(ctx)

					require.ErrorContains(t, err, "cannot define function with reserved name \"id\"")
				})
			}
		})
	})
}

func TestModuleExecError(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=playground", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `
package main

import (
	"context"
	"errors"
)

type Playground struct{}

func (p *Playground) DoThing(ctx context.Context) error {
	_, err := dag.Container().From("` + alpineImage + `").WithExec([]string{"sh", "-c", "exit 5"}).Sync(ctx)
	var e *ExecError
	if errors.As(err, &e) {
		if e.ExitCode == 5 {
			return nil
		}
	}
	panic("yikes")
}
`})

	_, err := modGen.
		With(daggerQuery(`{playground{doThing}}`)).
		Stdout(ctx)
	require.NoError(t, err)
}

func TestModuleCurrentModuleAPI(t *testing.T) {
	t.Parallel()

	t.Run("name", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=WaCkY", "--sdk=go")).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type WaCkY struct {}

			func (m *WaCkY) Fn(ctx context.Context) (string, error) {
				return dag.CurrentModule().Name(ctx)
			}
			`,
			}).
			With(daggerCall("fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "WaCkY", strings.TrimSpace(out))
	})

	t.Run("source", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/subdir/coolfile.txt", dagger.ContainerWithNewFileOpts{
				Contents: "nice",
			}).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) *File {
				return dag.CurrentModule().Source().File("subdir/coolfile.txt")
			}
			`,
			}).
			With(daggerCall("fn", "contents")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "nice", strings.TrimSpace(out))
	})

	t.Run("workdir", func(t *testing.T) {
		t.Parallel()

		t.Run("dir", func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

			import (
				"context"
				"os"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (*Directory, error) {
				if err := os.MkdirAll("subdir/moresubdir", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("subdir/moresubdir/coolfile.txt", []byte("nice"), 0644); err != nil {
					return nil, err
				}
				return dag.CurrentModule().Workdir("subdir/moresubdir"), nil
			}
			`,
				}).
				With(daggerCall("fn", "file", "--path=coolfile.txt", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("file", func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			out, err := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

			import (
				"context"
				"os"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (*File, error) {
				if err := os.MkdirAll("subdir/moresubdir", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("subdir/moresubdir/coolfile.txt", []byte("nice"), 0644); err != nil {
					return nil, err
				}
				return dag.CurrentModule().WorkdirFile("subdir/moresubdir/coolfile.txt"), nil
			}
			`,
				}).
				With(daggerCall("fn", "contents")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "nice", strings.TrimSpace(out))
		})

		t.Run("error on escape", func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			ctr := c.Container().From(golangImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
				WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

			import (
				"context"
				"os"
			)

			func New() (*Test, error) {
				if err := os.WriteFile("/rootfile.txt", []byte("notnice"), 0644); err != nil {
					return nil, err
				}
				if err := os.MkdirAll("/foo", 0755); err != nil {
					return nil, err
				}
				if err := os.WriteFile("/foo/foofile.txt", []byte("notnice"), 0644); err != nil {
					return nil, err
				}

				return &Test{}, nil
			}

			type Test struct {}

			func (m *Test) EscapeFile(ctx context.Context) *File {
				return dag.CurrentModule().WorkdirFile("../rootfile.txt")
			}

			func (m *Test) EscapeFileAbs(ctx context.Context) *File {
				return dag.CurrentModule().WorkdirFile("/rootfile.txt")
			}

			func (m *Test) EscapeDir(ctx context.Context) *Directory {
				return dag.CurrentModule().Workdir("../foo")
			}

			func (m *Test) EscapeDirAbs(ctx context.Context) *Directory {
				return dag.CurrentModule().Workdir("/foo")
			}
			`,
				})

			_, err := ctr.
				With(daggerCall("escape-file", "contents")).
				Stdout(ctx)
			require.ErrorContains(t, err, `workdir path "../rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-file-abs", "contents")).
				Stdout(ctx)
			require.ErrorContains(t, err, `workdir path "/rootfile.txt" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-dir", "entries")).
				Stdout(ctx)
			require.ErrorContains(t, err, `workdir path "../foo" escapes workdir`)

			_, err = ctr.
				With(daggerCall("escape-dir-abs", "entries")).
				Stdout(ctx)
			require.ErrorContains(t, err, `workdir path "/foo" escapes workdir`)
		})
	})
}

func TestModuleCustomSDK(t *testing.T) {
	t.Parallel()

	t.Run("local", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/coolsdk").
			With(daggerExec("init", "--source=.", "--name=cool-sdk", "--sdk=go")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

type CoolSdk struct {}

func (m *CoolSdk) ModuleRuntime(modSource *ModuleSource, introspectionJson *File) *Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *CoolSdk) Codegen(modSource *ModuleSource, introspectionJson *File) *GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("go").AsModule().GeneratedContextDirectory())
}

func (m *CoolSdk) RequiredPaths() []string {
	return []string{
		"**/go.mod",
		"**/go.sum",
		"**/go.work",
		"**/go.work.sum",
		"**/vendor/",
		"**/*.go",
	}
}
`,
			}).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=coolsdk")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import "os"

type Test struct {}

func (m *Test) Fn() string {
	return os.Getenv("COOL")
}
`,
			})

		out, err := ctr.
			With(daggerCall("fn")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "true", strings.TrimSpace(out))
	})

	t.Run("git", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk="+testGitModuleRef("cool-sdk"))).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import "os"

type Test struct {}

func (m *Test) Fn() string {
	return os.Getenv("COOL")
}
`,
			})

		out, err := ctr.
			With(daggerCall("fn")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "true", strings.TrimSpace(out))
	})
}

// TestModuleHostError verifies the host api is not exposed to modules
func TestModuleHostError(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main
 			import (
 				"context"
 			)
 			type Test struct {}
 			func (m *Test) Fn(ctx context.Context) *Directory {
 				return dag.Host().Directory(".")
 			}
 			`,
		}).
		With(daggerCall("fn")).
		Sync(ctx)
	require.ErrorContains(t, err, "dag.Host undefined")
}

func TestModuleDaggerListen(t *testing.T) {
	t.Parallel()

	t.Run("with mod", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())

		modDir := t.TempDir()
		_, err := hostDaggerExec(ctx, t, modDir, "--debug", "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)

		listenCmd := hostDaggerCommand(ctx, t, modDir, "--debug", "listen", "--listen", "127.0.0.1:12456")
		listenCmd.Env = append(listenCmd.Env, os.Environ()...)
		listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")

		listenOutput := make(chan []byte)
		go func() {
			out, _ := listenCmd.CombinedOutput()
			listenOutput <- out
		}()
		t.Cleanup(func() {
			t.Logf("listen output: %s", string(<-listenOutput))
		})
		t.Cleanup(cancel)

		var out []byte
		for range limitTicker(time.Second, 60) {
			callCmd := hostDaggerCommand(ctx, t, modDir, "--debug", "call", "container-echo", "--string-arg=hi", "stdout")
			copy(callCmd.Env, os.Environ())
			callCmd.Env = append(callCmd.Env, "DAGGER_SESSION_PORT=12456", "DAGGER_SESSION_TOKEN=lol")
			out, err = callCmd.CombinedOutput()
			if err == nil {
				lines := strings.Split(string(out), "\n")
				lastLine := lines[len(lines)-2]
				require.Equal(t, "hi", lastLine)
				return
			}
			time.Sleep(1 * time.Second)
		}
		t.Fatalf("failed to call container-echo: %s err: %v", string(out), err)
	})

	t.Run("disable read write", func(t *testing.T) {
		t.Parallel()

		t.Run("with mod", func(t *testing.T) {
			// mod load fails but should still be able to query base api
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())

			modDir := t.TempDir()
			_, err := hostDaggerExec(ctx, t, modDir, "--debug", "init", "--source=.", "--name=test", "--sdk=go")
			require.NoError(t, err)

			listenCmd := hostDaggerCommand(ctx, t, modDir, "--debug", "listen", "--disable-host-read-write", "--listen", "127.0.0.1:12457")
			listenCmd.Env = append(listenCmd.Env, os.Environ()...)
			listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")

			listenOutput := make(chan []byte)
			go func() {
				out, _ := listenCmd.CombinedOutput()
				listenOutput <- out
			}()
			t.Cleanup(func() {
				t.Logf("listen output: %s", string(<-listenOutput))
			})
			t.Cleanup(cancel)

			var out []byte
			for range limitTicker(time.Second, 60) {
				callCmd := hostDaggerCommand(ctx, t, modDir, "--debug", "query")
				callCmd.Stdin = strings.NewReader(fmt.Sprintf(`query{container{from(address:"%s"){file(path:"/etc/alpine-release"){contents}}}}`, alpineImage))
				callCmd.Env = append(callCmd.Env, os.Environ()...)
				callCmd.Env = append(callCmd.Env, "DAGGER_SESSION_PORT=12457", "DAGGER_SESSION_TOKEN=lol")
				out, err = callCmd.CombinedOutput()
				if err == nil {
					require.Contains(t, string(out), distconsts.AlpineVersion)
					return
				}
				time.Sleep(1 * time.Second)
			}
			t.Fatalf("failed to call query: %s err: %v", string(out), err)
		})

		t.Run("without mod", func(t *testing.T) {
			t.Parallel()

			tmpdir := t.TempDir()

			ctx, cancel := context.WithCancel(context.Background())

			listenCmd := hostDaggerCommand(ctx, t, tmpdir, "--debug", "listen", "--disable-host-read-write", "--listen", "127.0.0.1:12458")
			listenCmd.Env = append(listenCmd.Env, os.Environ()...)
			listenCmd.Env = append(listenCmd.Env, "DAGGER_SESSION_TOKEN=lol")

			listenOutput := make(chan []byte)
			go func() {
				out, _ := listenCmd.CombinedOutput()
				listenOutput <- out
			}()
			t.Cleanup(func() {
				t.Logf("listen output: %s", string(<-listenOutput))
			})
			t.Cleanup(cancel)

			var out []byte
			var err error
			for range limitTicker(time.Second, 60) {
				callCmd := hostDaggerCommand(ctx, t, tmpdir, "--debug", "query")
				callCmd.Stdin = strings.NewReader(fmt.Sprintf(`query{container{from(address:"%s"){file(path:"/etc/alpine-release"){contents}}}}`, alpineImage))
				callCmd.Env = append(callCmd.Env, os.Environ()...)
				callCmd.Env = append(callCmd.Env, "DAGGER_SESSION_PORT=12458", "DAGGER_SESSION_TOKEN=lol")
				out, err = callCmd.CombinedOutput()
				if err == nil {
					require.Contains(t, string(out), distconsts.AlpineVersion)
					return
				}
				time.Sleep(1 * time.Second)
			}
			t.Fatalf("failed to call query: %s err: %v", string(out), err)
		})
	})
}

func TestModuleSecretNested(t *testing.T) {
	t.Parallel()

	t.Run("pass secrets between modules", func(t *testing.T) {
		t.Parallel()
		// check that we can pass valid secret objects between functions in
		// different modules

		c, ctx := connect(t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/secreter").
			With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import "context"

type Secreter struct {}

func (_ *Secreter) Make() *Secret {
	return dag.SetSecret("FOO", "inner")
}

func (_ *Secreter) Get(ctx context.Context, secret *Secret) (string, error) {
	return secret.Plaintext(ctx)
}
`,
			})

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./secreter")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) TryReturn(ctx context.Context) error {
	text, err := dag.Secreter().Make().Plaintext(ctx)
	if err != nil {
		return err
	}
	if text != "inner" {
		return fmt.Errorf("expected \"inner\", but got %q", text)
	}
	return nil
}

func (t *Toplevel) TryArg(ctx context.Context) error {
	text, err := dag.Secreter().Get(ctx, dag.SetSecret("BAR", "outer"))
	if err != nil {
		return err
	}
	if text != "outer" {
		return fmt.Errorf("expected \"outer\", but got %q", text)
	}
	return nil
}
`,
			})

		t.Run("can pass secrets", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.With(daggerQuery(`{toplevel{tryArg}}`)).Stdout(ctx)
			require.NoError(t, err)
		})

		t.Run("can return secrets", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.With(daggerQuery(`{toplevel{tryReturn}}`)).Stdout(ctx)
			require.NoError(t, err)
		})
	})

	t.Run("duplicate secret names", func(t *testing.T) {
		// check that each module has it's own segmented secret store, by
		// writing secrets with the same name
		t.Parallel()

		var logs safeBuffer
		c, ctx := connect(t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/maker").
			With(daggerExec("init", "--name=maker", "--sdk=go", "--source=.")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import "context"

type Maker struct {}

func (_ *Maker) MakeSecret(ctx context.Context) (*Secret, error) {
	secret := dag.SetSecret("FOO", "inner")
	_, err := secret.ID(ctx)  // force the secret into the store
	if err != nil {
		return nil, err
	}
	return secret, nil
}
`,
			})

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./maker")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) Attempt(ctx context.Context) error {
	secret := dag.SetSecret("FOO", "outer")
	_, err := secret.ID(ctx)  // force the secret into the store
	if err != nil {
		return err
	}

	// this creates an inner secret "FOO", but it mustn't overwrite the outer one
	secret2 := dag.Maker().MakeSecret()

	plaintext, err := secret.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "outer" {
		return fmt.Errorf("expected \"outer\", but got %q", plaintext)
	}

	plaintext, err = secret2.Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "inner" {
		return fmt.Errorf("expected \"inner\", but got %q", plaintext)
	}

	return nil
}
`,
			})

		_, err := ctr.With(daggerQuery(`{toplevel{attempt}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("separate secret stores", func(t *testing.T) {
		// check that modules can't access each other's global secret stores,
		// by attempting to leak from each other
		t.Parallel()

		var logs safeBuffer
		c, ctx := connect(t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/leaker").
			With(daggerExec("init", "--name=leaker", "--sdk=go", "--source=.")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import (
	"context"
	"fmt"
)

type Leaker struct {}

func (l *Leaker) Leak(ctx context.Context) error {
	secret, _ := dag.Secret("mysecret").Plaintext(ctx)
	fmt.Println("trying to read secret:", secret)
	return nil
}
`,
			})

		ctr = ctr.
			WithWorkdir("/toplevel/leaker-build").
			With(daggerExec("init", "--name=leaker-build", "--sdk=go", "--source=.")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import "context"

type LeakerBuild struct {}

func (l *LeakerBuild) Leak(ctx context.Context) error {
	_, err := dag.Directory().
		WithNewFile("Dockerfile", "FROM alpine\nRUN --mount=type=secret,id=mysecret cat /run/secrets/mysecret || true").
		DockerBuild().
		Sync(ctx)
	return err
}
`,
			})

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./leaker")).
			With(daggerExec("install", "./leaker-build")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import "context"

type Toplevel struct {}

func (t *Toplevel) Attempt(ctx context.Context, uniq string) error {
	// get the id of a secret to force the engine to eval it
	_, err := dag.SetSecret("mysecret", "asdf" + "asdf").ID(ctx)
	if err != nil {
		return err
	}
	_, err = dag.Leaker().Leak(ctx)
	if err != nil {
		return err
	}
	_, err = dag.LeakerBuild().Leak(ctx)
	if err != nil {
		return err
	}
	return nil
}
`,
			})

		_, err := ctr.With(daggerQuery(`{toplevel{attempt(uniq: %q)}}`, identity.NewID())).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
		require.NotContains(t, logs.String(), "asdfasdf")
	})

	t.Run("secret by id leak", func(t *testing.T) {
		// check that modules can't access each other's global secret stores,
		// even when we know the underlying IDs
		t.Parallel()

		t.Skip("this protection is not yet implemented")

		var logs safeBuffer
		c, ctx := connect(t, dagger.WithLogOutput(io.MultiWriter(os.Stderr, &logs)))

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/leaker").
			With(daggerExec("init", "--name=leaker", "--sdk=go", "--source=.")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import (
	"context"
)

type Leaker struct {}

func (l *Leaker) Leak(ctx context.Context, target string) string {
	secret, _ := dag.LoadSecretFromID(SecretID(target)).Plaintext(ctx)
	return secret
}
`,
			})

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./leaker")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (t *Toplevel) Attempt(ctx context.Context, uniq string) error {
	secretID, err := dag.SetSecret("mysecret", "asdfasdf").ID(ctx)
	if err != nil {
		return err
	}

	// loading secret-by-id in the same module should succeed
	plaintext, err := dag.LoadSecretFromID(secretID).Plaintext(ctx)
	if err != nil {
		return err
	}
	if plaintext != "asdfasdf" {
		return fmt.Errorf("expected \"asdfasdf\", but got %q", plaintext)
	}

	// but getting a leaker module to do this should fail
	plaintext, err = dag.Leaker().Leak(ctx, string(secretID))
	if err != nil {
		return err
	}
	if plaintext != "" {
		return fmt.Errorf("expected \"\", but got %q", plaintext)
	}

	return nil
}
`,
			})

		_, err := ctr.With(daggerQuery(`{toplevel{attempt(uniq: %q)}}`, identity.NewID())).Stdout(ctx)
		require.NoError(t, err)
		require.NoError(t, c.Close())
	})

	t.Run("secrets cache normally", func(t *testing.T) {
		// check that secrets cache as they would without nested modules,
		// which is essentially dependent on whether they have stable IDs

		t.Parallel()

		c, ctx := connect(t)

		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

		ctr = ctr.
			WithWorkdir("/toplevel/secreter").
			With(daggerExec("init", "--name=secreter", "--sdk=go", "--source=.")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

type Secreter struct {}

func (_ *Secreter) Make(uniq string) *Secret {
	return dag.SetSecret("MY_SECRET", uniq)
}
`,
			})

		ctr = ctr.
			WithWorkdir("/toplevel").
			With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./secreter")).
			WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
				Contents: fmt.Sprintf(`package main

import (
	"context"
	"fmt"
)

type Toplevel struct {}

func (_ *Toplevel) AttemptInternal(ctx context.Context) error {
	return diffSecret(
		ctx,
		dag.SetSecret("MY_SECRET", "foo"),
		dag.SetSecret("MY_SECRET", "bar"),
	)
}

func (_ *Toplevel) AttemptExternal(ctx context.Context) error {
	return diffSecret(
		ctx,
		dag.Secreter().Make("foo"),
		dag.Secreter().Make("bar"),
	)
}

func diffSecret(ctx context.Context, first, second *Secret) error {
	firstOut, err := dag.Container().
		From("%[1]s").
		WithSecretVariable("VAR", first).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	secondOut, err := dag.Container().
		From("%[1]s").
		WithSecretVariable("VAR", second).
		WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
		Stdout(ctx)
	if err != nil {
		return err
	}

	if firstOut != secondOut {
		return fmt.Errorf("%%q != %%q", firstOut, secondOut)
	}
	return nil
}
`, alpineImage),
			})

		t.Run("internal secrets cache", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.With(daggerQuery(`{toplevel{attemptInternal}}`)).Stdout(ctx)
			require.NoError(t, err)
		})

		t.Run("external secrets cache", func(t *testing.T) {
			t.Parallel()
			_, err := ctr.With(daggerQuery(`{toplevel{attemptExternal}}`)).Stdout(ctx)
			require.NoError(t, err)
		})
	})
}

func TestModuleUnicodePath(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/wórk/sub/").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/wórk/sub/main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main
 			import (
 				"context"
 			)
 			type Test struct {}
 			func (m *Test) Hello(ctx context.Context) string {
				return "hello"
 			}
 			`,
		}).
		With(daggerQuery(`{test{hello}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"hello":"hello"}}`, out)
}

func TestModuleStartServices(t *testing.T) {
	t.Parallel()

	// regression test for https://github.com/dagger/dagger/pull/6914
	t.Run("use service in multiple functions", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: fmt.Sprintf(`package main
	import (
		"context"
		"fmt"
	)

	type Test struct {
	}

	func (m *Test) FnA(ctx context.Context) (*Sub, error) {
		svc := dag.Container().
			From("python").
			WithMountedDirectory(
				"/srv/www",
				dag.Directory().WithNewFile("index.html", "hey there"),
			).
			WithWorkdir("/srv/www").
			WithExposedPort(23457).
			WithExec([]string{"python", "-m", "http.server", "23457"}).
			AsService()

		ctr := dag.Container().
			From("%s").
			WithServiceBinding("svc", svc).
			WithExec([]string{"wget", "-O", "-", "http://svc:23457"})

		out, err := ctr.Stdout(ctx)
		if err != nil {
			return nil, err
		}
		if out != "hey there" {
			return nil, fmt.Errorf("unexpected output: %%q", out)
		}
		return &Sub{Ctr: ctr}, nil
	}

	type Sub struct {
		Ctr *Container
	}

	func (m *Sub) FnB(ctx context.Context) (string, error) {
		return m.Ctr.
			WithExec([]string{"wget", "-O", "-", "http://svc:23457"}).
			Stdout(ctx)
	}
	`, alpineImage),
			}).
			With(daggerCall("fn-a", "fn-b")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey there", strings.TrimSpace(out))
	})

	// regression test for https://github.com/dagger/dagger/issues/6951
	t.Run("service in multiple containers", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: fmt.Sprintf(`package main
import (
	"context"
)

type Test struct {
}

func (m *Test) Fn(ctx context.Context) *Container {
	redis := dag.Container().
		From("redis").
		WithExposedPort(6379).
		AsService()
	cli := dag.Container().
		From("redis").
		WithoutEntrypoint().
		WithServiceBinding("redis", redis)

	ctrA := cli.WithExec([]string{"sh", "-c", "redis-cli -h redis info >> /tmp/out.txt"})

	file := ctrA.Directory("/tmp").File("/out.txt")

	ctrB := dag.Container().
		From("%s").
		WithFile("/out.txt", file)

	return ctrB.WithExec([]string{"cat", "/out.txt"})
}
	`, alpineImage),
			}).
			With(daggerCall("fn", "stdout")).
			Sync(ctx)
		require.NoError(t, err)
	})
}

// regression test for https://github.com/dagger/dagger/issues/7334
// and https://github.com/dagger/dagger/pull/7336
func TestModuleCallSameModuleInParallel(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--name=dep", "--sdk=go")).
		With(sdkSource("go", `package main

import (
	"github.com/moby/buildkit/identity"
)

type Dep struct {}

func (m *Dep) DepFn(s *Secret) string {
	return identity.NewID()
}
`)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go")).
		With(sdkSource("go", `package main

import (
	"context"
	"golang.org/x/sync/errgroup"
)

type Test struct {}

func (m *Test) Fn(ctx context.Context) ([]string, error) {
	var eg errgroup.Group
	results := make([]string, 10)
	for i := 0; i < 10; i++ {
		i := i
		eg.Go(func() error {
			res, err := dag.Dep().DepFn(ctx, dag.SetSecret("foo", "bar"))
			if err != nil {
				return err
			}
			results[i] = res
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}
`)).
		With(daggerExec("install", "./dep")).
		With(daggerCall("fn"))

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	results := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, results, 10)
	expectedRes := results[0]
	for _, res := range results {
		require.Equal(t, expectedRes, res)
	}
}

func TestModuleLargeObjectFieldVal(t *testing.T) {
	// make sure we don't hit any limits when an object field value is large
	t.Parallel()
	c, ctx := connect(t)

	// put a timeout on this since failures modes could result in hangs
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	_, err := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go")).
		With(sdkSource("go", `package main

import "strings"

type Test struct {
	BigVal string
}

func New() *Test {
	return &Test{
		BigVal: strings.Repeat("a", 30*1024*1024),
	}
}

// add a func for returning the val in order to test mode codepaths that
// involve serializing and passing the object around
func (m *Test) Fn() string {
	return m.BigVal
}
`)).
		With(daggerCall("fn")).
		Sync(ctx)
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

// fileContents is syntax sugar for Container.WithNewFile.
func fileContents(path, contents string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithNewFile(path, dagger.ContainerWithNewFileOpts{
			Contents: heredoc.Doc(contents),
		})
	}
}

func configFile(dirPath string, cfg *modules.ModuleConfig) dagger.WithContainerFunc {
	cfgPath := filepath.Join(dirPath, "dagger.json")
	cfgBytes, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	return fileContents(cfgPath, string(cfgBytes))
}

// command for a dagger cli call direct on the host
func hostDaggerCommand(ctx context.Context, t testing.TB, workdir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(ctx, daggerCliPath(t), args...)
	cmd.Dir = workdir
	return cmd
}

// runs a dagger cli command directly on the host, rather than in an exec
func hostDaggerExec(ctx context.Context, t testing.TB, workdir string, args ...string) ([]byte, error) { //nolint: unparam
	t.Helper()
	cmd := hostDaggerCommand(ctx, t, workdir, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s: %w", string(output), err)
	}
	return output, err
}

func sdkSource(sdk, contents string) dagger.WithContainerFunc {
	return fileContents(sdkSourceFile(sdk), contents)
}

func sdkSourceFile(sdk string) string {
	switch sdk {
	case "go":
		return "dagger/main.go"
	case "python":
		return "dagger/" + pythonSourcePath
	case "typescript":
		return "dagger/src/index.ts"
	default:
		panic(fmt.Errorf("unknown sdk %q", sdk))
	}
}

func sdkCodegenFile(t *testing.T, sdk string) string {
	t.Helper()
	switch sdk {
	case "go":
		// FIXME: go codegen is split up into dagger/dagger.gen.go and
		// dagger/internal/dagger/dagger.gen.go
		return "dagger/internal/dagger/dagger.gen.go"
	case "python":
		return "dagger/sdk/src/dagger/client/gen.py"
	case "typescript":
		return "dagger/sdk/api/client.gen.ts"
	default:
		panic(fmt.Errorf("unknown sdk %q", sdk))
	}
}

func modInit(t *testing.T, c *dagger.Client, sdk, contents string) *dagger.Container {
	t.Helper()
	return daggerCliBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk="+sdk)).
		With(sdkSource(sdk, contents))
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

var moduleIntrospection = daggerQuery(`
query { host { directory(path: ".") { asModule { initialize {
    description
    objects {
        asObject {
            name
            description
            constructor {
                description
                args {
                    name
                    description
                    defaultValue
                }
            }
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
    enums {
        asEnum {
            name
            description
            values {
                name
				description
			}
        }
    }
} } } } }
`)

func inspectModule(ctx context.Context, t *testing.T, ctr *dagger.Container) gjson.Result {
	t.Helper()
	out, err := ctr.With(moduleIntrospection).Stdout(ctx)
	require.NoError(t, err)
	result := gjson.Get(out, "host.directory.asModule.initialize")
	t.Logf("module introspection:\n%v", result.Raw)
	return result
}

func inspectModuleObjects(ctx context.Context, t *testing.T, ctr *dagger.Container) gjson.Result {
	t.Helper()
	return inspectModule(ctx, t, ctr).Get("objects.#.asObject")
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
