package core

// These tests cover `dagger check`, which discovers and runs module check
// functions. They verify listing and running checks from SDK modules, legacy
// compat blueprints, and workspace-installed modules.
//
// See also:
// - generators_test.go: generator discovery and execution.
// - workspace_modules_test.go: installing modules into workspaces.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
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
	return c.Container().
			From(alpineImage).
			// init git in a directory containing both the modules and the java SDK
			// that way dagger sees this directory as the root
			WithWorkdir("/work").
			WithExec([]string{"apk", "add", "git"}).
			WithExec([]string{"git", "init"}).
			WithWorkdir("/work/modules/").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithDirectory(".", c.Host().Directory("./testdata/"+subfolder)).
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
				With(daggerExec("--progress=report", "check", "passing-*")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			require.Regexp(t, `passing-check.*OK`, out)
			require.Regexp(t, `passing-container.*OK`, out)
			// run a specific failing check
			out, err = modGen.
				With(daggerExecFail("--progress=report", "check", "failing-*")).
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

func (ChecksSuite) TestChecksNoMatch(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := checksTestEnv(t, c)
	require.NoError(t, err)

	out, err := modGen.
		WithWorkdir("hello-with-checks").
		With(daggerExecFail("--progress=report", "check", "missing-check")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `no checks matched pattern "missing-check"`)
}

func (ChecksSuite) TestChecksViaLegacyBlueprintConfig(ctx context.Context, t *testctx.T) {
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
			modGen = modGen.WithWorkdir("app").
				WithNewFile("dagger.json", `{"name":"app","blueprint":{"name":"blueprint","source":"../`+tc.path+`"}}`)
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

func (ChecksSuite) TestChecksGenerateAsCheck(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := checksTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("hello-with-generate-checks")

	t.Run("list includes generator checks inline", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.Contains(t, out, "empty-generate")
		require.Contains(t, out, "non-empty-generate")
		require.Regexp(t, `passing-check\s+# A regular passing check`, out)
		require.Regexp(t, `empty-generate\s+# Did you "`, out)
		require.NotContains(t, out, "Generators")
	})

	t.Run("list with no-generate excludes generators", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "--no-generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		// Should only list regular checks, no Generators section
		require.Contains(t, out, "passing-check")
		require.NotContains(t, out, "Generators")
		require.NotContains(t, out, "empty-generate")
		require.NotContains(t, out, "non-empty-generate")
	})

	t.Run("list with generate only includes generators", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "--generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		// Should only list generators (rendered with `# Did you "..."?` comments), no regular checks
		require.Regexp(t, `empty-generate\s+# Did you "`, out)
		require.Regexp(t, `non-empty-generate\s+# Did you "`, out)
		require.NotContains(t, out, "passing-check")
	})

	t.Run("run empty generator passes", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("--progress=report", "check", "empty-generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `empty-generate.*OK`, out)
	})

	t.Run("run non-empty generator fails", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExecFail("--progress=report", "check", "non-empty-generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `non-empty-generate.*ERROR`, out)
	})

	t.Run("run all includes generators", func(ctx context.Context, t *testctx.T) {
		// Should fail because non-empty-generate produces changes
		out, err := modGen.
			With(daggerExecFail("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passing-check.*OK`, out)
		require.Regexp(t, `empty-generate.*OK`, out)
		require.Regexp(t, `non-empty-generate.*ERROR`, out)
	})

	t.Run("run with no-generate skips generators", func(ctx context.Context, t *testctx.T) {
		// Should pass because only passing-check runs
		out, err := modGen.
			With(daggerExec("--progress=report", "check", "--no-generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passing-check.*OK`, out)
		require.NotContains(t, out, "empty-generate")
		require.NotContains(t, out, "non-empty-generate")
	})

	t.Run("run with generate skips annotated checks", func(ctx context.Context, t *testctx.T) {
		// Should fail because non-empty-generate produces changes
		out, err := modGen.
			With(daggerExecFail("--progress=report", "check", "--generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `empty-generate.*OK`, out)
		require.Regexp(t, `non-empty-generate.*ERROR`, out)
		require.NotContains(t, out, "passing-check")
	})

	t.Run("no-generate and generate are mutually exclusive", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExecFail("check", "--no-generate", "--generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "if any flags in the group [no-generate generate] are set none of the others can be")
	})
}

func (ChecksSuite) TestChecksSkipFlag(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := checksTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("hello-with-checks")

	t.Run("list with skip excludes matching checks", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "--skip", "failing-*")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.Contains(t, out, "passing-container")
		require.Contains(t, out, "test:lint")
		require.Contains(t, out, "test:unit")
		require.NotContains(t, out, "failing-check")
		require.NotContains(t, out, "failing-container")
	})

	t.Run("list with glob skip pattern", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "--skip", "**:unit")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "test:lint")
		require.NotContains(t, out, "test:unit")
	})

	t.Run("list with prefix skip pattern", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "--skip", "test")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.NotContains(t, out, "test:lint")
		require.NotContains(t, out, "test:unit")
	})

	t.Run("list with include and skip combined", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "test", "--skip", "**:unit")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "test:lint")
		require.NotContains(t, out, "test:unit")
		require.NotContains(t, out, "passing-check")
	})

	t.Run("run with skip excludes matching checks", func(ctx context.Context, t *testctx.T) {
		// Should pass because the failing checks are skipped
		out, err := modGen.
			With(daggerExec("--progress=report", "check", "--skip", "failing-*")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passing-check.*OK`, out)
		require.Regexp(t, `passing-container.*OK`, out)
		require.NotContains(t, out, "failing-check")
		require.NotContains(t, out, "failing-container")
	})
}

func (ChecksSuite) TestWorkspaceCheckSkip(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := checksTestEnv(t, c)
	require.NoError(t, err)

	ctr := modGen.WithNewFile("dagger.toml", `[modules.hello-with-checks]
source = "hello-with-checks"
check.skip = ["failing-check", "failing-container"]
`)

	out, err := ctr.With(daggerExec("check", "-l")).CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello-with-checks:passing-check")
	require.Contains(t, out, "hello-with-checks:passing-container")
	require.NotContains(t, out, "hello-with-checks:failing-check")
	require.NotContains(t, out, "hello-with-checks:failing-container")
}

func (ChecksSuite) TestWorkspaceCheckGeneratedSetting(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := checksTestEnv(t, c)
	require.NoError(t, err)

	// check-generated = false skips generate-as-checks by default.
	base := modGen.WithNewFile("dagger.toml", `check-generated = false

[modules.hello-with-generate-checks]
source = "hello-with-generate-checks"
`)

	t.Run("list excludes generators by default", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "-l")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "hello-with-generate-checks:passing-check")
		require.NotContains(t, out, "empty-generate")
		require.NotContains(t, out, "non-empty-generate")
	})

	t.Run("run passes by default despite stale generator", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("--progress=report", "check")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `passing-check.*OK`, out)
		require.NotContains(t, out, "non-empty-generate")
	})

	t.Run("--generate flag overrides the config", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "-l", "--generate")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `empty-generate\s+# Did you "`, out)
		require.Regexp(t, `non-empty-generate\s+# Did you "`, out)
		require.NotContains(t, out, "passing-check")
	})

	t.Run("--no-generate flag matches the config default", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "-l", "--no-generate")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "hello-with-generate-checks:passing-check")
		require.NotContains(t, out, "empty-generate")
	})
}

