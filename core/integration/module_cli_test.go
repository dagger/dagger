package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type CLISuite struct{}

func TestCLI(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CLISuite{})
}

func (CLISuite) TestDaggerInit(ctx context.Context, t *testctx.T) {
	t.Run("name defaults to source root dir name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=coolmod", "--sdk=go", "coolmod")).
			WithNewFile("/work/coolmod/main.go", `package main

			import "context"

			type Coolmod struct {}

			func (m *Coolmod) Fn(ctx context.Context) (string, error) {
				return dag.CurrentModule().Name(ctx)
			}
			`,
			).
			With(daggerCallAt("coolmod", "fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "coolmod", strings.TrimSpace(out))
	})

	t.Run("source dir default", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work")

		for _, tc := range []struct {
			sdk          string
			sourceDirEnt string
		}{
			{
				sdk:          "go",
				sourceDirEnt: "main.go",
			},
			{
				sdk:          "python",
				sourceDirEnt: "src/",
			},
			{
				sdk:          "typescript",
				sourceDirEnt: "src/",
			},
		} {
			t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
				srcRootDir := ctr.
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
					Directory(".")
				srcRootEnts, err := srcRootDir.Entries(ctx)
				require.NoError(t, err)
				require.Contains(t, srcRootEnts, "dagger.json")
				require.Contains(t, srcRootEnts, tc.sourceDirEnt)
				srcDirEnts, err := srcRootDir.Directory(".").Entries(ctx)
				require.NoError(t, err)
				require.Contains(t, srcDirEnts, tc.sourceDirEnt)
			})
		}
	})

	t.Run("source is made rel to source root by engine", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := goGitBase(t, c).
			WithWorkdir("/var").
			With(daggerExec("init", "--source=../work/some/subdir", "--name=test", "--sdk=go", "../work")).
			With(daggerCallAt("../work", "container-echo", "--string-arg", "yo", "stdout"))
		out, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo", strings.TrimSpace(out))

		ents, err := ctr.Directory("/work/some/subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, ents, "main.go")
	})

	t.Run("works inside subdir of other module", func(ctx context.Context, t *testctx.T) {
		// verifies find-up logic does NOT kick in here
		c := connect(ctx, t)

		ctr := goGitBase(t, c).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=a", "--sdk=go", ".")).
			WithWorkdir("/work/subdir").
			With(daggerExec("init", "--name=b", "--sdk=go", "--source=.", ".")).
			WithNewFile("./main.go", `package main

			type B struct {}

			func (m *B) Fn() string { return "yo" }
			`,
			).
			With(daggerCall("fn"))
		out, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo", strings.TrimSpace(out))
	})

	t.Run("init with absolute path", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		absPath := "/work/mymodule"
		ctr := goGitBase(t, c).
			WithWorkdir("/").
			With(daggerExec("init", "--source="+absPath, "--name=test", "--sdk=go", absPath)).
			WithNewFile(absPath+"/main.go", `package main
				type Test struct {}
				func (m *Test) Fn() string { return "hello from absolute path" }`,
			).
			With(daggerCallAt(absPath, "fn"))

		out, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from absolute path", strings.TrimSpace(out))
	})

	t.Run("init with absolute path and src .", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		absPath := "/work/mymodule"
		ctr := goGitBase(t, c).
			WithWorkdir("/").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go", absPath))

		_, err := ctr.Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "source subpath \"../..\" escapes source root")
	})
}

func (CLISuite) TestDaggerInitLICENSE(ctx context.Context, t *testctx.T) {
	t.Run("bootstraps Apache-2.0 LICENSE file if none found", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=licensed-to-ill", "--sdk=go"))

		content, err := modGen.File("LICENSE").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "Apache License, Version 2.0")
	})

	t.Run("do not bootstrap LICENSE file if license is set empty", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=empty-license", "--sdk=go", "--license="))

		files, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, files, "LICENSE")
	})

	t.Run("do not bootstrap LICENSE file if no sdk is specified", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=no-license", "--source=."))

		files, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, files, "LICENSE")

		t.Run("do not bootstrap LICENSE file if no sdk is specified", func(ctx context.Context, t *testctx.T) {
			modGen := modGen.With(daggerExec("develop", "--source=dagger"))

			files, err := modGen.Directory(".").Entries(ctx)
			require.NoError(t, err)
			require.NotContains(t, files, "LICENSE")
		})

		t.Run("do not bootstrap LICENSE file if license is empty", func(ctx context.Context, t *testctx.T) {
			modGen := modGen.With(daggerExec("develop", "--source=dagger", `--license=""`))

			files, err := modGen.Directory(".").Entries(ctx)
			require.NoError(t, err)
			require.NotContains(t, files, "LICENSE")
		})

		t.Run("bootstrap a license after sdk is set on dagger develop", func(ctx context.Context, t *testctx.T) {
			modGen := modGen.With(daggerExec("develop", "--sdk=go"))

			content, err := modGen.File("LICENSE").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, content, "Apache License, Version 2.0")
		})

		t.Run("bootstrap custom LICENSE file if sdk and license are specified", func(ctx context.Context, t *testctx.T) {
			modGen := modGen.With(daggerExec("develop", "--sdk=go", `--license=MIT`))

			content, err := modGen.File("LICENSE").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, content, "MIT License")
		})
	})

	t.Run("creates LICENSE file in the directory specified by arg", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=licensed-to-ill", "--sdk=go", "./mymod"))

		content, err := modGen.File("mymod/LICENSE").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "Apache License, Version 2.0")
	})

	t.Run("does not bootstrap LICENSE file if it exists in the parent context", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/LICENSE", "doesnt matter").
			WithWorkdir("/work/sub").
			With(daggerExec("init", "--name=licensed-to-ill", "--sdk=go"))

		// Check that the license file is not generated in the sub directory.
		files, err := modGen.Directory("/work/sub").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, files, "LICENSE")

		// Check that the parent directory actually has a LICENSE file.
		files, err = modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, files, "LICENSE")
	})

	t.Run("does not bootstrap LICENSE file if it exists in an arbitrary parent dir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/LICENSE", "doesnt matter").
			WithWorkdir("/work/sub").
			With(daggerExec("init", "--name=licensed-to-ill", "--sdk=go"))

		content, err := modGen.File("LICENSE").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "Apache License, Version 2.0")
	})

	t.Run("bootstraps a LICENSE file when requested, even if it exists in the parent dir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/LICENSE", "doesnt matter").
			WithWorkdir("/work/sub").
			With(daggerExec("init", "--name=licensed-to-ill", "--sdk=go", "--license=MIT"))

		content, err := modGen.File("LICENSE").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, content, "MIT License")
	})
}

