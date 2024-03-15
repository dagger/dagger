package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
	"github.com/stretchr/testify/require"
)

func TestModuleConfigs(t *testing.T) {
	// test dagger.json source configs that aren't inherently covered in other tests

	t.Parallel()
	t.Run("upgrade from old config", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		baseWithOldConfig := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/foo").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main
			type Test struct {}

			func (m *Test) Fn() string { return "wowzas" }
			`,
			}).
			WithNewFile("/work/dagger.json", dagger.ContainerWithNewFileOpts{
				Contents: `{"name": "test", "sdk": "go", "include": ["foo"], "exclude": ["blah"], "dependencies": ["foo"]}`,
			})

		// verify develop updates config to new format
		baseWithNewConfig := baseWithOldConfig.With(daggerExec("develop"))
		confContents, err := baseWithNewConfig.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		var modCfg modules.ModuleConfig
		require.NoError(t, json.Unmarshal([]byte(confContents), &modCfg))
		require.Equal(t, "test", modCfg.Name)
		require.Equal(t, "go", modCfg.SDK)
		require.Equal(t, []string{"!blah", "foo"}, modCfg.Include)
		require.Empty(t, modCfg.Exclude)
		require.Len(t, modCfg.Dependencies, 1)
		require.Equal(t, "foo", modCfg.Dependencies[0].Source)
		require.Equal(t, "dep", modCfg.Dependencies[0].Name)
		require.Equal(t, ".", modCfg.Source)
		require.NotEmpty(t, modCfg.EngineVersion) // version changes with any engine change

		// verify develop didn't overwrite main.go
		out, err := baseWithNewConfig.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "wowzas", strings.TrimSpace(out))

		// verify call works seamlessly even without explicit sync yet
		out, err = baseWithOldConfig.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "wowzas", strings.TrimSpace(out))
	})

	t.Run("malicious config", func(t *testing.T) {
		// verify a maliciously/incorrectly constructed dagger.json is still handled correctly
		t.Parallel()
		c, ctx := connect(t)

		base := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) GetSource(ctx context.Context) *Directory { 
				return dag.CurrentModule().Source()
			}
			`,
			}).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go"))

		t.Run("source points out of root", func(t *testing.T) {
			t.Parallel()

			t.Run("local", func(t *testing.T) {
				t.Parallel()
				base := base.
					With(configFile(".", &modules.ModuleConfig{
						Name:   "evil",
						SDK:    "go",
						Source: "..",
					}))

				_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				require.ErrorContains(t, err, `local module source path ".." escapes context "/work"`)

				_, err = base.With(daggerExec("develop")).Sync(ctx)
				require.ErrorContains(t, err, `local module source path ".." escapes context "/work"`)

				_, err = base.With(daggerExec("install", "./dep")).Sync(ctx)
				require.ErrorContains(t, err, `local module source path ".." escapes context "/work"`)
			})

			t.Run("git", func(t *testing.T) {
				t.Parallel()
				_, err := base.With(daggerCallAt(testGitModuleRef("invalid/bad-source"), "container-echo", "--string-arg", "plz fail")).Sync(ctx)
				require.ErrorContains(t, err, `source path "../../../" contains parent directory components`)
			})
		})

		t.Run("dep points out of root", func(t *testing.T) {
			t.Parallel()

			t.Run("local", func(t *testing.T) {
				t.Parallel()
				base := base.
					With(configFile(".", &modules.ModuleConfig{
						Name: "evil",
						SDK:  "go",
						Dependencies: []*modules.ModuleConfigDependency{{
							Name:   "escape",
							Source: "..",
						}},
					}))

				_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				require.ErrorContains(t, err, `local module dep source path ".." escapes context "/work"`)

				_, err = base.With(daggerExec("develop")).Sync(ctx)
				require.ErrorContains(t, err, `local module dep source path ".." escapes context "/work"`)

				_, err = base.With(daggerExec("install", "./dep")).Sync(ctx)
				require.ErrorContains(t, err, `local module dep source path ".." escapes context "/work"`)

				base = base.
					With(configFile(".", &modules.ModuleConfig{
						Name: "evil",
						SDK:  "go",
						Dependencies: []*modules.ModuleConfigDependency{{
							Name:   "escape",
							Source: "../work/dep",
						}},
					}))

				_, err = base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				require.ErrorContains(t, err, `module dep source root path "../work/dep" escapes root`)

				_, err = base.With(daggerExec("develop")).Sync(ctx)
				require.ErrorContains(t, err, `module dep source root path "../work/dep" escapes root`)

				_, err = base.With(daggerExec("install", "./dep")).Sync(ctx)
				require.ErrorContains(t, err, `module dep source root path "../work/dep" escapes root`)
			})

			t.Run("git", func(t *testing.T) {
				t.Parallel()
				_, err := base.With(daggerCallAt(testGitModuleRef("invalid/bad-dep"), "container-echo", "--string-arg", "plz fail")).Sync(ctx)
				require.ErrorContains(t, err, `module dep source root path "../../../foo" escapes root`)
			})
		})
	})
}

