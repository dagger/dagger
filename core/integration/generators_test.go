package core

// These tests cover `dagger generate`, which runs module generator functions
// that write files back to the caller's workspace. They verify listing and
// running generators from SDK modules, legacy compat blueprints, and
// workspace-installed modules.
//
// See also:
// - checks_test.go: check discovery and execution.
// - workspace_modules_test.go: installing modules into workspaces.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dagger.io/dagger"
	"github.com/dagger/dagger/util/lockfile"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type GeneratorsSuite struct{}

func TestGenerators(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(GeneratorsSuite{})
}

func generatorsTestEnv(t *testctx.T, c *dagger.Client) (*dagger.Container, error) {
	return specificTestEnv(t, c, "generators")
}

func (GeneratorsSuite) TestGeneratorsDirectSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-generators"},
		{"typescript", "hello-with-generators-ts"},
		{"python", "hello-with-generators-py"},
		{"java", "hello-with-generators-java"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			modGen, err := generatorsTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.WithWorkdir(tc.path)

			t.Run("list", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.
					With(daggerExec("generate", "-l")).
					CombinedOutput(ctx)
				require.NoError(t, err)
				require.Contains(t, out, "generate-files")
				require.Contains(t, out, "generate-other-files")
				require.Contains(t, out, "empty-changeset")
				require.Contains(t, out, "changeset-failure")
				require.Contains(t, out, "other-generators:gen-things")
			})

			t.Run("generate single", func(ctx context.Context, t *testctx.T) {
				modGen := modGen
				exists, err := modGen.Exists(ctx, "foo")
				require.NoError(t, err)
				require.False(t, exists)

				modGen = modGen.
					With(daggerExec("generate", "generate-files", "-y", "--progress=plain"))
				out, err := modGen.
					CombinedOutput(ctx)
				// require.ErrorContains(t, err, "plop")
				require.NoError(t, err)
				// there's no specific message when changes are applied
				require.NotContains(t, out, "no changes to apply")

				exists, err = modGen.Exists(ctx, "foo")
				require.NoError(t, err)
				require.True(t, exists)
			})

			t.Run("generate multiple", func(ctx context.Context, t *testctx.T) {
				modGen := modGen
				exists, err := modGen.Exists(ctx, "foo")
				require.NoError(t, err)
				require.False(t, exists)
				exists, err = modGen.Exists(ctx, "bar")
				require.NoError(t, err)
				require.False(t, exists)

				modGen = modGen.
					With(daggerExec("generate", "generate-*", "-y", "--progress=plain"))
				out, err := modGen.
					CombinedOutput(ctx)
				require.NoError(t, err)
				require.NotContains(t, out, "no changes to apply")

				exists, err = modGen.Exists(ctx, "foo")
				require.NoError(t, err)
				require.True(t, exists)
				exists, err = modGen.Exists(ctx, "bar")
				require.NoError(t, err)
				require.True(t, exists)
			})

			t.Run("empty changeset", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.
					With(daggerExec("generate", "empty-changeset", "-y", "--progress=plain")).
					CombinedOutput(ctx)
				require.NoError(t, err)
				require.Contains(t, out, "no changes to apply")
			})

			t.Run("error", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.
					WithExec([]string{"dagger", "generate", "changeset-failure", "-y", "--progress=plain"}, dagger.ContainerWithExecOpts{
						Expect:                        dagger.ReturnTypeAny,
						ExperimentalPrivilegedNesting: true,
					}).
					CombinedOutput(ctx)
				require.NoError(t, err)
				require.Regexp(t, `changeset-failure.*ERROR`, out)
				require.Contains(t, out, "could not generate the changeset")
			})
		})
	}
}

func (GeneratorsSuite) TestGenerateValidationRejectsBadSignature(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("wrong return type", func(ctx context.Context, t *testctx.T) {
		modGen, err := generatorsTestEnv(t, c)
		require.NoError(t, err)
		modGen = modGen.WithWorkdir("badgenerate-return")

		// badgenerate-return's @generate returns Directory!, which must be
		// rejected at module load.
		out, err := modGen.
			With(daggerExecFail("functions")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "@generate functions must return the core Changeset! type")

		// generate tolerates load failures by default (best-effort), but
		// --require-load turns the skipped module fatal.
		out, err = modGen.
			With(daggerExecFail("generate", "-l", "--require-load")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "could not be loaded")
	})

	t.Run("required arg", func(ctx context.Context, t *testctx.T) {
		modGen, err := generatorsTestEnv(t, c)
		require.NoError(t, err)
		modGen = modGen.WithWorkdir("badgenerate-arg")

		// badgenerate-arg's @generate declares a required `name: String!`, which
		// must be rejected at module load.
		out, err := modGen.
			With(daggerExecFail("functions")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "@generate functions must be callable with no arguments")
	})
}

func (GeneratorsSuite) TestGenerateApplyDisposition(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen, err := generatorsTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("hello-with-generators")
	agent := modGen.WithEnvVariable("CODEX_CI", "1")

	t.Run("agent requires an explicit choice before running", func(ctx context.Context, t *testctx.T) {
		failed := agent.With(daggerExecFail("generate", "generate-files"))
		out, err := failed.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "requires an explicit changeset choice")
		require.Contains(t, out, "-y/--auto-apply")
		require.Contains(t, out, "--no-apply")

		exists, err := failed.Exists(ctx, "foo")
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("agent list mode does not require a choice", func(ctx context.Context, t *testctx.T) {
		out, err := agent.
			With(daggerExec("generate", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "generate-files")
	})

	t.Run("no apply previews without exporting", func(ctx context.Context, t *testctx.T) {
		previewed := agent.With(daggerExec("generate", "generate-files", "--no-apply"))
		out, err := previewed.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "foo")
		require.Contains(t, out, "Generated changes were not applied (--no-apply).")

		exists, err := previewed.Exists(ctx, "foo")
		require.NoError(t, err)
		require.False(t, exists)
	})

	t.Run("report mode cannot wait for confirmation", func(ctx context.Context, t *testctx.T) {
		failed := modGen.With(daggerExecFail("generate", "generate-files", "--progress=report"))
		out, err := failed.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "interactive prompts are unavailable in report mode")
		require.Contains(t, out, "-y/--auto-apply")
		require.Contains(t, out, "--no-apply")

		exists, err := failed.Exists(ctx, "foo")
		require.NoError(t, err)
		require.False(t, exists)
	})
}