func (CLISuite) TestDaggerInitGit(ctx context.Context, t *testctx.T) {
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
		t.Run(fmt.Sprintf("module %s git", tc.sdk), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := goGitBase(t, c).
				With(daggerExec("init", "--name=bare", "--sdk="+tc.sdk))

			out, err := modGen.
				With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
				Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)

			t.Run("configures .gitattributes", func(ctx context.Context, t *testctx.T) {
				ignore, err := modGen.File(".gitattributes").Contents(ctx)
				require.NoError(t, err)
				for _, fileName := range tc.gitGeneratedFiles {
					require.Contains(t, ignore, fmt.Sprintf("%s linguist-generated\n", fileName))
				}
			})

			t.Run("configures .gitignore", func(ctx context.Context, t *testctx.T) {
				ignore, err := modGen.File(".gitignore").Contents(ctx)
				require.NoError(t, err)
				for _, fileName := range tc.gitIgnoredFiles {
					require.Contains(t, ignore, fileName)
				}
			})

			t.Run("does not configure .gitignore if disabled", func(ctx context.Context, t *testctx.T) {
				modGen := goGitBase(t, c).
					With(daggerExec("init", "--name=bare", "--source=."))

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

				modGen = modGen.
					WithNewFile("dagger.json", string(modCfgBytes)).
					With(daggerExec("develop", "--sdk=go"))

				_, err = modGen.File(".gitignore").Contents(ctx)
				requireErrOut(t, err, "no such file or directory")
			})
		})
	}
}