func TestModuleCustomDepNames(t *testing.T) {
	t.Parallel()

	t.Run("basic", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)
		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context) string { 
				return "hi from dep"
			}

			func (m *Dep) GetDepObj(ctx context.Context) *DepObj {
				return &DepObj{Str: "yo from dep"}
			}

			type DepObj struct {
				Str string
			}

			func (m *Dep) GetOtherObj(ctx context.Context) *OtherObj {
				return &OtherObj{Str: "hey from dep"}
			}

			type OtherObj struct {
				Str string
			}
			`,
			}).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", "--name", "foo", "./dep")).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) { 
				return dag.Foo().DepFn(ctx)
			}

			func (m *Test) GetObj(ctx context.Context) (string, error) { 
				var obj *FooObj
				obj = dag.Foo().GetDepObj()
				return obj.Str(ctx)
			}

			func (m *Test) GetOtherObj(ctx context.Context) (string, error) { 
				var obj *FooOtherObj
				obj = dag.Foo().GetOtherObj()
				return obj.Str(ctx)
			}

			func (m *Test) GetConflictNameObj(ctx context.Context) *Dep {
				return &Dep{Str: "it worked?"}
			}

			// should not be any name conflict with dep
			type Dep struct {
				Str string
			}
			`,
			})

		out, err := ctr.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep", strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("get-obj")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo from dep", strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("get-other-obj")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey from dep", strings.TrimSpace(out))

		out, err = ctr.With(daggerCall("get-conflict-name-obj", "str")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "it worked?", strings.TrimSpace(out))
	})

	t.Run("same mod name as dep", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) string { 
				return "hi from dep"
			}
			`,
			}).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", "--name", "foo", "./dep")).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) { 
				return dag.Foo().Fn(ctx)
			}
			`,
			})

		out, err := ctr.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep", strings.TrimSpace(out))
	})

	t.Run("two deps with same name", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep1").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep1/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) Fn(ctx context.Context) string { 
				return "hi from dep1"
			}
			`,
			}).
			WithWorkdir("/work/dep2").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep2/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) Fn(ctx context.Context) string { 
				return "hi from dep2"
			}
			`,
			}).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", "--name", "foo", "./dep1")).
			With(daggerExec("install", "--name", "bar", "./dep2")).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) { 
				dep1, err := dag.Foo().Fn(ctx)
				if err != nil {
					return "", err
				}
				dep2, err := dag.Bar().Fn(ctx)
				if err != nil {
					return "", err
				}
				return dep1 + " " + dep2, nil
			}
			`,
			})

		out, err := ctr.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep1 hi from dep2", strings.TrimSpace(out))
	})
}

func TestModuleDaggerInit(t *testing.T) {
	t.Parallel()
	t.Run("name defaults to source root dir name", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)
		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=coolmod", "--sdk=go", "coolmod")).
			WithNewFile("/work/coolmod/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Coolmod struct {}

			func (m *Coolmod) Fn(ctx context.Context) (string, error) {
				return dag.CurrentModule().Name(ctx)
			}
			`,
			}).
			With(daggerCallAt("coolmod", "fn")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "coolmod", strings.TrimSpace(out))
	})

	t.Run("source dir default", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)
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
			t.Run(tc.sdk, func(t *testing.T) {
				t.Parallel()
				srcRootDir := ctr.
					With(daggerExec("init", "--name=test", "--sdk="+tc.sdk)).
					Directory(".")
				srcRootEnts, err := srcRootDir.Entries(ctx)
				require.NoError(t, err)
				require.Contains(t, srcRootEnts, "dagger.json")
				require.NotContains(t, srcRootEnts, tc.sourceDirEnt)
				srcDirEnts, err := srcRootDir.Directory("dagger").Entries(ctx)
				require.NoError(t, err)
				require.Contains(t, srcDirEnts, tc.sourceDirEnt)
			})
		}
	})

	t.Run("source is made rel to source root by engine", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

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

	t.Run("works inside subdir of other module", func(t *testing.T) {
		// verifies find-up logic does NOT kick in here
		t.Parallel()
		c, ctx := connect(t)

		ctr := goGitBase(t, c).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=a", "--sdk=go", ".")).
			WithWorkdir("/work/subdir").
			With(daggerExec("init", "--name=b", "--sdk=go", "--source=.", ".")).
			WithNewFile("./main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			type B struct {}

			func (m *B) Fn() string { return "yo" }
			`,
			}).
			With(daggerCall("fn"))
		out, err := ctr.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "yo", strings.TrimSpace(out))
	})
}

func TestModuleDaggerDevelop(t *testing.T) {
	t.Parallel()

	t.Run("name and sdk", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) Fn(ctx context.Context) string { 
				return "hi from dep"
			}
			`,
			}).
			WithWorkdir("/work").
			With(daggerExec("init")).
			With(daggerExec("install", "./dep"))

		// should be able to invoke dep without name+sdk set yet
		out, err := base.With(daggerCallAt("dep", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep", strings.TrimSpace(out))

		// test develop from source root and from subdir (in which case find-up should kick in)
		for _, wd := range []string{"/work", "/work/from/some/otherdir"} {
			wd := wd
			t.Run(wd, func(t *testing.T) {
				t.Parallel()

				sourceDir, err := filepath.Rel(wd, "/work/cool/subdir")
				require.NoError(t, err)

				ctr := base.
					WithWorkdir(wd).
					With(daggerExec("develop", "--sdk", "go", "--source", sourceDir)).
					WithNewFile("/work/cool/subdir/main.go", dagger.ContainerWithNewFileOpts{
						Contents: `package main

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
					})

				// should be able to invoke it directly now
				out, err = ctr.With(daggerCall("fn")).Stdout(ctx)
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

	t.Run("source is made rel to source root by engine", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		ctr := goGitBase(t, c).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) Fn(ctx context.Context) string { 
				return "hi from dep"
			}
			`,
			}).
			WithWorkdir("/work").
			With(daggerExec("init")).
			With(daggerExec("install", "./dep")).
			WithWorkdir("/var").
			With(daggerExec("develop", "-m", "../work", "--source=../work/some/subdir", "--sdk=go")).
			WithNewFile("/work/some/subdir/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

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
			})

		out, err := ctr.With(daggerCallAt("../work", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from work hi from dep", strings.TrimSpace(out))

		ents, err := ctr.Directory("/work/some/subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, ents, "main.go")
	})

	t.Run("fails on git", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		_, err := goGitBase(t, c).
			With(daggerExec("develop", "-m", testGitModuleRef("top-level"))).
			Sync(ctx)
		require.ErrorContains(t, err, `module must be local`)
	})
}

func TestModuleDaggerInstall(t *testing.T) {
	t.Parallel()

	t.Run("local", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		base := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/subdir/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/subdir/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			}).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=test", "--name=test", "--sdk=go", "test")).
			With(daggerExec("install", "-m=test", "./subdir/dep")).
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

	t.Run("install dep from various places", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		base := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=subdir/dep", "--name=dep", "--sdk=go", "subdir/dep")).
			WithNewFile("/work/subdir/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			}).
			With(daggerExec("init", "--source=test", "--name=test", "--sdk=go", "test")).
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
				With(daggerExec("install", "../subdir/dep")).
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from src subdir with findup", func(t *testing.T) {
			t.Parallel()
			out, err := base.
				WithWorkdir("/work/test/some/other/dir").
				With(daggerExec("install", "../../../../subdir/dep")).
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from root", func(t *testing.T) {
			t.Parallel()
			out, err := base.
				WithWorkdir("/").
				With(daggerExec("install", "-m=./work/test", "./work/subdir/dep")).
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
				With(daggerExec("install", "-m=../../test", ".")).
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
				With(daggerExec("install", "-m=../work/test", "../work/subdir/dep")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})
	})

	t.Run("install out of tree dep fails", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		base := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/play/dep").
			With(daggerExec("init", "--name=dep", "--sdk=go")).
			WithWorkdir("/work/test").
			With(daggerExec("init", "--name=test", "--sdk=go"))

		t.Run("from src dir", func(t *testing.T) {
			t.Parallel()
			_, err := base.
				WithWorkdir("/work/test").
				With(daggerExec("install", "../../play/dep")).
				Sync(ctx)
			require.ErrorContains(t, err, `local module dep source path "../play/dep" escapes context "/work"`)
		})

		t.Run("from dep dir", func(t *testing.T) {
			t.Parallel()
			_, err := base.
				WithWorkdir("/play/dep").
				With(daggerExec("install", "-m=../../work/test", ".")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path "../play/dep" escapes context "/work"`)
		})

		t.Run("from root", func(t *testing.T) {
			t.Parallel()
			_, err := base.
				WithWorkdir("/").
				With(daggerExec("install", "-m=work/test", "play/dep")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path "../play/dep" escapes context "/work"`)
		})
	})

	t.Run("git", func(t *testing.T) {
		t.Parallel()
		t.Run("happy", func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			out, err := goGitBase(t, c).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", testGitModuleRef("top-level"))).
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

import "context"

type Test struct {}

func (m *Test) Fn(ctx context.Context) (string, error) {
	return dag.TopLevel().Fn(ctx)
}
`,
				}).
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi from top level hi from dep hi from dep2", strings.TrimSpace(out))
		})

		t.Run("sad", func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			_, err := goGitBase(t, c).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", testGitModuleRef("../../"))).
				Sync(ctx)
			require.ErrorContains(t, err, `git module source subpath points out of root: "../.."`)

			_, err = goGitBase(t, c).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", testGitModuleRef("this/just/does/not/exist"))).
				Sync(ctx)
			require.ErrorContains(t, err, `module "test" dependency "" with source root path "this/just/does/not/exist" does not exist or does not have a configuration file`)
		})

		t.Run("unpinned gets pinned", func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			out, err := goGitBase(t, c).
				WithWorkdir("/work").
				With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
				With(daggerExec("install", gitTestRepoURL)).
				File("/work/dagger.json").
				Contents(ctx)
			require.NoError(t, err)
			var modCfg modules.ModuleConfig
			require.NoError(t, json.Unmarshal([]byte(out), &modCfg))
			require.Len(t, modCfg.Dependencies, 1)
			url, commit, ok := strings.Cut(modCfg.Dependencies[0].Source, "@")
			require.True(t, ok)
			require.Equal(t, gitTestRepoURL, url)
			require.NotEmpty(t, commit)
		})
	})
}

// test the `dagger config` command
func TestModuleDaggerConfig(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go", "test"))

	for _, tc := range []struct {
		name       string
		workdir    string
		modFlagVal string
	}{
		{
			name:    "from source root",
			workdir: "/work/test",
		},
		{
			name:       "from source root parent",
			workdir:    "/work",
			modFlagVal: "test",
		},
		{
			// find-up should work
			name:    "from subdir",
			workdir: "/work/test/some/subdir",
		},
		{
			// not sure why anyone would do this, but it should work
			name:       "from subdir with mod flag",
			workdir:    "/work/test/some/subdir",
			modFlagVal: "..",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, err := ctr.
				WithWorkdir(tc.workdir).
				With(daggerExec("config", "-m", tc.modFlagVal)).
				Stdout(ctx)
			require.NoError(t, err)
			require.Regexp(t, `Name:\s+test`, out)
			require.Regexp(t, `SDK:\s+go`, out)
			require.Regexp(t, `Root Directory:\s+/work`, out)
			require.Regexp(t, `Source Directory:\s+/work/test`, out)
		})
	}
}

func TestModuleIncludeExclude(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		sdk                    string
		mainSource             string
		customSDKSource        string
		customSDKUnderlyingSDK string
	}{
		{
			sdk: "go",
			mainSource: `package main
type Test struct {}

func (m *Test) Fn() *Directory {
	return dag.CurrentModule().Source()
}
			`,
		},
		{
			sdk: "python",
			mainSource: `import dagger
from dagger import dag, function, object_type

@object_type
class Test:
    @function
    def fn(self) -> dagger.Directory:
        return dag.current_module().source()
`,
		},
		{
			sdk: "typescript",
			mainSource: `
import { dag, Directory, object, func } from "@dagger.io/dagger"

@object()
class Test {
  @func()
  fn(): Directory {
    return dag.currentModule().source()
  }
}`,
		},
		{
			sdk: "coolsdk",
			mainSource: `package main
type Test struct {}

func (m *Test) Fn() *Directory {
	return dag.CurrentModule().Source()
}
`,
			customSDKUnderlyingSDK: "go",
			customSDKSource: `package main

type Coolsdk struct {}

func (m *Coolsdk) ModuleRuntime(modSource *ModuleSource, introspectionJson string) *Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *Coolsdk) Codegen(modSource *ModuleSource, introspectionJson string) *GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("go").AsModule().GeneratedContextDirectory())
}

func (m *Coolsdk) RequiredPaths() []string {
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
		},
	} {
		tc := tc
		t.Run(tc.sdk, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)

			ctr := goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work")

			// special case custom sdk
			if tc.customSDKSource != "" {
				ctr = ctr.
					WithWorkdir("/work/" + tc.sdk).
					With(daggerExec("init", "--name="+tc.sdk, "--sdk="+tc.customSDKUnderlyingSDK)).
					With(sdkSource(tc.customSDKUnderlyingSDK, tc.customSDKSource)).
					WithWorkdir("/work")
			}

			ctr = ctr.With(daggerExec("init", "--name=test", "--sdk="+tc.sdk))

			if tc.customSDKSource != "" {
				// TODO: hardcoding that underlying sdk is go right now, could be generalized
				ctr = ctr.WithNewFile("dagger/main.go", dagger.ContainerWithNewFileOpts{
					Contents: tc.mainSource,
				})
			} else {
				ctr = ctr.With(sdkSource(tc.sdk, tc.mainSource))
			}

			ctr = ctr.With(daggerExec("config", "include", "set",
				"dagger/subdir/keepdir",
				"!dagger/subdir/keepdir/rmdir",
			)).WithDirectory("dagger/subdir/keepdir/rmdir", c.Directory())

			// call should work even though dagger.json and main source files weren't
			// explicitly included
			out, err := ctr.
				With(daggerCall("fn", "directory", "--path", "subdir", "entries")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "keepdir", strings.TrimSpace(out))

			out, err = ctr.
				With(daggerCall("fn", "directory", "--path", "subdir/keepdir", "entries")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "", strings.TrimSpace(out))

			// call should also work from other directories
			out, err = ctr.
				WithWorkdir("/mnt").
				With(daggerCallAt("../work", "fn", "directory", "--path", "subdir", "entries")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "keepdir", strings.TrimSpace(out))

			// call should still work after develop
			ctr = ctr.With(daggerExec("develop"))

			out, err = ctr.
				With(daggerCall("fn", "directory", "--path", "subdir", "entries")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "keepdir", strings.TrimSpace(out))
			out, err = ctr.
				With(daggerCall("fn", "directory", "--path", "subdir/keepdir", "entries")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "", strings.TrimSpace(out))
		})
	}
}

