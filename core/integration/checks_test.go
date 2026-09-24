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
	"github.com/tidwall/gjson"
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
			require.Contains(t, out, "test/lint")
			require.Contains(t, out, "test/unit")
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
		// A generate-derived check is listed under an up-to-date leaf, so its name
		// cannot be mistaken for the generator `dagger generate -l` lists.
		require.Regexp(t, `(?m)^dag\+check://empty-generate/stale\s+# `, out)
		require.Regexp(t, `(?m)^dag\+check://non-empty-generate/stale\s+# `, out)
		require.NotContains(t, out, "Generators")
	})

	t.Run("list with generated=false excludes generators", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "--generated=false")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		// Should only list regular checks, no Generators section
		require.Contains(t, out, "passing-check")
		require.NotContains(t, out, "Generators")
		require.NotContains(t, out, "empty-generate")
		require.NotContains(t, out, "non-empty-generate")
	})

	t.Run("list with generated=true includes all checks", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "--generated=true")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		// Generated and regular checks are both included.
		require.Regexp(t, `(?m)^--generated=true dag\+check://empty-generate/stale\s+# `, out)
		require.Regexp(t, `(?m)^--generated=true dag\+check://non-empty-generate/stale\s+# `, out)
		require.Contains(t, out, "passing-check")
	})

	t.Run("run empty generator passes", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("--progress=report", "check", "empty-generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		// The run report has to name the check as the list does.
		require.Regexp(t, `empty-generate/stale.*OK`, out)
	})

	t.Run("run non-empty generator fails", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExecFail("--progress=report", "check", "non-empty-generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `non-empty-generate/stale.*ERROR`, out)
		require.Contains(t, out, "generated files are not up to date")
	})

	t.Run("the listed name selects the check", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("--progress=report", "check", "empty-generate/stale")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `empty-generate/stale.*OK`, out)
		require.NotContains(t, out, "passing-check")

		out, err = modGen.
			With(daggerExecFail("--progress=report", "check", "non-empty-generate/stale")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `non-empty-generate/stale.*ERROR`, out)
	})

	t.Run("the artifact API resolves the address it reports", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{currentWorkspace{artifacts{filterUri(uri:"**/stale"){items{uri path}}}}}`)).Stdout(ctx)
		require.NoError(t, err)
		listed := gjson.Get(out, "currentWorkspace.artifacts.filterUri.items").Array()
		require.Len(t, listed, 2)
		for _, check := range listed {
			uri := check.Get("uri").String()
			require.Contains(t, []string{"dag://empty-generate/stale", "dag://non-empty-generate/stale"}, uri)
			out, err := modGen.With(daggerQuery(`{currentWorkspace{artifacts{filterUri(uri:%q){one{uri}}}}}`, uri)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, uri, gjson.Get(out, "currentWorkspace.artifacts.filterUri.one.uri").String())
		}
	})

	t.Run("a wildcard matching the generator still selects the check", func(ctx context.Context, t *testctx.T) {
		// "*" spans one segment, so this matches the generator and not the
		// longer check name. It selected the check before the up-to-date leaf.
		out, err := modGen.
			With(daggerExec("check", "-l", "empty-*")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `(?m)^dag\+check://empty-generate/stale\s+# `, out)
		require.NotContains(t, out, "passing-check")
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

	t.Run("run with generated=false skips generators", func(ctx context.Context, t *testctx.T) {
		// Should pass because only passing-check runs
		out, err := modGen.
			With(daggerExec("--progress=report", "check", "--generated=false")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passing-check.*OK`, out)
		require.NotContains(t, out, "empty-generate")
		require.NotContains(t, out, "non-empty-generate")
	})

	t.Run("run with generated=true includes all checks", func(ctx context.Context, t *testctx.T) {
		// Should fail because non-empty-generate produces changes
		out, err := modGen.
			With(daggerExecFail("--progress=report", "check", "--generated=true")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `empty-generate.*OK`, out)
		require.Regexp(t, `non-empty-generate.*ERROR`, out)
		require.Contains(t, out, "passing-check")
	})

	t.Run("generated requires a boolean", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExecFail("check", "--generated=maybe")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `invalid argument "maybe" for "--generated" flag`)
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
		require.Contains(t, out, "test/lint")
		require.Contains(t, out, "test/unit")
		require.NotContains(t, out, "failing-check")
		require.NotContains(t, out, "failing-container")
	})

	t.Run("list with glob skip pattern", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "--skip", "**:unit")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "test/lint")
		require.NotContains(t, out, "test/unit")
	})

	t.Run("list with prefix skip pattern", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "--skip", "test")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "passing-check")
		require.NotContains(t, out, "test/lint")
		require.NotContains(t, out, "test/unit")
	})

	t.Run("list with include and skip combined", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerExec("check", "-l", "test", "--skip", "**:unit")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "test/lint")
		require.NotContains(t, out, "test/unit")
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
	require.Contains(t, out, "hello-with-checks/passing-check")
	require.Contains(t, out, "hello-with-checks/passing-container")
	require.NotContains(t, out, "hello-with-checks/failing-check")
	require.NotContains(t, out, "hello-with-checks/failing-container")
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
		require.Contains(t, out, "hello-with-generate-checks/passing-check")
		require.NotContains(t, out, "empty-generate")
		require.NotContains(t, out, "non-empty-generate")
	})

	t.Run("run passes by default despite stale generator", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("--progress=report", "check")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `passing-check.*OK`, out)
		require.NotContains(t, out, "non-empty-generate")
	})

	t.Run("--generated=true flag overrides the config", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "-l", "--generated=true")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `(?m)^--generated=true dag\+check://hello-with-generate-checks/empty-generate/stale\s+# staleness check:`, out)
		require.Regexp(t, `(?m)^--generated=true dag\+check://hello-with-generate-checks/non-empty-generate/stale\s+# staleness check:`, out)
		require.Contains(t, out, "passing-check")
	})

	t.Run("--generated=false flag matches the config default", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "-l", "--generated=false")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "hello-with-generate-checks/passing-check")
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
	require.Contains(t, out, "hello-with-checks/passing-check")
	require.Contains(t, out, "hello-with-checks/passing-container")
	require.NotContains(t, out, "hello-with-checks/failing-check")
	require.NotContains(t, out, "hello-with-checks/failing-container")
}