// A generator whose changeset evaluates lazily and whose backing exec fails must
// surface that failure -- the command, its stderr, and its exit code -- to the
// user of `dagger generate`, rather than a bare "exit code: N" with the detail
// hidden. The failing exec is now forced inside the generator's span (see
// ModTreeNode.runGeneratorLocally), so the run fails there. Regression for
// #13606; the rendered-attribution half (a red generator row) is pinned by the
// generate-fail golden in dagql/idtui.
func (GeneratorsSuite) TestGeneratorLazyExecFailureSurfacesStderr(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen, err := generatorsTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("hello-with-generators")

	out, err := modGen.
		WithExec([]string{"dagger", "generate", "lazy-exec-failure", "-y", "--progress=plain"}, dagger.ContainerWithExecOpts{
			Expect:                        dagger.ReturnTypeAny,
			ExperimentalPrivilegedNesting: true,
		}).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "STDERR_ONLY_MARKER") // stderr surfaced
	require.Contains(t, out, "exit code: 3")       // exit code surfaced
	require.Contains(t, out, "sh -c")              // failed command surfaced
}

func (GeneratorsSuite) TestGeneratorsViaLegacyBlueprintConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-generators"},
		{"typescript", "hello-with-generators-ts"},
		{"python", "hello-with-generators-py"},
		{"java", "hello-with-generators-java"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			modGen, err := generatorsTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.WithWorkdir("app").
				WithNewFile("dagger.json", `{"name":"app","blueprint":{"name":"blueprint","source":"../`+tc.path+`"}}`)

			t.Run("list", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.
					With(daggerExec("generate", "-l")).
					CombinedOutput(ctx)
				require.NoError(t, err)
				require.Contains(t, out, "generate-files")
				require.Contains(t, out, "generate-other-files")
			})

			t.Run("generate", func(ctx context.Context, t *testctx.T) {
				modGen := modGen
				exists, err := modGen.Exists(ctx, "foo")
				require.NoError(t, err)
				require.False(t, exists)
				exists, err = modGen.Exists(ctx, "bar")
				require.NoError(t, err)
				require.False(t, exists)

				modGen = modGen.
					With(daggerExec("generate", "generate-*", "-y", "--progress=plain"))
				out, err := modGen.
					CombinedOutput(ctx)
				require.NoError(t, err)
				require.NotContains(t, out, "no changes to apply")

				exists, err = modGen.Exists(ctx, "foo")
				require.NoError(t, err)
				require.True(t, exists)
				exists, err = modGen.Exists(ctx, "bar")
				require.NoError(t, err)
				require.True(t, exists)
			})
		})
	}
}

func (GeneratorsSuite) TestGeneratorsInstalledInWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-generators"},
		{"typescript", "hello-with-generators-ts"},
		{"python", "hello-with-generators-py"},
		{"java", "hello-with-generators-java"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			modGen, err := generatorsTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.
				WithWorkdir("app").
				// Workspace creation is implicit on first install; the
				// `dagger workspace init` verb was removed in CLI 1.0.
				With(daggerExec("module", "install", "../"+tc.path))

			t.Run("list", func(ctx context.Context, t *testctx.T) {
				out, err := modGen.
					With(daggerExec("generate", "-l")).
					CombinedOutput(ctx)
				require.NoError(t, err)
				require.Contains(t, out, tc.path+":generate-files")
				require.Contains(t, out, tc.path+":generate-other-files")
			})

			t.Run("generate", func(ctx context.Context, t *testctx.T) {
				modGen := modGen
				exists, err := modGen.Exists(ctx, "foo")
				require.NoError(t, err)
				require.False(t, exists)
				exists, err = modGen.Exists(ctx, "bar")
				require.NoError(t, err)
				require.False(t, exists)

				modGen = modGen.
					With(daggerExec("generate", tc.path+":generate-*", "-y", "--progress=plain"))
				out, err := modGen.
					CombinedOutput(ctx)
				require.NoError(t, err)
				require.NotContains(t, out, "no changes to apply")

				exists, err = modGen.Exists(ctx, "foo")
				require.NoError(t, err)
				require.True(t, exists)
				exists, err = modGen.Exists(ctx, "bar")
				require.NoError(t, err)
				require.True(t, exists)
			})
		})
	}
}

