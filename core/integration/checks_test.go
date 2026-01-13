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
		With(daggerExec("check", "-l")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "passing-check")
	require.Contains(t, out, "failing-check")
	require.Contains(t, out, "passing-container")
	require.Contains(t, out, "failing-container")
	// run a specific passing check
	out, err = modGen.
		With(daggerExec("--progress=report", "check", "passing*")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Regexp(t, `passingCheck.*OK`, out)
	require.Regexp(t, `passingContainer.*OK`, out)
	// run a specific failing check
	out, err = modGen.
		With(daggerExecFail("--progress=report", "check", "failing*")).
		CombinedOutput(ctx)
	require.Regexp(t, "failingCheck.*ERROR", out)
	require.Regexp(t, "failingContainer.*ERROR", out)
	require.NoError(t, err)
	// run all checks
	out, err = modGen.
		With(daggerExecFail("--progress=report", "check")).
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
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check", "failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, "failingCheck.*ERROR", out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check")).
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
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check", "failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check")).
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
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check", "failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check")).
			CombinedOutput(ctx)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
	})
}

func (ChecksSuite) TestChecksIgnoreChecksTopLevel(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	t.Run("ignore checks on the module itself using top-level ignoreChecks", func(ctx context.Context, t *testctx.T) {
		// Set up test environment with checks test data
		modGen := checksTestEnv(t, c).
			WithWorkdir("hello-with-checks")

		// Verify all checks are visible by default
		out, err := modGen.
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.Contains(t, out, "failing-check")
		require.Contains(t, out, "passing-container")
		require.Contains(t, out, "failing-container")

		// Now add top-level ignoreChecks configuration to filter out failing checks
		modGen = modGen.WithNewFile("dagger.json", `{
  "name": "hello-with-checks",
  "sdk": "go",
  "engineVersion": "v0.16.0",
  "ignoreChecks": [
    "failing-check",
    "failing-container"
  ]
}`)

		// List checks again - should only show passing checks
		out, err = modGen.
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.Contains(t, out, "passing-container")
		require.NotContains(t, out, "failing-check")
		require.NotContains(t, out, "failing-container")

		// Run all checks - should only run passing checks (and succeed)
		out, err = modGen.
			With(daggerExec("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `passingContainer.*OK`, out)
		require.NotContains(t, out, "failingCheck")
		require.NotContains(t, out, "failingContainer")
	})

	t.Run("ignore checks with glob patterns", func(ctx context.Context, t *testctx.T) {
		// Set up test environment
		modGen := checksTestEnv(t, c).
			WithWorkdir("hello-with-checks")

		// Add top-level ignoreChecks with wildcard patterns
		modGen = modGen.WithNewFile("dagger.json", `{
  "name": "hello-with-checks",
  "sdk": "go",
  "engineVersion": "v0.16.0",
  "ignoreChecks": [
    "failing-*"
  ]
}`)

		// List checks - should only show passing checks
		out, err := modGen.
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.Contains(t, out, "passing-container")
		require.NotContains(t, out, "failing-check")
		require.NotContains(t, out, "failing-container")

		// Run all checks - should succeed since only passing checks run
		out, err = modGen.
			With(daggerExec("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `passingContainer.*OK`, out)
	})

	t.Run("ignoreChecks only applies to module itself, not when installed as toolchain", func(ctx context.Context, t *testctx.T) {
		// First, modify hello-with-checks to have top-level ignoreChecks
		modWithIgnore := checksTestEnv(t, c).
			WithWorkdir("hello-with-checks").
			WithNewFile("dagger.json", `{
  "name": "hello-with-checks",
  "sdk": "go",
  "engineVersion": "v0.16.0",
  "ignoreChecks": [
    "failing-check",
    "failing-container"
  ]
}`)

		// Verify that ignoreChecks works when running checks directly on the module
		out, err := modWithIgnore.
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.NotContains(t, out, "failing-check")

		// Now install this module as a toolchain in another module
		// The ignoreChecks should NOT be honored
		modGen := checksTestEnv(t, c).
			WithWorkdir("app").
			With(daggerExec("init"))

		// Copy the modified hello-with-checks into a temp location and install it
		modGen = modGen.
			WithDirectory("../hello-with-checks-modified", modWithIgnore.Directory(".")).
			With(daggerExec("toolchain", "install", "../hello-with-checks-modified"))

		// Verify that ALL checks from the toolchain are visible (ignoreChecks NOT honored)
		// Note: The toolchain name is "hello-with-checks" from dagger.json, not the directory name
		out, err = modGen.
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello-with-checks:passing-check")
		require.Contains(t, out, "hello-with-checks:failing-check")
		require.Contains(t, out, "hello-with-checks:passing-container")
		require.Contains(t, out, "hello-with-checks:failing-container")
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
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello-with-checks:passing-check")
		require.Contains(t, out, "hello-with-checks:failing-check")
		// run a specific passing check
		out, err = modGen.
			With(daggerExec("--progress=report", "check", "hello-with-checks:passing-check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check", "hello-with-checks:failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check")).
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
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello-with-checks-ts:passing-check")
		require.Contains(t, out, "hello-with-checks-ts:failing-check")
		// run a specific passing check
		out, err = modGen.
			With(daggerExec("--progress=report", "check", "hello-with-checks-ts:passing-check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check", "hello-with-checks-ts:failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check")).
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
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "hello-with-checks-py:passing-check")
		require.Contains(t, out, "hello-with-checks-py:failing-check")
		// run a specific passing check
		out, err = modGen.
			With(daggerExec("--progress=report", "check", "hello-with-checks-py:passing-check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passingCheck.*OK`, out)
		// run a specific failing check
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check", "hello-with-checks-py:failing-check")).
			CombinedOutput(ctx)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
		// run all checks
		out, err = modGen.
			With(daggerExecFail("--progress=report", "check")).
			CombinedOutput(ctx)
		require.Regexp(t, `passingCheck.*OK`, out)
		require.Regexp(t, `failingCheck.*ERROR`, out)
		require.NoError(t, err)
	})
}