func (CLISuite) TestDaggerDevelop(ctx context.Context, t *testctx.T) {
	t.Run("name and sdk", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) Fn(ctx context.Context) string {
				return "hi from dep"
			}
			`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.")).
			With(daggerExec("install", "./dep"))

		// should be able to invoke dep without name+sdk set yet
		out, err := base.With(daggerCallAt("dep", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep", strings.TrimSpace(out))

		// test develop from source root and from subdir (in which case find-up should kick in)
		for _, wd := range []string{"/work", "/work/from/some/otherdir"} {
			t.Run(wd, func(ctx context.Context, t *testctx.T) {
				sourceDir, err := filepath.Rel(wd, "/work/cool/subdir")
				require.NoError(t, err)

				ctr := base.
					WithWorkdir(wd).
					With(daggerExec("develop", "--sdk", "go", "--source", sourceDir)).
					WithNewFile("/work/cool/subdir/main.go", `package main

					import "context"

					type Work struct {}

					func (m *Work) Fn(ctx context.Context) (string, error) {
						depStr, err := dag.Dep().Fn(ctx)
						if err != nil {
							return "", err
						}
						return "hi from work " + depStr, nil
					}
					`,
					)

				// should be able to invoke it directly now
				out, err := ctr.With(daggerCall("fn")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "hi from work hi from dep", strings.TrimSpace(out))

				// currently, we don't support renaming or re-sdking a module, make sure that errors comprehensibly

				_, err = ctr.With(daggerExec("develop", "--sdk", "python")).Sync(ctx)
				requireErrOut(t, err, `cannot update module SDK that has already been set to "go"`)

				_, err = ctr.With(daggerExec("develop", "--source", "blahblahblaha/blah")).Sync(ctx)
				requireErrOut(t, err, `cannot update module source path that has already been set to "cool/subdir"`)
			})
		}
	})

	t.Run("source is made rel to source root by engine", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := goGitBase(t, c).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) Fn(ctx context.Context) string {
				return "hi from dep"
			}
			`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.")).
			With(daggerExec("install", "./dep")).
			WithWorkdir("/var").
			With(daggerExec("develop", "-m", "../work", "--source=../work/some/subdir", "--sdk=go")).
			WithNewFile("/work/some/subdir/main.go", `package main

			import "context"

			type Work struct {}

			func (m *Work) Fn(ctx context.Context) (string, error) {
				depStr, err := dag.Dep().Fn(ctx)
				if err != nil {
					return "", err
				}
				return "hi from work " + depStr, nil
			}
			`,
			)

		out, err := ctr.With(daggerCallAt("../work", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from work hi from dep", strings.TrimSpace(out))

		ents, err := ctr.Directory("/work/some/subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, ents, "main.go")
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("fails on git", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			privateSetup, cleanup := privateRepoSetup(c, t, tc)
			defer cleanup()

			_, err := goGitBase(t, c).
				With(privateSetup).
				With(daggerExec("develop", "-m", testGitModuleRef(tc, "top-level"))).
				Sync(ctx)
			requireErrRegexp(t, err, `module source ".*" kind must be "local", got "git"`)
		})
	})

	t.Run("source is made rel to source root by engine with absolute path", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		absPath := "/work"
		ctr := goGitBase(t, c).
			WithWorkdir(absPath+"/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile(absPath+"/dep/main.go", `package main

        import "context"

        type Dep struct {}

        func (m *Dep) Fn(ctx context.Context) string {
            return "hi from dep"
        }
        `,
			).
			WithWorkdir(absPath).
			With(daggerExec("init", "--source=.")).
			With(daggerExec("install", "./dep")).
			WithWorkdir("/var").
			With(daggerExec("develop", "-m", absPath, "--source="+absPath+"/some/subdir", "--sdk=go")).
			WithNewFile(absPath+"/some/subdir/main.go", `package main

        import "context"

        type Work struct {}

        func (m *Work) Fn(ctx context.Context) (string, error) {
            depStr, err := dag.Dep().Fn(ctx)
            if err != nil {
                return "", err
            }
            return "hi from work " + depStr, nil
        }
        `,
			)

		out, err := ctr.With(daggerCallAt(absPath, "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from work hi from dep", strings.TrimSpace(out))
	})

	t.Run("recursive", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithExec([]string{"rm", "dagger.gen.go"}).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--sdk=go")).
			With(daggerExec("install", "--name=cooldep", "./dep")).
			WithExec([]string{"rm", "dagger.gen.go"})
		developed := base.With(daggerExec("develop", "--recursive"))

		// check that the developed files get recreated
		_, err := developed.File("/work/dagger.gen.go").Contents(ctx)
		require.NoError(t, err)
		_, err = developed.File("/work/dep/dagger.gen.go").Contents(ctx)
		require.NoError(t, err)

		// make sure that even though we named the dep cooldep during install,
		// the updated dagger.json for the dep still has the original name
		depDaggerJSON, err := developed.File("/work/dep/dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, gjson.Get(depDaggerJSON, "name").String(), "dep")
	})
}

func (CLISuite) TestDaggerInstall(ctx context.Context, t *testctx.T) {
	t.Run("local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/subdir/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/subdir/dep/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=test", "--name=test", "--sdk=go", "test")).
			With(daggerExec("install", "-m=test", "./subdir/dep")).
			WithNewFile("/work/test/main.go", `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) { return dag.Dep().DepFn(ctx, "hi dep") }
			`,
			)

		// try invoking it from a few different paths, just for more corner case coverage

		t.Run("from src dir", func(ctx context.Context, t *testctx.T) {
			out, err := base.WithWorkdir("test").With(daggerCall("fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from src root", func(ctx context.Context, t *testctx.T) {
			out, err := base.With(daggerCallAt("test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from root", func(ctx context.Context, t *testctx.T) {
			out, err := base.WithWorkdir("/").With(daggerCallAt("work/test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from dep parent", func(ctx context.Context, t *testctx.T) {
			out, err := base.WithWorkdir("/work/subdir").With(daggerCallAt("../test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from dep dir", func(ctx context.Context, t *testctx.T) {
			out, err := base.WithWorkdir("/work/subdir/dep").With(daggerCallAt("../../test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})
		t.Run("from src dir with absolute path", func(ctx context.Context, t *testctx.T) {
			out, err := base.WithWorkdir("/work/test").With(daggerCall("fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from src root with absolute path", func(ctx context.Context, t *testctx.T) {
			out, err := base.With(daggerCallAt("/work/test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from root with absolute path", func(ctx context.Context, t *testctx.T) {
			out, err := base.WithWorkdir("/").With(daggerCallAt("/work/test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from dep parent with absolute path", func(ctx context.Context, t *testctx.T) {
			out, err := base.WithWorkdir("/work/subdir").With(daggerCallAt("/work/test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from dep dir with absolute path", func(ctx context.Context, t *testctx.T) {
			out, err := base.WithWorkdir("/work/subdir/dep").With(daggerCallAt("/work/test", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		// Test installing with absolute paths
		t.Run("install with absolute paths", func(ctx context.Context, t *testctx.T) {
			ctr := base.
				WithWorkdir("/").
				With(daggerExec("init", "--source=/work/test2", "--name=test2", "--sdk=go", "/work/test2")).
				With(daggerExec("install", "-m=/work/test2", "/work/subdir/dep")).
				WithNewFile("/work/test2/main.go", `package main

            import "context"

            type Test2 struct {}

            func (m *Test2) Fn(ctx context.Context) (string, error) { return dag.Dep().DepFn(ctx, "hi from test2") }
            `,
				)

			out, err := ctr.With(daggerCallAt("/work/test2", "fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi from test2", strings.TrimSpace(out))
		})
	})

	t.Run("installing a dependency with duplicate name is not allowed", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--sdk=go", "--name=dep", "--source=.")).
			WithWorkdir("/work/dep2").
			With(daggerExec("init", "--sdk=go", "--name=dep2", "--source=.")).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerExec("install", "./dep"))

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, `"dep"`)

		_, err = ctr.
			With(daggerExec("install", "./dep2", "--name=dep")).
			Sync(ctx)

		requireErrOut(t, err, fmt.Sprintf("duplicate dependency name %q", "dep"))
	})

	t.Run("installing a dependency with implicit duplicate name is not allowed", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--sdk=go", "--name=dep", "--source=.")).
			WithWorkdir("/work/dep2").
			With(daggerExec("init", "--sdk=go", "--name=dep", "--source=.")).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go", "--source=.")).
			With(daggerExec("install", "./dep"))

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, `"dep"`)

		_, err = ctr.
			With(daggerExec("install", "./dep2")).
			Sync(ctx)

		requireErrOut(t, err, fmt.Sprintf("duplicate dependency name %q", "dep"))
	})

	t.Run("install dep from various places", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=subdir/dep", "--name=dep", "--sdk=go", "subdir/dep")).
			WithNewFile("/work/subdir/dep/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			).
			With(daggerExec("init", "--source=test", "--name=test", "--sdk=go", "test")).
			WithNewFile("/work/test/main.go", `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) { return dag.Dep().DepFn(ctx, "hi dep") }
			`,
			)

		t.Run("from src dir", func(ctx context.Context, t *testctx.T) {
			// sanity test normal case

			out, err := base.
				WithWorkdir("/work/test").
				With(daggerExec("install", "../subdir/dep")).
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from src subdir with findup", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/work/test/some/other/dir").
				With(daggerExec("install", "../../../../subdir/dep")).
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from root", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/").
				With(daggerExec("install", "-m=./work/test", "./work/subdir/dep")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from dep", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/work/subdir/dep").
				With(daggerExec("install", "-m=../../test", ".")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from random place", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/var").
				With(daggerExec("install", "-m=../work/test", "../work/subdir/dep")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from src dir with absolute paths", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/work/test").
				With(daggerExec("install", "/work/subdir/dep")).
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from root with absolute paths", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/").
				With(daggerExec("install", "-m=/work/test", "/work/subdir/dep")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from random place with absolute paths", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/var").
				With(daggerExec("install", "-m=/work/test", "/work/subdir/dep")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})
	})

	t.Run("install out of tree dep fails", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/play/dep").
			With(daggerExec("init", "--name=dep", "--sdk=go")).
			WithWorkdir("/work/test").
			With(daggerExec("init", "--name=test", "--sdk=go"))

		t.Run("from src dir", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/work/test").
				With(daggerExec("install", "../../play/dep")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})

		t.Run("from src dir with absolute path", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/work/test").
				With(daggerExec("install", "/play/dep")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})

		t.Run("from dep dir", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/play/dep").
				With(daggerExec("install", "-m=../../work/test", ".")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})

		t.Run("from dep dir with absolute path", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/play/dep").
				With(daggerExec("install", "-m=/work/test", ".")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})

		t.Run("from root", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/").
				With(daggerExec("install", "-m=work/test", "play/dep")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})

		t.Run("from root with absolute path", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/").
				With(daggerExec("install", "-m=/work/test", "play/dep")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("git", func(ctx context.Context, t *testctx.T) {
			t.Run("happy", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				privateSetup, cleanup := privateRepoSetup(c, t, tc)
				defer cleanup()

				out, err := goGitBase(t, c).
					With(privateSetup).
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerExec("install", testGitModuleRef(tc, "top-level"))).
					WithNewFile("main.go", `package main

import "context"

type Test struct {}

func (m *Test) Fn(ctx context.Context) (string, error) {
	return dag.TopLevel().Fn(ctx)
}
`,
					).
					With(daggerCall("fn")).
					Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "hi from top level hi from dep hi from dep2", strings.TrimSpace(out))
			})

			t.Run("sad", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				privateSetup, cleanup := privateRepoSetup(c, t, tc)
				defer cleanup()

				_, err := goGitBase(t, c).
					With(privateSetup).
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerExec("install", testGitModuleRef(tc, "../../"))).
					Sync(ctx)
				requireErrOut(t, err, `git module source subpath points out of root: "../.."`)

				_, err = goGitBase(t, c).
					With(privateSetup).
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerExec("install", testGitModuleRef(tc, "this/just/does/not/exist"))).
					Sync(ctx)
				requireErrRegexp(t, err, `git module source .* does not contain a dagger config file`)
			})

			t.Run("unpinned gets pinned", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				privateSetup, cleanup := privateRepoSetup(c, t, tc)
				defer cleanup()

				out, err := goGitBase(t, c).
					With(privateSetup).
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerExec("install", tc.gitTestRepoRef)).
					File("/work/dagger.json").
					Contents(ctx)
				require.NoError(t, err)
				var modCfg modules.ModuleConfig
				require.NoError(t, json.Unmarshal([]byte(out), &modCfg))
				require.Len(t, modCfg.Dependencies, 1)
				require.Equal(t, tc.gitTestRepoRef+"@main", modCfg.Dependencies[0].Source)
				require.NotEmpty(t, modCfg.Dependencies[0].Pin)
			})
		})
	})
}

func (CLISuite) TestDaggerInstallOrder(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep-abc").
		With(daggerExec("init", "--source=.", "--name=dep-abc", "--sdk=go")).
		WithNewFile("/work/dep-abc/main.go", `package main

			import "context"

			type DepAbc struct {}

			func (m *DepAbc) DepFn(ctx context.Context, str string) string { return str }
			`,
		).
		WithWorkdir("/work/dep-xyz").
		With(daggerExec("init", "--source=.", "--name=dep-xyz", "--sdk=go")).
		WithNewFile("/work/dep-xyz/main.go", `package main

			import "context"

			type DepXyz struct {}

			func (m *DepXyz) DepFn(ctx context.Context, str string) string { return str }
			`,
		).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=test", "--name=test", "--sdk=go"))

	daggerJSON, err := base.
		With(daggerExec("install", "./dep-abc")).
		With(daggerExec("install", "./dep-xyz")).
		File("dagger.json").
		Contents(ctx)
	require.NoError(t, err)
	names := []string{}
	for _, name := range gjson.Get(daggerJSON, "dependencies.#.name").Array() {
		names = append(names, name.String())
	}
	require.Equal(t, []string{"dep-abc", "dep-xyz"}, names)

	daggerJSON, err = base.
		// switch the installation order
		With(daggerExec("install", "./dep-xyz")).
		With(daggerExec("install", "./dep-abc")).
		File("dagger.json").
		Contents(ctx)
	require.NoError(t, err)
	names = []string{}
	for _, name := range gjson.Get(daggerJSON, "dependencies.#.name").Array() {
		names = append(names, name.String())
	}
	require.Equal(t, []string{"dep-abc", "dep-xyz"}, names)
}

func (CLISuite) TestCLIFunctions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"context"

	"dagger/test/internal/dagger"
)

type Test struct{}

// doc for FnA
func (m *Test) FnA() *dagger.Container {
	return nil
}

// doc for FnB
func (m *Test) FnB() Duck {
	return nil
}

type Duck interface {
	DaggerObject
	// quack that thang
	Quack(ctx context.Context) (string, error)
}

// doc for FnC
func (m *Test) FnC() *Obj {
	return nil
}

// doc for Prim
func (m *Test) Prim() string {
	return "yo"
}

type Obj struct {
	// doc for FieldA
	FieldA *dagger.Container
	// doc for FieldB
	FieldB string
	// doc for FieldC
	FieldC *Obj
	// doc for FieldD
	FieldD *OtherObj
}

// doc for FnD
func (m *Obj) FnD() *dagger.Container {
	return nil
}

type OtherObj struct {
	// doc for OtherFieldA
	OtherFieldA *dagger.Container
	// doc for OtherFieldB
	OtherFieldB string
	// doc for OtherFieldC
	OtherFieldC *Obj
	// doc for OtherFieldD
	OtherFieldD *OtherObj
}

// doc for FnE
func (m *OtherObj) FnE() *dagger.Container {
	return nil
}

`,
		)

	t.Run("top-level", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions()).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn-a   doc for FnA")
		require.Contains(t, lines, "fn-b   doc for FnB")
		require.Contains(t, lines, "fn-c   doc for FnC")
		require.Contains(t, lines, "prim   doc for Prim")
	})

	t.Run("top-level from subdir", func(ctx context.Context, t *testctx.T) {
		// find-up should kick in
		out, err := ctr.
			WithWorkdir("/work/some/subdir").
			With(daggerFunctions()).
			Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "fn-a   doc for FnA")
		require.Contains(t, lines, "fn-b   doc for FnB")
		require.Contains(t, lines, "fn-c   doc for FnC")
		require.Contains(t, lines, "prim   doc for Prim")
	})

	t.Run("return core object", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("fn-a")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "file                          Retrieves a file at the given path.")
		require.Contains(t, lines, "as-tarball                    Package the container state as an OCI image, and return it as a tar archive")
	})

	t.Run("return primitive", func(ctx context.Context, t *testctx.T) {
		_, err := ctr.With(daggerFunctions("prim")).Stdout(ctx)
		requireErrOut(t, err, `function "prim" returns type "STRING_KIND" with no further functions available`)
	})

	t.Run("alt casing", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("fnA")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "file                          Retrieves a file at the given path.")
		require.Contains(t, lines, "as-tarball                    Package the container state as an OCI image, and return it as a tar archive")
	})

	t.Run("return user interface", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("fn-b")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		require.Contains(t, lines, "quack   quack that thang")
	})

	t.Run("return user object", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("fn-c")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "field-a   doc for FieldA")
		require.Contains(t, lines, "field-b   doc for FieldB")
		require.Contains(t, lines, "field-c   doc for FieldC")
		require.Contains(t, lines, "field-d   doc for FieldD")
		require.Contains(t, lines, "fn-d      doc for FnD")
	})

	t.Run("return user object nested", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("fn-c", "field-d")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "other-field-a   doc for OtherFieldA")
		require.Contains(t, lines, "other-field-b   doc for OtherFieldB")
		require.Contains(t, lines, "other-field-c   doc for OtherFieldC")
		require.Contains(t, lines, "other-field-d   doc for OtherFieldD")
		require.Contains(t, lines, "fn-e            doc for FnE")
	})

	t.Run("no module present errors nicely", func(ctx context.Context, t *testctx.T) {
		_, err := ctr.
			WithWorkdir("/empty").
			With(daggerFunctions()).
			Stdout(ctx)
		requireErrOut(t, err, `module not found`)
	})
}