func (GeneratorsSuite) TestGeneratorGroupChangesSyncWithNestedSDKCodegen(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	sdkModulePath, err := filepath.Abs("testdata/sdks/module-max-lifecycle")
	require.NoError(t, err)

	modGen := goGitBase(t, c).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c)).
		WithDirectory(".dagger/modules/module-max-lifecycle", c.Host().Directory(sdkModulePath)).
		WithNewFile("dagger.toml", `[modules.consumer]
source = ".dagger/modules/consumer"
entrypoint = true

[modules.go-sdk]
source = ".dagger/modules/module-max-lifecycle"

[modules.target-alias]
source = "target"

[modules.leaf]
source = "leaf"

[sdks.go]
module = "go-sdk"

[sdks.go.scopes."."]
is-module = true
name = "demo"
clients = ["target-alias"]

[sdks.go.scopes.target]
is-module = true
name = "target"
clients = ["leaf"]

[sdks.go.scopes.leaf]
is-module = true
name = "leaf"
`).
		WithNewFile(".dagger/modules/consumer/dagger.json", `{
  "name": "consumer",
  "engineVersion": "latest",
  "sdk": { "source": "go" },
  "source": "."
}`).
		WithNewFile(".dagger/modules/consumer/main.go", `package main

import (
	"context"

	"dagger/consumer/internal/dagger"
)

type Consumer struct{}

func (m *Consumer) SyncGenerators(ctx context.Context, workspace *dagger.Workspace) (string, error) {
	generatorChanges, err := workspace.
		Generators().
		Run().
		Changes(dagger.GeneratorGroupChangesOpts{
			OnConflict: dagger.ChangesetsMergeConflictFailEarly,
		}).
		Sync(ctx)
	if err != nil {
		return "", err
	}

	_, err = generatorChanges.Sync(ctx)
	if err != nil {
		return "", err
	}

	return "ok", nil
}
`)

	// One synthetic generator reconciles complete SDK scopes. The target scope
	// runs before the root scope because the root client targets it.
	listOut, err := modGen.
		With(daggerNonNestedExec("generate", "-l")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, listOut, "go-sdk:generate")
	require.NotContains(t, listOut, "go-sdk:generate-modules")
	require.NotContains(t, listOut, "go-sdk:generate-clients")

	synced := modGen.With(daggerNonNestedExec("call", "sync-generators"))
	out, err := synced.CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "ok")
	require.NotContains(t, out, "result *core.Changeset is detached")

	generated := modGen.With(daggerNonNestedExec("generate", "-y"))
	rootMarker, err := generated.File("internal/dagger/sdk-module-max.gen.txt").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, rootMarker, "for demo")
	targetMarker, err := generated.File("target/internal/dagger/sdk-module-max.gen.txt").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, targetMarker, "for target")
	clientMarker, err := generated.File("internal/dagger/clients/target.gen.go").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, clientMarker, "Code generated by module-max-lifecycle")

	// Workspace update must run the target scope before the root scope. The
	// target scope is a client scope too, and the root cannot load it after its
	// module manifest is removed until that scope runs again.
	withoutGeneratedTarget := generated.WithoutFile("target/dagger-module.toml")
	workspaceUpdated := withoutGeneratedTarget.With(daggerNonNestedExec("workspace", "update"))
	clientMarker, err = workspaceUpdated.File("internal/dagger/clients/target.gen.go").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, clientMarker, "Code generated by module-max-lifecycle")
	_, err = workspaceUpdated.File("target/dagger-module.toml").Contents(ctx)
	require.NoError(t, err)

	withoutClientMarker := generated.WithoutFile("internal/dagger/clients/target.gen.go")

	workspaceUpdatedWithoutGeneration := withoutClientMarker.With(daggerNonNestedExec("workspace", "update", "--no-generate"))
	exists, err := workspaceUpdatedWithoutGeneration.Exists(ctx, "internal/dagger/clients/target.gen.go")
	require.NoError(t, err)
	require.False(t, exists)

	moduleUpdated := withoutClientMarker.With(daggerNonNestedExec("module", "update", "target-alias"))
	clientMarker, err = moduleUpdated.File("internal/dagger/clients/target.gen.go").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, clientMarker, "Code generated by module-max-lifecycle")

	removed := generated.With(daggerNonNestedExec("module", "client", "rm", "target-alias", "-y"))
	exists, err = removed.Exists(ctx, "internal/dagger/clients/target.gen.go")
	require.NoError(t, err)
	require.False(t, exists)

	added := removed.With(daggerNonNestedExec("module", "client", "add", "target-alias", "-y"))
	config, err := added.File("dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, config, `clients = ["target-alias"]`)
	clientMarker, err = added.File("internal/dagger/clients/target.gen.go").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, clientMarker, "Code generated by module-max-lifecycle")

	clientUpdated := added.
		WithoutFile("internal/dagger/clients/target.gen.go").
		With(daggerNonNestedExec("module", "client", "update", "target-alias"))
	clientMarker, err = clientUpdated.File("internal/dagger/clients/target.gen.go").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, clientMarker, "Code generated by module-max-lifecycle")
}

func (GeneratorsSuite) TestSDKModuleClientUpdateRefreshesLockAndRegenerates(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	sdkModulePath, err := filepath.Abs("testdata/sdks/module-max-lifecycle")
	require.NoError(t, err)

	const target = "github.com/dagger/dagger/modules/wolfi@main"
	base := goGitBase(t, c).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c)).
		WithDirectory(".dagger/modules/module-max-lifecycle", c.Host().Directory(sdkModulePath)).
		WithNewFile("dagger.toml", `[modules.go-sdk]
source = ".dagger/modules/module-max-lifecycle"

[sdks.go]
module = "go-sdk"

[sdks.go.scopes."."]
name = "demo"
clients = ["`+target+`"]
`)

	seeded := base.With(daggerNonNestedExec("module", "client", "update", target, "-y"))
	out, err := seeded.CombinedOutput(ctx)
	require.NoError(t, err, out)
	initialLock, err := seeded.File("dagger.lock").Contents(ctx)
	require.NoError(t, err)

	parsed, err := lockfile.Parse([]byte(initialLock))
	require.NoError(t, err)
	stale := lockfile.New()
	staleCommit := strings.Repeat("0", 40)
	var resolvedCommit string
	for _, entry := range parsed.Entries() {
		value := entry.Value
		if resolvedCommit == "" && len(value) == 40 && strings.HasPrefix(entry.Operation, "git") {
			for _, input := range entry.Inputs {
				input, ok := input.(string)
				if ok && strings.Contains(input, "github.com/dagger/dagger") {
					resolvedCommit = value
					value = staleCommit
					break
				}
			}
		}
		require.NoError(t, stale.Set(entry.Namespace, entry.Operation, entry.Inputs, value))
	}
	require.NotEmpty(t, resolvedCommit, initialLock)
	staleLockBytes, err := stale.Marshal()
	require.NoError(t, err)
	staleLock := string(staleLockBytes)
	require.Contains(t, staleLock, staleCommit)
	updated := seeded.
		WithNewFile("dagger.lock", staleLock).
		WithoutFile("internal/dagger/clients/wolfi.gen.go").
		With(daggerNonNestedExec("module", "client", "update", target, "-y"))
	out, err = updated.CombinedOutput(ctx)
	require.NoError(t, err, out)

	marker, err := updated.File("internal/dagger/clients/wolfi.gen.go").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, marker, "Code generated by module-max-lifecycle")

	updatedLock, err := updated.File("dagger.lock").Contents(ctx)
	require.NoError(t, err)
	require.NotContains(t, updatedLock, staleCommit)
	require.Contains(t, updatedLock, resolvedCommit)
}