// verify that if there is no local .git in parent dirs then the context defaults to the source root
func TestModuleContextDefaultsToSourceRoot(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/coolsdk").
		With(daggerExec("init", "--source=.", "--name=cool-sdk", "--sdk=go")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type CoolSdk struct {}

func (m *CoolSdk) ModuleRuntime(modSource *ModuleSource, introspectionJson string) *Container {
	return modSource.WithSDK("go").AsModule().Runtime().
		WithMountedDirectory("/da-context", modSource.ContextDirectory())
}

func (m *CoolSdk) Codegen(modSource *ModuleSource, introspectionJson string) *GeneratedCode {
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
		WithNewFile("random-file").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=coolsdk")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

import "os"

type Test struct {}

func (m *Test) Fn() ([]string, error) {
	ents, err := os.ReadDir("/da-context")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ent := range ents {
		names = append(names, ent.Name())
	}
	return names, nil
}
`,
		})

	out, err := ctr.
		With(daggerCall("fn")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "random-file")
}

const (
	gitTestRepoURL    = "github.com/dagger/dagger-test-modules"
	gitTestRepoCommit = "d7299e935a195f3e1a29bc39537ed270f4f378d5"
)

func testGitModuleRef(subpath string) string {
	url := gitTestRepoURL
	if subpath != "" {
		if !strings.HasPrefix(subpath, "/") {
			subpath = "/" + subpath
		}
		url += subpath
	}
	return fmt.Sprintf("%s@%s", url, gitTestRepoCommit)
}

func TestModuleDaggerGitRefs(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	t.Run("root module", func(t *testing.T) {
		t.Parallel()
		rootModSrc := c.ModuleSource(testGitModuleRef(""))

		htmlURL, err := rootModSrc.AsGitSource().HTMLURL(ctx)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("https://%s/tree/%s", gitTestRepoURL, gitTestRepoCommit), htmlURL)
		resp, err := http.Get(htmlURL)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		cloneURL, err := rootModSrc.AsGitSource().CloneURL(ctx)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("https://%s", gitTestRepoURL), cloneURL)

		commit, err := rootModSrc.AsGitSource().Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, gitTestRepoCommit, commit)

		refStr, err := rootModSrc.AsString(ctx)
		require.NoError(t, err)
		require.Equal(t, testGitModuleRef(""), refStr)
	})

	t.Run("top-level module", func(t *testing.T) {
		t.Parallel()
		topLevelModSrc := c.ModuleSource(testGitModuleRef("top-level"))
		htmlURL, err := topLevelModSrc.AsGitSource().HTMLURL(ctx)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("https://%s/tree/%s/top-level", gitTestRepoURL, gitTestRepoCommit), htmlURL)
		resp, err := http.Get(htmlURL)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		cloneURL, err := topLevelModSrc.AsGitSource().CloneURL(ctx)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("https://%s", gitTestRepoURL), cloneURL)

		commit, err := topLevelModSrc.AsGitSource().Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, gitTestRepoCommit, commit)

		refStr, err := topLevelModSrc.AsString(ctx)
		require.NoError(t, err)
		require.Equal(t, testGitModuleRef("top-level"), refStr)
	})

	t.Run("subdir dep2 module", func(t *testing.T) {
		t.Parallel()
		subdirDepModSrc := c.ModuleSource(testGitModuleRef("subdir/dep2"))
		htmlURL, err := subdirDepModSrc.AsGitSource().HTMLURL(ctx)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("https://%s/tree/%s/subdir/dep2", gitTestRepoURL, gitTestRepoCommit), htmlURL)
		resp, err := http.Get(htmlURL)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		cloneURL, err := subdirDepModSrc.AsGitSource().CloneURL(ctx)
		require.NoError(t, err)
		require.Equal(t, fmt.Sprintf("https://%s", gitTestRepoURL), cloneURL)

		commit, err := subdirDepModSrc.AsGitSource().Commit(ctx)
		require.NoError(t, err)
		require.Equal(t, gitTestRepoCommit, commit)

		refStr, err := subdirDepModSrc.AsString(ctx)
		require.NoError(t, err)
		require.Equal(t, testGitModuleRef("subdir/dep2"), refStr)
	})

	t.Run("stable arg", func(t *testing.T) {
		t.Parallel()

		_, err := c.ModuleSource(gitTestRepoURL, dagger.ModuleSourceOpts{
			Stable: true,
		}).AsString(ctx)
		require.ErrorContains(t, err, fmt.Sprintf(`no version provided for stable remote ref: %s`, gitTestRepoURL))

		_, err = c.ModuleSource(testGitModuleRef("top-level"), dagger.ModuleSourceOpts{
			Stable: true,
		}).AsString(ctx)
		require.NoError(t, err)
	})
}

func TestModuleDaggerGitWithSources(t *testing.T) {
	t.Parallel()

	for _, modSubpath := range []string{"samedir", "subdir"} {
		modSubpath := modSubpath
		t.Run(modSubpath, func(t *testing.T) {
			t.Parallel()
			c, ctx := connect(t)
			ctr := goGitBase(t, c).
				WithWorkdir("/work").
				With(daggerExec("init")).
				With(daggerExec("install", "--name", "foo", testGitModuleRef("various-source-values/"+modSubpath)))

			out, err := ctr.With(daggerCallAt("foo", "container-echo", "--string-arg", "hi", "stdout")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi", strings.TrimSpace(out))

			ctr = ctr.With(daggerExec("develop", "--sdk=go", "--source=.")).
				WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
					Contents: `package main

import "context"

type Work struct {}

func (m *Work) Fn(ctx context.Context) (string, error) {
	return dag.Foo().ContainerEcho("hi").Stdout(ctx)
}
`})

			out, err = ctr.With(daggerCall("fn")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi", strings.TrimSpace(out))

			out, err = ctr.With(daggerCallAt(testGitModuleRef("various-source-values/"+modSubpath), "container-echo", "--string-arg", "hi", "stdout")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi", strings.TrimSpace(out))
		})
	}
}

func TestModuleViews(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	ctr := goGitBase(t, c).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithNewFile("main.go", dagger.ContainerWithNewFileOpts{
			Contents: `package main

type Test struct {}

func (m *Test) Fn(dir *Directory) *Directory {
	return dir
}
`}).WithDirectory("stuff", c.Directory().
		WithNewFile("nice-file", "nice").
		WithNewFile("mean-file", "mean").
		WithNewFile("foo.txt", "foo").
		WithDirectory("subdir", c.Directory().
			WithNewFile("other-nice-file", "nice").
			WithNewFile("other-mean-file", "mean").
			WithNewFile("bar.txt", "bar"),
		),
	)

	// setup nice-view
	ctr = ctr.With(daggerExec("config", "views", "set", "-n", "nice-view", "nice-file", "subdir/other-nice-file"))
	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "nice-file\nsubdir/other-nice-file")

	out, err = ctr.With(daggerExec("config", "views", "-n", "nice-view")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "nice-file\nsubdir/other-nice-file")

	out, err = ctr.With(daggerExec("config", "views")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "nice-file\nsubdir/other-nice-file")

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:nice-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "nice-file\nsubdir", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:nice-view", "directory", "--path=subdir", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "other-nice-file", strings.TrimSpace(out))

	// setup mean-view
	ctr = ctr.With(daggerExec("config", "views", "--json", "set", "-n", "mean-view", "mean-file", "subdir/other-mean-file"))
	out, err = ctr.Stdout(ctx)
	require.NoError(t, err)
	actual := []string{}
	require.NoError(t, json.Unmarshal([]byte(out), &actual))
	require.Equal(t, []string{"mean-file", "subdir/other-mean-file"}, actual)

	out, err = ctr.With(daggerExec("config", "views", "-n", "mean-view")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "mean-file\nsubdir/other-mean-file")

	out, err = ctr.With(daggerExec("config", "views")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "nice-file\nsubdir/other-nice-file")
	require.Contains(t, strings.TrimSpace(out), "mean-file\nsubdir/other-mean-file")

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:nice-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "nice-file\nsubdir", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:mean-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "mean-file\nsubdir", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:mean-view", "directory", "--path=subdir", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "other-mean-file", strings.TrimSpace(out))

	// setup txt-view
	ctr = ctr.With(daggerExec("config", "views", "set", "-n", "txt-view", "**/*.txt"))
	out, err = ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "**/*.txt")

	out, err = ctr.With(daggerExec("config", "views", "-n", "txt-view")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "**/*.txt")

	out, err = ctr.With(daggerExec("config", "views")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "nice-file\nsubdir/other-nice-file")
	require.Contains(t, strings.TrimSpace(out), "mean-file\nsubdir/other-mean-file")
	require.Contains(t, strings.TrimSpace(out), "**/*.txt")

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:nice-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "nice-file\nsubdir", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:mean-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "mean-file\nsubdir", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:txt-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "foo.txt\nsubdir", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:txt-view", "directory", "--path=subdir", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "bar.txt", strings.TrimSpace(out))

	// setup no-subdir-txt-view
	ctr = ctr.With(daggerExec("config", "views", "set", "-n", "no-subdir-txt-view", "**/*.txt", "!subdir"))
	out, err = ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "!subdir\n**/*.txt")

	out, err = ctr.With(daggerExec("config", "views", "-n", "no-subdir-txt-view")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "!subdir\n**/*.txt")

	out, err = ctr.With(daggerExec("config", "views", "--json")).Stdout(ctx)
	require.NoError(t, err)
	{
		actual := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(out), &actual))
		require.Equal(t, map[string]any{
			"nice-view":          []any{"nice-file", "subdir/other-nice-file"},
			"mean-view":          []any{"mean-file", "subdir/other-mean-file"},
			"txt-view":           []any{"**/*.txt"},
			"no-subdir-txt-view": []any{"!subdir", "**/*.txt"},
		}, actual)
	}

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:nice-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "nice-file\nsubdir", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:mean-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "mean-file\nsubdir", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:txt-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "foo.txt\nsubdir", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:no-subdir-txt-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "foo.txt", strings.TrimSpace(out))

	// add to txt-view
	ctr = ctr.With(daggerExec("config", "views", "add", "-n", "txt-view", "nice-file", "!subdir"))
	out, err = ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "!subdir\n**/*.txt\nnice-file")

	out, err = ctr.With(daggerExec("config", "views", "-n", "txt-view")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "!subdir\n**/*.txt\nnice-file")

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:txt-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "foo.txt\nnice-file", strings.TrimSpace(out))

	// remove from no-subdir-txt-view
	ctr = ctr.With(daggerExec("config", "views", "remove", "-n", "no-subdir-txt-view", "!subdir"))
	out, err = ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "**/*.txt")

	out, err = ctr.With(daggerExec("config", "views", "-n", "no-subdir-txt-view")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "**/*.txt")

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:no-subdir-txt-view", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "foo.txt\nsubdir", strings.TrimSpace(out))

	out, err = ctr.With(daggerCall("fn", "--dir", "stuff:no-subdir-txt-view", "directory", "--path=subdir", "entries")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "bar.txt", strings.TrimSpace(out))

	// remove mean-view
	ctr = ctr.With(daggerExec("config", "views", "-n", "mean-view", "remove"))
	out, err = ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), `View "mean-view" removed`)

	_, err = ctr.With(daggerExec("config", "views", "-n", "mean-view")).Stdout(ctx)
	require.ErrorContains(t, err, `view "mean-view" not found`)

	out, err = ctr.With(daggerExec("config", "views")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "nice-file\nsubdir/other-nice-file")
	require.Contains(t, strings.TrimSpace(out), "!subdir\n**/*.txt\nnice-file")
	require.Contains(t, strings.TrimSpace(out), "**/*.txt")

	// remove all views
	ctr = ctr.With(daggerExec("config", "views", "-n", "nice-view", "remove"))
	out, err = ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), `View "nice-view" removed`)
	ctr = ctr.With(daggerExec("config", "views", "-n", "txt-view", "remove"))
	out, err = ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), `View "txt-view" removed`)
	ctr = ctr.With(daggerExec("config", "views", "-n", "no-subdir-txt-view", "remove"))
	out, err = ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), `View "no-subdir-txt-view" removed`)

	out, err = ctr.With(daggerExec("config", "views")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "", strings.TrimSpace(out))
}
