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
		t.Fatal(`FIXME: implement migration preview coverage.

Run dagger migrate without -y and verify it previews the changeset without
modifying files on disk.`)
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
		require.Error(t, err, "old source directory 'ci' should have been removed")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/myapp/main.dang"}).Sync(ctx)
		require.NoError(t, err, "source file should exist at new location")

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger.json"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `"name": "myapp"`)
		require.NotContains(t, djson, `"source": "ci"`)

		configOut, err := ctr.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, "modules/myapp")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.Error(t, err, "root dagger.json should have been removed")
	})
}

// TestWorkspaceMigrateOutcomes should cover the main result classes of a
// migration.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateOutcomes(ctx context.Context, t *testctx.T) {
	t.Run("non-local source moves into a workspace module", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement non-local source migration coverage.

Move the current coverage for migrating source = "ci" into this file.`)
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
		require.Contains(t, configOut, `# settings.greeting = "hello" # string`)
		require.Contains(t, configOut, `# settings.password = "env://MY_SECRET" # Secret`)
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
		require.Contains(t, configOut, `# settings.count = 42 # int`)
		require.Contains(t, configOut, `[modules.defaults]`)
		require.Contains(t, configOut, `# settings.greeting = "hello" # string`)
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
		require.Contains(t, failedOut, `could not load modules to generate settings hints; use --force to migrate anyway`)

		migrate := ctr.With(daggerExec("migrate", "-f", "-y"))
		stdout, err := migrate.Stdout(ctx)
		require.NoError(t, err)
		stderr, err := migrate.Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout+stderr, `Warning: could not generate workspace settings hints for module "futureapp"`)

		configOut, err := migrate.WithExec([]string{"cat", ".dagger/config.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.defaults]`)
		require.Contains(t, configOut, `# settings.greeting = "hello" # string`)
	})

	t.Run("dot dagger source is pruned to workspace outputs", func(ctx context.Context, t *testctx.T) {
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

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/myapp/main.dang"}).Sync(ctx)
		require.NoError(t, err)

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/myapp/go.mod"}).Sync(ctx)
		require.NoError(t, err)

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/main.dang"}).Sync(ctx)
		require.Error(t, err, "legacy root source file should be pruned")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/go.mod"}).Sync(ctx)
		require.Error(t, err, "legacy root source metadata should be pruned")

		_, err = ctr.WithExec([]string{"test", "-d", ".dagger/modules/stale"}).Sync(ctx)
		require.Error(t, err, "pre-existing workspace-root modules subtree should be pruned")
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
	t.Fatal(`FIXME: implement migration scope coverage.

Verify Workspace.migrate operates on the compat workspace already attached to
the loaded Workspace rather than rediscovering a target from disk.`)
}

// TestWorkspaceMigrateSafety is the planning scaffold for migration properties
// that protect users from repeated or destructive application.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateSafety(ctx context.Context, t *testctx.T) {
	t.Run("rerunning migrate after apply is a no-op", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement migration idempotency coverage.

Apply dagger migrate -y to a compat-eligible project, then run it again.
Verify the second run does not rewrite files, recreate modules, or emit a fresh
migration summary.`)
	})

	t.Run("apply refuses to overwrite conflicting target paths", func(ctx context.Context, t *testctx.T) {
		t.Fatal(`FIXME: implement migration target-collision coverage.

Pre-create files or directories at the locations migration wants to write, such
as .dagger/config.toml or .dagger/modules/<name>. Verify migrate fails clearly
instead of overwriting unrelated user data.`)
	})
}