func (GeneratorsSuite) TestSDKModuleInitDefaults(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	sdkModulePath, err := filepath.Abs("testdata/sdks/module-max-lifecycle")
	require.NoError(t, err)

	base := goGitBase(t, c).
		WithDirectory("/work/apps/shop/.dagger/modules/module-max-lifecycle", c.Host().Directory(sdkModulePath)).
		WithNewFile("/work/apps/shop/dagger.toml", `[modules.go-sdk]
source = ".dagger/modules/module-max-lifecycle"

[sdks.go]
module = "go-sdk"
`).
		WithNewFile("/work/apps/shop/internal/.keep", "").
		WithWorkdir("/work/apps/shop/internal").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c))

	t.Run("requires an installed SDK", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerNonNestedExecFail("module", "init", "missing", "-y")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `"missing" is not installed as an SDK`)
	})

	t.Run("infers name and path", func(ctx context.Context, t *testctx.T) {
		initialized := base.
			WithNewFile("/work/apps/shop/go.mod", "module example.com/shop\n\ngo 1.25\n").
			With(daggerNonNestedExec("module", "init", "go", "-y"))
		out, err := initialized.CombinedOutput(ctx)
		require.NoError(t, err, out)

		moduleRoot := "/work/apps/shop/.dagger/modules/shop-dev"
		_, err = initialized.File(moduleRoot + "/dagger-module.toml").Contents(ctx)
		require.NoError(t, err)
		marker, err := initialized.File(moduleRoot + "/internal/dagger/sdk-module-max.gen.txt").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, marker, "for shop-dev")

		config, err := initialized.File("/work/apps/shop/dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `[sdks.go.scopes.".dagger/modules/shop-dev"]`)
		require.Contains(t, config, `name = "shop-dev"`)
	})
}

func (GeneratorsSuite) TestSDKModuleCanWriteAboveScope(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	sdkModulePath, err := filepath.Abs("testdata/sdks/module-max-workspace-writer")
	require.NoError(t, err)

	workspace := goGitBase(t, c).
		WithDirectory("/work/sdk", c.Host().Directory(sdkModulePath)).
		WithNewFile("/work/app/dagger.toml", `[modules.workspace-writer]
source = "../sdk"

[sdks.test]
module = "workspace-writer"

[sdks.test.scopes."generated/demo"]
is-module = true
name = "demo"
`).
		WithWorkdir("/work/app").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c))

	generated := workspace.With(daggerNonNestedExec("generate", "-y"))
	out, err := generated.CombinedOutput(ctx)
	require.NoError(t, err, out)

	goMod, err := generated.File("/work/app/go.mod").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, goMod, "module example.com/demo")
	_, err = generated.File("/work/app/generated/demo/dagger-module.toml").Contents(ctx)
	require.NoError(t, err)
}

func (GeneratorsSuite) TestSDKModuleDefaultModulePath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	sdkModulePath, err := filepath.Abs("testdata/sdks/module-max-workspace-writer")
	require.NoError(t, err)

	workspace := goGitBase(t, c).
		WithDirectory("/work/sdk", c.Host().Directory(sdkModulePath)).
		WithNewFile("/work/app/dagger.toml", `[modules.workspace-writer]
source = "../sdk"

[sdks.test]
module = "workspace-writer"
`).
		WithWorkdir("/work/app").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c))

	initialized := workspace.With(daggerNonNestedExec("module", "init", "test", "--name", "demo", "-y"))
	out, err := initialized.CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.NotContains(t, out, "Custom path; module was not installed.")

	_, err = initialized.File("/work/generated/demo/dagger-module.toml").Contents(ctx)
	require.NoError(t, err)
	goMod, err := initialized.File("/work/go.mod").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, goMod, "module example.com/demo")
	config, err := initialized.File("/work/app/dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, config, "[modules.demo]\nsource = \"../generated/demo\"")
	require.Contains(t, config, `[sdks.test.scopes."../generated/demo"]`)
	require.Contains(t, config, `name = "demo"`)

	custom := workspace.With(daggerNonNestedExec(
		"module", "init", "test", "--name", "custom", "--path", "custom", "-y",
	))
	out, err = custom.CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "Initialized module custom\nCustom path; module was not installed.\n")
	config, err = custom.File("/work/app/dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.NotContains(t, config, "[modules.custom]")
	require.Contains(t, config, `[sdks.test.scopes.custom]`)
}

