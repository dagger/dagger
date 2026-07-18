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
	clients, err := dag.CurrentModule().AsSDK(dagger.CurrentModuleAsSDKOpts{Workspace: ws}).Clients(ctx)
	if err != nil {
		return nil, err
	}

	generated := dag.Directory()
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

	return generated.Changes(dag.Directory()), nil
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

// TestWorkspaceGeneratorsSeeOverlayEdits locks in that a generator run via
// Workspace.generators observes the workspace it was called on — including
// overlay edits (Workspace.withNewFile, or an agent's applied changesets) —
// rather than the session's frozen current workspace. Regression test for the
// bug where an agent edited files, then `generate` re-ran against stale source
// and came back as a cache hit (see hack/designs/workspace-agents.md §4).
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
