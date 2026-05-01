package core

// Scope: Legacy dagger.json shapes that imply a compat workspace, plus compat warnings, migration seams, and direct-load errors.
// Intent: Own the legacy-to-workspace behavior explicitly.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// WorkspaceCompatSuite owns legacy dagger.json behavior and other legacy
// config inputs, like .env, that only exist for compat workspaces: detection,
// warnings, migration, and direct-load errors. This suite should answer "does
// this legacy project become a workspace, and what compat rules apply?"
// rather than generic module-loading arbitration.
//
// Native runtime loading and entrypoint arbitration belong in
// module_loading_test.go. Compat runtime equivalence tests belong here only as
// seam coverage between compat inference and native workspace behavior.
type WorkspaceCompatSuite struct{}

func TestWorkspaceCompat(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceCompatSuite{})
}

func compatDaggerExec(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func compatDaggerExecFail(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report"}, args...), dagger.ContainerWithExecOpts{
			ExperimentalPrivilegedNesting: true,
			Expect:                        dagger.ReturnTypeFailure,
		})
	}
}

func compatDaggerCall(args ...string) dagger.WithContainerFunc {
	return func(c *dagger.Container) *dagger.Container {
		return c.WithExec(append([]string{"dagger", "--progress=report", "call"}, args...), dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		})
	}
}

func legacyDangModule(dir, name, typeName, message string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithNewFile(dir+"/dagger.json", `{"name":"`+name+`","sdk":{"source":"dang"}}`).
			WithNewFile(dir+"/main.dang", `
type `+typeName+` {
  pub message: String! {
    "`+message+`"
  }
}
`)
	}
}

func legacyCompatDangSource(t testing.TB, c *dagger.Client, message string) *dagger.Container {
	t.Helper()

	return legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! {
    "`+message+`"
  }
}
`)
	})
}

func legacySDKOnlyDangSource(t testing.TB, c *dagger.Client, message string) *dagger.Container {
	t.Helper()

	return legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"}
}`, func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithNewFile("main.dang", `
type Myapp {
  pub greet: String! {
    "`+message+`"
  }
}
`)
	})
}

func legacyCompatRemoteRef(ctx context.Context, t *testctx.T, c *dagger.Client, content *dagger.Directory) string {
	t.Helper()

	gitSrv, _ := gitSmartHTTPServiceDirAuth(ctx, t, c, "", makeGitDir(c, content, "main"), "", nil)
	gitSrv, err := gitSrv.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = gitSrv.Stop(ctx) })

	shortHost, err := gitSrv.Hostname(ctx)
	require.NoError(t, err)

	getentOut, err := c.Container().From(alpineImage).
		WithExec([]string{"getent", "hosts", shortHost}).
		Stdout(ctx)
	require.NoError(t, err, "could not resolve git service hostname %q", shortHost)

	fields := strings.Fields(getentOut)
	require.NotEmpty(t, fields, "unexpected getent output: %q", getentOut)
	return "http://" + fields[0] + "/repo.git@main"
}

func legacyBlueprintTestEnv(t *testctx.T, c *dagger.Client) *dagger.Container {
	return c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "init"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithDirectory(".", c.Host().Directory("./testdata/test-blueprint")).
		WithDirectory("app", c.Directory())
}

// TestLegacyBlueprintInit replaces the old blueprint_test.go coverage.
// It should pin down what legacy --blueprint init still supports, and what it
// should reject, now that blueprint is a compatibility concept rather than a
// current workspace feature.
func (WorkspaceCompatSuite) TestLegacyBlueprintInit(ctx context.Context, t *testctx.T) {
	t.Run("local legacy blueprint init still works", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := legacyBlueprintTestEnv(t, c).
			WithWorkdir("app").
			With(daggerModuleExec("init", "--blueprint=../hello"))

		out, err := modGen.
			With(daggerExec("call", "message")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from blueprint")

		blueprintConfig, err := modGen.
			With(daggerExec("call", "blueprint-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, blueprintConfig, "this is the blueprint configuration")

		modGen = modGen.WithNewFile("app-config.txt", "this is the app configuration")
		appConfig, err := modGen.
			With(daggerExec("call", "app-config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, appConfig, "this is the app configuration")
	})

	t.Run("legacy blueprint init covers dependency-bearing and multi-sdk cases", func(ctx context.Context, t *testctx.T) {
		type testCase struct {
			name          string
			blueprintPath string
		}

		for _, tc := range []testCase{
			{
				name:          "use a blueprint which has a dependency",
				blueprintPath: "../myblueprint-with-dep",
			},
			{
				name:          "init with typescript blueprint",
				blueprintPath: "../myblueprint-ts",
			},
			{
				name:          "init with python blueprint",
				blueprintPath: "../myblueprint-py",
			},
		} {
			t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)
				modGen := legacyBlueprintTestEnv(t, c).
					WithWorkdir("app").
					With(daggerModuleExec("init", "--blueprint="+tc.blueprintPath))

				out, err := modGen.
					With(daggerExec("call", "hello")).
					Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, out, "hello from blueprint")
			})
		}
	})

	t.Run("legacy blueprint init still rejects --sdk with --blueprint", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		modGen := legacyBlueprintTestEnv(t, c).
			WithWorkdir("app").
			WithExec(
				[]string{"dagger", "module", "init", "--sdk=go", "--blueprint=../myblueprint"},
				dagger.ContainerWithExecOpts{
					ExperimentalPrivilegedNesting: true,
					Expect:                        dagger.ReturnTypeFailure,
				},
			)

		stderr, err := modGen.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "--sdk")
		require.Contains(t, stderr, "--blueprint")
	})
}

