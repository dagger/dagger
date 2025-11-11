package core

import (
	"context"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type ChecksSuite struct{}

func TestChecks(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ChecksSuite{})
}

func checksTestEnv(t *testctx.T, c *dagger.Client) *dagger.Container {
	return c.Container().
		From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithExec([]string{"git", "init"}).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithDirectory(".", c.Host().Directory("./testdata/checks")).
		WithDirectory("app", c.Directory())
}

func (ChecksSuite) TestChecksDirectSDK(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	// install hello-with-checks as blueprint
	modGen := checksTestEnv(t, c).
		WithWorkdir("hello-with-checks")
	// list checks
	out, err := modGen.
		With(daggerExec("checks", "-l")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "passing-check")
	require.Contains(t, out, "failing-check")
	require.Contains(t, out, "passing-container")
	require.Contains(t, out, "failing-container")
	// run a specific passing check
	out, err = modGen.
		With(daggerExec("--progress=report", "checks", "passing*")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Regexp(t, `passingCheck.*OK`, out)
	require.Regexp(t, `passingContainer.*OK`, out)
	// run a specific failing check
	out, err = modGen.
		With(daggerExecFail("--progress=report", "checks", "failing*")).
		CombinedOutput(ctx)
	require.Regexp(t, "failingCheck.*ERROR", out)
	require.Regexp(t, "failingContainer.*ERROR", out)
	require.NoError(t, err)
	// run all checks
	out, err = modGen.
		With(daggerExecFail("--progress=report", "checks")).
		CombinedOutput(ctx)
	require.Regexp(t, `passingCheck.*OK`, out)
	require.Regexp(t, `passingContainer.*OK`, out)
	require.Regexp(t, "failingCheck.*ERROR", out)
	require.Regexp(t, "failingContainer.*ERROR", out)
	require.NoError(t, err)
}

func (ChecksSuite) TestChecksAsBlueprint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("run checks from a blueprint (Go)", func(ctx context.Context, t *testctx.T) {
		// install hello-with-checks as blueprint
		modGen := checksTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init", "--blueprint", "../hello-with-checks"))
		// list checks
		out, err := modGen.
			With(daggerExec("checks", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.Contains(t, out, "failing-check")
		// run a specific passing check
		out, err = modGen.
			With(daggerExec("--progress=report", "checks", "passing-check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks", "failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, "failingCheck.*ERROR", out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks")).
			CombinedOutput(ctx)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
	})
	t.Run("run checks from a blueprint (TypeScript)", func(ctx context.Context, t *testctx.T) {
		// install hello-with-checks-ts as blueprint
		modGen := checksTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init", "--blueprint", "../hello-with-checks-ts"))
		// list checks
		out, err := modGen.
			With(daggerExec("checks", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.Contains(t, out, "failing-check")
		// run a specific passing check
		out, err = modGen.
			With(daggerExec("--progress=report", "checks", "passing-check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks", "failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks")).
			CombinedOutput(ctx)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
	})
	t.Run("run checks from a blueprint (Python)", func(ctx context.Context, t *testctx.T) {
		// install hello-with-checks-py as blueprint
		modGen := checksTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init", "--blueprint", "../hello-with-checks-py"))
		// list checks
		out, err := modGen.
			With(daggerExec("checks", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.Contains(t, out, "failing-check")
		// run a specific passing check
		out, err = modGen.
			With(daggerExec("--progress=report", "checks", "passing-check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks", "failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks")).
			CombinedOutput(ctx)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
	})
}

func (ChecksSuite) TestChecksAsToolchain(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("run checks from a toolchain (Go)", func(ctx context.Context, t *testctx.T) {
		// install hello-with-checks as toolchain
		modGen := checksTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "../hello-with-checks"))
		// list checks
		out, err := modGen.
			With(daggerExec("checks", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello-with-checks/passing-check")
		require.Contains(t, out, "hello-with-checks/failing-check")
		// run a specific passing check
		out, err = modGen.
			With(daggerExec("--progress=report", "checks", "hello-with-checks/passing-check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks", "hello-with-checks/failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks")).
			CombinedOutput(ctx)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
	})
	t.Run("run checks from a toolchain (TypeScript)", func(ctx context.Context, t *testctx.T) {
		// install hello-with-checks-ts as toolchain
		modGen := checksTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "../hello-with-checks-ts"))
		// list checks
		out, err := modGen.
			With(daggerExec("checks", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello-with-checks-ts/passing-check")
		require.Contains(t, out, "hello-with-checks-ts/failing-check")
		// run a specific passing check
		out, err = modGen.
			With(daggerExec("--progress=report", "checks", "hello-with-checks-ts/passing-check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks", "hello-with-checks-ts/failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks")).
			CombinedOutput(ctx)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
	})
	t.Run("run checks from a toolchain (Python)", func(ctx context.Context, t *testctx.T) {
		// install hello-with-checks-py as toolchain
		modGen := checksTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init")).
			With(daggerExec("toolchain", "install", "../hello-with-checks-py"))
		// list checks
		out, err := modGen.
			With(daggerExec("checks", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello-with-checks-py/passing-check")
		require.Contains(t, out, "hello-with-checks-py/failing-check")
		// run a specific passing check
		out, err = modGen.
			With(daggerExec("--progress=report", "checks", "hello-with-checks-py/passing-check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks", "hello-with-checks-py/failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "checks")).
			CombinedOutput(ctx)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
	})
}
