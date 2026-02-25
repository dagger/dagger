package core

import (
	"context"
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

func (GeneratorsSuite) TestGeneratorsAsBlueprint(ctx context.Context, t *testctx.T) {
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
				With(daggerExec("init", "--blueprint", "../"+tc.path))

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

func (GeneratorsSuite) TestGeneratorsAsToolchain(ctx context.Context, t *testctx.T) {
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
				With(daggerExec("init")).
				With(daggerExec("toolchain", "install", "../"+tc.path))

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
