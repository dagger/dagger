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
				With(daggerExec("workspace", "init")).
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

func (GeneratorsSuite) TestWorkspaceGenerateSkip(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen, err := generatorsTestEnv(t, c)
	require.NoError(t, err)

	ctr := modGen.WithNewFile(".dagger/config.toml", `[modules.hello-with-generators]
source = "../hello-with-generators"
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
		With(daggerExec("toolchain", "install", "./toolchain")).
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
