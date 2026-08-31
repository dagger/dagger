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
	"path"
	"strings"
	"testing"

	"dagger.io/dagger"
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
				With(daggerExec("install", "../"+tc.path))

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

// TestSDKGeneratorOwnsClients verifies the engine-to-SDK handoff for generated
// clients without depending on any production SDK's code generation. The SDK
// module reads the clients assigned to it in dagger.toml and exposes an ordinary
// generator that writes one marker file per client.
func (GeneratorsSuite) TestSDKGeneratorOwnsClients(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := goGitBase(t, c).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c)).
		WithNewFile("dagger.toml", `[modules.client-generator-fixture]
source = ".dagger/client-generator-fixture"

[modules.client-generator-fixture.as-sdk]
name = "fixture"

[[modules.client-generator-fixture.as-sdk.clients]]
path = "clients/one"
module = "github.com/shykes/hello"
pin = "deadbeef"

[[modules.client-generator-fixture.as-sdk.clients]]
path = "clients/two"
module = ".dagger/client-generator-fixture"
`).
		WithNewFile(".dagger/client-generator-fixture/dagger.json", `{
  "name": "client-generator-fixture",
  "engineVersion": "latest",
  "sdk": { "source": "go" },
  "source": "."
}`).
		WithNewFile(".dagger/client-generator-fixture/main.go", `package main

import (
	"context"
	"strings"

	"dagger/client-generator-fixture/internal/dagger"
)

type ClientGeneratorFixture struct{}

// +generate
func (m *ClientGeneratorFixture) GenerateClients(ctx context.Context, ws *dagger.Workspace) (*dagger.Changeset, error) {
	clients, err := dag.CurrentModule().AsSDK(ws).Clients(ctx)
	if err != nil {
		return nil, err
	}

	generated := ws
	for _, client := range clients {
		path, err := client.Path(ctx)
		if err != nil {
			return nil, err
		}
		module, err := client.Module(ctx)
		if err != nil {
			return nil, err
		}
		pin, err := client.Pin(ctx)
		if err != nil {
			return nil, err
		}
		contents := module + "\n" + pin + "\n"
		// For a locally-bound client, resolve its module source through the
		// bound workspace and record it, proving moduleSource resolves against
		// the workspace asSDK was called on.
		if strings.HasPrefix(module, ".") {
			src, err := client.ModuleSource().AsString(ctx)
			if err != nil {
				return nil, err
			}
			contents += "source=" + src + "\n"
		}
		generated = generated.WithNewFile(path+"/generated.txt", contents)
	}

	return generated.Changes(dagger.WorkspaceChangesOpts{From: ws}), nil
}
`)

	list, err := base.
		With(daggerExec("generate", "-l")).
		CombinedOutput(ctx)
	require.NoError(t, err, list)
	require.Contains(t, list, "client-generator-fixture:generate-clients")

	clients, err := base.
		With(daggerExec("api", "client", "list", "--json")).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `[
  {"sdk":"fixture","path":"clients/one","module":"github.com/shykes/hello","pin":"deadbeef"},
  {"sdk":"fixture","path":"clients/two","module":".dagger/client-generator-fixture"}
]`, clients)

	generated := base.With(daggerExec("generate", "-y"))

	one, err := generated.File("clients/one/generated.txt").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "github.com/shykes/hello\ndeadbeef\n", one)

	// clients/two is locally bound, so the generator additionally resolved its
	// module source through the bound workspace (see the fixture). A non-empty
	// source line proves currentModuleAsSDKClientModuleSource resolved against
	// the workspace asSDK was called on.
	two, err := generated.File("clients/two/generated.txt").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, two, ".dagger/client-generator-fixture\n\nsource=")
	require.Contains(t, two, "client-generator-fixture")
}

// initGeneratorFixture is an SDK module supporting `module init` / `client init`
// alongside cwd-anchored generators, mirroring how the real SDKs discover work
// (go-sdk's `modules(ws)`, typescript-sdk's `generateAllClient`): each generator
// emits a marker for the modules or clients its workspace entry manages at or
// below the workspace cwd. Paths in a returned changeset are cwd-relative, which
// is what lets the engine re-root a cwd-scoped run.
//
// The workspace it manages always holds one pre-existing module and one
// pre-existing client, so anything generated outside the initialized path shows
// up as a stray marker.
func initGeneratorFixture(t testing.TB, c *dagger.Client) *dagger.Container {
	return initGeneratorFixtureAt(t, c, ".")
}