func (ChecksSuite) TestWorkspaceCheckSkipRemote(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	remoteRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().
		WithNewFile("dagger.toml", `[modules.hello-with-checks]
source = ".dagger/modules/hello-with-checks"
check.skip = ["failing-check", "failing-container"]
`).
		WithDirectory(".dagger/modules/hello-with-checks", c.Host().Directory(testDataPath(t, "checks", "hello-with-checks"))))

	out, err := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/empty").
		With(workspaceSelectionDaggerExec("-W", remoteRef, "check", "-l")).
		CombinedOutput(ctx)
	require.NoError(t, err, out)
	require.Contains(t, out, "hello-with-checks:passing-check")
	require.Contains(t, out, "hello-with-checks:passing-container")
	require.NotContains(t, out, "hello-with-checks:failing-check")
	require.NotContains(t, out, "hello-with-checks:failing-container")
}

func (ChecksSuite) TestChecksFailFast(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	modGen, err := checksTestEnv(t, c)
	require.NoError(t, err)
	modGen = modGen.WithWorkdir("hello-with-checks")
	// run all checks with --failfast; should fail because there are failing checks
	out, err := modGen.
		With(daggerExecFail("--progress=report", "check", "--failfast")).
		CombinedOutput(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "ERROR")
	require.Contains(t, out, "context canceled")
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
			// Install hello-with-checks into the current workspace.
			modGen, err := checksTestEnv(t, c)
			require.NoError(t, err)
			modGen = modGen.
				WithWorkdir("app").
				WithNewFile("dagger.toml", fmt.Sprintf(`[modules.%s]
source = "../%s"
`, tc.path, tc.path))
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
			_, err = modGen.
				With(daggerExec("--progress=report", "check", tc.path+":passing-check")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			// run a specific failing check
			_, err = modGen.
				With(daggerExecFail("--progress=report", "check", tc.path+":failing-check")).
				CombinedOutput(ctx)
			require.NoError(t, err)
			// run all checks
			_, err = modGen.
				With(daggerExecFail("--progress=report", "check")).
				CombinedOutput(ctx)
			require.NoError(t, err)
		})
	}
}

