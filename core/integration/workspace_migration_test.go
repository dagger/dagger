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

// WorkspaceMigrationSuite owns explicit workspace migration behavior. The
// `dagger migrate` command was folded into `dagger setup` (its migrate step),
// so these tests drive migration through `dagger setup --auto-apply`. Preview
// is exercised directly against the `migrate` workspace API.
type WorkspaceMigrationSuite struct{}

func TestWorkspaceMigration(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceMigrationSuite{})
}

// materializeModuleFiles runs codegen and exports the module's generated
// files into the container: TOML modules don't regenerate at runtime.
func materializeModuleFiles(refString string) dagger.WithContainerFunc {
	return daggerQuery(`{moduleSource(refString:%q){generatedContextDirectory{export(path:".")}}}`, refString)
}

// TestWorkspaceMigratePreviewAndApply should cover the main CLI lifecycle:
// preview via the workspace `migrate` API (non-mutating) and apply via
// `dagger setup --auto-apply`.
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
		require.Contains(t, out, `"path": "dagger.toml"`)
		require.Contains(t, out, `"path": ".dagger/modules/myapp/dagger-module.toml"`)

		_, err = preview.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "preview should leave the legacy config on disk")

		_, err = preview.WithExec([]string{"test", "-f", "dagger.toml"}).Sync(ctx)
		require.Error(t, err, "preview should not write workspace config")

		_, err = preview.WithExec([]string{"test", "-f", ".dagger/modules/myapp/dagger-module.toml"}).Sync(ctx)
		require.Error(t, err, "preview should not write migrated module config")
	})

	t.Run("apply writes workspace config and migrated modules", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		migrateApply := daggerExec("setup", "--auto-apply")

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

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `name = "myapp"`)

		configOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, ".dagger/modules/myapp")

		out, err := ctr.With(daggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from migrated source", strings.TrimSpace(out))

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.Error(t, err, "root dagger.json should have been removed")
	})
}