// TestSDKScopeGenerationRepairsLocalDependency starts with invalid source in
// the root module. The nested scope needs that module's client schema. The
// engine must generate the root scope before it loads the root module for the
// nested client. This also covers the former root-dependency recursion: one
// top-level graph run must terminate without starting nested generation.
func (GeneratorsSuite) TestSDKScopeGenerationRepairsLocalDependency(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := workspaceFixture(t, c, "sdk-scope-generation").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c))

	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	generated := base.With(daggerNonNestedExec("generate", "-y"))
	out, err := generated.CombinedOutput(runCtx)
	require.NoError(t, err, out)

	rootSource, err := generated.File("main.dang").Contents(ctx)
	require.NoError(t, err)
	require.NotContains(t, rootSource, "intentionally invalid")

	clientSchema, err := generated.File("nested/generated/root.schema.json").Contents(ctx)
	require.NoError(t, err)
	// The SDK got this file from ModuleSource.clientSchemaIntrospectionJSON.
	// Materializing it loads the generated root module. Other tests cover the
	// contents of the client schema; this test only requires a valid schema.
	require.Contains(t, parseSchemaContents(t, clientSchema).typeNames(), "Query")
}

// TestWorkspaceListsSyntheticGeneratorWithoutLoadingSDK proves that generator
// discovery uses only workspace configuration. The SDK can be temporarily
// invalid because generation can be the operation that repairs it.
func (GeneratorsSuite) TestWorkspaceListsSyntheticGeneratorWithoutLoadingSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	workspace := workspaceFixture(t, c, "generators-broken").
		WithNewFile("dagger.toml", `[modules.good]
source = ".dagger/modules/good"

[modules.bad]
source = ".dagger/modules/bad"

[sdks.bad]
module = "bad"
`).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c))

	out, err := workspace.
		With(daggerNonNestedExec("generate", "-l")).
		CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "bad:generate")
}

// TestWorkspaceGenerateNarrowsToRequestedModule locks in that
// `dagger generate <module>` only loads the named generator's module. The
// workspace generators resolver loads modules on demand from its include
// argument, so an unrelated broken/stale workspace module is never loaded just
// to enumerate generators and cannot block regenerating a healthy module --
// including the case where running generate is itself the fix for the broken
// module.
//
// See also TestSingleQueryWorkspaceModuleLoadingSkipsUnreferencedBrokenModules
// in workspace_test.go, which covers the root-field demand path for raw
// queries.
func (GeneratorsSuite) TestWorkspaceGenerateNarrowsToRequestedModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "generators-broken")

	t.Run("listing only the healthy module skips the broken one", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("generate", "-l", "good")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("generating only the healthy module succeeds", func(ctx context.Context, t *testctx.T) {
		// generate -y is multi-request (list, then run+apply); the later
		// requests must keep recognizing the already-loaded module instead of
		// falling back to loading everything.
		out, err := base.
			With(daggerExec("generate", "good", "-y", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "no changes to apply")
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("listing across all modules enumerates the healthy generators despite a broken module", func(ctx context.Context, t *testctx.T) {
		// Enumerating all generators loads modules best-effort: the broken
		// module is still loaded (unlike the narrowed cases above, which never
		// touch it) but its failure is tolerated, so listing still succeeds and
		// shows the healthy generator instead of aborting. The skip itself is
		// surfaced as a span on the run path (asserted below), not in the list
		// table.
		out, err := base.
			With(daggerExec("generate", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "good:generate")
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("unscoped generate runs healthy generators despite a broken module", func(ctx context.Context, t *testctx.T) {
		// -l only lists; this exercises the run+apply path. The broken module is
		// reported as a skipped-module span, but the healthy `good` generator
		// still runs and applies -- it writes generated.txt with a known marker.
		ctr := base.With(daggerExec("generate", "-y", "--progress=plain"))
		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "no changes to apply")
		// In run mode the output is zoomed to the generators span; the
		// skipped-module span is revealed into that view so the user still sees
		// it (its load error names the broken module).
		require.Contains(t, out, "modules/bad")
		// grep -rl exits non-zero if the marker was never written, so NoError
		// proves the healthy generator applied.
		_, err = ctr.WithExec([]string{"grep", "-rl", "hello from good", "."}).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("--require-load makes a load failure fatal", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExecFail("generate", "-l", "--require-load")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "require-load")
	})

	t.Run("--require-load also catches an explicitly-selected unloadable module", func(ctx context.Context, t *testctx.T) {
		// Loading is best-effort even for an explicit selector, so naming the
		// broken module no longer aborts by itself; --require-load is what turns
		// its load failure into a hard error.
		out, err := base.
			With(daggerExecFail("generate", "bad", "--require-load")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "require-load")
		require.Contains(t, out, "modules/bad")
	})
}

// TestWorkspaceGenerateSkipsBrokenEntrypoint is a regression test for
// https://github.com/dagger/dagger/issues/13742: an entrypoint module that
// cannot load (e.g. a migrated v1 module whose local dependencies are missing
// their generated files) must not abort `dagger generate` — generate is often
// the repair for exactly that state. The generators listing already loads
// best-effort, but the CLI's follow-up queries are rooted at `node(id:)` (every
// post-Sync SDK handle is), and an unrecognized `node` root field used to
// strictly (re)load the pending entrypoint, failing every generate mode.
func (GeneratorsSuite) TestWorkspaceGenerateSkipsBrokenEntrypoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "generators-broken-entrypoint")

	t.Run("listing enumerates healthy generators despite a broken entrypoint", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("generate", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "good:generate")
	})

	t.Run("unscoped generate runs healthy generators despite a broken entrypoint", func(ctx context.Context, t *testctx.T) {
		ctr := base.With(daggerExec("generate", "-y", "--progress=plain"))
		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "no changes to apply")
		// The broken entrypoint is surfaced as a skipped-module span, not a
		// fatal error.
		require.Contains(t, out, "modules/bad")
		_, err = ctr.WithExec([]string{"grep", "-rl", "hello from good", "."}).Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("generate --no-apply previews despite a broken entrypoint", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("generate", "--no-apply", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "no changes to apply")
	})

	t.Run("--require-load still makes the entrypoint load failure fatal", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExecFail("generate", "-l", "--require-load")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "require-load")
	})
}

