package core

// Workspace alignment: aligned structurally, but coverage is still incomplete.
// Scope: Workspace migration planning and apply behavior for legacy projects.
// Intent: Keep migration behavior isolated from compat detection and finish the missing migration-scope cases.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/workspace"
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

	t.Run("sdk-only root-source modules are a no-op", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"}
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithNewFile("main.dang", `
type Myapp {
  pub greet: String! {
    "hello from root source"
  }
}
`)
		})

		out, err := ctr.With(daggerExec("migrate")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "No migration needed.")

		_, err = ctr.WithExec([]string{"test", "-f", "main.dang"}).Sync(ctx)
		require.NoError(t, err, "source file should remain at root")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "legacy dagger.json should remain in place")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/config.toml"}).Sync(ctx)
		require.Error(t, err, "workspace config should not be created")
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
	t.Run("summary is printed for applied migrations", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		t.Run("refreshing migrated remote refs", func(ctx context.Context, t *testctx.T) {
			ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {"name": "wolfi", "source": "github.com/dagger/dagger/modules/wolfi@main", "pin": "main"}
  ]
}`)

			migrate := ctr.With(daggerExec("migrate", "-y"))
			stdout, err := migrate.Stdout(ctx)
			require.NoError(t, err)
			stderr, err := migrate.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout+stderr, "prepare workspace migration")
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
				return ctr.WithNewFile("ci/main.dang", `
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
			require.Contains(t, stdout+stderr, "prepare workspace migration")
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
}`)

		migrate := ctr.With(daggerExec("migrate", "-y"))
		stdout, err := migrate.Stdout(ctx)
		require.NoError(t, err)
		stderr, err := migrate.Stderr(ctx)
		require.NoError(t, err)
		output := stdout + stderr
		require.Contains(t, output, "prepare workspace migration")
		require.Contains(t, output, "Warning: 2 migration gap(s) need manual review; see .dagger/migration-report.md")
		require.NotContains(t, output, "If you apply this migration, review .dagger/migration-report.md.")
		require.Equal(t, 1, strings.Count(output, "prepare workspace migration"))
		require.Equal(t, 1, strings.Count(output, "Warning: 2 migration gap(s) need manual review; see .dagger/migration-report.md"))
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

	ctr := workspaceBase(t, c).
		WithNewFile("dagger.json", `{
  "name": "outer",
  "sdk": {"source": "dang"},
  "source": "outer-src"
}`).
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
	require.NoError(t, err, "outer legacy config should not be migrated from the nested run")

	_, err = ctr.WithExec([]string{"test", "-f", "../.dagger/config.toml"}).Sync(ctx)
	require.Error(t, err, "migration should not write root workspace config")

	_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
	require.Error(t, err, "nested legacy config should be removed")

	configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, configOut, `[modules.inner]`)
	require.NotContains(t, configOut, `[modules.outer]`)

	djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/inner/dagger.json"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, djson, `"source": "../../../src"`)
}

// TestWorkspaceMigrateSafety is the planning scaffold for migration properties
// that protect users from repeated or destructive application.
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
		require.NotContains(t, out, "prepare workspace migration")
		require.NotContains(t, out, "Migrated to workspace format")

		after, err := rerun.WithExec(hashFiles).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, before, after, "second migration should not rewrite files")
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
