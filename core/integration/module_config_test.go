package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/testctx"
)

type ConfigSuite struct{}

func TestConfig(t *testing.T) {
	testctx.Run(testCtx, t, ConfigSuite{}, Middleware()...)
}

func (ConfigSuite) TestConfigs(ctx context.Context, t *testctx.T) {
	// test dagger.json source configs that aren't inherently covered in other tests

	t.Run("upgrade from old config", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		baseWithOldConfig := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/foo").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/main.go", `package main
			type Test struct {}

			func (m *Test) Fn() string { return "wowzas" }
			`,
			).
			WithNewFile("/work/dagger.json", `{"name": "test", "sdk": "go", "include": ["foo"], "exclude": ["blah"], "dependencies": ["foo"]}`)

		// verify develop updates config to new format
		baseWithNewConfig := baseWithOldConfig.With(daggerExec("develop"))
		confContents, err := baseWithNewConfig.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		var modCfg modules.ModuleConfig
		require.NoError(t, json.Unmarshal([]byte(confContents), &modCfg))
		require.Equal(t, "test", modCfg.Name)
		require.Equal(t, "go", modCfg.SDK)
		require.Equal(t, []string{"foo"}, modCfg.Include)
		require.Equal(t, []string{"blah"}, modCfg.Exclude)
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

	t.Run("malicious config", func(ctx context.Context, t *testctx.T) {
		// verify a maliciously/incorrectly constructed dagger.json is still handled correctly

		baseCtr := func(t *testctx.T, c *dagger.Client) *dagger.Container {
			return goGitBase(t, c).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/tmp/foo").
				With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
				WithWorkdir("/work/dep").
				With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
				WithNewFile("/work/dep/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) GetSource(ctx context.Context) *dagger.Directory {
			    return dag.CurrentModule().Source()
			}
			`,
				).
				WithWorkdir("/work").
				With(daggerExec("init", "--source=.", "--name=test", "--sdk=go"))
		}

		t.Run("source points out of root", func(ctx context.Context, t *testctx.T) {
			t.Run("local", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				base := baseCtr(t, c).
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

			t.Run("local with absolute path", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				base := baseCtr(t, c).
					With(configFile(".", &modules.ModuleConfig{
						Name:   "evil",
						SDK:    "go",
						Source: "/tmp",
					}))

				_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				require.ErrorContains(t, err, `source path "/tmp" contains parent directory components`)

				_, err = base.With(daggerExec("develop")).Sync(ctx)
				require.ErrorContains(t, err, `source path "/tmp" contains parent directory components`)

				_, err = base.With(daggerExec("install", "./dep")).Sync(ctx)
				require.ErrorContains(t, err, `source path "/tmp" contains parent directory components`)
			})

			testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
				t.Run("git", func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)
					mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
					defer cleanup()

					_, err := baseCtr(t, c).With(mountedSocket).With(daggerCallAt(testGitModuleRef(tc, "invalid/bad-source"), "container-echo", "--string-arg", "plz fail")).Sync(ctx)
					require.ErrorContains(t, err, `source path "../../../" contains parent directory components`)
				})
			})
		})

		t.Run("dep points out of root", func(ctx context.Context, t *testctx.T) {
			t.Run("local", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				base := baseCtr(t, c).
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

			t.Run("local with absolute path", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				base := baseCtr(t, c).
					With(configFile(".", &modules.ModuleConfig{
						Name: "evil",
						SDK:  "go",
						Dependencies: []*modules.ModuleConfigDependency{{
							Name:   "escape",
							Source: "/tmp/foo",
						}},
					}))

				_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				require.ErrorContains(t, err, `missing config file /work/tmp/foo/dagger.json`)

				_, err = base.With(daggerExec("develop")).Sync(ctx)
				require.ErrorContains(t, err, `missing config file /work/tmp/foo/dagger.json`)

				_, err = base.With(daggerExec("install", "./dep")).Sync(ctx)
				require.ErrorContains(t, err, `missing config file /work/tmp/foo/dagger.json`)

				base = base.
					With(configFile(".", &modules.ModuleConfig{
						Name: "evil",
						SDK:  "go",
						Dependencies: []*modules.ModuleConfigDependency{{
							Name:   "escape",
							Source: "/./dep",
						}},
					}))

				_, err = base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
				require.ErrorContains(t, err, `module dep source root path "../dep" escapes root`)

				_, err = base.With(daggerExec("develop")).Sync(ctx)
				require.ErrorContains(t, err, `module dep source root path "../dep" escapes root`)

				_, err = base.With(daggerExec("install", "./dep")).Sync(ctx)
				require.ErrorContains(t, err, `module dep source root path "../dep" escapes root`)
			})

			testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
				t.Run("git", func(ctx context.Context, t *testctx.T) {
					c := connect(ctx, t)
					mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
					defer cleanup()

					_, err := baseCtr(t, c).With(mountedSocket).With(daggerCallAt(testGitModuleRef(tc, "invalid/bad-dep"), "container-echo", "--string-arg", "plz fail")).Sync(ctx)
					require.ErrorContains(t, err, `module dep source root path "../../../foo" escapes root`)
				})
			})
		})
	})
}

func (ConfigSuite) TestCustomDepNames(ctx context.Context, t *testctx.T) {
	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", `package main

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
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", "--name", "foo", "./dep")).
			WithNewFile("/work/main.go", `package main

			import (
				"context"
				"dagger/test/internal/dagger"
			)

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) {
				return dag.Foo().DepFn(ctx)
			}

			func (m *Test) GetObj(ctx context.Context) (string, error) {
				var obj *dagger.FooObj
				obj = dag.Foo().GetDepObj()
				return obj.Str(ctx)
			}

			func (m *Test) GetOtherObj(ctx context.Context) (string, error) {
				var obj *dagger.FooOtherObj
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
			)

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

	t.Run("same mod name as dep", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithNewFile("/work/dep/main.go", `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) string {
				return "hi from dep"
			}
			`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", "--name", "foo", "./dep")).
			WithNewFile("/work/main.go", `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) {
				return dag.Foo().Fn(ctx)
			}
			`,
			)

		out, err := ctr.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep", strings.TrimSpace(out))
	})

	t.Run("two deps with same name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep1").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep1/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) Fn(ctx context.Context) string {
				return "hi from dep1"
			}
			`,
			).
			WithWorkdir("/work/dep2").
			With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep2/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) Fn(ctx context.Context) string {
				return "hi from dep2"
			}
			`,
			).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", "--name", "foo", "./dep1")).
			With(daggerExec("install", "--name", "bar", "./dep2")).
			WithNewFile("/work/main.go", `package main

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
			)

		out, err := ctr.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from dep1 hi from dep2", strings.TrimSpace(out))
	})
}