func (CLISuite) TestDaggerUnInstall(ctx context.Context, t *testctx.T) {
	t.Run("local dep", func(ctx context.Context, t *testctx.T) {
		t.Run("uninstall a dependency currently used in module", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := c.Container().
				From(alpineImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/bar").
				With(daggerExec("init", "--sdk=go", "--name=bar", "--source=.")).
				WithWorkdir("/work").
				With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
				With(daggerExec("install", "./bar")).
				WithNewFile("main.go", `package main

import (
	"context"
)

type Foo struct{}

func (f *Foo) ContainerEcho(ctx context.Context, input string) (string, error) {
	return dag.Bar().ContainerEcho(input).Stdout(ctx)
}
`)

			daggerjson, err := ctr.File("dagger.json").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, daggerjson, "bar")

			daggerjson, err = ctr.With(daggerExec("uninstall", "bar")).
				File("dagger.json").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, daggerjson, "bar")
		})

		testcases := []struct {
			name            string
			modName         string
			installCmd      []string
			beforeUninstall dagger.WithContainerFunc
			uninstallCmd    []string
		}{
			{
				name:         "uninstall a dependency configured in dagger.json by name",
				modName:      "baz",
				installCmd:   []string{"install", "./bar", "--name=baz"},
				uninstallCmd: []string{"uninstall", "baz"},
			},
			{
				name:         "uninstall a dependency configured in dagger.json",
				modName:      "bar",
				installCmd:   []string{"install", "./bar"},
				uninstallCmd: []string{"uninstall", "bar"},
			},
			{
				name:         "uninstall a dependency configured in dagger.json using relative path syntax",
				modName:      "bar",
				installCmd:   []string{"install", "./bar"},
				uninstallCmd: []string{"uninstall", "./bar"},
			},
			{
				name:         "uninstall a dependency not configured in dagger.json",
				modName:      "bar",
				installCmd:   []string{},
				uninstallCmd: []string{"uninstall", "./bar"},
			},
			{
				name:       "dependency source is removed before calling uninstall",
				modName:    "bar",
				installCmd: []string{"install", "./bar"},
				beforeUninstall: func(ctr *dagger.Container) *dagger.Container {
					return ctr.WithoutDirectory("/work/bar")
				},
				uninstallCmd: []string{"uninstall", "bar"},
			},
			{
				name:       "dependency source is removed before calling uninstall using relative path",
				modName:    "bar",
				installCmd: []string{"install", "./bar"},
				beforeUninstall: func(ctr *dagger.Container) *dagger.Container {
					return ctr.WithoutDirectory("/work/bar")
				},
				uninstallCmd: []string{"uninstall", "./bar"},
			},
		}

		for _, tc := range testcases {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				ctr := c.Container().
					From(alpineImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work/bar").
					With(daggerExec("init", "--sdk=go", "--name=bar", "--source=.")).
					WithWorkdir("/work").
					With(daggerExec("init", "--sdk=go", "--name=foo", "--source=."))

				if len(tc.installCmd) > 0 {
					ctr = ctr.With(daggerExec(tc.installCmd...))
				}

				daggerjson, err := ctr.File("dagger.json").Contents(ctx)
				require.NoError(t, err)

				if len(tc.installCmd) > 0 {
					require.Contains(t, daggerjson, tc.modName)
				}

				if tc.beforeUninstall != nil {
					tc.beforeUninstall(ctr)
				}

				daggerjson, err = ctr.With(daggerExec(tc.uninstallCmd...)).
					File("dagger.json").Contents(ctx)
				require.NoError(t, err)
				require.NotContains(t, daggerjson, tc.modName)
			})
		}
	})

	t.Run("git dependency", func(ctx context.Context, t *testctx.T) {
		testcases := []struct {
			// the mod used when running dagger install <mod>.
			// empty means the dep is not installed
			installCmdMod string

			// the mod used when running dagger uninstall <mod>
			uninstallCmdMod string

			expectedError string
		}{
			{
				installCmdMod:   "github.com/shykes/daggerverse/hello@v0.3.0",
				uninstallCmdMod: "github.com/shykes/daggerverse/hello@v0.3.0",
			},
			{
				installCmdMod:   "github.com/shykes/daggerverse/hello@v0.3.0",
				uninstallCmdMod: "github.com/shykes/daggerverse/hello",
			},
			{
				installCmdMod:   "github.com/shykes/daggerverse/hello@v0.3.0",
				uninstallCmdMod: "hello",
			},
			{
				installCmdMod:   "github.com/shykes/daggerverse/hello",
				uninstallCmdMod: "github.com/shykes/daggerverse/hello",
			},
			{
				installCmdMod:   "github.com/shykes/daggerverse/hello",
				uninstallCmdMod: "github.com/shykes/daggerverse/hello@v0.3.0",
				expectedError:   `version "v0.3.0" was requested to be uninstalled but the dependency "github.com/shykes/daggerverse/hello" was installed with "main"`,
			},
			{
				installCmdMod:   "github.com/shykes/daggerverse/hello",
				uninstallCmdMod: "hello",
			},
			{
				installCmdMod:   "github.com/shykes/daggerverse/hello@v0.1.2",
				uninstallCmdMod: "github.com/shykes/daggerverse/hello@v0.3.0",
				expectedError:   `version "v0.3.0" was requested to be uninstalled but the dependency "github.com/shykes/daggerverse/hello" was installed with "v0.1.2"`,
			},
			{
				installCmdMod:   "",
				uninstallCmdMod: "github.com/shykes/daggerverse/hello@v0.3.0",
			},
			{
				installCmdMod:   "",
				uninstallCmdMod: "github.com/shykes/daggerverse/hello",
			},
			{
				installCmdMod:   "",
				uninstallCmdMod: "hello",
			},
		}

		for _, tc := range testcases {
			t.Run(fmt.Sprintf("installed using %q, uninstalled using %q", tc.installCmdMod, tc.uninstallCmdMod), func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				ctr := c.Container().
					From(alpineImage).
					WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
					WithWorkdir("/work").
					With(daggerExec("init", "--sdk=go", "--name=foo", "--source=."))

				if tc.installCmdMod != "" {
					ctr = ctr.With(daggerExec("install", tc.installCmdMod))
				}

				daggerjson, err := ctr.File("dagger.json").Contents(ctx)
				require.NoError(t, err)

				if tc.installCmdMod != "" {
					require.Contains(t, daggerjson, "hello")
				}

				daggerjson, err = ctr.With(daggerExec("uninstall", tc.uninstallCmdMod)).
					File("dagger.json").Contents(ctx)

				if tc.expectedError != "" {
					requireErrOut(t, err, tc.expectedError)
				} else {
					require.NoError(t, err)
					require.NotContains(t, daggerjson, "hello")
				}
			})
		}
	})
}

