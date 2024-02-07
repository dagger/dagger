package core

import (
	"encoding/json"
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
		expectedConf := modules.ModuleConfig{
			Name:    "test",
			SDK:     "go",
			Include: []string{"foo"},
			Exclude: []string{"blah"},
			Dependencies: []*modules.ModuleConfigDependency{{
				Name:   "dep",
				Source: "foo",
			}},
		}
		expectedConfBytes, err := json.Marshal(expectedConf)
		require.NoError(t, err)
		require.JSONEq(t, strings.TrimSpace(string(expectedConfBytes)), confContents)

		// verify develop didn't overwrite main.go
		out, err := baseWithNewConfig.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "wowzas", strings.TrimSpace(out))

		// verify call works seamlessly even without explicit sync yet
		out, err = baseWithOldConfig.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "wowzas", strings.TrimSpace(out))
	})

	t.Run("dep has separate config", func(t *testing.T) {
		// Verify that if a local dep has its own dagger.json, that's used to load it correctly.
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
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go", "test")).
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

	t.Run("install dep from weird places", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		base := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go", "subdir/dep")).
			WithNewFile("/work/subdir/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			}).
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go", "test")).
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

		t.Run("dep points out of root", func(t *testing.T) {
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
			With(daggerExec("init", "--source=.", "--sdk=go", "coolmod")).
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
				srcDirEnts, err := srcRootDir.Directory("dagger/" + tc.sdk).Entries(ctx)
				require.NoError(t, err)
				require.Contains(t, srcDirEnts, tc.sourceDirEnt)
			})
		}
	})
}

func TestModuleDaggerDevelop(t *testing.T) {
	t.Parallel()
	t.Run("name and sdk", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		ctr := c.Container().From(golangImage).
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
		out, err := ctr.With(daggerCallAt("dep", "fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep", strings.TrimSpace(out))

		// now add an sdk+source
		ctr = ctr.
			With(daggerExec("develop", "--sdk", "go", "--source", "cool/subdir")).
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

		_, err = ctr.With(daggerExec("develop", "--name", "foo")).Sync(ctx)
		require.ErrorContains(t, err, `cannot update module name that has already been set to "work"`)

		_, err = ctr.With(daggerExec("develop", "--sdk", "python")).Sync(ctx)
		require.ErrorContains(t, err, `cannot update module SDK that has already been set to "go"`)

		_, err = ctr.With(daggerExec("develop", "--source", "blahblahblaha/blah")).Sync(ctx)
		require.ErrorContains(t, err, `cannot update module source path that has already been set to "cool/subdir"`)
	})
}

func TestModuleDaggerConfig(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	out, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go", "test")).
		With(daggerExec("config", "-m", "test")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Regexp(t, `Name:\s+test`, out)
	require.Regexp(t, `SDK:\s+go`, out)
	require.Regexp(t, `Root Directory:\s+/work`, out)
	require.Regexp(t, `Source Directory:\s+/work/test`, out)
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
				ctr = ctr.WithNewFile("dagger/"+tc.sdk+"/main.go", dagger.ContainerWithNewFileOpts{
					Contents: tc.mainSource,
				})
			} else {
				ctr = ctr.With(sdkSource(tc.sdk, tc.mainSource))
			}

			// TODO: use cli to configure include/exclude once supported
			ctr = ctr.
				With(configFile(".", &modules.ModuleConfig{
					Name:    "test",
					SDK:     tc.sdk,
					Include: []string{"dagger/" + tc.sdk + "/subdir/keepdir"},
					Exclude: []string{"dagger/" + tc.sdk + "/subdir/keepdir/rmdir"},
					Source:  "dagger/" + tc.sdk,
				})).
				WithDirectory("dagger/"+tc.sdk+"/subdir/keepdir/rmdir", c.Directory())

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