// initGeneratorFixtureAt builds the fixture with its dagger.toml under
// configDir, so tests can exercise a workspace config that is not at the
// workspace (git) root. Every path in dagger.toml — [modules.X].source and the
// as-sdk entries alike — is recorded relative to that config directory, so the
// fixture below reads the same wherever configDir sits.
func initGeneratorFixtureAt(t testing.TB, c *dagger.Client, configDir string) *dagger.Container {
	inConfigDir := func(p string) string { return path.Join(configDir, p) }
	return goGitBase(t, c).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c)).
		WithNewFile(inConfigDir("dagger.toml"), `[modules.init-fixture]
source = "sdk/init-fixture"

[modules.init-fixture.as-sdk]
name = "fixture"

[[modules.init-fixture.as-sdk.modules]]
path = "existing/mod"

[[modules.init-fixture.as-sdk.clients]]
path = "existing/client"
module = "sdk/init-fixture"
`).
		WithNewFile(inConfigDir("sdk/init-fixture/dagger.json"), `{
  "name": "init-fixture",
  "engineVersion": "latest",
  "sdk": { "source": "go" },
  "source": "."
}`).
		WithNewFile(inConfigDir("sdk/init-fixture/main.go"), `package main

import (
	"context"
	"fmt"
	"path"
	"strings"

	"dagger/init-fixture/internal/dagger"
)

type InitFixture struct{}

// InitModule scaffolds the SDK-owned files for a new module.
func (m *InitFixture) InitModule(ctx context.Context, ws *dagger.Workspace, name string, path string) (*dagger.Changeset, error) {
	local, err := cwdLocalPath(ctx, ws, path)
	if err != nil {
		return nil, err
	}
	return dag.Directory().WithNewFile(local+"/scaffold.txt", name+"\n").Changes(dag.Directory()), nil
}

// InitClient scaffolds the SDK-owned files for a new client.
func (m *InitFixture) InitClient(ctx context.Context, ws *dagger.Workspace, path string, module string) (*dagger.Changeset, error) {
	local, err := cwdLocalPath(ctx, ws, path)
	if err != nil {
		return nil, err
	}
	return dag.Directory().WithNewFile(local+"/scaffold.txt", module+"\n").Changes(dag.Directory()), nil
}

// cwdLocalPath mirrors the polyfill's workspace fork: the engine hands an SDK
// workspace-root-relative paths, but a returned changeset is applied wherever
// the caller stands, so the path has to be rebased onto the workspace cwd — and
// cannot be staged at all when it lies outside it.
func cwdLocalPath(ctx context.Context, ws *dagger.Workspace, workspacePath string) (string, error) {
	cwd, err := workspaceCwd(ctx, ws)
	if err != nil {
		return "", err
	}
	rel, ok := relativeToCwd(cwd, workspacePath)
	if !ok {
		return "", fmt.Errorf("workspace path %s is outside changeset root %s", workspacePath, cwd)
	}
	return rel, nil
}

// +generate
func (m *InitFixture) GenerateModules(ctx context.Context, ws *dagger.Workspace) (*dagger.Changeset, error) {
	cwd, err := workspaceCwd(ctx, ws)
	if err != nil {
		return nil, err
	}
	modules, err := dag.CurrentModule().AsSDK(ws).Modules(ctx)
	if err != nil {
		return nil, err
	}

	generated := ws
	for _, mod := range modules {
		modPath, err := mod.Path(ctx)
		if err != nil {
			return nil, err
		}
		rel, ok := relativeToCwd(cwd, modPath)
		if !ok {
			continue
		}
		generated = generated.WithNewFile(path.Join(rel, "generated-module.txt"), modPath+"\n")
	}
	return generated.Changes(dagger.WorkspaceChangesOpts{From: ws}), nil
}

// +generate
func (m *InitFixture) GenerateClients(ctx context.Context, ws *dagger.Workspace) (*dagger.Changeset, error) {
	cwd, err := workspaceCwd(ctx, ws)
	if err != nil {
		return nil, err
	}
	clients, err := dag.CurrentModule().AsSDK(ws).Clients(ctx)
	if err != nil {
		return nil, err
	}

	generated := ws
	for _, client := range clients {
		clientPath, err := client.Path(ctx)
		if err != nil {
			return nil, err
		}
		module, err := client.Module(ctx)
		if err != nil {
			return nil, err
		}
		rel, ok := relativeToCwd(cwd, clientPath)
		if !ok {
			continue
		}
		generated = generated.WithNewFile(path.Join(rel, "generated-client.txt"), module+"\n")
	}
	return generated.Changes(dagger.WorkspaceChangesOpts{From: ws}), nil
}

func workspaceCwd(ctx context.Context, ws *dagger.Workspace) (string, error) {
	cwd, err := ws.Cwd(ctx)
	if err != nil {
		return "", err
	}
	return normalizeWorkspacePath(cwd), nil
}

func normalizeWorkspacePath(p string) string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return "."
	}
	return path.Clean(trimmed)
}

// relativeToCwd reports target relative to cwd, and whether it is at or below
// it. A returned changeset can only carry paths under the caller's location, so
// anything outside is skipped.
func relativeToCwd(cwd, target string) (string, bool) {
	target = normalizeWorkspacePath(target)
	if cwd == "." {
		return target, true
	}
	if target == cwd {
		return ".", true
	}
	if strings.HasPrefix(target, cwd+"/") {
		return strings.TrimPrefix(target, cwd+"/"), true
	}
	return "", false
}
`)
}

