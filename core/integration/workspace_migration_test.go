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
		require.Contains(t, out, `"path": "dagger-module.toml"`)

		_, err = preview.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "preview should leave the legacy config on disk")

		_, err = preview.WithExec([]string{"test", "-f", "dagger.toml"}).Sync(ctx)
		require.Error(t, err, "preview should not write workspace config")

		_, err = preview.WithExec([]string{"test", "-f", "dagger-module.toml"}).Sync(ctx)
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

		djson, err := ctr.WithExec([]string{"cat", "dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `name = "myapp"`)
		require.Contains(t, djson, `source = "ci"`)

		configOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, strings.Join([]string{
			"[modules.myapp]",
			`source = "."`,
		}, "\n"))

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
	t.Run("migrated module ignores are cleaned", func(ctx context.Context, t *testctx.T) {
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
	})

	t.Run("nested-workspace dependency keeps its ignores", func(ctx context.Context, t *testctx.T) {
		// A dependency with its own toolchains stays legacy and keeps runtime
		// codegen, so its generated-code ignore rules must survive the
		// migration happening around it.
		c := connect(ctx, t)

		nestedGitignore := "# nested rules\n/dagger.gen.go\n/internal/dagger\n/internal/telemetry\n"
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
				WithNewFile("nested/dagger.json", `{"name":"nested","sdk":{"source":"go"},"toolchains":[{"name":"x","source":"./x"}]}`).
				WithNewFile("nested/main.go", `package main

type Nested struct{}

func (m *Nested) Message() string {
	return "nested"
}
`).
				WithNewFile("nested/.gitignore", nestedGitignore).
				With(legacyDangModule("nested/x", "x", "X", "hello from toolchain"))
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		_, err = ctr.WithExec([]string{"test", "-f", "nested/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "the nested workspace dependency should stay legacy")

		gitignore, err := ctr.File("nested/.gitignore").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, nestedGitignore, gitignore, "a module left in legacy format keeps runtime codegen; its ignore rules must stay")
	})
}

// TestWorkspaceMigrateOutcomes should cover the main result classes of a
// migration.
func (WorkspaceMigrationSuite) TestWorkspaceMigrateOutcomes(ctx context.Context, t *testctx.T) {
	t.Run("module config replaces dagger.json in place", func(ctx context.Context, t *testctx.T) {
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

		_, err = ctr.WithExec([]string{"test", "!", "-e", ".dagger/modules"}).Sync(ctx)
		require.NoError(t, err, "migration must not synthesize configs under .dagger/modules")

		_, err = ctr.WithExec([]string{"test", "!", "-e", "ci/dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "the module config must not move into the source directory: installs by path point at the dagger.json location")

		djson, err := ctr.WithExec([]string{"cat", "dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `name = "myapp"`)
		require.Contains(t, djson, `source = "ci"`,
			"the source path is preserved as-is")

		out, err := ctr.With(daggerCall("greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from original source", strings.TrimSpace(out))
	})

	t.Run("sdk-only config migrates before install", func(ctx context.Context, t *testctx.T) {
		// The allowed path for an SDK-only dagger.json is explicit migration
		// first, then workspace mutation. Migration converts the module config
		// in place and creates a minimal workspace config pinning only the SDK
		// runtime, so a later `dagger install` can safely add dependencies to
		// dagger.toml.
		c := connect(ctx, t)
		ctr := legacySDKOnlyGoSource(t, c, "hello from root source").
			With(legacyDangModule("dep", "dep", "Dep", "hello from dep")).
			// The converted module keeps working only with its generated code
			// committed (current-format modules have no runtime codegen).
			With(materializeModuleFiles(".")).
			With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)
		// The "requires explicit loading" warning is recorded in the on-disk
		// migration report (asserted below), not printed to setup stdout.

		_, err = ctr.WithExec([]string{"test", "-f", "main.go"}).Sync(ctx)
		require.NoError(t, err, "source file should remain at root")

		_, err = ctr.WithExec([]string{"test", "!", "-e", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "legacy dagger.json should be converted in place")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "module config should be converted in place at the root")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.toml"}).Sync(ctx)
		require.NoError(t, err, "minimal workspace config should be created")

		configOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.dagger-go-sdk]`)
		require.Contains(t, configOut, `source = "github.com/dagger/go-sdk"`)
		require.NotContains(t, configOut, `[modules.myapp]`,
			"a repo that is just a dagger module is not installed into the workspace")
		require.NotContains(t, configOut, "entrypoint")

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

	t.Run("unreferenced module in default dot dagger modules directory is not touched", func(ctx context.Context, t *testctx.T) {
		// Migration no longer crawls the repo (not even .dagger/modules): only
		// the selected root config and the local modules it references migrate.
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

		_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/project/dagger.json"}).Sync(ctx)
		require.NoError(t, err, "an unreferenced module keeps its legacy config")

		_, err = ctr.WithExec([]string{"test", "!", "-e", ".dagger/modules/project/dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "an unreferenced module is not converted")
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

	t.Run("migration omits commented settings hints and preserves active settings", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		toolchainSrc := filepath.Join("testdata", "modules", "go", "defaults")

		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "toolchains": [
    {
      "name": "defaults",
      "source": "./toolchain",
      "customizations": [
        {"argument": "greeting", "default": "bonjour"}
      ]
    }
  ]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.WithDirectory("toolchain", c.Host().Directory(toolchainSrc))
		}).With(daggerExec("setup", "--auto-apply"))

		configOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, configOut, `[modules.defaults]`)
		require.Contains(t, configOut, `[modules.defaults.settings]`)
		require.Contains(t, configOut, `greeting = "bonjour"`)
		require.NotContains(t, configOut, `# settings.`)
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

		djson, err := ctr.WithExec([]string{"cat", "dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `name = "myapp"`)
		require.Contains(t, djson, `source = ".dagger"`,
			"the source path is preserved as-is")

		_, err = ctr.WithExec([]string{"test", "!", "-e", ".dagger/dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "the module config must not move into the source directory")

		_, err = ctr.WithExec([]string{"test", "!", "-e", ".dagger/modules/myapp"}).Sync(ctx)
		require.NoError(t, err, "migration must not synthesize configs under .dagger/modules")

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
			// In 0.21 a module's toolchains were also loaded into its own API,
			// so module code could call them like dependencies. This module
			// does exactly that and must keep working after migration.
			return ctr.
				WithNewFile("ci/main.dang", `
type Myapp {
  pub greet: String! { "hello from root" }
  pub viaToolchain: String! { tc.message }
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

		// The toolchain is also a dependency of the migrated root module, so
		// code that called it in 0.21 still resolves.
		mainCfg, err := ctr.WithExec([]string{"cat", "dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, mainCfg, `name = "tc"`)
		require.Contains(t, mainCfg, `source = "./toolchain"`)
		require.NotContains(t, mainCfg, "toolchains")

		// The converted module loads from its own runtime field.
		callOut, err := ctr.With(daggerCallAt("toolchain", "message")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from toolchain", strings.TrimSpace(callOut))

		// The root module can still reach the toolchain as a dependency.
		viaOut, err := ctr.With(daggerCall("via-toolchain")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from toolchain", strings.TrimSpace(viaOut))
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

		mainCfg, err := ctr.WithExec([]string{"cat", "dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, mainCfg, `"./libs/foo"`,
			"main module's dependency reference is preserved as-is: the config did not move")

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

	t.Run("discovered dependency SDK installs resolve to real refs", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		// A discovered local dependency's runtime is recorded in the migrated
		// workspace config by bare short name and must be resolved to a real
		// ref. The tool uses `php`, whose sdks.json ref
		// (github.com/dagger/php-sdk) differs from the engine-side runtime
		// mapping, so this also pins that the CLI registry — what `dagger sdk
		// install` uses — wins.
		ctr := legacyWorkspaceBase(t, c, `{
  "name": "myapp",
  "sdk": {"source": "dang"},
  "source": "ci",
  "dependencies": [{"name": "tool", "source": "./tool"}]
}`, func(ctr *dagger.Container) *dagger.Container {
			return ctr.
				WithNewFile("ci/main.dang", "\ntype Myapp {\n  pub greet: String! { \"hi\" }\n}\n").
				WithNewFile("tool/dagger.json", `{"name":"tool","sdk":{"source":"php"},"source":"src"}`).
				WithNewFile("tool/src/index.php", "<?php\n")
		}).With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		wsOut, err := ctr.WithExec([]string{"cat", "dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		// The root module's dang runtime and the discovered tool's php runtime
		// both resolve to their sdks.json refs.
		require.Contains(t, wsOut, `source = "github.com/dagger/php-sdk"`, wsOut)
		require.Contains(t, wsOut, `source = "github.com/dagger/dang-sdk"`, wsOut)
		require.NotContains(t, wsOut, `source = "php"`, wsOut)
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
			require.Contains(t, stdout+stderr, "move module: dagger.json -> dagger-module.toml")
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
		// The main expected subdirectory case: a nested dagger.json whose
		// module source is in a subdirectory migrates module-only — the config
		// converts to dagger-module.toml in place, and no dagger.toml is
		// created anywhere (nested workspace configs are never written).
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

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "Migrated the module in place; no workspace config was created.")

		_, err = ctr.WithExec([]string{"test", "!", "-e", "dagger.toml"}).Sync(ctx)
		require.NoError(t, err, "migration should not write a nested workspace config")

		_, err = ctr.WithExec([]string{"test", "-f", "../dagger.json"}).Sync(ctx)
		require.NoError(t, err, "outer legacy config should not be migrated from the nested run")

		_, err = ctr.WithExec([]string{"test", "!", "-e", "../dagger.toml"}).Sync(ctx)
		require.NoError(t, err, "migration should not write root workspace config")

		_, err = ctr.WithExec([]string{"test", "!", "-e", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "nested legacy config should be removed")

		_, err = ctr.WithExec([]string{"test", "!", "-e", "../dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "outer migrated module config should not be created")

		djson, err := ctr.WithExec([]string{"cat", "dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, djson, `name = "inner"`)
		require.Contains(t, djson, `source = "src"`,
			"the source path is preserved as-is")
	})

	t.Run("plain module in a subdirectory migrates module only", func(ctx context.Context, t *testctx.T) {
		// Setup run from a subdirectory that is a dagger module migrates just
		// that module from v0 to v1: dagger.json converts to dagger-module.toml
		// in place (its local dependencies too), no workspace config is created
		// anywhere, and module recommendations are skipped.
		ctr := workspaceBase(t, c).
			WithNewFile("tools/hello/dagger.json", `{"name":"hello","sdk":{"source":"dang"},"dependencies":[{"name":"dep","source":"./dep"}]}`).
			WithNewFile("tools/hello/main.dang", `
type Hello {
  pub greet: String! {
    "hello from subdirectory module"
  }
}
`).
			With(legacyDangModule("tools/hello/dep", "dep", "Dep", "hello from dep")).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "initial"}).
			WithWorkdir("/work/tools/hello").
			With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "Migrated the module in place; no workspace config was created.")
		require.NotContains(t, out, "Step 3: Recommended modules")
		require.NotContains(t, out, "Run `dagger setup` again")

		for _, dir := range []string{".", "dep"} {
			_, err = ctr.WithExec([]string{"test", "-f", dir + "/dagger-module.toml"}).Sync(ctx)
			require.NoError(t, err, "%s should be converted in place", dir)
			_, err = ctr.WithExec([]string{"test", "!", "-e", dir + "/dagger.json"}).Sync(ctx)
			require.NoError(t, err, "%s legacy config should be removed", dir)
		}

		for _, cfg := range []string{"dagger.toml", "dep/dagger.toml", "../../dagger.toml"} {
			_, err = ctr.WithExec([]string{"test", "!", "-e", cfg}).Sync(ctx)
			require.NoError(t, err, "no workspace config should be created at %s", cfg)
		}
		_, err = ctr.WithExec([]string{"test", "!", "-e", ".dagger/migration-report.md"}).Sync(ctx)
		require.NoError(t, err, "module-only migration writes no migration report")

		// The converted module config still names its sdk, so it loads without
		// any workspace.
		callOut, err := ctr.With(daggerCallAt(".", "greet")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from subdirectory module", strings.TrimSpace(callOut))
	})

	t.Run("subdirectory toolchains hoist into the repo-root workspace", func(ctx context.Context, t *testctx.T) {
		// Nested dagger.toml files are never created: a subdirectory config
		// with toolchains installs them into a dagger.toml at the repo root
		// (local sources rebased), while the module config converts in place
		// and the module itself is not installed into the workspace.
		ctr := workspaceBase(t, c).
			WithNewFile("tools/hello/dagger.json", `{
  "name": "hello",
  "sdk": {"source": "dang"},
  "toolchains": [{"name": "tc", "source": "./toolchain"}]
}`).
			WithNewFile("tools/hello/main.dang", `
type Hello {
  pub greet: String! {
    "hello from subdirectory module"
  }
}
`).
			With(legacyDangModule("tools/hello/toolchain", "tc", "Tc", "hello from toolchain")).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "initial"}).
			WithWorkdir("/work/tools/hello").
			With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)

		_, err = ctr.WithExec([]string{"test", "!", "-e", "dagger.toml"}).Sync(ctx)
		require.NoError(t, err, "no nested workspace config should be created in the module directory")

		wsOut, err := ctr.WithExec([]string{"cat", "/work/dagger.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, wsOut, `[modules.tc]`)
		require.Contains(t, wsOut, `source = "./tools/hello/toolchain"`,
			"local toolchain sources are rebased to the repo root")
		require.NotContains(t, wsOut, `[modules.hello]`,
			"the subdirectory module is not installed into the repo-wide workspace")
		// The module's runtime is pinned in the hoisted root workspace and
		// resolved to its real ref by the setup SDK fixup pass.
		require.Contains(t, wsOut, `[modules.dagger-dang-sdk]`)
		require.Contains(t, wsOut, `source = "github.com/dagger/dang-sdk"`)

		mjson, err := ctr.WithExec([]string{"cat", "dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, mjson, `name = "hello"`)
		_, err = ctr.WithExec([]string{"test", "!", "-e", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "legacy config should be removed after in-place conversion")

		_, err = ctr.WithExec([]string{"test", "-f", "toolchain/dagger-module.toml"}).Sync(ctx)
		require.NoError(t, err, "the local toolchain converts in place")

		reportOut, err := ctr.WithExec([]string{"cat", "/work/.dagger/migration-report.md"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, reportOut, "## tools/hello requires explicit loading")
	})

	t.Run("subdirectory blueprint config is left legacy with a warning", func(ctx context.Context, t *testctx.T) {
		// A blueprint needs a workspace config, and hoisting an entrypoint to
		// the repo root would change repo-wide behavior — the config is left
		// as legacy, and setup explains the skip instead of claiming there is
		// nothing to migrate.
		ctr := workspaceBase(t, c).
			WithNewFile("tools/hello/dagger.json", `{
  "name": "hello",
  "blueprint": {"name": "bp", "source": "github.com/dagger/dagger/modules/wolfi@main"}
}`).
			WithExec([]string{"git", "add", "."}).
			WithExec([]string{"git", "commit", "-m", "initial"}).
			WithWorkdir("/work/tools/hello").
			With(daggerExec("setup", "--auto-apply"))

		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "Skipped: skipped migrating tools/hello/dagger.json: it defines a blueprint")
		require.NotContains(t, out, "Step 3: Recommended modules")

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "the blueprint config stays legacy")
		for _, cfg := range []string{"dagger.toml", "dagger-module.toml", "/work/dagger.toml"} {
			_, err = ctr.WithExec([]string{"test", "!", "-e", cfg}).Sync(ctx)
			require.NoError(t, err, "nothing should be written for a skipped blueprint config (%s)", cfg)
		}
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

	t.Run("modules in default dot dagger modules directory are not crawled", func(ctx context.Context, t *testctx.T) {
		// Migration only follows the selected root config and its local
		// references; with no root dagger.json, modules sitting under
		// .dagger/modules are left exactly as they are.
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

		_, err = ctr.WithExec([]string{"test", "!", "-e", "dagger.toml"}).Sync(ctx)
		require.NoError(t, err, "no workspace config should be created")

		for _, mod := range []string{"videostitch", "clipper"} {
			_, err = ctr.WithExec([]string{"test", "-f", ".dagger/modules/" + mod + "/dagger.json"}).Sync(ctx)
			require.NoError(t, err, "unreferenced module %s keeps its legacy config", mod)

			_, err = ctr.WithExec([]string{"test", "!", "-e", ".dagger/modules/" + mod + "/dagger-module.toml"}).Sync(ctx)
			require.NoError(t, err, "unreferenced module %s is not converted", mod)
		}

		_, err = ctr.WithExec([]string{"test", "!", "-e", ".dagger/migration-report.md"}).Sync(ctx)
		require.NoError(t, err, "no migration report should be written")
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

		source := "github.com/dagger/dagger/modules/wolfi@main"
		pin := strings.Repeat("1", 40)
		legacyLock := workspace.NewLock()
		require.NoError(t, legacyLock.SetLookup(
			"",
			"oci-sha",
			[]any{"docker.io/library/alpine:latest"},
			"sha256:"+strings.Repeat("0", 64),
		))
		require.NoError(t, legacyLock.SetLookup("", "modules.resolve", []any{source}, pin))
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
		assertOCISHALockEntry(t, []byte(lockOut))
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
				WithNewFile("dagger-module.toml", `name = "some-other-module"
`)
		})

		out, err := ctr.With(daggerExecFail("setup", "--auto-apply")).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `migration target "dagger-module.toml" already exists`)
		require.Contains(t, out, "refusing to overwrite")

		conflictOut, err := ctr.WithExec([]string{"cat", "dagger-module.toml"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, conflictOut, `name = "some-other-module"`)

		_, err = ctr.WithExec([]string{"test", "-f", "dagger.json"}).Sync(ctx)
		require.NoError(t, err, "failed migration should leave legacy config in place")
	})
}