// TestChecksReportUnloadableModules covers `dagger check`'s handling of a
// workspace module that cannot be loaded: the modules that do load still run,
// and the one that does not is reported as a check that fails. check stays a
// gate -- the run exits non-zero even when every check that ran passed -- but a
// broken module no longer costs the whole report, and listing no longer aborts.
func (ChecksSuite) TestChecksReportUnloadableModules(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceFixture(t, c, "generators-broken")

	t.Run("generated=true retains load failures", func(ctx context.Context, t *testctx.T) {
		out, err := base.With(daggerExec("check", "-l", "--generated=true")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "bad/load")
		require.Contains(t, out, "good/verify")
		out, err = base.With(daggerExecFail("check", "--generated=true", "--progress=report")).CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `bad/load.*ERROR`, out)
	})

	t.Run("listing succeeds and names the module that could not be loaded", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "good/verify")
		require.Contains(t, out, "bad/load")
		require.Contains(t, out, "this workspace module could not be loaded")
	})

	t.Run("running reports the load failure as a failed check and still runs the rest", func(ctx context.Context, t *testctx.T) {
		// good/verify passes, so the non-zero exit can only come from the
		// module that could not be loaded.
		out, err := base.
			With(daggerExecFail("check", "--generated=false", "--progress=report")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `good/verify.*OK`, out)
		require.Regexp(t, `bad/load.*ERROR`, out)
		require.Contains(t, out, `loading module "/work/.dagger/modules/bad"`)
	})

	t.Run("scoping to the broken module reports its load failure", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExecFail("check", "bad", "--generated=false", "--progress=report")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `bad/load.*ERROR`, out)
	})

	t.Run("scoping to the healthy module passes and never mentions the broken one", func(ctx context.Context, t *testctx.T) {
		out, err := base.
			With(daggerExec("check", "good", "--generated=false", "--progress=report")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `good/verify.*OK`, out)
		require.NotContains(t, out, "modules/bad")
	})

	t.Run("a module that cannot load does not suppress an SDK's derived check", func(ctx context.Context, t *testctx.T) {
		// The two reach allChecks from different places: the derived check is
		// built from workspace config (so it survives a module that cannot
		// load), the load-failure check from the load failures themselves.
		out, err := workspaceFixture(t, c, "sdk-generate-check").
			WithNewFile("dagger.toml", `[modules.alpha-sdk]
source = ".dagger/modules/alpha-sdk"

[sdks.alpha]
module = "alpha-sdk"

[sdks.alpha.scopes."alpha"]
is-module = true
name = "alpha"

[modules.bad]
source = ".dagger/modules/bad"
`).
			WithNewFile(".dagger/modules/bad/dagger-module.toml", `name = "bad"
engineVersion = "v0.21.9"

[runtime]
  source = "dang"
`).
			WithNewFile(".dagger/modules/bad/main.dang", "this is intentionally invalid dang source").
			With(daggerExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "alpha-sdk/generate/stale")
		require.Contains(t, out, "bad/load")
	})

	t.Run("a module missing its generated files is still told to generate", func(ctx context.Context, t *testctx.T) {
		// The reason best-effort loading cannot reuse generate's phrasing:
		// generate drops the "run `dagger generate`" advice because that is
		// what is running, and for check it is the fix (see ModuleLoadMode).
		out, err := workspaceFixture(t, c, "generate-load-failures").
			With(daggerExecFail("check", "ungenerated", "--progress=report")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "run `dagger generate`")
		require.NotContains(t, out, "skipped until it is generated")
	})
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
			require.Contains(t, out, "dag://"+tc.path+"/passing-check")
			require.Contains(t, out, "dag://"+tc.path+"/failing-check")
			require.Contains(t, out, "dag://"+tc.path+"/test/lint")
			require.Contains(t, out, "dag://"+tc.path+"/test/unit")
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
		// Generator exposes its stale check at a child address.
		require.Regexp(t, `(?m)^--check=(alpha-sdk/generate/)?stale\s+# staleness check:`, out)
	})

	t.Run("the generator keeps the un-suffixed name", func(ctx context.Context, t *testctx.T) {
		// The up-to-date leaf exists so the two lists never show the same name.
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("generate", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `(?m)^--generator=(alpha-sdk/)?generate\s+#`, out)
		require.NotContains(t, out, "/stale")
	})

	t.Run("generated=false excludes the derived check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("check", "-l", "--generated=false")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "alpha-sdk/generate")
	})

	t.Run("generated=true includes the derived check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("check", "-l", "--generated=true")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "alpha-sdk/generate/stale")
	})

	t.Run("check-generated false excludes the derived check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, "check-generated = false\n\n"+alphaOnly).
			With(daggerNonNestedExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "alpha-sdk/generate")
	})

	t.Run("skip excludes the derived check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("check", "-l", "--skip", "alpha-sdk/generate")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "alpha-sdk/generate")
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
		require.NotContains(t, out, "alpha-sdk/generate")
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
		require.Regexp(t, `(?m)^dag\+check://generate/stale\s+# staleness check:`, out)
		require.NotContains(t, out, "alpha-sdk/generate")
	})

	t.Run("the derived check passes when the scope is generated", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("--progress=report", "check", "alpha-sdk/generate")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `alpha-sdk/generate/stale.*OK`, out)
	})

	t.Run("the listed name selects the derived check", func(ctx context.Context, t *testctx.T) {
		// `dagger check -l` output is meant to be typed back, so the up-to-date
		// name has to select and run the check, not just label it.
		base := generated(ctx, t, bothSDKs)
		out, err := base.
			With(daggerNonNestedExec("check", "-l", "alpha-sdk/generate/stale")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `(?m)^--check=(alpha-sdk/generate/)?stale\s+# staleness check:`, out)
		require.NotContains(t, out, "beta-sdk")

		out, err = base.
			With(daggerNonNestedExec("--progress=report", "check", "alpha-sdk/generate/stale")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Regexp(t, `alpha-sdk/generate/stale.*OK`, out)

		// Running it is what makes a stale scope fail under that name.
		out, err = base.
			WithoutFile(alphaMarker).
			With(daggerNonNestedExecFail("--progress=report", "check", "alpha-sdk/generate/stale")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `alpha-sdk/generate/stale.*ERROR`, out)
	})

	t.Run("skipping the listed name excludes the derived check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			With(daggerNonNestedExec("check", "-l", "--skip", "alpha-sdk/generate/stale")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.NotContains(t, out, "alpha-sdk/generate")
	})

	t.Run("the derived check fails when the scope is stale", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, alphaOnly).
			WithoutFile(alphaMarker).
			With(daggerNonNestedExecFail("--progress=report", "check", "alpha-sdk/generate")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `alpha-sdk/generate/stale.*ERROR`, out)
		require.Contains(t, out, "generated files are not up to date")
	})

	t.Run("an explicit check cannot share the generator address", func(ctx context.Context, t *testctx.T) {
		config := strings.Replace(alphaOnly,
			`.dagger/modules/alpha-sdk"`,
			`.dagger/modules/alpha-sdk-checked"`, 1)
		base := workspaceFixture(t, c, "sdk-generate-check").
			WithNewFile("dagger.toml", config).
			WithEnvVariable("_EXPERIMENTAL_DAGGER_CLI_BIN", testCLIBinPath).
			With(nonNestedDevEngine(c))
		out, err := base.
			With(daggerNonNestedExecFail("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, `ambiguous artifact path "alpha-sdk/generate"`)
	})

	t.Run("each SDK gets its own check", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, bothSDKs).
			With(daggerNonNestedExec("check", "-l")).
			CombinedOutput(ctx)
		require.NoError(t, err, out)
		require.Contains(t, out, "alpha-sdk/generate/stale")
		require.Contains(t, out, "beta-sdk/generate/stale")
	})

	t.Run("each SDK check covers its own generated files", func(ctx context.Context, t *testctx.T) {
		// Dependency generation prepares inputs; each changeset contains only its own SDK output.
		out, err := generated(ctx, t, bothSDKs).
			WithoutFile(alphaMarker).
			With(daggerNonNestedExecFail("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `alpha-sdk/generate/stale.*ERROR`, out)
		require.Regexp(t, `beta-sdk/generate/stale.*OK`, out)
	})

	t.Run("an independent SDK's check is unaffected", func(ctx context.Context, t *testctx.T) {
		out, err := generated(ctx, t, independentSDKs).
			WithoutFile(alphaMarker).
			With(daggerNonNestedExecFail("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `alpha-sdk/generate/stale.*ERROR`, out)
		require.Regexp(t, `beta-sdk/generate/stale.*OK`, out)
	})
}