// test the `dagger config` command
func (ConfigSuite) TestDaggerConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
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

func (ConfigSuite) TestIncludeExclude(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		sdk                    string
		mainSource             string
		customSDKSource        string
		customSDKUnderlyingSDK string
	}{
		{
			sdk: "go",
			mainSource: `package main
import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn() *dagger.Directory {
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
export class Test {
  @func()
  fn(): Directory {
    return dag.currentModule().source()
  }
}`,
		},
		{
			sdk: "coolsdk",
			mainSource: `package main
import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn() *dagger.Directory {
	return dag.CurrentModule().Source()
}
`,
			customSDKUnderlyingSDK: "go",
			customSDKSource: `package main

import (
	"dagger/coolsdk/internal/dagger"
)

type Coolsdk struct {}

func (m *Coolsdk) ModuleRuntime(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.Container {
	return modSource.WithSDK("go").AsModule().Runtime().WithEnvVariable("COOL", "true")
}

func (m *Coolsdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
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
		t.Run(tc.sdk, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

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

			ctr = ctr.With(daggerExec("init", "--name=test", "--source=dagger", "--sdk="+tc.sdk))

			if tc.customSDKSource != "" {
				// TODO: hardcoding that underlying sdk is go right now, could be generalized
				ctr = ctr.WithNewFile("dagger/main.go", tc.mainSource)
			} else {
				ctr = ctr.WithWorkdir("/work/dagger").With(sdkSource(tc.sdk, tc.mainSource)).WithWorkdir("/work")
			}

			// TODO: use cli to configure include/exclude once supported
			ctr = ctr.
				With(configFile(".", &modules.ModuleConfig{
					Name:    "test",
					SDK:     tc.sdk,
					Include: []string{"dagger/subdir/keepdir"},
					Exclude: []string{"dagger/subdir/keepdir/rmdir"},
					Source:  "dagger",
				})).
				WithDirectory("dagger/subdir/keepdir/rmdir", c.Directory())

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

	t.Run("dependency", func(ctx context.Context, t *testctx.T) {
		source := func(name string) dagger.WithContainerFunc {
			return sdkSourceAt(".dagger", "go", fmt.Sprintf(`package main

import (
	"io/fs"
	"path/filepath"
)

type %[1]s struct{}

func (m *%[1]s) ContextDirectory() ([]string, error) {
	var files []string
	err := filepath.WalkDir("/src", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
`, name))
		}

		c := connect(ctx, t)

		ctr := goGitBase(t, c).
			With(daggerExec("init", "--sdk=go", "--name=test", "--source=.dagger")).
			With(source("Test")).
			WithNewFile("foo", "").
			WithNewFile(".dagger/bar", "").
			WithWorkdir("dep").
			With(daggerExec("init", "--sdk=go", "--source=.dagger")).
			With(source("Dep")).
			WithNewFile("foo", "").
			WithNewFile(".dagger/bar", "").
			With(configFile(".", &modules.ModuleConfig{
				Name:    "dep",
				SDK:     "go",
				Include: []string{"**/foo"},
				Exclude: []string{"**/bar"},
				Source:  ".dagger",
			})).
			WithWorkdir("..").
			With(daggerExec("install", "./dep"))

		t.Run("dependency filtered", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.
				With(daggerCallAt("dep", "context-directory")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, out, "/src/dep/foo")
			require.NotContains(t, out, "/src/dep/.dagger/bar")
		})

		t.Run("main module not affected", func(ctx context.Context, t *testctx.T) {
			out, err := ctr.
				With(daggerCall("context-directory")).
				Stdout(ctx)

			require.NoError(t, err)
			require.NotContains(t, out, "/src/foo")
			require.Contains(t, out, "/src/.dagger/bar")
		})
	})
}

// verify that if there is no local .git in parent dirs then the context defaults to the source root
func (ConfigSuite) TestContextDefaultsToSourceRoot(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/coolsdk").
		With(daggerExec("init", "--source=.", "--name=cool-sdk", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"dagger/cool-sdk/internal/dagger"
)

type CoolSdk struct {}

func (m *CoolSdk) ModuleRuntime(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.Container {
	return modSource.WithSDK("go").AsModule().Runtime().
		WithMountedDirectory("/da-context", modSource.ContextDirectory())
}

func (m *CoolSdk) Codegen(modSource *dagger.ModuleSource, introspectionJson *dagger.File) *dagger.GeneratedCode {
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
		).
		WithWorkdir("/work").
		WithNewFile("random-file", "").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=coolsdk")).
		WithNewFile("main.go", `package main

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
		)

	out, err := ctr.
		With(daggerCall("fn")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "random-file")
}

// Git hosting providers to test behavior against
type vcsTestCase struct {
	name              string
	gitTestRepoRef    string
	gitTestRepoCommit string
	// host component of repoURL
	expectedHost string
	// base HTML URL might differ from ref (e.g. not contain .git ; vanity URLs )
	expectedBaseHTMLURL string
	// path separator to access `tree` view of src at commit, per provider
	expectedURLPathComponent string
	// Azure needs a path prefix
	expectedPathPrefix string
	isPrivateRepo      bool
	skipProxyTest      bool
}

var vcsTestCases = []vcsTestCase{
	// Test cases for public repositories using Go-style references, without '.git' suffix (optional)
	// These cases verify correct handling of repository URLs across different Git hosting providers

	// GitHub public repository
	{
		name:                     "GitHub public",
		gitTestRepoRef:           "github.com/dagger/dagger-test-modules",
		gitTestRepoCommit:        "323d56c9ece3492d13f58b8b603d31a7c511cd41",
		expectedHost:             "github.com",
		expectedBaseHTMLURL:      "github.com/dagger/dagger-test-modules",
		expectedURLPathComponent: "tree",
		expectedPathPrefix:       "",
	},
	{
		name:                     "GitLab public",
		gitTestRepoRef:           "gitlab.com/dagger-modules/test/more/dagger-test-modules-public",
		gitTestRepoCommit:        "323d56c9ece3492d13f58b8b603d31a7c511cd41",
		expectedHost:             "gitlab.com",
		expectedBaseHTMLURL:      "gitlab.com/dagger-modules/test/more/dagger-test-modules-public",
		expectedURLPathComponent: "tree",
		expectedPathPrefix:       "",
	},
	{
		name:                     "BitBucket public",
		gitTestRepoRef:           "bitbucket.org/dagger-modules/dagger-test-modules-public",
		gitTestRepoCommit:        "323d56c9ece3492d13f58b8b603d31a7c511cd41",
		expectedHost:             "bitbucket.org",
		expectedBaseHTMLURL:      "bitbucket.org/dagger-modules/dagger-test-modules-public",
		expectedURLPathComponent: "src",
		expectedPathPrefix:       "",
	},
	{
		name:                     "Azure DevOps public",
		gitTestRepoRef:           "dev.azure.com/daggere2e/public/_git/dagger-test-modules",
		gitTestRepoCommit:        "323d56c9ece3492d13f58b8b603d31a7c511cd41",
		expectedHost:             "dev.azure.com",
		expectedBaseHTMLURL:      "dev.azure.com/daggere2e/public/_git/dagger-test-modules",
		expectedURLPathComponent: "commit",
		expectedPathPrefix:       "?path=",
	},

	// SSH references support both private and public repositories across various Git hosting providers.
	// The following test cases demonstrate the handling of SSH references for different scenarios.

	// GitLab private repository using explicit SSH reference format
	{
		name:                     "SSH Private GitLab",
		gitTestRepoRef:           "ssh://gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private.git",
		gitTestRepoCommit:        "323d56c9ece3492d13f58b8b603d31a7c511cd41",
		expectedHost:             "gitlab.com",
		expectedBaseHTMLURL:      "gitlab.com/dagger-modules/private/test/more/dagger-test-modules-private",
		expectedURLPathComponent: "tree",
		expectedPathPrefix:       "",
		isPrivateRepo:            true,
		skipProxyTest:            true,
	},
	// BitBucket private repository using SCP-like SSH reference format
	{
		name:                     "SSH Private BitBucket",
		gitTestRepoRef:           "git@bitbucket.org:dagger-modules/private-modules-test.git",
		gitTestRepoCommit:        "323d56c9ece3492d13f58b8b603d31a7c511cd41",
		expectedHost:             "bitbucket.org",
		expectedBaseHTMLURL:      "bitbucket.org/dagger-modules/private-modules-test",
		expectedURLPathComponent: "src",
		expectedPathPrefix:       "",
		isPrivateRepo:            true,
		skipProxyTest:            true,
	},
	// GitHub public repository using SSH reference
	// Note: This format is also valid for private GitHub repositories
	{
		name:                     "SSH Public GitHub",
		gitTestRepoRef:           "git@github.com:dagger/dagger-test-modules.git",
		gitTestRepoCommit:        "323d56c9ece3492d13f58b8b603d31a7c511cd41",
		expectedHost:             "github.com",
		expectedBaseHTMLURL:      "github.com/dagger/dagger-test-modules",
		expectedURLPathComponent: "tree",
		expectedPathPrefix:       "",
		skipProxyTest:            true,
	},
	// Azure DevOps private repository using SSH reference
	// Note: Currently commented out due to Azure DevOps limitations on scoped SSH keys at the repository level
	//
	//	{
	//		name:                     "SSH Private Azure",
	//		gitTestRepoRef:           "git@ssh.dev.azure.com:v3/daggere2e/private/dagger-test-modules",
	//		gitTestRepoCommit:        "323d56c9ece3492d13f58b8b603d31a7c511cd41",
	//		expectedHost:             "dev.azure.com",
	//		expectedBaseHTMLURL:      "dev.azure.com/daggere2e/private/_git/dagger-test-modules",
	//		expectedURLPathComponent: "commit",
	//		expectedPathPrefix:       "?path=",
	//		isPrivateRepo:              true,
	//	},
}

func testOnMultipleVCS(t *testctx.T, testFunc func(ctx context.Context, t *testctx.T, tc vcsTestCase)) {
	for _, tc := range vcsTestCases {
		tc := tc
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			testFunc(ctx, t, tc)
		})
	}
}