func (WorkspaceCompatSuite) TestLegacyToolchainCompat(ctx context.Context, t *testctx.T) {
	t.Run("customization default changes invalidate omitted arg cache", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		selected := func(out string) string {
			lines := strings.Split(strings.TrimSpace(out), "\n")
			if len(lines) == 0 {
				return ""
			}
			return strings.TrimSpace(lines[len(lines)-1])
		}
		daggerJSON := func(defaultValue string) string {
			return fmt.Sprintf(`{
  "name": "default-cache-repro",
  "engineVersion": "v0.20.6",
  "toolchains": [
    {
      "name": "probe",
      "source": "tool",
      "customizations": [
        {
          "argument": "value",
          "default": %q
        }
      ]
    }
  ]
}`, defaultValue)
		}

		base := goGitBase(t, c).
			WithWorkdir("/work/tool").
			With(daggerModuleExec("init", "--sdk=go", "--name=probe", "--source=.")).
			WithNewFile("main.go", `package main

type Probe struct {
	Value string
}

func New(
	// +optional
	value string,
) *Probe {
	return &Probe{Value: value}
}

func (m *Probe) Selected() string {
	return m.Value
}
`)

		alpha := base.
			WithWorkdir("/work").
			WithNewFile("dagger.json", daggerJSON("alpha"))

		out, err := alpha.
			With(daggerExecRaw("--silent", "call", "probe", "selected")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "alpha", selected(out))

		beta := alpha.WithNewFile("dagger.json", daggerJSON("beta"))

		out, err = beta.
			With(daggerExecRaw("--silent", "call", "probe", "selected")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "beta", selected(out))

		out, err = beta.
			With(daggerExecRaw("--silent", "call", "probe", "--value", "beta", "selected")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "beta", selected(out))
	})
}

// TestCompatDetection should lock down which legacy dagger.json files become a
// compat workspace and which do not.
func (WorkspaceCompatSuite) TestCompatDetection(ctx context.Context, t *testctx.T) {
	t.Run("blueprint config creates a compat workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "app",
  "blueprint": {
    "name": "blueprint",
    "source": "./blueprint"
  }
}`, legacyDangModule("blueprint", "blueprint", "Blueprint", "hello from blueprint"))

		out, err := ctr.With(compatDaggerCall("message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from blueprint", strings.TrimSpace(out))
	})

	t.Run("toolchains config creates a compat workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "app",
  "toolchains": [
    {
      "name": "toolchain",
      "source": "./toolchain"
    }
  ]
}`, legacyDangModule("toolchain", "toolchain", "Toolchain", "hello from toolchain"))

		out, err := ctr.With(compatDaggerCall("toolchain", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from toolchain", strings.TrimSpace(out))
	})

	t.Run("non-dot source creates a compat workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyCompatDangSource(t, c, "hello from compat source")

		out, err := ctr.With(compatDaggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from compat source", strings.TrimSpace(out))
	})

	t.Run("sdk-only root source does not create a compat workspace", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacySDKOnlyDangSource(t, c, "hello from root source")

		out, err := ctr.With(compatDaggerCall("greet")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "hello from root source")
		require.NotContains(t, out, "inferring from dagger.json")
	})

	t.Run("native workspace config suppresses compat inference", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithNewFile("dagger.json", `{
  "name": "legacy",
  "sdk": {"source": "dang"},
  "source": "legacy-src"
}`).
			WithNewFile("legacy-src/main.dang", `
type Legacy {
  pub greet: String! {
    "hello from legacy module"
  }
}
`).
			WithNewFile(".dagger/config.toml", `[modules.native]
source = "modules/native"
`).
			WithNewFile(".dagger/modules/native/dagger.json", `{"name":"native","sdk":{"source":"dang"}}`).
			WithNewFile(".dagger/modules/native/main.dang", `
type Native {
  pub greet: String! {
    "hello from native workspace"
  }
}
`)

		out, err := ctr.With(compatDaggerCall("native", "greet")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "hello from native workspace")
		require.NotContains(t, out, "inferring from dagger.json")
	})
}

