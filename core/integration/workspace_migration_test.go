package core

// These tests cover workspace migration for legacy projects. They verify both
// dry-run planning and applying the generated changes.
//
// See also:
// - workspace_compat_test.go: detecting and running legacy compat workspaces.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// WorkspaceMigrationSuite owns explicit workspace migration behavior through
// dagger migrate.
type WorkspaceMigrationSuite struct{}

func TestWorkspaceMigration(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceMigrationSuite{})
}

// TestWorkspaceMigratePreviewAndApply should cover the main CLI lifecycle now
// that migrate is preview-by-default and apply-with-yes.
func (WorkspaceMigrationSuite) TestWorkspaceMigratePreviewAndApply(ctx context.Context, t *testctx.T) {
	t.Run("preview reports changes without applying them", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! {
    "hello from preview"
  }
}
`)
		})

		preview := ctr.WithExec([]string{"dagger", "--progress=report", "query"}, dagger.ContainerWithExecOpts{
			Stdin: `{
  currentWorkspace {
    migrate {
      changes {
        isEmpty
        diffStats {
          path
        }
      }
    }
  }
}`,
			ExperimentalPrivilegedNesting: true,
		})
		out, err := preview.Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"isEmpty": false`)
		require.Contains(t, out, `"path": ".dagger/config.toml"`)
		require.Contains(t, out, `"path": ".dagger/modules/myapp/dagger.json"`)

		_, err = preview.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "preview should leave the legacy config on disk")

		_, err = preview.WithExec([]string{"test", "-f", ".dagger/config.toml"}).Sync(ctx)
		require.Error(t, err, "preview should not write workspace config")

		_, err = preview.WithExec([]string{"test", "-f", ".dagger/modules/myapp/dagger.json"}).Sync(ctx)
		require.Error(t, err, "preview should not write migrated module config")
	})

	t.Run("preview does not write planned lockfile", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		source := "github.com/dagger/dagger/modules/wolfi@main"
		pin := strings.Repeat("1", 40)

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {"name": "wolfi", "source": "`+source+`", "pin": "`+pin+`"}
  ]
}`)

		preview := ctr.WithExec([]string{"dagger", "--progress=report", "query"}, dagger.ContainerWithExecOpts{
			Stdin: `{
  currentWorkspace {
    migrate(force: true) {
      changes {
        diffStats {
          path
        }
      }
    }
  }
}`,
			ExperimentalPrivilegedNesting: true,
		})
		out, err := preview.Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"path": ".dagger/lock"`)

		_, err = preview.WithExec([]string{"test", "-f", ".dagger/lock"}).Sync(ctx)
		require.Error(t, err, "preview should not write the planned lockfile")
	})

	t.Run("apply writes workspace config and migrated modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		migrateApply := daggerExec("migrate", "-y")

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! {
    "hello from migrated source"
  }
}
`)
		}).With(migrateApply)

		_, err := ctr.WithExec([]string{"test", "-d", "ci"}).Sync(ctx)
		require.NoError(t, err, "source directory should remain available after migration")

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `"name": "myapp"`)

		configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, "modules/myapp")

		out, err := ctr.With(daggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from migrated source", strings.TrimSpace(out))

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.Error(t, err, "root dagger.json should have been removed")
	})
}

// TestWorkspaceMigrateOutcomes should cover the main result classes of a
// migration.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateOutcomes(ctx context.Context, t *testctx.T) {
	t.Run("non-local source stays in place behind moved config", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! {
    "hello from original source"
  }
}
`)
		}).With(daggerExec("migrate", "-y"))

		_, err := ctr.WithExec([]string{"test", "-f", "ci/main.dang"}).Sync(ctx)
		require.NoError(t, err, "source file should remain in its original directory")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/myapp/main.dang"}).Sync(ctx)
		require.Error(t, err, "source file should not be copied to the migrated module config directory")

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `"source": "../../../ci"`)

		out, err := ctr.With(daggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from original source", strings.TrimSpace(out))
	})

	t.Run("sdk-only config migrates before install", func(ctx context.Context, t *testctx.T) {
		// The allowed path for an SDK-only dagger.json is explicit migration
		// first, then workspace mutation. Migration creates the parent native
		// workspace config and preserves the root module, so a later `dagger
		// install` can safely add dependencies to .dagger/config.toml.
		c := connect(ctx, t)
		ctr := legacySDKOnlyGoSource(t, c, "hello from root source").
			With(legacyDangModule("dep", "dep", "Dep", "hello from dep")).
			With(daggerExec("migrate", "-y"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "Warning: Root module requires explicit loading. If your scripts rely on implicit loading, change them to `dagger -m . ...`.")

		_, err = ctr.WithExec([]string{"test", "-f", "main.go"}).Sync(ctx)
		require.NoError(t, err, "source file should remain at root")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "legacy dagger.json should remain in place")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/config.toml"}).Sync(ctx)
		require.NoError(t, err, "root parent workspace config should be created")

		configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.go-sdk]`)
		require.Contains(t, configOut, `source = "github.com/dagger/go-sdk"`)

		reportOut, err := ctr.WithExec([]string{"cat", ".dagger/migration-report.md"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, reportOut, "## Root module requires explicit loading")
		require.Contains(t, reportOut, "**This works**: `dagger -m . call --help`")
		require.Contains(t, reportOut, "**This no longer works**: `dagger call --help`")

		callOut, err := ctr.With(daggerCallAt(".", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from root source", strings.TrimSpace(callOut))

		ctr = ctr.With(daggerExec("install", "./dep"))
		installOut, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, installOut)
		require.Contains(t, installOut, `Installed module "dep" in /work/.dagger/config.toml`)

		configOut, err = ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.dep]`)
		require.Contains(t, configOut, `source = "../dep"`)
	})

	t.Run("remote refs refresh lock entries", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		source := "github.com/dagger/dagger/modules/wolfi@main"
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {"name": "tc", "source": "`+source+`"}
  ]
}`).With(daggerExec("migrate", "-y"))

		lockOut, err := ctr.File("/work/.dagger/lock").Contents(ctx)
		require.NoError(t, err)
		assertModuleResolveLockEntry(t, []byte(lockOut), source, workspace.PolicyFloat)
	})

	t.Run("toolchains are marked with legacy default path", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		toolchainSrc := filepath.Join("testdata", "modules", "go", "defaults")

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {"name": "defaults", "source": "./toolchain"}
  ]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithDirectory("toolchain", c.Host().Directory(toolchainSrc))
		}).With(daggerExec("migrate", "-y"))

		configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, strings.Join([]string{
			"[modules.defaults]",
			`source = "../toolchain"`,
			"legacy-default-path = true",
		}, "\n"))
	})

	t.Run("local migrated modules include commented setting hints", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		toolchainSrc := filepath.Join("testdata", "modules", "go", "defaults")

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {"name": "defaults", "source": "./toolchain"}
  ]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithDirectory("toolchain", c.Host().Directory(toolchainSrc))
		}).With(daggerExec("migrate", "-y"))

		configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.defaults]`)
		require.Contains(t, configOut, `# settings.greeting = "hello"`)
		require.Contains(t, configOut, `# settings.password = "env://MY_SECRET"`)
		require.NotContains(t, configOut, `# string`)
		require.NotContains(t, configOut, `# Secret`)
	})

	t.Run("settings hints skip args not configurable from workspace settings", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		toolchainSrc := filepath.Join("testdata", "modules", "go", "defaults")

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {"name": "defaults", "source": "./toolchain"}
  ]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithDirectory("toolchain", c.Host().Directory(toolchainSrc)).
				WithNewFile("toolchain/main.go", `package main

import "dagger/defaults/internal/dagger"

type Defaults struct{}

func New(
	// Greeting to use.
	// +default="hello"
	greeting string,
	// Secret reference.
	// +optional
	secret *dagger.Secret,
	// Source directory.
	// +optional
	dir *dagger.Directory,
	// Workspace is injected by Dagger.
	workspace *dagger.Workspace,
	// Cache volume cannot be resolved from workspace settings.
	cache *dagger.CacheVolume,
) *Defaults {
	return &Defaults{}
}
`)
		}).With(daggerExec("migrate", "-y"))

		configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.defaults]`)
		require.Contains(t, configOut, `# settings.greeting = "hello"`)
		require.Contains(t, configOut, `# settings.secret = "env://MY_SECRET"`)
		require.Contains(t, configOut, `# settings.dir = "./path"`)
		require.NotContains(t, configOut, `settings.workspace`)
		require.NotContains(t, configOut, `settings.cache`)
	})

	t.Run("dot dagger source keeps toolchain and migrated main module hints", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		mainModuleSrc := filepath.Join("testdata", "modules", "go", "defaults", "superconstructor")
		toolchainSrc := filepath.Join("testdata", "modules", "go", "defaults")
		legacyConfig := `{
  "name": "superconstructor",
  "engineVersion": "v0.20.1",
  "sdk": {"source": "go"},
  "source": ".dagger",
  "toolchains": [
    {"name": "defaults", "source": "./toolchain"}
  ],
  "disableDefaultFunctionCaching": true
}`

		ctr := legacyWorkspaceBase(t, c, legacyConfig, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithDirectory(".dagger", c.Host().Directory(mainModuleSrc)).
				WithDirectory("toolchain", c.Host().Directory(toolchainSrc)).
				WithNewFile("dagger.json", legacyConfig)
		}).With(daggerExec("migrate", "-y"))

		configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.superconstructor]`)
		require.Contains(t, configOut, `# settings.count = 42`)
		require.Contains(t, configOut, `[modules.defaults]`)
		require.Contains(t, configOut, `# settings.greeting = "hello"`)
		require.NotContains(t, configOut, `# int`)
		require.NotContains(t, configOut, `# string`)
	})

	t.Run("failed migrated main module introspection requires force", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		mainModuleSrc := filepath.Join("testdata", "modules", "go", "defaults", "superconstructor")
		toolchainSrc := filepath.Join("testdata", "modules", "go", "defaults")
		legacyConfig := `{
  "name": "futureapp",
  "engineVersion": "v999.0.0",
  "sdk": {"source": "go"},
  "source": ".dagger",
  "toolchains": [
    {"name": "defaults", "source": "./toolchain"}
  ]
}`

		ctr := legacyWorkspaceBase(t, c, legacyConfig, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithDirectory(".dagger", c.Host().Directory(mainModuleSrc)).
				WithDirectory("toolchain", c.Host().Directory(toolchainSrc)).
				WithNewFile("dagger.json", legacyConfig)
		})

		failedOut, err := ctr.With(daggerExecFail("migrate", "-y")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, failedOut, `could not load modules to generate settings hints:`)
		require.Contains(t, failedOut, `could not generate workspace settings hints for module "futureapp"`)
		require.Contains(t, failedOut, `use --force to migrate anyway`)

		migrate := ctr.With(daggerExec("migrate", "-f", "-y"))
		stdout, err := migrate.Stdout(ctx)
		require.NoError(t, err)
		stderr, err := migrate.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout+stderr, `Warning: could not generate workspace settings hints for module "futureapp"`)

		configOut, err := migrate.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.defaults]`)
		require.Contains(t, configOut, `# settings.greeting = "hello"`)
		require.NotContains(t, configOut, `# string`)
	})

	t.Run("dot dagger source remains in place", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "./.dagger/"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile(".dagger/main.dang", `
type Myapp {
  pub greet: String! {
    "hello from dot dagger source"
  }
}
`).
				WithNewFile(".dagger/go.mod", "module example.com/myapp\n").
				WithNewFile(".dagger/modules/stale/old.txt", "legacy root content")
		}).With(daggerExec("migrate", "-y"))

		_, err := ctr.WithExec([]string{"test", "-f", ".dagger/config.toml"}).Sync(ctx)
		require.NoError(t, err)

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `"source": "../.."`)

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/myapp/main.dang"}).Sync(ctx)
		require.Error(t, err, "source file should not be copied to the migrated module config directory")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/myapp/go.mod"}).Sync(ctx)
		require.Error(t, err, "source metadata should not be copied to the migrated module config directory")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/main.dang"}).Sync(ctx)
		require.NoError(t, err, "source file should remain in place")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/go.mod"}).Sync(ctx)
		require.NoError(t, err, "source metadata should remain in place")

		_, err = ctr.WithExec([]string{"test", "-d", ".dagger/modules/stale"}).Sync(ctx)
		require.NoError(t, err, "existing source subtree should remain in place")

		out, err := ctr.With(daggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from dot dagger source", strings.TrimSpace(out))
	})
}

