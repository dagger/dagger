package core

// Workspace alignment: aligned; this file already matches the workspace-era split.
// Scope: Explicit module init CLI behavior, including license and Git initialization paths.
// Intent: Keep module initialization behavior separate from workspace init and legacy aliases.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Module init coverage stays separate from workspace init coverage, which is
// owned by the workspace-specific suites.
func (CLISuite) TestModuleInit(ctx context.Context, t *testctx.T) {
	t.Run("name defaults to source root dir name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--source=coolmod", "--sdk=go", "coolmod")).
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
					With(daggerModuleExec("init", "--name=test", "--sdk="+tc.sdk)).
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
			With(daggerModuleExec("init", "--source=../work/some/subdir", "--name=test", "--sdk=go", "../work")).
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
			With(daggerModuleExec("init", "--name=a", "--sdk=go", ".")).
			WithWorkdir("/work/subdir").
			With(daggerModuleExec("init", "--name=b", "--sdk=go", "--source=.", ".")).
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
			With(daggerModuleExec("init", "--source="+absPath, "--name=test", "--sdk=go", absPath)).
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
			With(daggerModuleExec("init", "--source=.", "--name=test", "--sdk=go", absPath))

		_, err := ctr.Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "source subpath \"../..\" escapes source root")
	})
}

func (CLISuite) TestModuleInitLicense(ctx context.Context, t *testctx.T) {
	t.Run("does not create LICENSE file on init with sdk", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--name=licensed-to-ill", "--sdk=go"))

		files, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, files, "LICENSE")
	})

	t.Run("license=false is a no-op", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--name=empty-license", "--sdk=go", "--license=false"))

		files, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, files, "LICENSE")
	})

	t.Run("license=true fails with deprecation error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--name=no-license", "--sdk=go", "--license=true"))

		_, err := modGen.Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "--license is deprecated and no longer generates a LICENSE file; create one manually")
	})

	t.Run("license values other than false also fail with deprecation error", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--name=no-license", "--sdk=go", "--license=MIT"))

		_, err := modGen.Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "--license is deprecated and no longer generates a LICENSE file; create one manually")
	})

	t.Run("does not create LICENSE file on develop with sdk", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerModuleExec("init", "--name=no-license", "--source=.")).
			With(daggerExecRaw("develop", "--sdk=go"))

		files, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, files, "LICENSE")
	})
}

func (CLISuite) TestModuleInitGit(ctx context.Context, t *testctx.T) {
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
				With(daggerModuleExec("init", "--name=bare", "--sdk="+tc.sdk))

			out, err := modGen.
				With(daggerQuery(`{containerEcho(stringArg:"hello"){stdout}}`)).
				Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"containerEcho":{"stdout":"hello\n"}}`, out)

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
					With(daggerModuleExec("init", "--name=bare", "--source=."))

				// TODO: make this configurable
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
					With(daggerExecRaw("develop", "--sdk=go"))

				_, err = modGen.File(".gitignore").Contents(ctx)
				requireErrOut(t, err, "no such file or directory")
			})
		})
	}
}