// TestWorkspaceGenerateReportsLoadFailureDetail covers what best-effort
// `dagger generate` says about the modules it skips
// (https://github.com/dagger/dagger/issues/13973): the skip must carry the
// reason a user can act on, not a bare exit code, and must not tell them to run
// the command they are running.
//
//   - broken-build: a Go module whose source does not compile. The compiler
//     output lives in the SDK runtime's `go build` exec, hidden under the
//     internal module-load spans; the skipped-module report inlines it.
//   - ungenerated: a dagger-module.toml Go module with no committed generated
//     files. Its strict-load error says "run `dagger generate`" — generate
//     itself reports it as skipped until generated.
//   - stale-build: does not compile either, and the regen generator writes
//     into its directory this run — so generate loads it again with the
//     changes applied. It still does not compile: the report shows the
//     post-generation error, not a hedge.
//   - fixable: does not compile until the fixer generator rewrites its
//     main.go this run. Loaded again with the changes applied it works: the
//     report says so and drops its pre-generation compiler output.
func (GeneratorsSuite) TestWorkspaceGenerateReportsLoadFailureDetail(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "generate-load-failures")

	requireSkipDetail := func(t *testctx.T, out string) {
		t.Helper()
		// The compile error is the reason broken-build was skipped: the go
		// build output follows the load error.
		require.Contains(t, out, "modules/broken-build")
		require.Contains(t, out, "exit code: 1")
		require.Contains(t, out, "undefined: intentionallyUndefinedSymbol")
		// Missing generated files: skipped, without the misleading hint.
		require.Contains(t, out, "modules/ungenerated")
		require.Contains(t, out, `generated file ".dagger/modules/ungenerated/dagger.gen.go" is missing (skipped until it is generated)`)
	}

	t.Run("report", func(ctx context.Context, t *testctx.T) {
		// The default non-interactive frontend renders the persisted SKIPPED
		// MODULES report section after the generators.
		out, err := base.
			With(daggerExec("generate", "--no-apply")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "good:generate")
		require.Contains(t, out, "SKIPPED MODULES")
		requireSkipDetail(t, out)
		// Only the skipped-module rows describe the failures, and they never
		// tell the user to run the command they are running.
		require.NotContains(t, out, "run `dagger generate`")
		// fixable was rewritten by the fixer generator and loads with the
		// changes applied: reported as such, pre-generation error dropped.
		require.Contains(t, out, "REGENERATED")
		require.Contains(t, out, "could not load before this run's changes; loads with them applied")
		require.NotContains(t, out, "fixableUndefinedSymbol")
		// stale-build was touched too but still does not compile: the report
		// carries the post-generation error (same compiler output, once) next
		// to the untouched broken-build's original one.
		require.Contains(t, out, "still fails to load with this run's changes")
		require.Equal(t, 2, strings.Count(out, "undefined: intentionallyUndefinedSymbol"), out)
	})

	t.Run("listing cannot classify, so it reports every skip", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("generate", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "good:generate")
		require.Contains(t, out, "modules/stale-build")
		require.Equal(t, 2, strings.Count(out, "undefined: intentionallyUndefinedSymbol"), out)
		require.Contains(t, out, "fixableUndefinedSymbol")
		require.NotContains(t, out, "REGENERATED")
	})

	t.Run("plain progress", func(ctx context.Context, t *testctx.T) {
		// Plain progress prints the skipped-module span's status inline on the
		// run path (the listing never shows skips). It also streams the raw
		// load spans as they fail, so the SDK's own message is visible too;
		// what matters is that the skip row carries the detail.
		out, err := base.
			With(daggerExec("generate", "--no-apply", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		requireSkipDetail(t, out)
	})

	t.Run("--require-load reports the detail through the API", func(ctx context.Context, t *testctx.T) {
		// loadFailures carries the same described messages, so an API consumer
		// (or --require-load's telemetry) sees the compiler output too.
		out, err := base.
			With(daggerExecFail("generate", "-l", "--require-load", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "require-load")
		require.Contains(t, out, "undefined: intentionallyUndefinedSymbol")
	})

	t.Run("strict load keeps the run-dagger-generate hint", func(ctx context.Context, t *testctx.T) {
		// Only generate rewrites the hint; a call that needs the module loaded
		// should still point the user at generate.
		out, err := base.
			With(daggerExecFail("call", "ungenerated", "hello")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "run `dagger generate`")
	})
}

// TestWorkspaceCheckNarrowsToRequestedModule mirrors
// TestWorkspaceGenerateNarrowsToRequestedModule for `dagger check`: an unrelated
// broken/stale workspace module must not be loaded just to enumerate or run a
// healthy module's checks, so it cannot block checking that module.
func (GeneratorsSuite) TestWorkspaceCheckNarrowsToRequestedModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "generators-broken")

	t.Run("listing only the healthy module skips the broken one", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("check", "-l", "good")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("running only the healthy module's checks succeeds", func(ctx context.Context, t *testctx.T) {
		// --no-generate runs only annotated checks; generate-as-checks are
		// excluded because the healthy module's generator legitimately reports
		// pending output (covered by the generate narrowing test), which is
		// unrelated to whether the broken module was loaded.
		out, err := base.
			With(daggerExec("check", "good", "--no-generate", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("checking across all modules still loads the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExecFail("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "bad")
	})
}

// TestWorkspaceUpNarrowsToRequestedModule mirrors
// TestWorkspaceGenerateNarrowsToRequestedModule for `dagger up`: an unrelated
// broken/stale workspace module must not be loaded just to enumerate a healthy
// module's services. `dagger up` starts services and blocks, so the assertions
// use list mode (-l), which still loads workspace modules to enumerate services
// and thus exercises the same narrowing.
func (GeneratorsSuite) TestWorkspaceUpNarrowsToRequestedModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "generators-broken")

	t.Run("listing only the healthy module skips the broken one", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("up", "-l", "good")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("listing across all modules still loads the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExecFail("up", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "bad")
	})
}

// TestWorkspaceCallNarrowsToRequestedModule mirrors
// TestWorkspaceGenerateNarrowsToRequestedModule for `dagger api call`:
// targeting a healthy module's function must not load every workspace module
// just to build the command tree. The CLI forwards its leading token as the
// workspace_module_scope client metadata hint and the engine narrows the
// currentTypeDefs introspection to that module, so an unrelated broken/stale
// module cannot block the call.
func (GeneratorsSuite) TestWorkspaceCallNarrowsToRequestedModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "generators-broken")

	t.Run("calling a healthy module's function skips the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("api", "call", "good", "verify", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("scoped help skips the broken module", func(ctx context.Context, t *testctx.T) {
		// --help builds the same command tree without executing, so it
		// exercises the narrowed introspection on its own.
		out, err := base.
			With(daggerExec("api", "call", "good", "--help")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("listing the healthy module's functions skips the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("api", "functions", "good", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("listing all workspace functions still loads the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExecFail("api", "functions")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "bad")
	})
}

