package core

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func (CLISuite) TestModuleDevelop(ctx context.Context, t *testctx.T) {
	t.Run("name and sdk", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerModuleExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) Fn(ctx context.Context) string {
				return "hi from dep"
			}
			`,
			).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--source=.")).
			With(daggerModuleExec("install", "./dep"))

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
					With(daggerExecRaw("develop", "--sdk", "go", "--source", sourceDir)).
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
				_, err = ctr.With(daggerExecRaw("develop", "--sdk", "python")).Sync(ctx)
				requireErrOut(t, err, `cannot update module SDK that has already been set to "go"`)

				_, err = ctr.With(daggerExecRaw("develop", "--source", "blahblahblaha/blah")).Sync(ctx)
				requireErrOut(t, err, `cannot update module source path that has already been set to "cool/subdir"`)
			})
		}
	})

	t.Run("source is made rel to source root by engine", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := goGitBase(t, c).
			WithWorkdir("/work/dep").
			With(daggerModuleExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", `package main

			import "context"

			type Dep struct {}

			func (m *Dep) Fn(ctx context.Context) string {
				return "hi from dep"
			}
			`,
			).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--source=.")).
			With(daggerModuleExec("install", "./dep")).
			WithWorkdir("/var").
			With(daggerExecRaw("develop", "-m", "../work", "--source=../work/some/subdir", "--sdk=go")).
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
				With(daggerExecRaw("develop", "-m", testGitModuleRef(tc, "top-level"))).
				Sync(ctx)
			requireErrRegexp(t, err, `module source ".*" kind must be "local", got "git"`)
		})
	})

	t.Run("source is made rel to source root by engine with absolute path", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		absPath := "/work"
		ctr := goGitBase(t, c).
			WithWorkdir(absPath+"/dep").
			With(daggerModuleExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithNewFile(absPath+"/dep/main.go", `package main

        import "context"

        type Dep struct {}

        func (m *Dep) Fn(ctx context.Context) string {
            return "hi from dep"
        }
        `,
			).
			WithWorkdir(absPath).
			With(daggerModuleExec("init", "--source=.")).
			With(daggerModuleExec("install", "./dep")).
			WithWorkdir("/var").
			With(daggerExecRaw("develop", "-m", absPath, "--source="+absPath+"/some/subdir", "--sdk=go")).
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
			With(daggerModuleExec("init", "--source=.", "--name=dep", "--sdk=go")).
			WithExec([]string{"rm", "dagger.gen.go"}).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--source=.", "--sdk=go")).
			With(daggerModuleExec("install", "--name=cooldep", "./dep")).
			WithExec([]string{"rm", "dagger.gen.go"})
		developed := base.With(daggerExecRaw("develop", "--recursive"))

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

func (CLISuite) TestModuleDevelopDeterministicCodegen(ctx context.Context, t *testctx.T) {
	// Test that running codegen multiple times produces identical output.
	// This is critical for version control - we want to be able to commit
	// generated files and know they won't change unless the API changes.
	t.Run("go split methods across files", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("/work/main.go", `package main

type GitRepo struct{}

func (m *GitRepo) RemoteA() *RemoteA {
	return &RemoteA{}
}

func (m *GitRepo) RemoteB() *RemoteB {
	return &RemoteB{}
}
`,
			).
			WithNewFile("/work/remote_a.go", `package main

type RemoteA struct{}
`,
			).
			WithNewFile("/work/remote_b.go", `package main

type RemoteB struct{}
`,
			)

		modGen = modGen.With(daggerExecRaw("develop"))
		firstGen, err := modGen.File("/work/dagger.gen.go").Contents(ctx)
		require.NoError(t, err)

		modGen = modGen.With(daggerExecRaw("develop"))
		secondGen, err := modGen.File("/work/dagger.gen.go").Contents(ctx)
		require.NoError(t, err)

		require.Equal(t, firstGen, secondGen, "Generated code should be deterministic across multiple runs")
	})

	t.Run("go with dependencies", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Set up a module with dependencies to test case ordering
		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/dep").
			With(daggerModuleExec("init", "--name=dep", "--sdk=go")).
			WithNewFile("/work/dep/main.go", `package main

import "context"

type Dep struct {}

func (m *Dep) Method1(ctx context.Context) string {
	return "method1"
}

func (m *Dep) Method2(ctx context.Context) string {
	return "method2"
}

func (m *Dep) Method3(ctx context.Context) string {
	return "method3"
}
`,
			).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithNewFile("/work/main.go", `package main

import "context"

type Test struct {}

func (m *Test) UsesDep(ctx context.Context) (string, error) {
	return dag.Dep().Method2(ctx)
}
`,
			).
			With(daggerModuleExec("install", "./dep"))

		// Generate code the first time
		modGen = modGen.With(daggerExecRaw("develop"))
		firstGen, err := modGen.File("/work/dagger.gen.go").Contents(ctx)
		require.NoError(t, err)

		// Generate code a second time - should be identical
		modGen = modGen.With(daggerExecRaw("develop"))
		secondGen, err := modGen.File("/work/dagger.gen.go").Contents(ctx)
		require.NoError(t, err)

		// The generated code should be byte-for-byte identical
		require.Equal(t, firstGen, secondGen, "Generated code should be deterministic across multiple runs")
	})
}
