package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type CLISuite struct{}

func TestCLI(t *testing.T) {
	testctx.Run(testCtx, t, CLISuite{}, Middleware()...)
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
				sourceDirEnt: "src",
			},
			{
				sdk:          "typescript",
				sourceDirEnt: "src",
			},
		} {
			tc := tc
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
		require.ErrorContains(t, err, "source subdir path \"../..\" escapes context")
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

	t.Run("do not boostrap LICENSE file if license is set empty", func(ctx context.Context, t *testctx.T) {
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

		t.Run("boostrap custom LICENSE file if sdk and license are specified", func(ctx context.Context, t *testctx.T) {
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
		tc := tc
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
				require.ErrorContains(t, err, "no such file or directory")
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
			wd := wd
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
				require.ErrorContains(t, err, `cannot update module SDK that has already been set to "go"`)

				_, err = ctr.With(daggerExec("develop", "--source", "blahblahblaha/blah")).Sync(ctx)
				require.ErrorContains(t, err, `cannot update module source path that has already been set to "cool/subdir"`)
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
			mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
			defer cleanup()

			_, err := goGitBase(t, c).
				With(mountedSocket).
				With(daggerExec("develop", "-m", testGitModuleRef(tc, "top-level"))).
				Sync(ctx)
			require.ErrorContains(t, err, `module must be local`)
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
			require.ErrorContains(t, err, `local module dep source path "../play/dep" escapes context "/work"`)
		})

		t.Run("from src dir with absolute path", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/work/test").
				With(daggerExec("install", "/play/dep")).
				Sync(ctx)
			require.ErrorContains(t, err, `local module dep source path "../play/dep" escapes context "/work"`)
		})

		t.Run("from dep dir", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/play/dep").
				With(daggerExec("install", "-m=../../work/test", ".")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path "../play/dep" escapes context "/work"`)
		})

		t.Run("from dep dir with absolute path", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/play/dep").
				With(daggerExec("install", "-m=/work/test", ".")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path "../play/dep" escapes context "/work"`)
		})

		t.Run("from root", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/").
				With(daggerExec("install", "-m=work/test", "play/dep")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path "../play/dep" escapes context "/work"`)
		})

		t.Run("from root with absolute path", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/").
				With(daggerExec("install", "-m=/work/test", "play/dep")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path "../play/dep" escapes context "/work"`)
		})
	})

	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		t.Run("git", func(ctx context.Context, t *testctx.T) {
			t.Run("happy", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
				defer cleanup()

				out, err := goGitBase(t, c).
					With(mountedSocket).
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
				mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
				defer cleanup()

				_, err := goGitBase(t, c).
					With(mountedSocket).
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerExec("install", testGitModuleRef(tc, "../../"))).
					Sync(ctx)
				require.ErrorContains(t, err, `git module source subpath points out of root: "../.."`)

				_, err = goGitBase(t, c).
					With(mountedSocket).
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerExec("install", testGitModuleRef(tc, "this/just/does/not/exist"))).
					Sync(ctx)
				require.ErrorContains(t, err, `module "test" dependency "" with source root path "this/just/does/not/exist" does not exist or does not have a configuration file`)
			})

			t.Run("unpinned gets pinned", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
				defer cleanup()

				out, err := goGitBase(t, c).
					With(mountedSocket).
					WithWorkdir("/work").
					With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerExec("install", tc.gitTestRepoRef)).
					File("/work/dagger.json").
					Contents(ctx)
				require.NoError(t, err)
				var modCfg modules.ModuleConfig
				require.NoError(t, json.Unmarshal([]byte(out), &modCfg))
				require.Len(t, modCfg.Dependencies, 1)
				require.Equal(t, tc.gitTestRepoRef, modCfg.Dependencies[0].Source)
				require.NotEmpty(t, modCfg.Dependencies[0].Pin)
			})
		})
	})
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
		require.Contains(t, lines, "as-tarball                    Returns a File representing the container serialized to a tarball.")
	})

	t.Run("return primitive", func(ctx context.Context, t *testctx.T) {
		_, err := ctr.With(daggerFunctions("prim")).Stdout(ctx)
		require.ErrorContains(t, err, `function "prim" returns type "STRING_KIND" with no further functions available`)
	})

	t.Run("alt casing", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(daggerFunctions("fnA")).Stdout(ctx)
		require.NoError(t, err)
		lines := strings.Split(out, "\n")
		// just verify some of the container funcs are there, too many to be exhaustive
		require.Contains(t, lines, "file                          Retrieves a file at the given path.")
		require.Contains(t, lines, "as-tarball                    Returns a File representing the container serialized to a tarball.")
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
}