// TestWorkspaceCallNarrowsByCliNameAndEntrypoint covers the two demand shapes
// TestWorkspaceCallNarrowsToRequestedModule cannot see with its single-word
// module names:
//
//   - the CLI targets modules by their kebab-case command name, so a module
//     declared as "goodMod" is called as `dagger api call good-mod ...` and
//     the engine must normalize both sides the same way to narrow loading;
//   - with a workspace entrypoint configured, the first argument may be one of
//     the entrypoint's root-proxied functions rather than a module name, in
//     which case the entrypoint module must load — and suffice — to resolve
//     the call.
//
// In both cases the broken sibling module must stay unloaded.
func (GeneratorsSuite) TestWorkspaceCallNarrowsByCliNameAndEntrypoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "call-narrowing")

	t.Run("kebab-case module target skips the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("api", "call", "good-mod", "ping", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "pong from goodMod")
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("entrypoint function target skips the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("api", "call", "greet", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from entrypoint")
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("kebab-case functions listing skips the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("api", "functions", "good-mod", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("kebab-case generate listing skips the broken module", func(ctx context.Context, t *testctx.T) {
		// The selector resolvers (generate/check/up) match include patterns
		// kebab-normalized; on-demand loading must normalize the same way.
		out, err := base.
			With(daggerExec("generate", "-l", "good-mod")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("listing all workspace functions still loads the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExecFail("api", "functions")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "bad")
	})

	t.Run("scope is one-shot within a session", func(ctx context.Context, t *testctx.T) {
		// Two invocations in one exec share the nested client: the first scoped
		// introspection narrows; the second, bare listing must widen to every
		// remaining module and surface the broken one.
		out, err := base.
			WithExec([]string{"sh", "-c", "set -e; dagger api call good-mod ping; if dagger api functions >/dev/null 2>&1; then echo BARE_LISTING_PASSED; else echo BARE_LISTING_FAILED; fi"}, dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true}).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "pong from goodMod")
		require.Contains(t, out, "BARE_LISTING_FAILED")
	})
}

func (GeneratorsSuite) TestWorkspaceGenerateSkip(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen, err := generatorsTestEnv(t, c)
	require.NoError(t, err)

	ctr := modGen.WithNewFile("dagger.toml", `[modules.hello-with-generators]
source = "hello-with-generators"
generate.skip = ["generate-other-files", "other-generators:*"]
`)

	listOut, err := ctr.With(daggerExec("generate", "-l")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, listOut, "hello-with-generators:generate-files")
	require.NotContains(t, listOut, "hello-with-generators:generate-other-files")
	require.NotContains(t, listOut, "hello-with-generators:other-generators:gen-things")
}

func (GeneratorsSuite) TestWorkspaceGeneratorsVisibleFromModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := generatorsTestEnv(t, c)
	require.NoError(t, err)

	out, err := modGen.
		WithWorkdir("hello-with-generators").
		WithNewFile("dagger.toml", `[modules.hello-with-generators]
source = "."
entrypoint = true

[modules.toolchain-generators]
source = "toolchain"
`).
		With(daggerCall("workspace-generators-empty")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "false", strings.TrimSpace(out))
}

func (GeneratorsSuite) TestGeneratorResultFieldsRequireRun(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen, err := generatorsTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("hello-with-generators")

	t.Run("group isEmpty requires run", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.
			With(daggerQuery(`{currentWorkspace{generators(include:["generate-files"]){isEmpty}}}`)).
			Stdout(ctx)
		requireErrOut(t, err, "must be run before querying isEmpty")
	})

	t.Run("group changes requires run", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.
			With(daggerQuery(`{currentWorkspace{generators(include:["generate-files"]){changes{isEmpty}}}}`)).
			Stdout(ctx)
		requireErrOut(t, err, "must be run before querying changes")
	})

	t.Run("single generator isEmpty requires run", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.
			With(daggerQuery(`{currentWorkspace{generators(include:["generate-files"]){list{isEmpty}}}}`)).
			Stdout(ctx)
		requireErrOut(t, err, "must be run before querying isEmpty")
	})

	t.Run("single generator changes requires run", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.
			With(daggerQuery(`{currentWorkspace{generators(include:["generate-files"]){list{changes{isEmpty}}}}}`)).
			Stdout(ctx)
		requireErrOut(t, err, "must be run before querying changes")
	})

	t.Run("group result fields work after run", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQuery(`{currentWorkspace{generators(include:["generate-files"]){run{isEmpty changes{isEmpty}}}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"generators":{"run":{"isEmpty":false,"changes":{"isEmpty":false}}}}}`, out)
	})

	t.Run("single generator result fields work after run", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQuery(`{currentWorkspace{generators(include:["generate-files"]){list{run{isEmpty changes{isEmpty}}}}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"currentWorkspace":{"generators":{"list":[{"run":{"isEmpty":false,"changes":{"isEmpty":false}}}]}}}`, out)
	})
}

