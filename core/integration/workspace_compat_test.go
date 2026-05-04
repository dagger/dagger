package core

// Workspace alignment: aligned structurally, but coverage is still incomplete.
// Scope: Legacy dagger.json shapes that imply a compat workspace, plus compat warnings, migration seams, and direct-load errors.
// Intent: Own the legacy-to-workspace seam explicitly and finish the missing compat detection and migration cases.

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
		t.Fatal(`FIXME: implement compat detection coverage for legacy blueprint configs.`)
	})

	t.Run("toolchains config creates a compat workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat detection coverage for legacy toolchain configs.`)
	})

	t.Run("non-dot source creates a compat workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat detection coverage for legacy non-dot source configs.`)
	})

	t.Run("sdk-only root source does not create a compat workspace", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement the negative compat detection case for sdk-only root-source modules.`)
	})

	t.Run("native workspace config suppresses compat inference", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement native-config-wins coverage.

Create a repo with both a compat-eligible dagger.json and an initialized native
workspace config. Verify runtime selection uses the native workspace and does
not infer a CompatWorkspace from dagger.json.`)
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
	require.Contains(t, string(out), "No workspace config found, inferring from dagger.json. Run 'dagger migrate' soon.")
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
		t.Fatal(`FIXME: implement nested local legacy workspace source error coverage.

Point a workspace module source at another local legacy workspace and verify
the error tells the user to run dagger migrate there and retarget one of its
migrated modules.`)
	})

	t.Run("remote workspace module source requires a migrated upstream", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement nested remote legacy workspace source error coverage.

Point a workspace module source at a remote legacy workspace and verify the
error clearly says a migrated upstream ref is required.`)
	})

	t.Run("explicit -W loads a compat workspace successfully", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement explicit compat opt-in coverage.

Use dagger -W against a compat-eligible legacy project and verify it loads the
CompatWorkspace successfully instead of hitting the direct-load error path.`)
	})
}

// TestCompatMigration should cover the explicit handoff from compat runtime to
// workspace migration.
func (WorkspaceCompatSuite) TestCompatMigration(ctx context.Context, t *testctx.T) {
	t.Run("migrate converts a compat workspace into workspace config plus modules", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat migration coverage.

Run dagger migrate -y on a compat-eligible project and verify the legacy
dagger.json is replaced by .dagger/config.toml plus migrated module files.`)
	})

	t.Run("migrate is a no-op for sdk-only root-source modules", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat migration no-op coverage for sdk-only root-source modules.`)
	})

	t.Run("migrate writes a migration report for unsupported gaps", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement compat migration gap-report coverage.

Verify gap warnings are surfaced to the user and .dagger/migration-report.md is
written when manual follow-up is required.`)
	})
}

// TestCompatAndMigratedWorkspaceMatch should prove the core contract of the
// new design: compat mode and migrated workspace mode expose the same runtime
// behavior for the same legacy project.
func (WorkspaceCompatSuite) TestCompatAndMigratedWorkspaceMatch(ctx context.Context, t *testctx.T) {
	t.Fatal(`FIXME: implement compat-vs-migrated equivalence coverage.

For the same legacy project, compare a compat-backed invocation with the same
project after dagger migrate -y. Verify they expose the same entrypoint and
module behavior.`)
}