func testGitModuleRef(tc vcsTestCase, subpath string) string {
	url := tc.gitTestRepoRef
	if subpath != "" {
		if !strings.HasPrefix(subpath, "/") {
			subpath = "/" + subpath
		}
		url += subpath
	}
	return fmt.Sprintf("%s@%s", url, tc.gitTestRepoCommit)
}

func (ConfigSuite) TestDaggerGitRefs(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		c := connect(ctx, t)

		t.Run("root module", func(ctx context.Context, t *testctx.T) {
			rootModSrc := c.ModuleSource(testGitModuleRef(tc, ""))

			htmlURL, err := rootModSrc.AsGitSource().HTMLURL(ctx)
			require.NoError(t, err)
			expectedURL := fmt.Sprintf("https://%s/%s/%s", tc.expectedBaseHTMLURL, tc.expectedURLPathComponent, tc.gitTestRepoCommit)
			require.Equal(t, expectedURL, htmlURL)
			// URL format matches public repo from same provider.
			// No need to test with auth on those refs
			if !tc.isPrivateRepo {
				resp, err := http.Get(htmlURL)
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, fmt.Sprintf("https://%s/%s/%s", tc.expectedBaseHTMLURL, tc.expectedURLPathComponent, tc.gitTestRepoCommit), htmlURL)
			}

			commit, err := rootModSrc.AsGitSource().Commit(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.gitTestRepoCommit, commit)

			refStr, err := rootModSrc.AsString(ctx)
			require.NoError(t, err)
			require.Equal(t, testGitModuleRef(tc, ""), refStr)
		})

		t.Run("top-level module", func(ctx context.Context, t *testctx.T) {
			topLevelModSrc := c.ModuleSource(testGitModuleRef(tc, "top-level"))
			htmlURL, err := topLevelModSrc.AsGitSource().HTMLURL(ctx)
			require.NoError(t, err)
			expectedURL := fmt.Sprintf("https://%s/%s/%s%s/top-level", tc.expectedBaseHTMLURL, tc.expectedURLPathComponent, tc.gitTestRepoCommit, tc.expectedPathPrefix)
			require.Equal(t, expectedURL, htmlURL)

			// URL format matches public repo from same provider.
			// No need to test with auth on those refs
			if !tc.isPrivateRepo {
				resp, err := http.Get(htmlURL)
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
			}

			commit, err := topLevelModSrc.AsGitSource().Commit(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.gitTestRepoCommit, commit)

			refStr, err := topLevelModSrc.AsString(ctx)
			require.NoError(t, err)
			require.Equal(t, testGitModuleRef(tc, "top-level"), refStr)
		})

		t.Run("subdir dep2 module", func(ctx context.Context, t *testctx.T) {
			subdirDepModSrc := c.ModuleSource(testGitModuleRef(tc, "subdir/dep2"))
			htmlURL, err := subdirDepModSrc.AsGitSource().HTMLURL(ctx)
			require.NoError(t, err)
			expectedURL := fmt.Sprintf("https://%s/%s/%s%s/subdir/dep2", tc.expectedBaseHTMLURL, tc.expectedURLPathComponent, tc.gitTestRepoCommit, tc.expectedPathPrefix)
			require.Equal(t, expectedURL, htmlURL)

			// URL format matches public repo from same provider.
			// No need to test with auth on those refs
			if !tc.isPrivateRepo {
				resp, err := http.Get(htmlURL)
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
			}

			commit, err := subdirDepModSrc.AsGitSource().Commit(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.gitTestRepoCommit, commit)

			refStr, err := subdirDepModSrc.AsString(ctx)
			require.NoError(t, err)
			require.Equal(t, testGitModuleRef(tc, "subdir/dep2"), refStr)
		})

		t.Run("stable arg", func(ctx context.Context, t *testctx.T) {
			_, err := c.ModuleSource(tc.gitTestRepoRef, dagger.ModuleSourceOpts{
				Stable: true,
			}).AsString(ctx)
			require.ErrorContains(t, err, fmt.Sprintf(`no version provided for stable remote ref: %s`, tc.gitTestRepoRef))

			_, err = c.ModuleSource(testGitModuleRef(tc, "top-level"), dagger.ModuleSourceOpts{
				Stable: true,
			}).AsString(ctx)
			require.NoError(t, err)
		})
	})
}