// Generated SDK files become part of the module source after migration. Setup
// must therefore remove the ignore rules written for the legacy runtime-codegen
// model while leaving the user's own rules alone.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateGeneratedCodeGitignore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := legacySDKOnlyGoSource(t, c, "hello after migration").
		WithNewFile(".gitignore", "# user-owned rules\n*.log\n").
		With(materializeModuleFiles(".")).
		WithExec([]string{"grep", "-Fx", "/dagger.gen.go", ".gitignore"}).
		With(daggerExec("setup", "--auto-apply"))

	gitignore, err := ctr.File(".gitignore").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "# user-owned rules\n*.log\n/.env\n", gitignore)

	out, err := ctr.With(daggerCallAt(".", "greet")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello after migration", strings.TrimSpace(out))
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
		}).With(daggerExec("setup", "--auto-apply"))

		_, err := ctr.WithExec([]string{"test", "-f", "ci/main.dang"}).Sync(ctx)
		require.NoError(t, err, "source file should remain in its original directory")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/myapp/main.dang"}).Sync(ctx)
		require.Error(t, err, "source file should not be copied to the migrated module config directory")

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `source = "../../../ci"`)

		out, err := ctr.With(daggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from original source", strings.TrimSpace(out))
	})

	t.Run("sdk-only config migrates before install", func(ctx context.Context, t *testctx.T) {
		// The allowed path for an SDK-only dagger.json is explicit migration
		// first, then workspace mutation. Migration creates the parent native
		// workspace config and preserves the root module, so a later `dagger
		// install` can safely add dependencies to dagger.toml.
		c := connect(ctx, t)
		ctr := legacySDKOnlyGoSource(t, c, "hello from root source").
			With(legacyDangModule("dep", "dep", "Dep", "hello from dep")).
			With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)
		// The "requires explicit loading" warning is recorded in the on-disk
		// migration report (asserted below), not printed to setup stdout.

		_, err = ctr.WithExec([]string{"test", "-f", "main.go"}).Sync(ctx)
		require.NoError(t, err, "source file should remain at root")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "legacy dagger.json should remain in place")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.toml"}).Sync(ctx)
		require.NoError(t, err, "root parent workspace config should be created")

		configOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.dagger-go-sdk]`)
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
		require.Contains(t, installOut, `Installed module "dep" in /work/dagger.toml`)

		configOut, err = ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.dep]`)
		require.Contains(t, configOut, `source = "dep"`)
	})

	t.Run("plain module in default dot dagger modules directory migrates", func(ctx context.Context, t *testctx.T) {
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
    "hello from root"
  }
}
`).
				With(legacyDangModule(".dagger/modules/project", "project", "Project", "hello from project"))
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		cfgOut, err := ctr.WithExec([]string{"cat", ".dagger/modules/project/dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, cfgOut, `name = "project"`)
		require.Contains(t, cfgOut, `[runtime]`)

		_, err = ctr.WithExec([]string{"test", "!", "-e", ".dagger/modules/project/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "legacy project-local module config should be removed after conversion")
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
		}).With(daggerExec("setup", "--auto-apply"))

		configOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, strings.Join([]string{
			"[modules.defaults]",
			`source = "./toolchain"`,
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
		}).With(daggerExec("setup", "--auto-apply"))

		configOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
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
		}).With(daggerExec("setup", "--auto-apply"))

		configOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
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
  "codegen": {"automaticGitignore": false},
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
		},
			// The fixture's committed generated files predate this engine. The
			// migrated module is dagger-module.toml and builds from them as-is,
			// so refresh them with the current engine's codegen.
			materializeModuleFiles("."),
		).With(daggerExec("setup", "--auto-apply"))

		configOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.superconstructor]`)
		require.Contains(t, configOut, `# settings.count = 42`)
		require.Contains(t, configOut, `[modules.defaults]`)
		require.Contains(t, configOut, `# settings.greeting = "hello"`)
		require.NotContains(t, configOut, `# int`)
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
		}).With(daggerExec("setup", "--auto-apply"))

		_, err := ctr.WithExec([]string{"test", "-f", "dagger.toml"}).Sync(ctx)
		require.NoError(t, err)

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `source = "../.."`)

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

	t.Run("local toolchain migrates in place", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci",
  "toolchains": [{"name": "tc", "source": "./toolchain"}]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! { "hello from root" }
}
`).
				With(legacyDangModule("toolchain", "tc", "Tc", "hello from toolchain"))
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		cfgOut, err := ctr.WithExec([]string{"cat", "toolchain/dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, cfgOut, `name = "tc"`)
		require.Contains(t, cfgOut, `[runtime]`)

		_, err = ctr.WithExec([]string{"test", "!", "-e", "toolchain/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "legacy toolchain config should be removed after in-place conversion")

		wsOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, wsOut, `[modules.tc]`)
		require.Contains(t, wsOut, `source = "./toolchain"`)
		// The converted toolchain's runtime is installed and pinned in the
		// workspace, sharing the root module's dang SDK install.
		require.Contains(t, wsOut, `[modules.dagger-dang-sdk]`)
		require.Contains(t, wsOut, `path = "toolchain"`)

		// The converted module loads from its own runtime field.
		callOut, err := ctr.With(daggerCallAt("toolchain", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from toolchain", strings.TrimSpace(callOut))
	})

	t.Run("local dependency migrates in place behind rebased reference", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci",
  "dependencies": [{"name": "foo", "source": "./libs/foo"}]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! { "hello from root" }
}
`).
				With(legacyDangModule("libs/foo", "foo", "Foo", "hello from foo"))
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		cfgOut, err := ctr.WithExec([]string{"cat", "libs/foo/dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, cfgOut, `name = "foo"`)

		_, err = ctr.WithExec([]string{"test", "!", "-e", "libs/foo/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "legacy dependency config should be removed after in-place conversion")

		mainCfg, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, mainCfg, "../../../libs/foo",
			"main module's dependency reference is rebased to the still-in-place converted dependency")

		// The converted dependency's runtime is installed and pinned in the
		// workspace.
		wsOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, wsOut, `[modules.dagger-dang-sdk]`)
		require.Contains(t, wsOut, `path = "libs/foo"`)
		// The runtime is resolved to its real ref, matching `dagger sdk install`,
		// not left as the bare "dang" short name.
		require.Contains(t, wsOut, `source = "github.com/dagger/dang-sdk"`)
		require.NotContains(t, wsOut, `source = "dang"`)

		// The converted dependency loads from its own runtime field.
		callOut, err := ctr.With(daggerCallAt("libs/foo", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from foo", strings.TrimSpace(callOut))
	})

	t.Run("transitive local dependencies migrate", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci",
  "dependencies": [{"name": "foo", "source": "./libs/foo"}]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! { "hello from root" }
}
`).
				WithNewFile("libs/foo/dagger.json", `{"name":"foo","sdk":{"source":"dang"},"dependencies":[{"name":"bar","source":"../bar"}]}`).
				WithNewFile("libs/foo/main.dang", "\ntype Foo {\n  pub message: String! { \"foo\" }\n}\n").
				With(legacyDangModule("libs/bar", "bar", "Bar", "hello from bar"))
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		for _, dir := range []string{"libs/foo", "libs/bar"} {
			_, err = ctr.WithExec([]string{"test", "-f", dir + "/dagger-module.toml"}).Sync(ctx)
			require.NoError(t, err, "%s should be converted", dir)
			_, err = ctr.WithExec([]string{"test", "!", "-e", dir + "/dagger.json"}).Sync(ctx)
			require.NoError(t, err, "%s legacy config should be removed", dir)
		}
	})

	t.Run("nested workspace plan resolves its SDK installs", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		// A globbed .dagger/modules/tool config that itself must-migrate (its
		// source is a non-root subdir) becomes its OWN workspace plan, with its
		// own dagger.toml — separate from the top-level workspace the setup
		// command runs in. Its SDK install (and any it owns for discovered deps)
		// must still be resolved to a real ref, even though it lives in a config
		// the current-workspace fixup pass doesn't read. The tool uses `php`,
		// whose sdks.json ref (github.com/dagger/php-sdk) differs from the
		// engine-side runtime mapping, so this also pins that the CLI registry —
		// what `dagger sdk install` uses — wins for these nested configs.
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", "\ntype Myapp {\n  pub greet: String! { \"hi\" }\n}\n").
				WithNewFile(".dagger/modules/tool/dagger.json", `{"name":"tool","sdk":{"source":"php"},"source":"src","dependencies":[{"name":"helper","source":"./helper"}]}`).
				WithNewFile(".dagger/modules/tool/src/index.php", "<?php\n").
				With(legacyDangModule(".dagger/modules/tool/helper", "helper", "Helper", "help"))
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		nested, err := ctr.WithExec([]string{"cat", ".dagger/modules/tool/dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		// The tool's own runtime resolves to its sdks.json ref (CLI registry),
		// and the discovered helper's dang runtime resolves too.
		require.Contains(t, nested, `source = "github.com/dagger/php-sdk"`, nested)
		require.Contains(t, nested, `source = "github.com/dagger/dang-sdk"`, nested)
		require.NotContains(t, nested, `source = "php"`, nested)
	})

	t.Run("pre-existing nested workspace config is left untouched", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		// A pre-existing dagger.toml under tools/ makes tools/ its own workspace,
		// which migration treats as an ownership boundary and does not touch. SDK
		// resolution must respect that boundary too: a bare short-name install
		// there is not a migration artifact and must not be rewritten, even though
		// "go" would otherwise resolve through sdks.json.
		preExisting := `[modules.local-go]
source = "go"

[modules.local-go.as-sdk]
name = "my-go"
`
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", "\ntype Myapp {\n  pub greet: String! { \"hi\" }\n}\n").
				WithNewFile("tools/dagger.toml", preExisting)
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		after, err := ctr.WithExec([]string{"cat", "tools/dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, preExisting, after, "pre-existing nested workspace config must not be rewritten")
	})

	t.Run("diamond dependency is migrated once", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		// main -> a, main -> b, a -> ../shared, b -> ../shared: shared is reached
		// via two paths (a diamond) and must be converted exactly once — a second
		// conversion would target the same path twice and fail the migration.
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci",
  "dependencies": [{"name": "a", "source": "./libs/a"}, {"name": "b", "source": "./libs/b"}]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", "\ntype Myapp {\n  pub greet: String! { \"hello from root\" }\n}\n").
				WithNewFile("libs/a/dagger.json", `{"name":"a","sdk":{"source":"dang"},"dependencies":[{"name":"shared","source":"../shared"}]}`).
				WithNewFile("libs/a/main.dang", "\ntype A {\n  pub message: String! { \"a\" }\n}\n").
				WithNewFile("libs/b/dagger.json", `{"name":"b","sdk":{"source":"dang"},"dependencies":[{"name":"shared","source":"../shared"}]}`).
				WithNewFile("libs/b/main.dang", "\ntype B {\n  pub message: String! { \"b\" }\n}\n").
				With(legacyDangModule("libs/shared", "shared", "Shared", "hello from shared"))
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		for _, dir := range []string{"libs/a", "libs/b", "libs/shared"} {
			_, err = ctr.WithExec([]string{"test", "-f", dir + "/dagger-module.toml"}).Sync(ctx)
			require.NoError(t, err, "%s should be converted", dir)
			_, err = ctr.WithExec([]string{"test", "!", "-e", dir + "/dagger.json"}).Sync(ctx)
			require.NoError(t, err, "%s legacy config should be removed", dir)
		}
	})

	t.Run("cyclic local dependencies terminate and migrate once", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		// a -> ../b, b -> ../a is a dependency cycle; discovery must terminate
		// (the visited set) and convert each module once rather than looping.
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci",
  "dependencies": [{"name": "a", "source": "./libs/a"}]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", "\ntype Myapp {\n  pub greet: String! { \"hello from root\" }\n}\n").
				WithNewFile("libs/a/dagger.json", `{"name":"a","sdk":{"source":"dang"},"dependencies":[{"name":"b","source":"../b"}]}`).
				WithNewFile("libs/a/main.dang", "\ntype A {\n  pub message: String! { \"a\" }\n}\n").
				WithNewFile("libs/b/dagger.json", `{"name":"b","sdk":{"source":"dang"},"dependencies":[{"name":"a","source":"../a"}]}`).
				WithNewFile("libs/b/main.dang", "\ntype B {\n  pub message: String! { \"b\" }\n}\n")
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		for _, dir := range []string{"libs/a", "libs/b"} {
			_, err = ctr.WithExec([]string{"test", "-f", dir + "/dagger-module.toml"}).Sync(ctx)
			require.NoError(t, err, "%s should be converted", dir)
			_, err = ctr.WithExec([]string{"test", "!", "-e", dir + "/dagger.json"}).Sync(ctx)
			require.NoError(t, err, "%s legacy config should be removed", dir)
		}
	})

	t.Run("nested-workspace dependency is left as legacy", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci",
  "dependencies": [{"name": "nested", "source": "./nested"}]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! { "hello from root" }
}
`).
				WithNewFile("nested/dagger.json", `{"name":"nested","sdk":{"source":"dang"},"toolchains":[{"name":"x","source":"./x"}]}`).
				WithNewFile("nested/main.dang", "\ntype Nested {\n  pub message: String! { \"nested\" }\n}\n")
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		_, err = ctr.WithExec([]string{"test", "-f", "nested/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "a dependency that owns toolchains/blueprint is not converted in place")
		_, err = ctr.WithExec([]string{"test", "!", "-e", "nested/dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "nested workspace dependency should not be converted")
	})

	t.Run("absolute local reference is not migrated as an in-tree module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		// An absolute source is not a workspace-relative module; it must not be
		// rebased under the workspace root and migrated. Use a toolchain, since a
		// main-module dependency with an absolute source is rejected earlier.
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci",
  "toolchains": [{"name": "tc", "source": "/libs/foo"}]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! { "hello from root" }
}
`).
				With(legacyDangModule("libs/foo", "foo", "Foo", "hello from foo"))
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		_, err = ctr.WithExec([]string{"test", "-f", "libs/foo/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "an absolute reference must not be resolved as ./libs/foo and migrated")
		_, err = ctr.WithExec([]string{"test", "!", "-e", "libs/foo/dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "libs/foo should not be converted from an absolute reference")
	})

	t.Run("hidden default-modules config does not pull in its dependencies", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		// A hidden .dagger/modules/.scratch config is ignored by migration; it
		// must not seed the dependency walk and drag its local deps in.
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci"
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! { "hello from root" }
}
`).
				WithNewFile(".dagger/modules/.scratch/dagger.json", `{"name":"scratch","sdk":{"source":"dang"},"dependencies":[{"name":"shared","source":"../../../libs/shared"}]}`).
				WithNewFile(".dagger/modules/.scratch/main.dang", "\ntype Scratch {\n  pub message: String! { \"scratch\" }\n}\n").
				With(legacyDangModule("libs/shared", "shared", "Shared", "hello from shared"))
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		_, err = ctr.WithExec([]string{"test", "-f", "libs/shared/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "a dependency reached only through an ignored hidden config must not be migrated")
		_, err = ctr.WithExec([]string{"test", "!", "-e", "libs/shared/dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "libs/shared should not be converted")
	})
}

// TestWorkspaceMigrateUserFeedback should cover the user-facing output of
// explicit migration.
//
// The migration feedback is telemetry spans streamed by the progress frontend,
// so these tests pin plain progress: setup is a human-facing wizard (TTY), but
// non-TTY auto resolves to the report frontend, which renders nothing for a
// passing run.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateUserFeedback(ctx context.Context, t *testctx.T) {
	withPlainProgress := func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithEnvVariable("DAGGER_PROGRESS", "plain")
	}

	withFreshMigrationProgress := func(ctr *dagger.Container) *dagger.Container {
		workdir := "/work-" + identity.NewID()
		return ctr.
			WithExec([]string{"mv", "/work", workdir}).
			WithWorkdir(workdir).
			WithEnvVariable("OTEL_BAGGAGE", "repeat-telemetry=true").
			With(withPlainProgress)
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
				With(daggerExec("setup", "--auto-apply"))
			stdout, err := migrate.Stdout(ctx)
			require.NoError(t, err)
			stderr, err := migrate.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout+stderr, "prepare migration diff")
			require.Contains(t, stdout+stderr, "workspace configuration: dagger.toml")
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
				With(daggerExec("setup", "--auto-apply"))
			stdout, err := migrate.Stdout(ctx)
			require.NoError(t, err)
			stderr, err := migrate.Stderr(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout+stderr, "prepare migration diff")
			require.Contains(t, stdout+stderr, "workspace configuration: dagger.toml")
			require.Contains(t, stdout+stderr, "move module: dagger.json -> .dagger/modules/myapp/dagger-module.toml")
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
			With(daggerExec("setup", "--auto-apply"))
		stdout, err := migrate.Stdout(ctx)
		require.NoError(t, err)
		stderr, err := migrate.Stderr(ctx)
		require.NoError(t, err)
		output := stdout + stderr
		require.Contains(t, output, "prepare migration diff")
		require.Contains(t, output, "workspace configuration: dagger.toml")
		require.Contains(t, output, "install module: ./toolchain")
		require.Contains(t, output, "migration report: .dagger/migration-report.md")
		require.NotContains(t, output, "If you apply this migration, review .dagger/migration-report.md.")
		require.NotContains(t, output, "Migrated to workspace format")

		// The "N old setting(s) need review" summary and per-gap details land in
		// the on-disk migration report rather than setup stdout.
		report, err := migrate.WithExec([]string{"cat", ".dagger/migration-report.md"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, report, "# Migration Report")
		require.Contains(t, report, "`toolchain` needs a manual check")
		require.Contains(t, report, `constructor arg "src" has 'ignore' and 'defaultPath', which workspace settings do not support`)
		require.Contains(t, report, `function setting "build.tag" is not supported in workspace config`)
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

		migrate := ctr.
			With(withPlainProgress).
			With(daggerExec("setup", "--auto-apply"))
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

	t.Run("migrates selected nested config without touching outer config", func(ctx context.Context, t *testctx.T) {
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
			With(daggerExec("setup", "--auto-apply"))

		_, err := ctr.WithExec([]string{"test", "-f", "dagger.toml"}).Sync(ctx)
		require.NoError(t, err, "nested compat workspace should be migrated")

		_, err = ctr.WithExec([]string{"test", "-f", "../dagger.json"}).Sync(ctx)
		require.NoError(t, err, "outer legacy config should not be migrated from the nested run")

		_, err = ctr.WithExec([]string{"test", "-f", "../dagger.toml"}).Sync(ctx)
		require.Error(t, err, "migration should not write root workspace config")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.Error(t, err, "nested legacy config should be removed")

		nestedConfigOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, nestedConfigOut, `[modules.inner]`)
		require.NotContains(t, nestedConfigOut, `[modules.outer]`)

		_, err = ctr.WithExec([]string{"test", "!", "-e", "../.dagger/modules/outer/dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "outer migrated module config should not be created")

		djson, err := ctr.WithExec([]string{"cat", ".dagger/modules/inner/dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `source = "../../../src"`)
	})

	t.Run("does not migrate unrelated child config from root", func(ctx context.Context, t *testctx.T) {
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
			With(daggerExec("setup", "--auto-apply"))

		_, err := ctr.WithExec([]string{"test", "-f", "services/api/dagger.toml"}).Sync(ctx)
		require.Error(t, err, "child config should not be migrated from root")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.toml"}).Sync(ctx)
		require.Error(t, err, "root workspace config is not needed for an unrelated child")

		_, err = ctr.WithExec([]string{"test", "-f", "services/api/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "child legacy config should stay in place")
	})

	t.Run("plain modules in default dot dagger modules directory create parent workspace", func(ctx context.Context, t *testctx.T) {
		ctr := workspaceBase(t, c).
			WithNewFile(".dagger/modules/videostitch/dagger.json", `{
  "name": "videostitch",
  "sdk": {"source": "go"}
}`).
			WithNewFile(".dagger/modules/videostitch/main.go", `package main

type Videostitch struct{}
`).
			WithNewFile(".dagger/modules/clipper/dagger.json", `{
  "name": "clipper",
  "sdk": {"source": "typescript"}
}`).
			WithNewFile(".dagger/modules/clipper/src/index.ts", `export class Clipper {}`).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "initial"}).
			With(daggerExec("setup", "--auto-apply"))

		output, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, output)
		// The per-module "requires explicit loading" warnings are recorded in the
		// on-disk migration report (asserted below), not printed to setup stdout.

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.toml"}).Sync(ctx)
		require.NoError(t, err, "root parent workspace config should be created")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/videostitch/dagger.json"}).Sync(ctx)
		require.Error(t, err, "legacy plain module config should be converted")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/videostitch/dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "plain module config should be converted")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/clipper/dagger.json"}).Sync(ctx)
		require.Error(t, err, "legacy plain module config should be converted")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/clipper/dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "plain module config should be converted")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/videostitch/dagger.toml"}).Sync(ctx)
		require.Error(t, err, "plain module should not get its own workspace config")

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/clipper/dagger.toml"}).Sync(ctx)
		require.Error(t, err, "plain module should not get its own workspace config")

		configOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.dagger-go-sdk]`)
		require.Contains(t, configOut, `source = "github.com/dagger/go-sdk"`)
		require.Contains(t, configOut, `[modules.dagger-typescript-sdk]`)
		require.Contains(t, configOut, `source = "github.com/dagger/typescript-sdk"`)

		reportOut, err := ctr.WithExec([]string{"cat", ".dagger/migration-report.md"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, reportOut, "## .dagger/modules/videostitch requires explicit loading")
		require.Contains(t, reportOut, "**This works**: `dagger -m .dagger/modules/videostitch call --help`")
		require.Contains(t, reportOut, "**This no longer works**: `cd .dagger/modules/videostitch; dagger call --help`")
		require.Contains(t, reportOut, "## .dagger/modules/clipper requires explicit loading")
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
		}).With(daggerExec("setup", "--auto-apply"))

		hashFiles := []string{"sh", "-c", "find . -path './.git' -prune -o -type f -print | sort | xargs sha256sum"}
		before, err := migrated.WithExec(hashFiles).Stdout(ctx)
		require.NoError(t, err)

		rerun := migrated.With(daggerExec("setup", "--auto-apply"))
		out, err := rerun.CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "No migration needed.")
		require.NotContains(t, out, "prepare migration diff")
		require.NotContains(t, out, "Migrated to workspace format")

		after, err := rerun.WithExec(hashFiles).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, before, after, "second migration should not rewrite files")
	})

	t.Run("apply moves legacy lockfile while staging migrated pins", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		platform, err := c.DefaultPlatform(ctx)
		require.NoError(t, err)

		source := "github.com/dagger/dagger/modules/wolfi@main"
		pin := strings.Repeat("1", 40)
		legacyLock := workspace.NewLock()
		require.NoError(t, legacyLock.SetLookup("", "container.from", []any{"docker.io/library/alpine:latest", string(platform)}, workspace.LookupResult{
			Value:  "sha256:" + strings.Repeat("0", 64),
			Policy: workspace.PolicyPin,
		}))
		require.NoError(t, legacyLock.SetLookup("", "modules.resolve", []any{source}, workspace.LookupResult{
			Value:  pin,
			Policy: workspace.PolicyFloat,
		}))
		existingLockBytes, err := legacyLock.Marshal()
		require.NoError(t, err)

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {"name": "tc", "source": "`+source+`", "pin": "`+pin+`"}
  ]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithNewFile(".dagger/lock", string(existingLockBytes))
		})

		migrated := ctr.With(daggerExec("setup", "--auto-apply"))
		out, err := migrated.CombinedOutput(ctx)
		require.NoError(t, err, out)

		lockOut, err := migrated.File("/work/dagger.lock").Contents(ctx)
		require.NoError(t, err)
		assertContainerFromLockEntry(t, []byte(lockOut), workspace.PolicyPin)
		assertNoModuleResolveLockEntry(t, []byte(lockOut))
		_, err = migrated.WithExec([]string{"test", "!", "-e", ".dagger/lock"}).Sync(ctx)
		require.NoError(t, err)

		configOut, err := migrated.File("/work/dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `source = "`+source[:strings.LastIndex(source, "@")+1]+pin+`"`)
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
				WithNewFile(".dagger/modules/myapp/dagger-module.toml", `name = "some-other-module"
`)
		})

		out, err := ctr.With(daggerExecFail("setup", "--auto-apply")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `migration target ".dagger/modules/myapp/dagger-module.toml" already exists`)
		require.Contains(t, out, "refusing to overwrite")

		conflictOut, err := ctr.WithExec([]string{"cat", ".dagger/modules/myapp/dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, conflictOut, `name = "some-other-module"`)

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "failed migration should leave legacy config in place")
	})
}