// TestWorkspaceMigrateUserFeedback should cover the user-facing output of
// explicit migration.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateUserFeedback(ctx context.Context, t *testctx.T) {
	withFreshMigrationProgress := func(ctr *dagger.Container) *dagger.Container {
		workdir := "/work-" + identity.NewID()
		return ctr.
			WithExec([]string{"mv", "/work", workdir}).
			WithWorkdir(workdir).
			WithEnvVariable("OTEL_BAGGAGE", "repeat-telemetry=true")
	}

	t.Run("summary is printed for applied migrations", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		t.Run("refreshing migrated remote refs", func(ctx context.Context, t *testctx.T) {
			ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {"name": "wolfi", "source": "github.com/dagger/dagger/modules/wolfi@main", "pin": "main"}
  ]
}`)

			migrate := ctr.
				With(withFreshMigrationProgress).
				With(daggerExec("migrate", "-y"))
			stdout, err := migrate.Stdout(ctx)
			require.NoError(t, err)
			stderr, err := migrate.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout+stderr, "prepare migration diff")
			require.Contains(t, stdout+stderr, "workspace configuration: .dagger/config.toml")
			require.Contains(t, stdout+stderr, "install module: github.com/dagger/dagger/modules/wolfi@main")
			require.NotContains(t, stdout+stderr, "Migrated to workspace format")
		})

		t.Run("general migration summary", func(ctx context.Context, t *testctx.T) {
			ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci",
  "dependencies": [
    {"name": "dep1", "source": "./lib/dep1"}
  ],
  "include": ["extra/"]
}`, func(ctr *dagger.Container) *dagger.Container {
				return ctr.
					WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! { "hi" }
}
`).
					WithNewFile("lib/dep1/dagger.json", `{
  "name": "dep1",
  "sdk": {"source": "dang"},
  "source": "."
}`).
					WithNewFile("lib/dep1/main.dang", `
type Dep1 {
  pub value: String! { "dep1" }
}
`)
			})

			migrate := ctr.
				With(withFreshMigrationProgress).
				With(daggerExec("migrate", "-y"))
			stdout, err := migrate.Stdout(ctx)
			require.NoError(t, err)
			stderr, err := migrate.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout+stderr, "prepare migration diff")
			require.Contains(t, stdout+stderr, "workspace configuration: .dagger/config.toml")
			require.Contains(t, stdout+stderr, "move module: dagger.json -> .dagger/modules/myapp/dagger.json")
			require.NotContains(t, stdout+stderr, "Migrated to workspace format")
		})
	})

	t.Run("migration report is written for unsupported gaps", func(ctx context.Context, t *testctx.T) {
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
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("toolchain/dagger.json", `{
  "name": "toolchain",
  "sdk": {"source": "dang"},
  "source": "."
}`).
				WithNewFile("toolchain/main.dang", `
type Toolchain {
  pub build(tag: String! = "dev"): String! {
    tag
  }
}
`)
		})

		migrate := ctr.
			With(withFreshMigrationProgress).
			With(daggerExec("migrate", "-y"))
		stdout, err := migrate.Stdout(ctx)
		require.NoError(t, err)
		stderr, err := migrate.Stderr(ctx)
		require.NoError(t, err)
		output := stdout + stderr
		require.Contains(t, output, "prepare migration diff")
		require.Contains(t, output, "workspace configuration: .dagger/config.toml")
		require.Contains(t, output, "install module: ./toolchain")
		require.Contains(t, output, "migration report: .dagger/migration-report.md")
		require.Contains(t, output, "Warning: 2 old setting(s) need review; see .dagger/migration-report.md")
		require.NotContains(t, output, "If you apply this migration, review .dagger/migration-report.md.")
		require.Equal(t, 1, strings.Count(output, "Warning: 2 old setting(s) need review; see .dagger/migration-report.md"))
		require.NotContains(t, output, "Migrated to workspace format")
	})

	t.Run("dot dagger source does not warn about skipped cleanup", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": ".dagger"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithNewFile(".dagger/main.dang", `
type Myapp {
  pub greet: String! { "hi" }
}
`)
		})

		migrate := ctr.With(daggerExec("migrate", "-y"))
		stdout, err := migrate.Stdout(ctx)
		require.NoError(t, err)
		stderr, err := migrate.Stderr(ctx)
		require.NoError(t, err)
		require.NotContains(t, stdout+stderr, `Warning: old source dir ".dagger" is ancestor of new location; skipped cleanup`)
	})
}

// TestWorkspaceMigrateScope should lock down what the migration actually uses
// as input.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateScope(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("migrates all workspace-ish configs from a nested run", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("dagger.json", `{
  "name": "outer",
  "sdk": {"source": "dang"},
  "source": "outer-src"
}`).
			WithNewFile("outer-src/main.dang", `
type Outer {
  pub greet: String! {
    "hello from outer source"
  }
}
`).
			WithNewFile("nested/dagger.json", `{
  "name": "inner",
  "sdk": {"source": "dang"},
  "source": "src"
}`).
			WithNewFile("nested/src/main.dang", `
type Inner {
  pub greet: String! {
    "hello from nested source"
  }
}
`).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "initial"}).
			WithWorkdir("/work/nested").
			With(daggerExec("migrate", "-y"))

		_, err := ctr.WithExec([]string{"test", "-f", ".dagger/config.toml"}).Sync(ctx)
		require.NoError(t, err, "nested compat workspace should be migrated")

		_, err = ctr.WithExec([]string{"test", "-f", "../dagger.json"}).Sync(ctx)
		require.Error(t, err, "outer legacy config should be migrated from the nested run")

		_, err = ctr.WithExec([]string{"test", "-f", "../.dagger/config.toml"}).Sync(ctx)
		require.NoError(t, err, "migration should write root workspace config")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.Error(t, err, "nested legacy config should be removed")

		rootConfigOut, err := ctr.WithExec([]string{"cat", "../.dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, rootConfigOut, `[modules.outer]`)
		require.NotContains(t, rootConfigOut, `[modules.inner]`)

		nestedConfigOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, nestedConfigOut, `[modules.inner]`)
		require.NotContains(t, nestedConfigOut, `[modules.outer]`)

		rootDjson, err := ctr.WithExec([]string{"cat", "../.dagger/modules/outer/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, rootDjson, `"source": "../../../outer-src"`)

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/inner/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `"source": "../../../src"`)
	})

	t.Run("migrates discovered child config without selected compat config", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("services/api/dagger.json", `{
  "name": "api",
  "sdk": {"source": "dang"},
  "source": "src"
}`).
			WithNewFile("services/api/src/main.dang", `
type Api {
  pub greet: String! {
    "hello from api"
  }
}
`).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "initial"}).
			With(daggerExec("migrate", "-y"))

		_, err := ctr.WithExec([]string{"test", "-f", "services/api/.dagger/config.toml"}).Sync(ctx)
		require.NoError(t, err, "child config should be migrated")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/config.toml"}).Sync(ctx)
		require.Error(t, err, "root workspace config is not needed for a workspace-ish child")

		_, err = ctr.WithExec([]string{"test", "-f", "services/api/dagger.json"}).Sync(ctx)
		require.Error(t, err, "child legacy config should be removed")

		configOut, err := ctr.WithExec([]string{"cat", "services/api/.dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.api]`)

		djson, err := ctr.WithExec([]string{"cat", "services/api/.dagger/modules/api/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `"source": "../../../src"`)
	})

	t.Run("migrates nested pinned refs into nested lockfile", func(ctx context.Context, t *testctx.T) {
		source := "github.com/dagger/dagger/modules/wolfi@main"
		pin := strings.Repeat("2", 40)

		ctr := workspaceBase(t, c).
			WithNewFile("services/api/dagger.json", `{
  "name": "api",
  "toolchains": [
    {"name": "wolfi", "source": "`+source+`", "pin": "`+pin+`"}
  ]
}`).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "initial"}).
			With(daggerExec("migrate", "-f", "-y"))

		_, err := ctr.WithExec([]string{"test", "-f", "services/api/.dagger/lock"}).Sync(ctx)
		require.NoError(t, err, "nested migration should write a nested workspace lock")

		lockOut, err := ctr.File("/work/services/api/.dagger/lock").Contents(ctx)
		require.NoError(t, err)
		assertModuleResolveLockEntry(t, []byte(lockOut), source, workspace.PolicyPin)

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/lock"}).Sync(ctx)
		require.Error(t, err, "nested lock entries should not be staged into the root workspace lock")
	})

	t.Run("plain child modules create root parent workspace", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile("videostitch/dagger.json", `{
  "name": "videostitch",
  "sdk": {"source": "go"}
}`).
			WithNewFile("videostitch/main.go", `package main

type Videostitch struct{}
`).
			WithNewFile("clipper/dagger.json", `{
  "name": "clipper",
  "sdk": {"source": "typescript"}
}`).
			WithNewFile("clipper/src/index.ts", `export class Clipper {}`).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "initial"}).
			With(daggerExec("migrate", "-y"))

		output, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, output)
		require.Contains(t, output, "Warning: videostitch requires explicit loading. If your scripts rely on implicit loading, change them to `dagger -m videostitch ...`.")
		require.Contains(t, output, "Warning: clipper requires explicit loading. If your scripts rely on implicit loading, change them to `dagger -m clipper ...`.")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/config.toml"}).Sync(ctx)
		require.NoError(t, err, "root parent workspace config should be created")

		_, err = ctr.WithExec([]string{"test", "-f", "videostitch/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "plain module config should stay in place")

		_, err = ctr.WithExec([]string{"test", "-f", "clipper/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "plain module config should stay in place")

		_, err = ctr.WithExec([]string{"test", "-f", "videostitch/.dagger/config.toml"}).Sync(ctx)
		require.Error(t, err, "plain module should not get its own workspace config")

		_, err = ctr.WithExec([]string{"test", "-f", "clipper/.dagger/config.toml"}).Sync(ctx)
		require.Error(t, err, "plain module should not get its own workspace config")

		configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.go-sdk]`)
		require.Contains(t, configOut, `source = "github.com/dagger/go-sdk"`)
		require.Contains(t, configOut, `[modules.typescript-sdk]`)
		require.Contains(t, configOut, `source = "github.com/dagger/typescript-sdk"`)

		reportOut, err := ctr.WithExec([]string{"cat", ".dagger/migration-report.md"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, reportOut, "## videostitch requires explicit loading")
		require.Contains(t, reportOut, "**This works**: `dagger -m videostitch call --help`")
		require.Contains(t, reportOut, "**This no longer works**: `cd videostitch; dagger call --help`")
		require.Contains(t, reportOut, "## clipper requires explicit loading")
	})
}

func (WorkspaceMigrationSuite) TestWorkspaceMigrateSafety(ctx context.Context, t *testctx.T) {
	t.Run("rerunning migrate after apply is a no-op", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		migrated := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! {
    "hello from first migration"
  }
}
`)
		}).With(daggerExec("migrate", "-y"))

		hashFiles := []string{"sh", "-c", "find . -path './.git' -prune -o -type f -print | sort | xargs sha256sum"}
		before, err := migrated.WithExec(hashFiles).Stdout(ctx)
		require.NoError(t, err)

		rerun := migrated.With(daggerExec("migrate", "-y"))
		out, err := rerun.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "No migration needed.")
		require.NotContains(t, out, "prepare migration diff")
		require.NotContains(t, out, "Migrated to workspace format")

		after, err := rerun.WithExec(hashFiles).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, before, after, "second migration should not rewrite files")
	})

	t.Run("apply preserves existing lockfile while staging migrated pins", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		platform, err := c.DefaultPlatform(ctx)
		require.NoError(t, err)

		source := "github.com/dagger/dagger/modules/wolfi@main"
		pin := strings.Repeat("1", 40)
		existingLock := mustMarshalContainerFromLock(t, string(platform), "sha256:"+strings.Repeat("0", 64), workspace.PolicyPin)

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {"name": "tc", "source": "`+source+`", "pin": "`+pin+`"}
  ]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithNewFile(".dagger/lock", existingLock)
		})

		migrated := ctr.With(daggerExec("migrate", "-f", "-y"))
		out, err := migrated.CombinedOutput(ctx)
		require.NoError(t, err, out)

		lockOut, err := migrated.File("/work/.dagger/lock").Contents(ctx)
		require.NoError(t, err)
		assertContainerFromLockEntry(t, []byte(lockOut), workspace.PolicyPin)
		assertModuleResolveLockEntry(t, []byte(lockOut), source, workspace.PolicyPin)
	})

	t.Run("apply refuses to overwrite conflicting target paths", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! {
    "hello from conflicted migration"
  }
}
`).
				WithNewFile(".dagger/modules/myapp/dagger.json", `{"name":"some-other-module"}`)
		})

		out, err := ctr.With(daggerExecFail("migrate", "-y")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `migration target ".dagger/modules/myapp/dagger.json" already exists`)
		require.Contains(t, out, "refusing to overwrite")

		conflictOut, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"name":"some-other-module"}`, conflictOut)

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "failed migration should leave legacy config in place")
	})
}