func (ConfigSuite) TestDaggerGitWithSources(ctx context.Context, t *testctx.T) {
	testOnMultipleVCS(t, func(ctx context.Context, t *testctx.T, tc vcsTestCase) {
		for _, modSubpath := range []string{"samedir", "subdir"} {
			modSubpath := modSubpath
			t.Run(modSubpath, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				mountedSocket, cleanup := mountedPrivateRepoSocket(c, t)
				defer cleanup()

				ctr := goGitBase(t, c).
					With(mountedSocket).
					WithWorkdir("/work").
					With(daggerExec("init", "--source=.")).
					With(daggerExec("install", "--name", "foo", testGitModuleRef(tc, "various-source-values/"+modSubpath)))

				out, err := ctr.With(daggerCallAt("foo", "container-echo", "--string-arg", "hi", "stdout")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "hi", strings.TrimSpace(out))

				ctr = ctr.With(daggerExec("develop", "--sdk=go", "--source=.")).
					WithNewFile("main.go", `package main

import "context"

type Work struct {}

func (m *Work) Fn(ctx context.Context) (string, error) {
	return dag.Foo().ContainerEcho("hi").Stdout(ctx)
}
`,
					)

				out, err = ctr.With(daggerCall("fn")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "hi", strings.TrimSpace(out))

				out, err = ctr.With(daggerCallAt(testGitModuleRef(tc, "various-source-values/"+modSubpath), "container-echo", "--string-arg", "hi", "stdout")).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, "hi", strings.TrimSpace(out))
			})
		}
	})
}

