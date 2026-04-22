package core

// Workspace alignment: aligned; this file already matches the workspace-era split.
// Scope: Generator discovery and execution across direct SDK, compat blueprint, and workspace-installed modules.
// Intent: Keep successor workspace behavior and legacy compat coverage explicit and separate.

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

func (GeneratorsSuite) TestGeneratorsViaLegacyBlueprintInit(ctx context.Context, t *testctx.T) {
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
				With(daggerModuleExec("init", "--blueprint", "../"+tc.path))

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
				With(daggerWorkspaceExec("init")).
				With(daggerWorkspaceInstall("../" + tc.path))

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

func (GeneratorsSuite) TestToolchainIgnoreGenerators(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen, err := generatorsTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.
		WithWorkdir("app").
		With(daggerExec("init")).
		With(daggerExec("toolchain", "install", "../hello-with-generators"))

	out, err := modGen.
		With(daggerExec("generate", "-l")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello-with-generators:generate-files")
	require.Contains(t, out, "hello-with-generators:generate-other-files")
	require.Contains(t, out, "hello-with-generators:other-generators:gen-things")

	modGen = modGen.WithNewFile("dagger.json", `{
  "name": "app",
  "engineVersion": "v0.20.5",
  "toolchains": [
    {
      "name": "hello-with-generators",
      "source": "../hello-with-generators",
      "ignoreGenerators": [
        "generate-other-files",
        "other-generators:*"
      ]
    }
  ]
}`)

	out, err = modGen.
		With(daggerExec("generate", "-l")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello-with-generators:generate-files")
	require.NotContains(t, out, "hello-with-generators:generate-other-files")
	require.NotContains(t, out, "hello-with-generators:other-generators:gen-things")

	modGen = modGen.
		With(daggerExec("generate", "hello-with-generators:generate-*", "-y", "--progress=plain"))
	out, err = modGen.
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello-with-generators:generate-files")
	require.NotContains(t, out, "hello-with-generators:generate-other-files")

	exists, err := modGen.Exists(ctx, "foo")
	require.NoError(t, err)
	require.True(t, exists)
	exists, err = modGen.Exists(ctx, "bar")
	require.NoError(t, err)
	require.False(t, exists)

	modGen = modGen.
		With(daggerExec("generate", "hello-with-generators:other-generators:*", "-y", "--progress=plain"))
	out, err = modGen.
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.NotContains(t, out, "hello-with-generators:other-generators:gen-things")

	exists, err = modGen.Exists(ctx, "meta-gen")
	require.NoError(t, err)
	require.False(t, exists)
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