// TestModuleInitGeneratesForNewModuleOnly covers the `dagger module init` half
// of dagger/dagger#13714: init runs the owning SDK's generators for what it just
// created, so the module is usable without a separate `dagger generate`, and
// nothing else in the workspace is touched.
func (GeneratorsSuite) TestModuleInitGeneratesForNewModuleOnly(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := initGeneratorFixture(t, c)

	t.Run("generates the new module", func(ctx context.Context, t *testctx.T) {
		initialized := base.With(daggerExec("module", "init", "fixture", "newmod", "--auto-apply"))
		out, err := initialized.CombinedOutput(ctx)
		require.NoError(t, err, out)

		// The SDK's scaffold and the engine's module config both landed...
		scaffold, err := initialized.File(".dagger/modules/newmod/scaffold.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "newmod\n", scaffold)
		_, err = initialized.File(".dagger/modules/newmod/dagger-module.toml").Contents(ctx)
		require.NoError(t, err)

		// ...and so did the generator output, in the same apply.
		generated, err := initialized.File(".dagger/modules/newmod/generated-module.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, ".dagger/modules/newmod\n", generated)

		// The pre-existing module and client sit outside the new module's cwd,
		// so the scoped run never reached them.
		for _, stray := range []string{"existing/mod/generated-module.txt", "existing/client/generated-client.txt"} {
			exists, err := initialized.Exists(ctx, stray)
			require.NoError(t, err)
			require.False(t, exists, "generation must not escape the initialized module: %s", stray)
		}
	})

	t.Run("--no-generate scaffolds without generating", func(ctx context.Context, t *testctx.T) {
		initialized := base.With(daggerExec("module", "init", "fixture", "newmod", "--no-generate", "--auto-apply"))

		scaffold, err := initialized.File(".dagger/modules/newmod/scaffold.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "newmod\n", scaffold)

		exists, err := initialized.Exists(ctx, ".dagger/modules/newmod/generated-module.txt")
		require.NoError(t, err)
		require.False(t, exists)
	})
}

// TestAPIClientInitGeneratesForNewClientOnly covers the `dagger api client init`
// half of dagger/dagger#13714.
func (GeneratorsSuite) TestAPIClientInitGeneratesForNewClientOnly(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := initGeneratorFixture(t, c)

	t.Run("generates the new client", func(ctx context.Context, t *testctx.T) {
		initialized := base.With(daggerExec(
			"api", "client", "init", "fixture", "clients/one", "sdk/init-fixture", "--auto-apply"))
		out, err := initialized.CombinedOutput(ctx)
		require.NoError(t, err, out)

		scaffold, err := initialized.File("clients/one/scaffold.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "sdk/init-fixture\n", scaffold)

		generated, err := initialized.File("clients/one/generated-client.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "sdk/init-fixture\n", generated)

		for _, stray := range []string{"existing/mod/generated-module.txt", "existing/client/generated-client.txt"} {
			exists, err := initialized.Exists(ctx, stray)
			require.NoError(t, err)
			require.False(t, exists, "generation must not escape the initialized client: %s", stray)
		}
	})

	t.Run("--no-generate scaffolds without generating", func(ctx context.Context, t *testctx.T) {
		initialized := base.With(daggerExec(
			"api", "client", "init", "fixture", "clients/one", "sdk/init-fixture", "--no-generate", "--auto-apply"))

		scaffold, err := initialized.File("clients/one/scaffold.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "sdk/init-fixture\n", scaffold)

		exists, err := initialized.Exists(ctx, "clients/one/generated-client.txt")
		require.NoError(t, err)
		require.False(t, exists)
	})
}

// TestInitFromSubdirectoryWorkspace covers dagger/dagger#13889: a dagger.toml
// in a subdirectory of the git repo (the monorepo layout, several projects
// under one root) leaves the caller's workspace cwd below the workspace root.
// Init changesets are applied at the workspace root, so the SDK has to be shown
// a cwd that matches — otherwise it either refuses the path outright or strips
// the cwd prefix and splits the new module across two directories.
func (GeneratorsSuite) TestInitFromSubdirectoryWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := initGeneratorFixtureAt(t, c, "common").WithWorkdir("/work/common")

	// withInitModule changes the workspace cwd internally, to measure and
	// export its root-owned edits. That must not leak: as a value, the
	// workspace it returns is the caller's, moved only by the changeset.
	t.Run("the returned workspace keeps the caller cwd", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerQuery(`{
			currentWorkspace {
				cwd
				withInitModule(name: "cwdcheck", sdk: "fixture", noGenerate: true) {
					cwd
				}
			}
		}`)).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, `"cwd": "/"`,
			"init must not move the caller's cwd to the workspace root")
		require.Equal(t, 2, strings.Count(out, `"cwd": "/common"`),
			"the workspace before and after init should share a cwd: %s", out)
	})

	t.Run("module init scaffolds beside its dagger.toml", func(ctx context.Context, t *testctx.T) {
		initialized := base.With(daggerExec("module", "init", "fixture", "newmod", "--auto-apply"))
		out, err := initialized.CombinedOutput(ctx)
		require.NoError(t, err, out)

		// The engine's module config and the SDK's scaffold land together,
		// under the project the dagger.toml belongs to.
		scaffold, err := initialized.File("/work/common/.dagger/modules/newmod/scaffold.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "newmod\n", scaffold)
		_, err = initialized.File("/work/common/.dagger/modules/newmod/dagger-module.toml").Contents(ctx)
		require.NoError(t, err)

		// ...and so does the generator output, in the same apply.
		generated, err := initialized.File("/work/common/.dagger/modules/newmod/generated-module.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "common/.dagger/modules/newmod\n", generated)

		// Nothing was written at the git root, where the cwd-stripped path
		// would have put it.
		exists, err := initialized.Exists(ctx, "/work/.dagger")
		require.NoError(t, err)
		require.False(t, exists, "module init must not scaffold outside the config directory")

		// Both paths dagger.toml records for the new module — its install
		// source and the as-sdk entry — are relative to the config directory,
		// so they read the same.
		config, err := initialized.File("/work/common/dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `source = ".dagger/modules/newmod"`)
		require.Contains(t, config, `path = ".dagger/modules/newmod"`)
	})

	t.Run("module init resolves an explicit --path against the caller", func(ctx context.Context, t *testctx.T) {
		t.Run("relative to the current directory", func(ctx context.Context, t *testctx.T) {
			initialized := base.With(daggerExec(
				"module", "init", "fixture", "hello", "--path", "hello", "--auto-apply"))
			out, err := initialized.CombinedOutput(ctx)
			require.NoError(t, err, out)

			scaffold, err := initialized.File("/work/common/hello/scaffold.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello\n", scaffold)
			_, err = initialized.File("/work/common/hello/dagger-module.toml").Contents(ctx)
			require.NoError(t, err)

			// The engine config and the SDK scaffold land together. Splitting
			// them across directories is the failure this whole suite is about.
			exists, err := initialized.Exists(ctx, "/work/hello")
			require.NoError(t, err)
			require.False(t, exists, "module init must not split the module across directories")
		})

		t.Run("leading slash means the workspace root", func(ctx context.Context, t *testctx.T) {
			initialized := base.With(daggerExec(
				"module", "init", "fixture", "hello", "--path", "/tools/hello", "--auto-apply"))
			out, err := initialized.CombinedOutput(ctx)
			require.NoError(t, err, out)

			scaffold, err := initialized.File("/work/tools/hello/scaffold.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello\n", scaffold)
			_, err = initialized.File("/work/tools/hello/dagger-module.toml").Contents(ctx)
			require.NoError(t, err)

			exists, err := initialized.Exists(ctx, "/work/common/tools")
			require.NoError(t, err)
			require.False(t, exists, "a rooted --path must not resolve under the caller")
		})

		t.Run("climbing out of the current directory", func(ctx context.Context, t *testctx.T) {
			initialized := base.With(daggerExec(
				"module", "init", "fixture", "hello", "--path", "../tools/hello", "--auto-apply"))
			out, err := initialized.CombinedOutput(ctx)
			require.NoError(t, err, out)

			_, err = initialized.File("/work/tools/hello/dagger-module.toml").Contents(ctx)
			require.NoError(t, err)
		})

		t.Run("escaping the workspace root is rejected", func(ctx context.Context, t *testctx.T) {
			out, err := base.With(daggerExecFail(
				"module", "init", "fixture", "hello", "--path", "../../escape", "--auto-apply")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "must not escape the workspace root")
		})
	})

	t.Run("client init resolves its path against the caller", func(ctx context.Context, t *testctx.T) {
		initialized := base.With(daggerExec(
			"api", "client", "init", "fixture", "clients/one", "common/sdk/init-fixture", "--auto-apply"))
		out, err := initialized.CombinedOutput(ctx)
		require.NoError(t, err, out)

		scaffold, err := initialized.File("/work/common/clients/one/scaffold.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "common/sdk/init-fixture\n", scaffold)

		exists, err := initialized.Exists(ctx, "/work/clients")
		require.NoError(t, err)
		require.False(t, exists, "client init must not scaffold outside the requested path")

		// The recorded client is anchored at the dagger.toml holding it, its
		// local module ref included, while the SDK is handed the
		// workspace-root-relative form above. The fixture already records one
		// client against the same module, so the new entry is the second.
		config, err := initialized.File("/work/common/dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `path = "clients/one"`)
		require.Equal(t, 2, strings.Count(config, `module = "sdk/init-fixture"`), config)
	})
}

// TestInitFromSubdirectoryCwd covers the plainest form of the same mismatch:
// the workspace config is at the root, where it always has been, and only the
// caller has moved. Init writes the workspace dagger.toml and the module under
// the config directory — both above the caller — so it is only expressible
// against the workspace root, which is also where init exports.
func (GeneratorsSuite) TestInitFromSubdirectoryCwd(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := initGeneratorFixture(t, c).
		WithExec([]string{"mkdir", "-p", "/work/sub"}).
		WithWorkdir("/work/sub")

	t.Run("module init from below the config directory", func(ctx context.Context, t *testctx.T) {
		initialized := base.With(daggerExec("module", "init", "fixture", "newmod", "--auto-apply"))
		out, err := initialized.CombinedOutput(ctx)
		require.NoError(t, err, out)

		scaffold, err := initialized.File("/work/.dagger/modules/newmod/scaffold.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "newmod\n", scaffold)
		_, err = initialized.File("/work/.dagger/modules/newmod/dagger-module.toml").Contents(ctx)
		require.NoError(t, err)

		config, err := initialized.File("/work/dagger.toml").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, config, `source = ".dagger/modules/newmod"`)

		// Nothing lands under the caller: the cwd-measured reading of these
		// paths is what used to strip the leading directories.
		exists, err := initialized.Exists(ctx, "/work/sub/.dagger")
		require.NoError(t, err)
		require.False(t, exists, "init must not scaffold under the caller cwd")
	})

	t.Run("client init from below the config directory", func(ctx context.Context, t *testctx.T) {
		initialized := base.With(daggerExec(
			"api", "client", "init", "fixture", "clients/one", "sdk/init-fixture", "--auto-apply"))
		out, err := initialized.CombinedOutput(ctx)
		require.NoError(t, err, out)

		// Relative to the caller, so it lands under sub/ even though the
		// dagger.toml it edits is a directory above.
		scaffold, err := initialized.File("/work/sub/clients/one/scaffold.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "sdk/init-fixture\n", scaffold)
	})

	t.Run("client init with a rooted path", func(ctx context.Context, t *testctx.T) {
		initialized := base.With(daggerExec(
			"api", "client", "init", "fixture", "/clients/one", "sdk/init-fixture", "--auto-apply"))
		out, err := initialized.CombinedOutput(ctx)
		require.NoError(t, err, out)

		scaffold, err := initialized.File("/work/clients/one/scaffold.txt").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "sdk/init-fixture\n", scaffold)

		exists, err := initialized.Exists(ctx, "/work/sub/clients")
		require.NoError(t, err)
		require.False(t, exists, "a rooted client path must not resolve under the caller")
	})
}

// TestAPIClientInitDottedModulePath covers a module ref whose dot segment is
// not the first one ("common/.dagger/target"), the shape a root-relative ref
// takes from a monorepo subdirectory. The ref classifier used to read any dot
// as a hostname and route such a ref to git.
func (GeneratorsSuite) TestAPIClientInitDottedModulePath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	base := initGeneratorFixture(t, c).
		WithNewFile("common/.dagger/target/dagger.json", `{
  "name": "target",
  "engineVersion": "latest",
  "sdk": { "source": "go" },
  "source": "."
}`)

	for _, tc := range []struct {
		name string
		ref  string
	}{
		{name: "workspace-relative", ref: "common/.dagger/target"},
		{name: "explicitly relative", ref: "./common/.dagger/target"},
		// A ref typed on Windows reaches the Linux engine verbatim.
		{name: "windows separators", ref: `common\.dagger\target`},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			initialized := base.
				WithWorkdir("/work/common").
				With(daggerExec(
					"api", "client", "init", "fixture", "clients/dotted", tc.ref, "--auto-apply"))
			out, err := initialized.CombinedOutput(ctx)
			require.NoError(t, err, out)

			// The client path resolves against the caller, so it lands under
			// common/; the module ref stays workspace-root-relative.
			scaffold, err := initialized.File("/work/common/clients/dotted/scaffold.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "common/.dagger/target\n", scaffold)

			// The generators ran for the new client too, which reads the
			// recorded ref back through the SDK's client list.
			generated, err := initialized.File("/work/common/clients/dotted/generated-client.txt").Contents(ctx)
			require.NoError(t, err)
			require.Equal(t, "common/.dagger/target\n", generated)

			config, err := initialized.File("/work/dagger.toml").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, config, `module = "common/.dagger/target"`)
		})
	}
}

func (GeneratorsSuite) TestGeneratorGroupChangesSyncWithNestedSDKCodegen(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := goGitBase(t, c).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
		With(nonNestedDevEngine(c)).
		WithNewFile("dagger.toml", `[modules.consumer]
source = ".dagger/modules/consumer"
entrypoint = true

[modules.go-sdk]
source = "github.com/dagger/go-sdk"

[modules.go-sdk.as-sdk]
name = "go"
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

	// SDK generators own their generated client output, so syncing generator
	// changes is the complete generation contract.
	out, err := modGen.
		With(daggerNonNestedExec("call", "sync-generators")).
		CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "ok")
	require.NotContains(t, out, "result *core.Changeset is detached")
}

// TestGenerateLocalDependenciesTerminatesOnRootDep locks in that
// Internal local-dependency generation terminates when a module's local
// dependency closure leads back to a dependency that is currently being
// generated one level up.
//
// The shape (mirroring the go-sdk workspace, where this recursed forever): the
// workspace root module is managed as-sdk by an SDK module whose generator —
// like the real SDK management modules — stages every visible module's local
// dependency closure before generating it, and a nested module depends on the
// workspace root. Staging the nested module's closure generates the root via
// that SDK generator, whose own staging pass walks the nested module again;
// since the root dependency is recorded in the staged workspace's
// StagedGeneration set, the nested walk must skip it instead of recursing
// through the generator with a fresh workspace ID per round.
func (GeneratorsSuite) TestGenerateLocalDependenciesTerminatesOnRootDep(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := goGitBase(t, c).
		WithNewFile("dagger.toml", `[modules.root-mod]
source = "."

[modules.nested]
source = ".dagger/modules/nested"

[modules.fanout-sdk]
source = ".dagger/modules/fanout-sdk"

[modules.fanout-sdk.as-sdk]

[[modules.fanout-sdk.as-sdk.modules]]
path = "."
`).
		WithNewFile("dagger.json", `{
  "name": "root-mod",
  "engineVersion": "latest",
  "sdk": { "source": "go" },
  "source": "."
}`).
		WithNewFile(".dagger/modules/nested/dagger.json", `{
  "name": "nested",
  "engineVersion": "latest",
  "sdk": { "source": "go" },
  "dependencies": [
    { "name": "root-mod", "source": "../../.." }
  ],
  "source": "."
}`).
		WithNewFile(".dagger/modules/fanout-sdk/dagger.json", `{
  "name": "fanout-sdk",
  "engineVersion": "latest",
  "sdk": { "source": "go" },
  "source": "."
}`).
		WithNewFile(".dagger/modules/fanout-sdk/main.go", `package main

import (
	"context"

	"dagger/fanout-sdk/internal/dagger"
)

type FanoutSdk struct{}

// Mimic an SDK-wide generator: stage each visible module's local dependency
// closure, then emit a marker changeset.
// +generate
func (m *FanoutSdk) Generate(ctx context.Context, ws *dagger.Workspace) (*dagger.Changeset, error) {
	for _, path := range []string{".", ".dagger/modules/nested"} {
		_, err := ws.ModuleSource(path).GenerateLocalDependencies(ws).Sync(ctx)
		if err != nil {
			return nil, err
		}
	}
	return dag.Directory().WithNewFile("fanout-generated", "ok").Changes(dag.Directory()), nil
}
`)

	// Bound the run so a regression fails instead of hanging: pre-fix this
	// recursed with a fresh workspace per round until the client disconnected.
	ctr := base.WithExec(
		[]string{"timeout", "300", "dagger", "generate", "fanout-sdk", "-y", "--progress=plain"},
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	out, err := ctr.CombinedOutput(ctx)
	require.NoError(t, err, out)

	generated, err := ctr.File("fanout-generated").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "ok", generated)
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

// TestCurrentModuleAsSDKClientModuleSourceField is a lighter engine-level check
// that CurrentModuleAsSDKClient exposes the moduleSource field (which resolves
// the bound module from its stored {module, pin}). Exercising it end-to-end
// requires an installed-SDK module execution context; here we assert the field
// is registered on the v1.0 schema view with the right type.
func (GeneratorsSuite) TestCurrentModuleAsSDKClientModuleSourceField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	out, err := goGitBase(t, c).
		With(daggerQuery(`{__type(name:"CurrentModuleAsSDKClient"){fields{name type{kind ofType{kind name}}}}}`)).
		Stdout(ctx)
	require.NoError(t, err)

	var resp struct {
		Type *struct {
			Fields []struct {
				Name string `json:"name"`
				Type struct {
					Kind   string `json:"kind"`
					OfType *struct {
						Kind string `json:"kind"`
						Name string `json:"name"`
					} `json:"ofType"`
				} `json:"type"`
			} `json:"fields"`
		} `json:"__type"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	require.NotNil(t, resp.Type, "CurrentModuleAsSDKClient should be present in the v1.0 schema view")

	var found bool
	for _, f := range resp.Type.Fields {
		if f.Name != "moduleSource" {
			continue
		}
		found = true
		require.Equal(t, "NON_NULL", f.Type.Kind, "moduleSource must be non-null")
		require.NotNil(t, f.Type.OfType)
		require.Equal(t, "OBJECT", f.Type.OfType.Kind)
		require.Equal(t, "ModuleSource", f.Type.OfType.Name)
	}
	require.True(t, found, "CurrentModuleAsSDKClient should expose a moduleSource field")
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
