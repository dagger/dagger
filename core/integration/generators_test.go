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

	_, err = dag.Changeset().
		WithChangesets([]*dagger.Changeset{
			generatorChanges,
			workspace.ClientGenerate(),
		}, dagger.ChangesetWithChangesetsOpts{
			OnConflict: dagger.ChangesetsMergeConflictFailEarly,
		}).
		Sync(ctx)
	if err != nil {
		return "", err
	}

	return "ok", nil
}
`)

	// This mirrors the generated Go SDK contract used by `dagger generate`:
	// generator changes from nested SDK/codegen are merged with client-generation
	// changes, then the merged changeset is synced.
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

	t.Run("generating across all modules still loads the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExecFail("generate", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "bad")
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
// TestWorkspaceGenerateNarrowsToRequestedModule for `dagger call`: targeting a
// healthy module's function must not load every workspace module just to build
// the command tree. The CLI names its typedefs introspection operation after
// the leading positional token, and the engine peeks that to load only the
// targeted module on demand, so an unrelated broken/stale module cannot block
// the call.
func (GeneratorsSuite) TestWorkspaceCallNarrowsToRequestedModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "generators-broken")

	t.Run("calling a healthy module's function skips the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("call", "good", "verify", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("listing the healthy module's functions skips the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("functions", "good", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("listing all workspace functions still loads the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExecFail("functions")).
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
//     declared as "goodMod" is called as `dagger call good-mod ...` and the
//     engine must normalize both sides the same way to narrow loading;
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
			With(daggerExec("call", "good-mod", "ping", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "pong from goodMod")
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("entrypoint function target skips the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("call", "greet", "--progress=plain")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello from entrypoint")
		require.NotContains(t, out, "intentionally invalid")
	})

	t.Run("kebab-case functions listing skips the broken module", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("functions", "good-mod", "--progress=plain")).
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
			With(daggerExecFail("functions")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "bad")
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