// TestCompatWarning should pin down the user-facing warning emitted when the
// engine infers workspace behavior from a legacy dagger.json.
func (WorkspaceCompatSuite) TestCompatWarning(ctx context.Context, t *testctx.T) {
	workdir := t.TempDir()
	blueprintDir := filepath.Join(workdir, "blueprint")
	require.NoError(t, os.MkdirAll(blueprintDir, 0o755))

	_, err := hostDaggerModuleExec(ctx, t, blueprintDir, "init", "--sdk=go", "--name=hello")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(blueprintDir, "main.go"), []byte(`package main

import "context"

type Hello struct{}

func (m *Hello) Greet(ctx context.Context) string {
	return "hello from blueprint"
}
`), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.json"), []byte(`{
  "name": "app",
  "blueprint": {
    "name": "hello",
    "source": "./blueprint"
  }
}`), 0o644))

	out, err := hostDaggerExec(ctx, t, workdir, "--silent", "call", "greet")
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "No workspace config found, inferring from dagger.json.\nRun 'dagger migrate' when ready.")
	require.Contains(t, string(out), "hello from blueprint")
}

// TestLegacyWorkspaceDirectLoadErrors should cover the new hard failures when
// legacy workspace concepts are used through generic module loading.
func (WorkspaceCompatSuite) TestLegacyWorkspaceDirectLoadErrors(ctx context.Context, t *testctx.T) {
	t.Run("direct load tells the user to use -W", func(ctx context.Context, t *testctx.T) {
		workdir := t.TempDir()
		initGitRepo(ctx, t, workdir)

		require.NoError(t, os.MkdirAll(filepath.Join(workdir, "toolchains", "go"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(workdir, "dagger.json"), []byte(`{
  "name": "app",
  "toolchains": [
    {
      "name": "go",
      "source": "./toolchains/go"
    }
  ]
}`), 0o644))

		_, err := hostDaggerExec(ctx, t, workdir, "--silent", "functions", "-m", ".")
		require.Error(t, err)
		requireErrOut(t, err, "This module's dagger.json uses toolchains or blueprints, which have moved to workspaces.\n\nTry: dagger -W .\n\nTo learn more: https://docs.dagger.io/reference/upgrade-to-workspaces")
	})

	t.Run("local workspace module source tells the user to migrate that project", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := workspaceBase(t, c).
			WithNewFile(".dagger/config.toml", `[modules.legacy]
source = "../legacy"
`).
			WithNewFile("legacy/dagger.json", `{
  "name": "legacy",
  "blueprint": {
    "name": "blueprint",
    "source": "./blueprint"
  }
}`).
			With(legacyDangModule("legacy/blueprint", "blueprint", "Blueprint", "hello from nested blueprint"))

		out, err := ctr.With(compatDaggerExecFail("call", "legacy", "message")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "points at a legacy workspace, not a plain module")
		require.Contains(t, out, `uses legacy workspace fields "blueprint"`)
		require.Contains(t, out, "run `dagger migrate` in")
		require.Contains(t, out, ".dagger/modules")
	})

	t.Run("remote workspace module source requires a migrated upstream", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		remoteRef := legacyCompatRemoteRef(ctx, t, c, c.Directory().
			WithNewFile("dagger.json", `{
  "name": "legacy",
  "blueprint": {
    "name": "blueprint",
    "source": "./blueprint"
  }
}`).
			WithNewFile("blueprint/dagger.json", `{"name":"blueprint","sdk":{"source":"dang"}}`).
			WithNewFile("blueprint/main.dang", `
type Blueprint {
  pub message: String! {
    "hello from remote blueprint"
  }
}
`))

		ctr := workspaceBase(t, c).
			WithNewFile(".dagger/config.toml", `[modules.legacy]