// TestChecksSyntheticSDKGenerateAsCheck covers the check `dagger check`
// derives from an engine-injected SDK generator. A module-declared +generate
// function has always produced such a check; a workspace that moved to its
// SDK's built-in generate mechanism must keep it.
func (ChecksSuite) TestChecksSyntheticSDKGenerateAsCheck(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	const alphaOnly = `[modules.alpha-sdk]
source = ".dagger/modules/alpha-sdk"

[sdks.alpha]
module = "alpha-sdk"

[sdks.alpha.scopes."alpha"]
is-module = true
name = "alpha"
`
	// beta's scope generates a client for alpha's module scope, so beta's
	// generation depends on alpha's.
	const bothSDKs = alphaOnly + `
[modules.beta-sdk]
source = ".dagger/modules/beta-sdk"

[sdks.beta]
module = "beta-sdk"

[sdks.beta.scopes."beta"]
clients = ["./alpha"]
`

	// beta owns its own module scope here, so the two SDKs share no dependency
	// edge and one stale scope can only fail its owner's check.
	const independentSDKs = alphaOnly + `
[modules.beta-sdk]
source = ".dagger/modules/beta-sdk"

[sdks.beta]
module = "beta-sdk"

[sdks.beta.scopes."beta"]
is-module = true
name = "beta"
`

	const alphaMarker = "/work/alpha/generated/alpha.txt"

	// generated returns the fixture with config applied and every SDK scope
	// already generated, which is the state a derived check passes in.
	generated := func(ctx context.Context, t *testctx.T, config string) *dagger.Container {
		t.Helper()
		ctr := workspaceFixture(t, c, "sdk-generate-check").
			WithNewFile("dagger.toml", config).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
			With(nonNestedDevEngine(c)).
			With(daggerNonNestedExec("generate", "-y"))
		// Evaluate here so a setup failure reports what generate printed
		// instead of surfacing as a bare exit code from a later assertion.
		out, err := ctr.CombinedOutput(ctx)
		require.NoError(t, err, out)
		return ctr
	}

	t.Run("generating twice changes nothing", func(ctx context.Context, t *testctx.T) {
		// The derived check is only meaningful if regenerating an already
		// generated scope is a no-op, so assert that before anything else.
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("generate", "-y")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "no changes to apply")
	})

	t.Run("list includes the derived check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		// The "Did you ..." comment is how the CLI renders a check whose type
		// is "generate", so matching it proves the derived check is one.
		require.Regexp(t, `alpha-sdk:generate\s+# Did you "`, out)
	})

	t.Run("no-generate excludes the derived check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("check", "-l", "--no-generate")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "alpha-sdk:generate")
	})

	t.Run("generate only includes the derived check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("check", "-l", "--generate")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "alpha-sdk:generate")
	})

	t.Run("check-generated false excludes the derived check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, "check-generated = false\n\n"+alphaOnly).
			With(daggerNonNestedExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "alpha-sdk:generate")
	})

	t.Run("skip excludes the derived check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("check", "-l", "--skip", "alpha-sdk:generate")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "alpha-sdk:generate")
	})

	t.Run("a configured module skip excludes the derived check", func(ctx context.Context, t *testctx.T) {
		config := alphaOnly + `
[modules.alpha-sdk.check]
skip = ["generate"]
`
		out, err := generated(ctx, t, config).
			With(daggerNonNestedExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "alpha-sdk:generate")
	})

	t.Run("an entrypoint provider drops the module prefix", func(ctx context.Context, t *testctx.T) {
		config := strings.Replace(alphaOnly,
			`source = ".dagger/modules/alpha-sdk"`,
			`source = ".dagger/modules/alpha-sdk"
entrypoint = true`, 1)
		out, err := generated(ctx, t, config).
			With(daggerNonNestedExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `(?m)^\s*generate\b`, out)
		require.NotContains(t, out, "alpha-sdk:generate")
	})

	t.Run("the derived check passes when the scope is generated", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("--progress=report", "check", "alpha-sdk:generate")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `alpha-sdk:generate.*OK`, out)
	})

	t.Run("the derived check fails when the scope is stale", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			WithoutFile(alphaMarker).
			With(daggerNonNestedExecFail("--progress=report", "check", "alpha-sdk:generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `alpha-sdk:generate.*ERROR`, out)
		require.Contains(t, out, "run 'dagger generate alpha-sdk:generate' to apply")
	})

	t.Run("an explicit check at the same name wins", func(ctx context.Context, t *testctx.T) {
		// alpha-sdk-checked is alpha-sdk plus its own `generate` check, under
		// the same workspace module name.
		config := strings.Replace(alphaOnly,
			`.dagger/modules/alpha-sdk"`,
			`.dagger/modules/alpha-sdk-checked"`, 1)
		base := generated(ctx, t, config)

		out, err := base.
			With(daggerNonNestedExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Equal(t, 1, strings.Count(out, "alpha-sdk:generate"))

		// The explicit check passes on a stale scope; the derived check would
		// have failed, so this proves which one survived the dedup.
		out, err = base.
			WithoutFile(alphaMarker).
			With(daggerNonNestedExec("--progress=report", "check", "alpha-sdk:generate")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `alpha-sdk:generate.*OK`, out)
	})

	t.Run("each SDK gets its own check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, bothSDKs).
			With(daggerNonNestedExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "alpha-sdk:generate")
		require.Contains(t, out, "beta-sdk:generate")
	})

	t.Run("a stale dependency fails the dependent SDK's check too", func(ctx context.Context, t *testctx.T) {
		// A derived check verifies its SDK's scopes and the scopes they depend
		// on, so stale output in alpha's scope also fails beta's check.
		out, err := generated(ctx, t, bothSDKs).
			WithoutFile(alphaMarker).
			With(daggerNonNestedExecFail("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `alpha-sdk:generate.*ERROR`, out)
		require.Regexp(t, `beta-sdk:generate.*ERROR`, out)
	})

	t.Run("an independent SDK's check is unaffected", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, independentSDKs).
			WithoutFile(alphaMarker).
			With(daggerNonNestedExecFail("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `alpha-sdk:generate.*ERROR`, out)
		require.Regexp(t, `beta-sdk:generate.*OK`, out)
	})

	t.Run("originalModule reports that the check is engine-defined", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			WithNewFile("/query.graphql", `{
  currentWorkspace {
    checks(include: ["alpha-sdk:generate"]) {
      list {
        name
        originalModule { name }
      }
    }
  }
}
`).
			With(daggerNonNestedExecFail("api", "query", "--doc=/query.graphql")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "has no original module")
	})
}