func (CLISuite) TestDaggerUpdate(ctx context.Context, t *testctx.T) {
	const (
		// pins from github.com/shykes/daggerverse/docker module
		// to facilitate testing that pins are updated to expected
		// values when we run dagger update
		randomMainPin  = "b20176e68d27edc9660960ec27f323d33dba633b"
		randomWolfiPin = "3d608cb5e6b4b18036a471400bc4e6c753f229d7"
		v041DockerPin  = "5c8b312cd7c8493966d28c118834d4e9565c7c62"
		v042DockerPin  = "7f2dcf2dbfb24af68c4c83a6da94dd5d885e58b8"
		v013WolfiPin   = "3338120927f8e291c4780de691ef63a7c9d825c0"
	)

	// sample dagger.json files to use simulating initial setup
	// we pin the dep to a random commit and then verify
	// that the update actually changes the pin
	noDeps := `{
		"name": "foo",
		"sdk": "go"
	}`

	// pin a random old commit to verify pin is changed
	depHasOldVersion := `{
		"name": "foo",
		"sdk": "go",
		"dependencies": [
			{
				"name": "docker",
				"source": "github.com/shykes/daggerverse/docker@docker/v0.4.1",
				"pin": "` + randomMainPin + `"
			}
		]
	}`

	depHasBranch := `{
		"name": "foo",
		"sdk": "go",
		"dependencies": [
			{
				"name": "docker",
				"source": "github.com/shykes/daggerverse/docker@main",
				"pin": "` + randomMainPin + `"
			}
		]
	}`

	depIsLocal := `{
		"name": "foo",
		"sdk": "go",
		"dependencies": [
			{
				"name": "bar",
				"source": "./bar"
			}
		]
	}`

	depHasNoVersion := `{
		"name": "foo",
		"sdk": "go",
		"dependencies": [
			{
				"name": "docker",
				"source": "github.com/shykes/daggerverse/docker",
				"pin": "` + randomMainPin + `"
			}
		]
	}`

	multipleDeps := `{
		"name": "foo",
		"sdk": "go",
		"dependencies": [
			{
				"name": "docker",
				"source": "github.com/shykes/daggerverse/docker@v0.4.1",
				"pin": "` + randomMainPin + `"
			},
			{
				"name": "wolfi",
				"source": "github.com/shykes/daggerverse/wolfi@v0.1.3",
				"pin": "` + randomWolfiPin + `"
			}
		]
	}`

	testcases := []struct {
		name          string
		daggerjson    string
		updateCmd     []string
		contains      []string
		notContains   []string
		expectedError string
	}{
		{
			name:        "existing dep has version, update cmd has version",
			daggerjson:  depHasOldVersion,
			updateCmd:   []string{"update", "github.com/shykes/daggerverse/docker@v0.4.2"},
			contains:    []string{`"github.com/shykes/daggerverse/docker@docker/v0.4.2"`, v042DockerPin},
			notContains: []string{`github.com/shykes/daggerverse/docker@docker/v0.4.1`},
		},
		{
			name:        "existing dep has branch, update cmd has version",
			daggerjson:  depHasBranch,
			updateCmd:   []string{"update", "github.com/shykes/daggerverse/docker@v0.4.2"},
			contains:    []string{`"github.com/shykes/daggerverse/docker@docker/v0.4.2"`, v042DockerPin},
			notContains: []string{`github.com/shykes/daggerverse/docker@main`, randomMainPin},
		},
		{
			name:        "existing dep dont have version, update cmd has version",
			daggerjson:  depHasNoVersion,
			updateCmd:   []string{"update", "github.com/shykes/daggerverse/docker@v0.4.2"},
			contains:    []string{`github.com/shykes/daggerverse/docker@docker/v0.4.2`, v042DockerPin},
			notContains: []string{`"github.com/shykes/daggerverse/docker"`},
		},
		{
			name:        "existing dep dont have version, update cmd dont have version",
			daggerjson:  depHasNoVersion,
			updateCmd:   []string{"update", "github.com/shykes/daggerverse/docker"},
			notContains: []string{randomMainPin},
		},
		{
			name:        "existing dep use branch, update cmd dont have version",
			daggerjson:  depHasBranch,
			updateCmd:   []string{"update", "github.com/shykes/daggerverse/docker"},
			contains:    []string{`"github.com/shykes/daggerverse/docker@main`},
			notContains: []string{`"github.com/shykes/daggerverse/docker"`, randomMainPin},
		},
		{
			name:        "existing dep have version, update cmd dont have version",
			daggerjson:  depHasOldVersion,
			updateCmd:   []string{"update", "github.com/shykes/daggerverse/docker"},
			contains:    []string{`github.com/shykes/daggerverse/docker@docker/v0.4.1`, v041DockerPin},
			notContains: []string{`"github.com/shykes/daggerverse/docker"`},
		},
		{
			name:        "existing dep dont have version, update cmd use name without version",
			daggerjson:  depHasNoVersion,
			updateCmd:   []string{"update", "docker"},
			contains:    []string{`"github.com/shykes/daggerverse/docker@main"`},
			notContains: []string{randomMainPin},
		},
		{
			name:        "existing dep use branch, update cmd use name without version",
			daggerjson:  depHasBranch,
			updateCmd:   []string{"update", "docker"},
			contains:    []string{`"github.com/shykes/daggerverse/docker@main`},
			notContains: []string{`"github.com/shykes/daggerverse/docker"`, randomMainPin},
		},
		{
			name:        "existing dep have version, update cmd use name without version",
			daggerjson:  depHasOldVersion,
			updateCmd:   []string{"update", "docker"},
			contains:    []string{`github.com/shykes/daggerverse/docker@docker/v0.4.1`, v041DockerPin},
			notContains: []string{`"github.com/shykes/daggerverse/docker"`},
		},
		{
			name:       "existing dep dont have version, update cmd use name with version",
			daggerjson: depHasNoVersion,
			updateCmd:  []string{"update", "docker@v0.4.2"},
			contains:   []string{`"github.com/shykes/daggerverse/docker@docker/v0.4.2"`, v042DockerPin},
		},
		{
			name:        "existing dep use branch, update cmd use name with version",
			daggerjson:  depHasBranch,
			updateCmd:   []string{"update", "docker@v0.4.2"},
			contains:    []string{`"github.com/shykes/daggerverse/docker@docker/v0.4.2`, v042DockerPin},
			notContains: []string{`"github.com/shykes/daggerverse/docker@main"`},
		},
		{
			name:        "existing dep have version, update cmd use name with version",
			daggerjson:  depHasOldVersion,
			updateCmd:   []string{"update", "docker@v0.4.2"},
			contains:    []string{`github.com/shykes/daggerverse/docker@docker/v0.4.2`, v042DockerPin},
			notContains: []string{`"github.com/shykes/daggerverse/docker@docker/v0.4.1"`, v041DockerPin},
		},
		{
			name:          "update a dependency not configured in dagger.json",
			daggerjson:    noDeps,
			updateCmd:     []string{"update", "github.com/shykes/daggerverse/docker@v0.4.2"},
			expectedError: `dependency "github.com/shykes/daggerverse/docker" was requested to be updated, but it is not found in the dependencies list`,
		},
		{
			name:       "can update all dependencies",
			daggerjson: multipleDeps,
			updateCmd:  []string{"update"},
			contains:   []string{`"github.com/shykes/daggerverse/docker@docker/v0.4.1"`, v041DockerPin, `"github.com/shykes/daggerverse/wolfi@wolfi/v0.1.3"`, v013WolfiPin},
		},
		{
			name:          "cannot update a local dependency",
			daggerjson:    depIsLocal,
			updateCmd:     []string{"update", "bar"},
			expectedError: `updating local dependencies is not supported`,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := c.Container().
				From("alpine:latest").
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work").
				With(daggerExec("init", "--sdk=go", "--name=foo", "--source=.")).
				WithWorkdir("bar").
				With(daggerExec("init", "--sdk=go", "--name=bar", "--source=.")).
				WithWorkdir("/work").
				WithNewFile("dagger.json", tc.daggerjson)

			daggerjson, err := ctr.
				With(daggerExec(tc.updateCmd...)).
				File("dagger.json").
				Contents(ctx)

			if tc.expectedError != "" {
				requireErrOut(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				for _, s := range tc.contains {
					require.Contains(t, daggerjson, s)
				}

				for _, s := range tc.notContains {
					require.NotContains(t, daggerjson, s)
				}
			}
		})
	}
}

