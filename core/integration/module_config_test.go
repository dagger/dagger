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
			With(daggerExec("init", "--name=dep", "--sdk=go")).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go")).
			WithNewFile("/work/dagger.json", dagger.ContainerWithNewFileOpts{
				Contents: `{"name": "test", "sdk": "go", "include": ["*"], "exclude": ["bar"], "dependencies": ["foo"]}`,
			})

		// verify develop updates config to new format
		confContents, err := baseWithOldConfig.With(daggerExec("develop")).File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		expectedConf := modules.ModuleConfig{
			Name:    "test",
			SDK:     "go",
			Include: []string{"*"},
			Exclude: []string{"bar"},
			Dependencies: []*modules.ModuleConfigDependency{{
				Name:   "dep",
				Source: "foo",
			}},
			RootFor: []*modules.ModuleConfigRootFor{{
				Source: ".",
			}},
		}
		expectedConfBytes, err := json.Marshal(expectedConf)
		require.NoError(t, err)
		require.JSONEq(t, strings.TrimSpace(string(expectedConfBytes)), confContents)

		// verify call works seamlessly even without explicit develop yet
		out, err := baseWithOldConfig.With(daggerCall("container-echo", "--string-arg", "hey", "stdout")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hey", strings.TrimSpace(out))
	})

	t.Run("old config with root fails", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)

		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go")).
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
		c, ctx := connect(t)

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/subdir/dep").
			With(daggerExec("init", "--name=dep", "--sdk=go")).
			WithNewFile("/work/subdir/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			}).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=go", "test")).
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

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=dep", "--sdk=go", "subdir/dep")).
			WithNewFile("/work/subdir/dep/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			}).
			With(daggerExec("init", "--name=test", "--sdk=go", "test")).
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

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--name=dep", "--sdk=go")).
			WithWorkdir("/work/test").
			With(daggerExec("init", "--name=test", "--sdk=go"))

		t.Run("from src dir", func(t *testing.T) {
			t.Parallel()
			_, err := base.
				WithWorkdir("/work/test").
				With(daggerExec("install", "../dep")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path "../dep" escapes root "/"`)
		})

		t.Run("from dep dir", func(t *testing.T) {
			t.Parallel()
			_, err := base.
				WithWorkdir("/work/dep").
				With(daggerExec("install", "-m=../test", ".")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path "../dep" escapes root "/"`)
		})

		t.Run("from root", func(t *testing.T) {
			t.Parallel()
			_, err := base.
				WithWorkdir("/").
				With(daggerExec("install", "-m=work/test", "work/dep")).
				Sync(ctx)
			require.ErrorContains(t, err, `module dep source path "../dep" escapes root "/"`)
		})
	})

	t.Run("malicious config", func(t *testing.T) {
		// verify a maliciously/incorrectly constructed dagger.json is still handled correctly
		t.Parallel()
		c, ctx := connect(t)

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--name=dep", "--sdk=go")).
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
			With(daggerExec("init", "--name=test", "--sdk=go"))

		t.Run("no root for", func(t *testing.T) {
			t.Parallel()

			base := base.
				With(configFile("/work/dep", &modules.ModuleConfig{
					Name: "dep",
					SDK:  "go",
				}))

			out, err := base.With(daggerCallAt("dep", "get-source", "entries")).Stdout(ctx)
			require.NoError(t, err)
			// shouldn't default the root dir to /work just because we called it from there,
			// it should default to just using dep's source dir in this case
			ents := strings.Fields(strings.TrimSpace(out))
			require.Equal(t, []string{
				".gitattributes",
				"LICENSE",
				"dagger.gen.go",
				"dagger.json",
				"go.mod",
				"go.sum",
				"main.go",
				"querybuilder",
				// no "dep" dir
			}, ents)
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
					RootFor: []*modules.ModuleConfigRootFor{{
						Source: ".",
					}},
				}))

			_, err := base.With(daggerCall("container-echo", "--string-arg", "plz fail")).Sync(ctx)
			require.ErrorContains(t, err, `module dep source path ".." escapes root "/"`)

			_, err = base.With(daggerExec("develop")).Sync(ctx)
			require.ErrorContains(t, err, `module dep source path ".." escapes root "/"`)

			_, err = base.With(daggerExec("install", "./dep")).Sync(ctx)
			require.ErrorContains(t, err, `module dep source path ".." escapes root "/"`)
		})
	})
}

func TestModuleCustomDepNames(t *testing.T) {
	t.Parallel()

	t.Run("basic", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)
		ctr := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerExec("init", "--name=dep", "--sdk=go")).
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
			With(daggerExec("init", "--name=test", "--sdk=go")).
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
			With(daggerExec("init", "--name=test", "--sdk=go")).
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
			With(daggerExec("init", "--name=test", "--sdk=go")).
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
			With(daggerExec("init", "--name=dep", "--sdk=go")).
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
			With(daggerExec("init", "--name=dep", "--sdk=go")).
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
			With(daggerExec("init", "--name=test", "--sdk=go")).
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
	t.Run("name and sdk must be set together", func(t *testing.T) {
		t.Parallel()
		c, ctx := connect(t)
		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test")).
			Stdout(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, `if any flags in the group [sdk name] are set they must all be set; missing [sdk]`)

		_, err = c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go")).
			Stdout(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, `if any flags in the group [sdk name] are set they must all be set; missing [name]`)
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
			With(daggerExec("init", "--name=dep", "--sdk=go")).
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

		// now add an sdk+name
		ctr = ctr.
			With(daggerExec("develop", "--sdk", "go", "--name", "test")).
			WithNewFile("/work/main.go", dagger.ContainerWithNewFileOpts{
				Contents: `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) { 
				depStr, err := dag.Dep().Fn(ctx)
				if err != nil {
					return "", err
				}
				return "hi from test " + depStr, nil
			}
			`,
			})

		// should be able to invoke it directly now
		out, err = ctr.With(daggerCall("fn")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi from test hi from dep", strings.TrimSpace(out))

		// currently, we don't support renaming or re-sdking a module, make sure that errors comprehensibly

		_, err = ctr.With(daggerExec("develop", "--sdk", "python", "--name", "foo")).Sync(ctx)
		require.ErrorContains(t, err, `cannot update module name that has already been set to "test"`)
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
