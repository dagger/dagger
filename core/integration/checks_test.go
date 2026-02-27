package core

import (
	"context"
	"path/filepath"
	"testing"

	dagger "github.com/dagger/dagger/internal/testutil/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ChecksSuite struct{}

func TestChecks(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ChecksSuite{})
}

func checksTestEnv(t *testctx.T, c *dagger.Client) (*dagger.Container, error) {
	return specificTestEnv(t, c, "checks")
}

func specificTestEnv(t *testctx.T, c *dagger.Client, subfolder string) (*dagger.Container, error) {
	// java SDK is not embedded in the engine, so we mount the java sdk to be able
	// to test non released features
	javaSdkSrc, err := filepath.Abs("../../sdk/java")
	if err != nil {
		return nil, err
	}

	testdataPath, err := filepath.Abs(filepath.Join("testdata", subfolder))
	if err != nil {
		return nil, err
	}

	return c.Container().
			From(alpineImage).
			// init git in a directory containing both the modules and the java SDK
			// that way dagger sees this directory as the root
			WithWorkdir("/work").
			WithExec([]string{"apk", "add", "git"}).
			WithExec([]string{"git", "init"}).
			WithWorkdir("/work/modules/").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory(".", c.Host().Directory(testdataPath)).
			WithMountedDirectory("/work/sdk/java", c.Host().Directory(javaSdkSrc)).
			WithDirectory("app", c.Directory()),
		nil
}

func (ChecksSuite) TestChecksDirectSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-checks"},
		{"typescript", "hello-with-checks-ts"},
		{"python", "hello-with-checks-py"},
		{"java", "hello-with-checks-java"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			modGen, err := checksTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.
				WithWorkdir(tc.path)
			// list checks
			out, err := modGen.
				With(daggerExec("check", "-l")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "passing-check")
			require.Contains(t, out, "failing-check")
			require.Contains(t, out, "passing-container")
			require.Contains(t, out, "failing-container")
			require.Contains(t, out, "test:lint")
			require.Contains(t, out, "test:unit")
			// run a specific passing check
			out, err = modGen.
				With(daggerExec("--progress=report", "check", "passing*")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Regexp(t, `passing-check.*OK`, out)
			require.Regexp(t, `passing-container.*OK`, out)
			// run a specific failing check
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check", "failing*")).
				CombinedOutput(ctx)
			require.Regexp(t, "failing-check.*ERROR", out)
			require.Regexp(t, "failing-container.*ERROR", out)
			require.NoError(t, err)
			// run all checks
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check")).
				CombinedOutput(ctx)
			require.Regexp(t, `passing-check.*OK`, out)
			require.Regexp(t, `passing-container.*OK`, out)
			require.Regexp(t, "failing-check.*ERROR", out)
			require.Regexp(t, "failing-container.*ERROR", out)
			require.NoError(t, err)
		})
	}
}

func (ChecksSuite) TestChecksAsBlueprint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-checks"},
		{"typescript", "hello-with-checks-ts"},
		{"python", "hello-with-checks-py"},
		{"java", "hello-with-checks-java"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			// install hello-with-checks as blueprint
			modGen, err := checksTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.WithWorkdir("app").
				With(daggerExec("init", "--blueprint", "../"+tc.path))
			// list checks
			out, err := modGen.
				With(daggerExec("check", "-l")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "passing-check")
			require.Contains(t, out, "failing-check")
			// run a specific passing check
			out, err = modGen.
				With(daggerExec("--progress=report", "check", "passing-check")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Regexp(t, `passing-check.*OK`, out)
			// run a specific failing check
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check", "failing-check")).
				CombinedOutput(ctx)
			require.Regexp(t, "failing-check.*ERROR", out)
			require.NoError(t, err)
			// run all checks
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check")).
				CombinedOutput(ctx)
			require.Regexp(t, `passing-check.*OK`, out)
			require.Regexp(t, `failing-check.*ERROR`, out)
			require.NoError(t, err)
		})
	}
}

func (ChecksSuite) TestChecksAsToolchain(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"go", "hello-with-checks"},
		{"typescript", "hello-with-checks-ts"},
		{"python", "hello-with-checks-py"},
		{"java", "hello-with-checks-java"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			// install hello-with-checks as toolchain
			modGen, err := checksTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.
				WithWorkdir("app").
				With(daggerExec("init")).
				With(daggerExec("toolchain", "install", "../"+tc.path))
			// list checks
			out, err := modGen.
				With(daggerExec("check", "-l")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Contains(t, out, tc.path+":passing-check")
			require.Contains(t, out, tc.path+":failing-check")
			require.Contains(t, out, tc.path+":test:lint")
			require.Contains(t, out, tc.path+":test:unit")
			// run a specific passing check
			out, err = modGen.
				With(daggerExec("--progress=report", "check", tc.path+":passing-check")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Regexp(t, `passing-check.*OK`, out)
			// run a specific failing check
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check", tc.path+":failing-check")).
				CombinedOutput(ctx)
			require.Regexp(t, `failing-check.*ERROR`, out)
			require.NoError(t, err)
			// run all checks
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check")).
				CombinedOutput(ctx)
			require.Regexp(t, `passing-check.*OK`, out)
			require.Regexp(t, `failing-check.*ERROR`, out)
			require.NoError(t, err)
		})
	}
}