// TestClientSchemaIntrospectionJSON locks in that
// moduleSource.clientSchemaIntrospectionJSON returns the *client-facing* schema
// -- the one client codegen consumes. Unlike the module-facing
// introspectionSchemaJSON, it hides no core types (Host and the Engine* family
// stay visible) and installs the bound module namespaced on Query
// (Query.minimal), so a client reaches its functions via dag.minimal -- never a
// promoted root field (no Query.hello). The two schemas are deliberately
// different; feeding the module-facing one to client codegen would produce an
// incomplete client.
func (GeneratorsSuite) TestClientSchemaIntrospectionJSON(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/minimal")

	clientTypes, clientQueryFields := introspectModuleSourceSchema(ctx, t, modGen, "clientSchemaIntrospectionJSON")
	moduleTypes, moduleQueryFields := introspectModuleSourceSchema(ctx, t, modGen, "introspectionSchemaJSON")

	// No hidden types in the client-facing schema: Host and Engine* (both hidden
	// from the module-facing schema) are present.
	require.Contains(t, clientTypes, "Host")
	require.Contains(t, clientTypes, "EngineCache")
	require.NotContains(t, moduleTypes, "Host")
	require.NotContains(t, moduleTypes, "EngineCache")

	// The bound module is namespaced on Query (dag.minimal), never promoted to
	// the root: `minimal` is a Query field but its function `hello` is not.
	require.Contains(t, clientQueryFields, "minimal")
	require.NotContains(t, clientQueryFields, "hello")
	// The module-facing schema installs neither the module nor its functions.
	require.NotContains(t, moduleQueryFields, "minimal")
	require.NotContains(t, moduleQueryFields, "hello")
}

// introspectModuleSourceSchema selects the named introspection-schema field on
// the module source at ".", returning the schema's type names and its Query
// root field names.
func introspectModuleSourceSchema(ctx context.Context, t *testctx.T, ctr *dagger.Container, field string) (typeNames, queryFields []string) {
	t.Helper()
	out, err := ctr.
		With(daggerQuery(`{moduleSource(refString:"."){%s{contents}}}`, field)).
		Stdout(ctx)
	require.NoError(t, err)

	var resp struct {
		ModuleSource map[string]struct {
			Contents string `json:"contents"`
		} `json:"moduleSource"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	contents := resp.ModuleSource[field].Contents
	require.NotEmpty(t, contents)

	schema := parseSchemaContents(t, contents)
	for _, typ := range schema.Schema.Types {
		if typ.Name == "Query" {
			for _, f := range typ.Fields {
				queryFields = append(queryFields, f.Name)
			}
		}
	}
	return schema.typeNames(), queryFields
}

func (GeneratorsSuite) TestBetaSDKModuleAPIIsRemoved(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	out, err := goGitBase(t, c).
		With(daggerQuery(`{
			current: __type(name: "CurrentModule") { fields { name } }
			moduleSource: __type(name: "ModuleSource") { fields { name } }
			workspace: __type(name: "Workspace") { fields { name } }
			schema: __schema { types { name } }
		}`)).
		Stdout(ctx)
	require.NoError(t, err)

	var resp struct {
		Current struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"current"`
		ModuleSource struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"moduleSource"`
		Workspace struct {
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"workspace"`
		Schema struct {
			Types []struct {
				Name string `json:"name"`
			} `json:"types"`
		} `json:"schema"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	for _, field := range resp.Current.Fields {
		require.NotEqual(t, "asSDK", field.Name)
	}
	for _, field := range resp.ModuleSource.Fields {
		require.NotEqual(t, "generateLocalDependencies", field.Name)
	}
	for _, field := range resp.Workspace.Fields {
		require.NotEqual(t, "__withGeneratedLocalDependencies", field.Name)
	}
	typeNames := make([]string, 0, len(resp.Schema.Types))
	for _, typ := range resp.Schema.Types {
		typeNames = append(typeNames, typ.Name)
	}
	require.NotContains(t, typeNames, "CurrentModuleAsSDK")
	require.NotContains(t, typeNames, "CurrentModuleAsSDKModule")
	require.NotContains(t, typeNames, "CurrentModuleAsSDKClient")
}

// TestWorkspaceGeneratorsSeeOverlayEdits locks in that a generator run via
// Workspace.generators observes the workspace it was called on — including
// overlay edits (Workspace.withNewFile, or an agent's applied changesets) —
// rather than the session's frozen current workspace. The group run threads
// its receiver workspace into every leaf (GeneratorGroup.BoundWorkspace), which
// also gives every generator across the group's SDK modules the same workspace
// ID — without it, each leaf re-derives a per-call equivalent workspace, and
// nothing keyed by (module, workspace) is shared across the group.
//
// The generator-workspace-sync fixture's `repro:gen` reads input.txt from the
// workspace and writes output.txt = "generated from: <input>", so the output
// reveals which workspace the generator actually read.
func (GeneratorsSuite) TestWorkspaceGeneratorsSeeOverlayEdits(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "generator-workspace-sync")

	t.Run("baseline reads input.txt from the workspace", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerQuery(`{currentWorkspace{generators(include:["repro"]){run{changes{layer{file(path:"output.txt"){contents}}}}}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "generated from: A")
	})

	t.Run("generator sees an overlay edit applied to the workspace", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerQuery(`{currentWorkspace{withNewFile(path:"input.txt",contents:"B-OVERLAY"){generators(include:["repro"]){run{changes{layer{file(path:"output.txt"){contents}}}}}}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "generated from: B-OVERLAY")
	})
}
