package core

// Workspace alignment: aligned; this file already matches the workspace-era split.
// Scope: Explicit module dependency install, uninstall, and update CLI behavior.
// Intent: Keep module dependency mutations separate from workspace install behavior and legacy command aliases.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Dependency mutation coverage stays separate from workspace install/update:
// `dagger module install` and `dagger module update` mutate dagger.json, while
// workspace-level install/update are covered by the workspace suites.
func (CLISuite) TestModuleDependencyInstall(ctx context.Context, t *testctx.T) {
	t.Run("local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/subdir/dep").
			With(daggerModuleExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/subdir/dep/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--source=test", "--name=test", "--sdk=go", "test")).
			With(daggerModuleExec("install", "-m=test", "./subdir/dep")).
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

		t.Run("install with absolute paths", func(ctx context.Context, t *testctx.T) {
			ctr := base.
				WithWorkdir("/").
				With(daggerModuleExec("init", "--source=/work/test2", "--name=test2", "--sdk=go", "/work/test2")).
				With(daggerModuleExec("install", "-m=/work/test2", "/work/subdir/dep")).
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
			With(daggerModuleExec("init", "--sdk=go", "--name=dep", "--source=.")).
			WithWorkdir("/work/dep2").
			With(daggerModuleExec("init", "--sdk=go", "--name=dep2", "--source=.")).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerModuleExec("install", "./dep"))

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, `"dep"`)

		_, err = ctr.
			With(daggerModuleExec("install", "./dep2", "--name=dep")).
			Sync(ctx)
		requireErrOut(t, err, fmt.Sprintf("duplicate dependency name %q", "dep"))
	})

	t.Run("installing a dependency with implicit duplicate name is not allowed", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := c.Container().
			From("alpine:latest").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerModuleExec("init", "--sdk=go", "--name=dep", "--source=.")).
			WithWorkdir("/work/dep2").
			With(daggerModuleExec("init", "--sdk=go", "--name=dep", "--source=.")).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--sdk=go", "--source=.")).
			With(daggerModuleExec("install", "./dep"))

		daggerjson, err := ctr.File("dagger.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, `"dep"`)

		_, err = ctr.
			With(daggerModuleExec("install", "./dep2")).
			Sync(ctx)
		requireErrOut(t, err, fmt.Sprintf("duplicate dependency name %q", "dep"))
	})

	t.Run("install with eager-runtime", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerModuleExec("init", "--sdk=go", "--name=dep", "--source=.")).
			WithNewFile("/work/dep/main.go", `package main

import "context"

type Dep struct{}

func (m *Dep) Fn(ctx context.Context) string {
	return definitelyUndefinedSymbol
}
`,
			).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--sdk=go", "--name=foo", "--source=.")).
			With(daggerModuleExec("install", "--eager-runtime", "./dep")).
			Sync(ctx)

		requireErrOut(t, err, "failed to install dependency")
		requireErrOut(t, err, "definitelyUndefinedSymbol")
	})

	t.Run("install dep from various places", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--source=subdir/dep", "--name=dep", "--sdk=go", "subdir/dep")).
			WithNewFile("/work/subdir/dep/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) DepFn(ctx context.Context, str string) string { return str }
			`,
			).
			With(daggerModuleExec("init", "--source=test", "--name=test", "--sdk=go", "test")).
			WithNewFile("/work/test/main.go", `package main

			import "context"

			type Test struct {}

			func (m *Test) Fn(ctx context.Context) (string, error) { return dag.Dep().DepFn(ctx, "hi dep") }
			`,
			)

		t.Run("from src dir", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/work/test").
				With(daggerModuleExec("install", "../subdir/dep")).
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from src subdir with findup", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/work/test/some/other/dir").
				With(daggerModuleExec("install", "../../../../subdir/dep")).
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from root", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/").
				With(daggerModuleExec("install", "-m=./work/test", "./work/subdir/dep")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from dep", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/work/subdir/dep").
				With(daggerModuleExec("install", "-m=../../test", ".")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from random place", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/var").
				With(daggerModuleExec("install", "-m=../work/test", "../work/subdir/dep")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from src dir with absolute paths", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/work/test").
				With(daggerModuleExec("install", "/work/subdir/dep")).
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from root with absolute paths", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/").
				With(daggerModuleExec("install", "-m=/work/test", "/work/subdir/dep")).
				WithWorkdir("/work/test").
				With(daggerCall("fn")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hi dep", strings.TrimSpace(out))
		})

		t.Run("from random place with absolute paths", func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithWorkdir("/var").
				With(daggerModuleExec("install", "-m=/work/test", "/work/subdir/dep")).
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
			With(daggerModuleExec("init", "--name=dep", "--sdk=go")).
			WithWorkdir("/work/test").
			With(daggerModuleExec("init", "--name=test", "--sdk=go"))

		t.Run("from src dir", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/work/test").
				With(daggerModuleExec("install", "../../play/dep")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})

		t.Run("from src dir with absolute path", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/work/test").
				With(daggerModuleExec("install", "/play/dep")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})

		t.Run("from dep dir", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/play/dep").
				With(daggerModuleExec("install", "-m=../../work/test", ".")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})

		t.Run("from dep dir with absolute path", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/play/dep").
				With(daggerModuleExec("install", "-m=/work/test", ".")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})

		t.Run("from root", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/").
				With(daggerModuleExec("install", "-m=work/test", "play/dep")).
				Sync(ctx)
			requireErrOut(t, err, `local module dependency context directory "/play/dep" is not in parent context directory "/work"`)
		})

		t.Run("from root with absolute path", func(ctx context.Context, t *testctx.T) {
			_, err := base.
				WithWorkdir("/").
				With(daggerModuleExec("install", "-m=/work/test", "play/dep")).
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
					With(daggerModuleExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerModuleExec("install", testGitModuleRef(tc, "top-level"))).
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
					With(daggerModuleExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerModuleExec("install", testGitModuleRef(tc, "../../"))).
					Sync(ctx)
				requireErrOut(t, err, `git module source subpath points out of root: "../.."`)

				_, err = goGitBase(t, c).
					With(privateSetup).
					WithWorkdir("/work").
					With(daggerModuleExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerModuleExec("install", testGitModuleRef(tc, "this/just/does/not/exist"))).
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
					With(daggerModuleExec("init", "--name=test", "--sdk=go", "--source=.")).
					With(daggerModuleExec("install", tc.gitTestRepoRef)).
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

func (CLISuite) TestModuleDependencyInstallOrder(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep-abc").
		With(daggerModuleExec("init", "--source=.", "--name=dep-abc", "--sdk=go")).
		WithNewFile("/work/dep-abc/main.go", `package main

			import "context"

			type DepAbc struct {}

			func (m *DepAbc) DepFn(ctx context.Context, str string) string { return str }
			`,
		).
		WithWorkdir("/work/dep-xyz").
		With(daggerModuleExec("init", "--source=.", "--name=dep-xyz", "--sdk=go")).
		WithNewFile("/work/dep-xyz/main.go", `package main

			import "context"

			type DepXyz struct {}

			func (m *DepXyz) DepFn(ctx context.Context, str string) string { return str }
			`,
		).
		WithWorkdir("/work").
		With(daggerModuleExec("init", "--source=test", "--name=test", "--sdk=go"))

	daggerJSON, err := base.
		With(daggerModuleExec("install", "./dep-abc")).
		With(daggerModuleExec("install", "./dep-xyz")).
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
		With(daggerModuleExec("install", "./dep-xyz")).
		With(daggerModuleExec("install", "./dep-abc")).
		File("dagger.json").
		Contents(ctx)
	require.NoError(t, err)
	names = []string{}
	for _, name := range gjson.Get(daggerJSON, "dependencies.#.name").Array() {
		names = append(names, name.String())
	}
	require.Equal(t, []string{"dep-abc", "dep-xyz"}, names)
}

func (CLISuite) TestModuleDependencyUninstall(ctx context.Context, t *testctx.T) {
	t.Run("local dep", func(ctx context.Context, t *testctx.T) {
		t.Run("uninstall a dependency currently used in module", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			ctr := c.Container().
				From(alpineImage).
				WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
				WithWorkdir("/work/bar").
				With(daggerModuleExec("init", "--sdk=go", "--name=bar", "--source=.")).
				WithWorkdir("/work").
				With(daggerModuleExec("init", "--sdk=go", "--name=foo", "--source=.")).
				With(daggerModuleExec("install", "./bar")).
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

			daggerjson, err = ctr.With(daggerExecRaw("uninstall", "bar")).
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
					With(daggerModuleExec("init", "--sdk=go", "--name=bar", "--source=.")).
					WithWorkdir("/work").
					With(daggerModuleExec("init", "--sdk=go", "--name=foo", "--source=."))

				if len(tc.installCmd) > 0 {
					ctr = ctr.With(daggerModuleExec(tc.installCmd...))
				}

				daggerjson, err := ctr.File("dagger.json").Contents(ctx)
				require.NoError(t, err)

				if len(tc.installCmd) > 0 {
					require.Contains(t, daggerjson, tc.modName)
				}

				if tc.beforeUninstall != nil {
					ctr = tc.beforeUninstall(ctr)
				}

				daggerjson, err = ctr.With(daggerExecRaw(tc.uninstallCmd...)).
					File("dagger.json").Contents(ctx)
				require.NoError(t, err)
				require.NotContains(t, daggerjson, tc.modName)
			})
		}
	})

	t.Run("git dependency", func(ctx context.Context, t *testctx.T) {
		testcases := []struct {
			// the mod used when running dagger module install <mod>.
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
					With(daggerModuleExec("init", "--sdk=go", "--name=foo", "--source=."))

				if tc.installCmdMod != "" {
					ctr = ctr.With(daggerModuleExec("install", tc.installCmdMod))
				}

				daggerjson, err := ctr.File("dagger.json").Contents(ctx)
				require.NoError(t, err)

				if tc.installCmdMod != "" {
					require.Contains(t, daggerjson, "hello")
				}

				daggerjson, err = ctr.With(daggerExecRaw("uninstall", tc.uninstallCmdMod)).
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

func (CLISuite) TestModuleDependencyUpdate(ctx context.Context, t *testctx.T) {
	const (
		// pins from github.com/shykes/daggerverse/docker module
		// to facilitate testing that pins are updated to expected
		// values when we run dagger module update
		randomMainPin  = "b20176e68d27edc9660960ec27f323d33dba633b"
		randomWolfiPin = "3d608cb5e6b4b18036a471400bc4e6c753f229d7"
		v041DockerPin  = "5c8b312cd7c8493966d28c118834d4e9565c7c62"
		v042DockerPin  = "7f2dcf2dbfb24af68c4c83a6da94dd5d885e58b8"
		v013WolfiPin   = "3338120927f8e291c4780de691ef63a7c9d825c0"
	)

	noDeps := `{
		"name": "foo",
		"sdk": "go"
	}`

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
				With(daggerModuleExec("init", "--sdk=go", "--name=foo", "--source=.")).
				WithWorkdir("bar").
				With(daggerModuleExec("init", "--sdk=go", "--name=bar", "--source=.")).
				WithWorkdir("/work").
				WithNewFile("dagger.json", tc.daggerjson)

			daggerjson, err := ctr.
				With(daggerModuleExec(tc.updateCmd...)).
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