func (CLISuite) TestInvalidModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("normal context dir", func(ctx context.Context, t *testctx.T) {
		modGen := goGitBase(t, c).
			WithNewFile("dagger.json", `{"name": "broke", "engineVersion": "v100.0.0", "sdk": 666}`)

		_, err := modGen.With(daggerQuery(`{version}`)).Stdout(ctx)
		requireErrOut(t, err, `failed to check if module exists`)
	})

	t.Run("fallback context dir", func(ctx context.Context, t *testctx.T) {
		modGen := daggerCliBase(t, c).
			WithNewFile("dagger.json", `{"name": "broke", "engineVersion": "v100.0.0", "sdk": 666}`)

		_, err := modGen.With(daggerQuery(`{version}`)).Stdout(ctx)
		requireErrOut(t, err, `failed to check if module exists`)
	})
}

func (CLISuite) TestNoSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	helloCode := `package main

// A Dagger module to say hello to the world!
type Hello struct{}

// Hello prints out a greeting
func (m *Hello) Hello() string {
	return "hi"
}
`

	// Set up the base container with Dagger CLI
	base := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work")

	// Create the nested module structure and initialize modules
	testCtr := base.
		// Initialize 'hello' module with Go SDK
		WithWorkdir("/work/test/nosdk/hello").
		With(daggerExec("init", "--sdk=go", "--name=hello")).
		WithNewFile("main.go", helloCode).
		// Initialize 'nosdk' module and install 'hello'
		WithWorkdir("/work/test/nosdk").
		With(daggerExec("init", "--name=nosdk")).
		With(daggerExec("install", "./hello")).
		// Initialize 'test' module and install 'nosdk'
		WithWorkdir("/work/test").
		With(daggerExec("init", "--name=test")).
		With(daggerExec("install", "./nosdk"))

	// Verify that the top-level 'test' module has no SDK
	daggerJSON, err := testCtr.File("dagger.json").Contents(ctx)
	require.NoError(t, err)
	require.NotContains(t, daggerJSON, `"sdk"`)

	t.Run("shell help does not segfault and stdlib functions are shown", func(ctx context.Context, t *testctx.T) {
		out, err := testCtr.With(daggerShell(".help")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "container") // stdlib
		require.Contains(t, out, "directory") // stdlib
		require.Contains(t, out, "Use \".help <command> | <function>\" for more information.")
	})

	t.Run("core types are still available and working", func(ctx context.Context, t *testctx.T) {
		out, err := testCtr.With(daggerShell("container | from alpine | with-exec echo Hello | stdout")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "Hello\n", out)
	})

	t.Run("functions with no SDK show just the headers", func(ctx context.Context, t *testctx.T) {
		out, err := testCtr.With(daggerExec("functions")).Stdout(ctx)
		require.NoError(t, err)

		// Split output into lines and verify it's empty except for headers
		lines := strings.Split(strings.TrimSpace(out), "\n")
		require.LessOrEqual(t, len(lines), 2, "Should only show headers or be empty")

		// If there are lines, they should only be "Name, Description" header
		if len(lines) > 0 {
			for _, line := range lines {
				// Skip empty lines
				if strings.TrimSpace(line) == "" {
					continue
				}
				require.Contains(t, line, "Name", "Should only contain header")
				require.Contains(t, line, "Description", "Should only contain header")
			}
		}
	})

	t.Run("call a module without sdk", func(ctx context.Context, t *testctx.T) {
		_, err := testCtr.WithWorkdir("/work/test/nosdk").With(daggerExec("call")).Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("no-sdk module can load module with sdk", func(ctx context.Context, t *testctx.T) {
		out, err := testCtr.WithWorkdir("/work/test/nosdk").With(daggerShell("hello | hello")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi", out)
	})
}