source = "`+remoteRef+`"
`)

		out, err := ctr.With(compatDaggerExecFail("call", "legacy", "message")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "points at a legacy workspace, not a plain module")
		require.Contains(t, out, `uses legacy workspace fields "blueprint"`)
		require.Contains(t, out, "use a migrated ref that points at one of its real modules")
		require.Contains(t, out, "migrate it first")
	})

	t.Run("explicit -W loads a compat workspace successfully", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyCompatDangSource(t, c, "hello from explicit workspace")

		out, err := ctr.WithExec([]string{"dagger", "--progress=report", "-W", ".", "call", "greet"}, dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from explicit workspace", strings.TrimSpace(out))
	})
}

// TestCompatMigration should cover the explicit handoff from compat runtime to
// workspace migration.
func (WorkspaceCompatSuite) TestCompatMigration(ctx context.Context, t *testctx.T) {
	t.Run("migrate converts a compat workspace into workspace config plus modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyCompatDangSource(t, c, "hello from migrated compat").
			With(compatDaggerExec("migrate", "-y"))

		_, err := ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.Error(t, err, "root dagger.json should be removed after migration")

		configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.myapp]`)
		require.Contains(t, configOut, `source = "modules/myapp"`)
		require.Contains(t, configOut, `entrypoint = true`)

		moduleOut, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, moduleOut, `"name": "myapp"`)
		require.Contains(t, moduleOut, `"source": "../../../ci"`)

		out, err := ctr.With(compatDaggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from migrated compat", strings.TrimSpace(out))
	})

	t.Run("migrate is a no-op for sdk-only root-source modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacySDKOnlyDangSource(t, c, "hello from sdk-only root")

		out, err := ctr.With(compatDaggerExec("migrate")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "No migration needed.")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "sdk-only dagger.json should remain in place")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/config.toml"}).Sync(ctx)
		require.Error(t, err, "sdk-only modules should not create workspace config")
	})

	t.Run("migrate writes a migration report for unsupported gaps", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {
      "name": "toolchain",
      "source": "./toolchain",
      "customizations": [
        {
          "argument": "src",
          "defaultPath": "./custom-config.txt",
          "ignore": ["node_modules"]
        },
        {
          "function": ["build"],
          "argument": "tag",
          "default": "dev"
        }
      ]
    }
  ]
}`, legacyDangModule("toolchain", "toolchain", "Toolchain", "hello from toolchain")).
			With(compatDaggerExec("migrate", "-y"))

		output, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, output)
		require.Contains(t, output, "Warning: 2 migration gap(s) need manual review; see .dagger/migration-report.md")

		report, err := ctr.WithExec([]string{"cat", ".dagger/migration-report.md"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, report, "# Migration Report")
		require.Contains(t, report, "Module `toolchain`")
		require.Contains(t, report, `constructor arg "src" has 'ignore' and 'defaultPath' customization`)
		require.Contains(t, report, `function customization for "build" could not be migrated automatically`)
	})
}

// TestCompatAndMigratedWorkspaceMatch should prove the core contract of the
// new design: compat mode and migrated workspace mode expose the same runtime
// behavior for the same legacy project.
func (WorkspaceCompatSuite) TestCompatAndMigratedWorkspaceMatch(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci",
  "toolchains": [
    {
      "name": "helper",
      "source": "./helper"
    }
  ]
}`, func(ctr *dagger.Container) *dagger.Container {
		return ctr.
			WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! {
    "hello from entrypoint"
  }
}
`).
			WithNewFile("helper/dagger.json", `{"name":"helper","sdk":{"source":"dang"}}`).
			WithNewFile("helper/main.dang", `
type Helper {
  pub message: String! {
    "hello from helper"
  }
}
`)
	})

	compatEntrypoint, err := base.With(compatDaggerCall("greet")).Stdout(ctx)
	require.NoError(t, err)
	compatHelper, err := base.With(compatDaggerCall("helper", "message")).Stdout(ctx)
	require.NoError(t, err)

	migrated := base.With(compatDaggerExec("migrate", "-y"))
	migratedEntrypoint, err := migrated.With(compatDaggerCall("greet")).Stdout(ctx)
	require.NoError(t, err)
	migratedHelper, err := migrated.With(compatDaggerCall("helper", "message")).Stdout(ctx)
	require.NoError(t, err)

	require.Equal(t, strings.TrimSpace(compatEntrypoint), strings.TrimSpace(migratedEntrypoint))
	require.Equal(t, strings.TrimSpace(compatHelper), strings.TrimSpace(migratedHelper))
}