func (ConfigSuite) TestViews(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithNewFile("main.go", `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) Fn(dir *dagger.Directory) *dagger.Directory {
	return dir
}
`,
		).
		WithDirectory("stuff", c.Directory().
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
	require.Contains(t, strings.TrimSpace(out), "**/*.txt\n!subdir")

	out, err = ctr.With(daggerExec("config", "views", "-n", "no-subdir-txt-view")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "**/*.txt\n!subdir")

	out, err = ctr.With(daggerExec("config", "views", "--json")).Stdout(ctx)
	require.NoError(t, err)
	{
		actual := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(out), &actual))
		require.Equal(t, map[string]any{
			"nice-view":          []any{"nice-file", "subdir/other-nice-file"},
			"mean-view":          []any{"mean-file", "subdir/other-mean-file"},
			"txt-view":           []any{"**/*.txt"},
			"no-subdir-txt-view": []any{"**/*.txt", "!subdir"},
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
	require.Contains(t, strings.TrimSpace(out), "**/*.txt\nnice-file\n!subdir")

	out, err = ctr.With(daggerExec("config", "views", "-n", "txt-view")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, strings.TrimSpace(out), "**/*.txt\nnice-file\n!subdir")

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
	require.Contains(t, strings.TrimSpace(out), "**/*.txt\nnice-file\n!subdir")
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

func (ConfigSuite) TestDepPins(ctx context.Context, t *testctx.T) {
	// check that pins are correctly followed and loaded

	c := connect(ctx, t)

	repo := "github.com/dagger/dagger-test-modules/versioned"
	branch := "main"
	commit := "82adc5f7997e43ab3027810347298405f32a44db"

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/work/main.go", `package main
			import (
				"context"
				"strings"
			)

			type Test struct {}

			func (m *Test) Hello(ctx context.Context) (string, error) {
				s, err := dag.Versioned().Hello(ctx)
				if err != nil {
					return "", err
				}
				return strings.ToUpper(s), nil
			}
			`,
		)

	modCfgContents, err := ctr.
		File("dagger.json").
		Contents(ctx)
	require.NoError(t, err)

	var modCfg modules.ModuleConfig
	require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
	modCfg.Dependencies = append(modCfg.Dependencies, &modules.ModuleConfigDependency{
		Name:   "versioned",
		Source: repo + "@" + branch,
		Pin:    commit,
	})
	rewrittenModCfg, err := json.Marshal(modCfg)
	require.NoError(t, err)
	ctr = ctr.WithNewFile("dagger.json", string(rewrittenModCfg))

	out, err := ctr.With(daggerExec("call", "hello")).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "VERSION 2")
}

func (ConfigSuite) TestDepPinsStayPinned(ctx context.Context, t *testctx.T) {
	// check that pins stay pinned when running "dagger develop"

	c := connect(ctx, t)

	repo := "github.com/dagger/dagger-test-modules/versioned"
	branch := "main"
	commit := "82adc5f7997e43ab3027810347298405f32a44db"

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go"))

	modCfgContents, err := ctr.
		File("dagger.json").
		Contents(ctx)
	require.NoError(t, err)
	var modCfg modules.ModuleConfig
	require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
	modCfg.Dependencies = append(modCfg.Dependencies, &modules.ModuleConfigDependency{
		Name:   "versioned",
		Source: repo + "@" + branch,
		Pin:    commit,
	})
	rewrittenModCfg, err := json.Marshal(modCfg)
	require.NoError(t, err)
	ctr = ctr.WithNewFile("dagger.json", string(rewrittenModCfg))

	ctr = ctr.With(daggerExec("develop"))
	modCfgContents, err = ctr.
		File("dagger.json").
		Contents(ctx)
	require.NoError(t, err)
	var modCfgNew modules.ModuleConfig
	require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfgNew))
	require.Equal(t, modCfg, modCfgNew)
}

func (ConfigSuite) TestDepWritePins(ctx context.Context, t *testctx.T) {
	// check that pins are correctly written into dagger.json

	t.Run("install head", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// get the latest commit on main
		repo := "github.com/dagger/dagger-test-modules"
		commit, err := c.Git(repo).Head().Commit(ctx)
		require.NoError(t, err)

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", repo))

		modCfgContents, err := ctr.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)

		var modCfg modules.ModuleConfig
		require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
		require.Len(t, modCfg.Dependencies, 1)
		dep := modCfg.Dependencies[0]

		require.Equal(t, "root-mod", dep.Name)
		require.Equal(t, repo, dep.Source)
		require.Equal(t, commit, dep.Pin)
	})

	t.Run("install branch", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// get the latest commit on main
		repo := "github.com/dagger/dagger-test-modules"
		branch := "main"
		commit, err := c.Git(repo).Branch(branch).Commit(ctx)
		require.NoError(t, err)

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			With(daggerExec("install", repo+"@"+branch))

		modCfgContents, err := ctr.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)

		var modCfg modules.ModuleConfig
		require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
		require.Len(t, modCfg.Dependencies, 1)
		dep := modCfg.Dependencies[0]

		require.Equal(t, "root-mod", dep.Name)
		require.Equal(t, repo+"@"+branch, dep.Source)
		require.Equal(t, commit, dep.Pin)
	})

	t.Run("from legacy", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// get the latest commit on main
		repo := "github.com/dagger/dagger-test-modules"
		branch := "main"
		commit, err := c.Git(repo).Branch(branch).Commit(ctx)
		require.NoError(t, err)

		ctr := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go"))
		modCfgContents, err := ctr.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)

		var modCfg modules.ModuleConfig
		require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
		modCfg.Dependencies = append(modCfg.Dependencies, &modules.ModuleConfigDependency{
			Name:   "root-mod",
			Source: repo + "@" + commit,
		})
		rewrittenModCfg, err := json.Marshal(modCfg)
		require.NoError(t, err)

		ctr = ctr.
			WithNewFile("dagger.json", string(rewrittenModCfg)).
			With(daggerExec("develop"))

		modCfgContents, err = ctr.
			File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)

		modCfg = modules.ModuleConfig{}
		require.NoError(t, json.Unmarshal([]byte(modCfgContents), &modCfg))
		require.Len(t, modCfg.Dependencies, 1)
		dep := modCfg.Dependencies[0]

		require.Equal(t, "root-mod", dep.Name)
		require.Equal(t, repo+"@"+commit, dep.Source)
		require.Equal(t, commit, dep.Pin)
	})
}
