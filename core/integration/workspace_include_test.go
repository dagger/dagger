package core

// These tests cover [[include]] in dagger.toml: a workspace naming another
// config whose contents are merged underneath its own. A source is either a
// path next to the including config or a git ref.
//
// A git include's repository is served by workspaceSelectionRemoteRef, at an
// IP-addressed URL, because the engine resolves the include itself — there is
// no client-side service binding to attach.
//
// See also:
// - workspace_config_test.go: reads and writes of the local config.
// - workspace_selection_test.go: the remote-workspace machinery a git include
//   reuses.
// - lockfile_test.go: the git-sha lock entries a git include records.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type WorkspaceIncludeSuite struct{}

func TestWorkspaceInclude(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(WorkspaceIncludeSuite{})
}

// workspaceIncludeModuleRepo serves a repository holding a single dang module,
// so an include workspace can install it the way a published base config
// would: by remote ref.
func workspaceIncludeModuleRepo(ctx context.Context, t *testctx.T, c *dagger.Client, name, typeName, result string) string {
	t.Helper()

	return workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().
		WithNewFile("dagger.json", `{"name":"`+name+`","sdk":{"source":"dang"}}`).
		WithNewFile("main.dang", workspaceSelectionDangSource(typeName, "identify", result)))
}

// workspaceIncludeConsumer is the consuming workspace: a git-rooted /work — the
// boundary local workspace detection walks up to — whose dagger.toml declares
// the include.
func workspaceIncludeConsumer(t *testctx.T, c *dagger.Client, configTOML string) *core.Container {
	t.Helper()

	return workspaceBase(t, c).WithNewFile("/work/dagger.toml", configTOML)
}

func workspaceIncludeConfig(args ...string) core.WithContainerFunc {
	return func(ctr *core.Container) *core.Container {
		return ctr.WithExec(
			append([]string{"dagger", "workspace", "config"}, args...),
		)
	}
}

func workspaceIncludeConfigFail(args ...string) core.WithContainerFunc {
	return func(ctr *core.Container) *core.Container {
		return ctr.WithExec(
			append([]string{"dagger", "workspace", "config"}, args...),
			core.ContainerWithExecOpts{
				Expect: core.ReturnTypeFailure,
			},
		)
	}
}

func (WorkspaceIncludeSuite) TestMergesIncludedConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	moduleRef := workspaceIncludeModuleRepo(ctx, t, c, "greeter", "Greeter", "hello from the base")
	baseRef := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("dagger.toml", `ignore = ["base-dist"]

[modules.greeter]
source = "`+moduleRef+`"
entrypoint = true

[modules.greeter.settings]
greeting = "hello"
tone = "formal"
`))

	// The overriding entry carries no source: it inherits the module's ref from
	// the include config and only changes one setting.
	ctr := workspaceIncludeConsumer(t, c, `ignore = ["dist"]

[modules.greeter.settings]
greeting = "hola"

[[include]]
source = "`+baseRef+`"
`)

	t.Run("reports the merged config", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceIncludeConfig()).Stdout(ctx)
		require.NoError(t, err)

		require.Contains(t, out, `source = "`+moduleRef+`"`, "the module ref is inherited")
		require.Contains(t, out, `greeting = "hola"`, "the local override wins")
		require.Contains(t, out, `tone = "formal"`, "settings merge key by key")
		require.Contains(t, out, "main = true", "other fields are inherited")
		require.Contains(t, out, `ignore = ["dist"]`, "a top-level key the file sets wins")
		require.NotContains(t, out, "base-dist")

		require.Contains(t, out, "# included: "+baseRef)
		require.NotContains(t, out, "[[include]]", "the applied layer must not be re-applicable from this output")
	})

	t.Run("the effective API read is the same standalone snapshot", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceSelectionDaggerQuery(`{currentWorkspace{configRead(effective: true)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "# included: "+baseRef)
		require.NotContains(t, out, "[[include]]")
	})

	t.Run("reads the include list from the local config", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceIncludeConfig("include")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, baseRef, strings.TrimSpace(out))
	})

	// The include entry is the workspace main, so its functions are
	// proxied onto the root rather than namespaced under the module name.
	t.Run("loads a module the include contributes", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceSelectionDaggerCall("identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from the base", strings.TrimSpace(out))
	})
}

// workspaceIncludeMonorepoIn runs a command from dir inside the consuming
// repository, which is what a monorepo's per-project workspace looks like: the
// git root is the workspace, and each project's dagger.toml sits below it.
//
// No --progress: the commands exercised here include `workspace config`, which
// renders no pipeline and so does not accept the flag.
func workspaceIncludeMonorepoIn(dir string, args ...string) core.WithContainerFunc {
	return func(ctr *core.Container) *core.Container {
		return ctr.WithWorkdir(dir).WithExec(
			append([]string{"dagger"}, args...),
			core.ContainerWithExecOpts{
				UseEntrypoint: true,
			},
		)
	}
}

func (WorkspaceIncludeSuite) TestMonorepoProjectsShareModulesThroughAnInclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The shape this feature exists for: shared/ holds the modules, common/
	// packages them as configuration, and each project includes common/. Every
	// config lives in one workspace — the git root — so common's own relative
	// paths only have to change what they are relative to.
	monorepo := workspaceBase(t, c).
		// shared/ is a workspace in its own right — that is where someone
		// authoring the modules works. Nothing includes it, and it must not
		// interfere with a project that includes common/.
		WithNewFile("/work/shared/dagger.toml", `[modules.dagger-dang-sdk]
source = "github.com/dagger/dang-sdk"

[sdks.dang]
module = "dagger-dang-sdk"

[sdks.dang.scopes.tester]
is-module = true

[sdks.dang.scopes.builder]
is-module = true
`).
		WithNewFile("/work/shared/tester/dagger.json", `{"name":"tester","sdk":{"source":"dang"}}`).
		WithNewFile("/work/shared/tester/main.dang", workspaceSelectionDangSource("Tester", "identify", "shared tester")).
		WithNewFile("/work/shared/builder/dagger.json", `{"name":"builder","sdk":{"source":"dang"}}`).
		WithNewFile("/work/shared/builder/main.dang", workspaceSelectionDangSource("Builder", "identify", "shared builder")).
		WithNewFile("/work/common/dagger.toml", `[modules.tester]
source = "../shared/tester"

[modules.builder]
source = "../shared/builder"
`).
		WithNewFile("/work/project-a/dagger.toml", `[[include]]
source = "../common"
`).
		WithNewFile("/work/project-b/dagger.toml", `[[include]]
source = "../common"

[modules.builder.settings]
platform = "arm64"
`)

	t.Run("dagger module list shows the shared modules", func(ctx context.Context, t *testctx.T) {
		out, err := monorepo.With(workspaceIncludeMonorepoIn("/work/project-a", "module", "list")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "tester")
		require.Contains(t, out, "builder")
	})

	t.Run("they can be called", func(ctx context.Context, t *testctx.T) {
		for _, tc := range []struct{ project, module, want string }{
			{project: "/work/project-a", module: "tester", want: "shared tester"},
			{project: "/work/project-a", module: "builder", want: "shared builder"},
			{project: "/work/project-b", module: "tester", want: "shared tester"},
		} {
			out, err := monorepo.
				With(workspaceIncludeMonorepoIn(tc.project, "call", tc.module, "identify")).
				Stdout(ctx)
			require.NoError(t, err, "%s %s", tc.project, tc.module)
			require.Equal(t, tc.want, strings.TrimSpace(out))
		}
	})

	t.Run("the effective config addresses them from the including project", func(ctx context.Context, t *testctx.T) {
		out, err := monorepo.
			With(workspaceIncludeMonorepoIn("/work/project-a", "workspace", "config")).
			Stdout(ctx)
		require.NoError(t, err)
		// Relative to project-a, not to common, and dot-prefixed so it can
		// never read back as a git ref.
		require.Contains(t, out, `source = "../shared/tester"`)
		require.Contains(t, out, "# included: ../common")
	})

	t.Run("a project overrides a shared module's settings without repeating its source", func(ctx context.Context, t *testctx.T) {
		out, err := monorepo.
			With(workspaceIncludeMonorepoIn("/work/project-b", "workspace", "config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `platform = "arm64"`)
		require.Contains(t, out, `source = "../shared/builder"`)
	})
}

func (WorkspaceIncludeSuite) TestIncludedModulesReachTheCommandTree(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// `module list` reads the config; the command tree is built from the API
	// schema, which needs the modules actually attached to Query. A workspace
	// can therefore list an inherited module and still not offer it as a
	// command — reported from the field against an earlier build, so it is
	// pinned here rather than left to the config read alone.
	monorepo := workspaceBase(t, c).
		WithNewFile("/work/shared/tester/dagger.json", `{"name":"tester","sdk":{"source":"dang"}}`).
		WithNewFile("/work/shared/tester/main.dang", workspaceSelectionDangSource("Tester", "identify", "shared tester")).
		WithNewFile("/work/common/dagger.toml", `[modules.tester]
source = "../shared/tester"
`).
		WithNewFile("/work/project-a/dagger.toml", `[[include]]
source = "../common"

[modules.tester.settings]
greeting = "hello"
`)

	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "functions", args: []string{"functions"}},
		{name: "call --help", args: []string{"call", "--help"}},
		{name: "api functions", args: []string{"api", "functions"}},
		{name: "api call --help", args: []string{"api", "call", "--help"}},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := monorepo.
				With(workspaceIncludeMonorepoIn("/work/project-a", tc.args...)).
				Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, "tester", "an inherited module has to be callable, not just listed")
		})
	}
}

func (WorkspaceIncludeSuite) TestLocalModulesOfAGitIncludeAreInherited(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", `[modules.local-ci]
source = ".dagger/modules/local-ci"

[modules.remote-tool]
source = "github.com/acme/tool@v1"
`).
		WithNewFile(".dagger/modules/local-ci/dagger.json", `{"name":"local-ci","sdk":{"source":"dang"}}`).
		WithNewFile(".dagger/modules/local-ci/main.dang", workspaceSelectionDangSource("LocalCi", "identify", "base local module")))

	// The consuming workspace holds a different module at the very same path,
	// which is what the rewrite has to get right: the entry must reach the
	// included repository's module, not the one next to the consumer.
	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "`+baseRef+`"
`).
		WithNewFile("/work/.dagger/modules/local-ci/dagger.json", `{"name":"local-ci","sdk":{"source":"dang"}}`).
		WithNewFile("/work/.dagger/modules/local-ci/main.dang", workspaceSelectionDangSource("LocalCi", "identify", "consumer local module"))

	t.Run("the entry becomes a ref into the included repository", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceIncludeConfig()).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "/.dagger/modules/local-ci@", "addressed in the repository the config came from")
		require.NotContains(t, out, `source = ".dagger/modules/local-ci"`, "not left as a path the consumer would resolve")
		require.Contains(t, out, `source = "github.com/acme/tool@v1"`, "remote entries are untouched")
	})

	t.Run("it is pinned to the commit the config was read at", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceIncludeConfig()).Stdout(ctx)
		require.NoError(t, err)
		ref := ""
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "/.dagger/modules/local-ci@") {
				ref = line
			}
		}
		require.NotEmpty(t, ref)
		_, version, _ := strings.Cut(ref, "/.dagger/modules/local-ci@")
		version = strings.Trim(strings.TrimSpace(version), `"`)
		require.Len(t, version, 40, "a full commit SHA, not the symbolic version: %q", version)
	})

	t.Run("calling it reaches the included repository's module", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceSelectionDaggerCall("local-ci", "identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "base local module", strings.TrimSpace(out))
	})
}

func (WorkspaceIncludeSuite) TestGitIncludeNamesAConfigInsideTheRepository(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The fragment spelling the reference page advertises, and the one place
	// the file-versus-directory rule meets a parsed subdir: the config is not
	// at the repository root, so the modules beside it have to be re-addressed
	// against its directory rather than against the clone.
	moduleRef := workspaceIncludeModuleRepo(ctx, t, c, "greeter", "Greeter", "hello from the base")
	baseRepo := core.NewQuery(c).Directory().
		WithNewFile("dagger/base.toml", `[modules.greeter]
source = "`+moduleRef+`"

[modules.local-ci]
source = "modules/ci"
`).
		WithNewFile("dagger/modules/ci/dagger.json", `{"name":"local-ci","sdk":{"source":"dang"}}`).
		WithNewFile("dagger/modules/ci/main.dang", workspaceSelectionDangSource("LocalCi", "identify", "ci beside the base config"))
	baseRef := strings.TrimSuffix(workspaceSelectionRemoteRef(ctx, t, c, baseRepo), "@main") + "#main:dagger/base.toml"

	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "`+baseRef+`"
`)

	out, err := ctr.With(workspaceIncludeConfig()).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "`+moduleRef+`"`, "a remote entry comes through untouched")
	baseURL, _, ok := strings.Cut(baseRef, "#")
	require.True(t, ok)
	require.Regexp(t,
		regexp.MustCompile(regexp.QuoteMeta(`source = "`+baseURL+`#`)+`[0-9a-f]{40}:dagger/modules/ci"`),
		out,
		"a local entry is addressed against the config's own directory with its explicit selector")

	out, err = ctr.With(workspaceSelectionDaggerCall("local-ci", "identify")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "ci beside the base config", strings.TrimSpace(out))
}

func (WorkspaceIncludeSuite) TestModulesWithNoAddressAreDropped(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// A built-in SDK install is the shape that is easiest to get wrong: its
	// source is a runtime name the engine resolves in-process, not a path, so
	// it has no address in the included repository. An entry escaping that
	// repository has none either.
	baseRef := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().
		WithNewFile("dagger.toml", `[modules.dagger-dang-sdk]
source = "dang"

[modules.escaping]
source = "../outside/ci"

[modules.remote-tool]
source = "github.com/acme/tool@v1"

[sdks.dang]
module = "dagger-dang-sdk"
`))

	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "`+baseRef+`"
`)

	t.Run("they do not appear in the effective config", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceIncludeConfig()).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "dagger-dang-sdk")
		require.NotContains(t, out, "escaping")
		require.Contains(t, out, `source = "github.com/acme/tool@v1"`)
	})

	t.Run("and the run says so", func(ctx context.Context, t *testctx.T) {
		// Reported without --silent, which suppresses progress output.
		// --progress cannot be set on this command: it does not render a
		// pipeline, so the flag is not among its capabilities. The
		// warning has to reach a command that skips workspace modules
		// entirely, which is exactly what `workspace config` does.
		out, err := ctr.WithExec(
			[]string{"dagger", "workspace", "config"},
		).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "dagger-dang-sdk (built-in SDK runtime)")
		require.Contains(t, out, "escaping (no address outside the config it came from)")
	})
}

func (WorkspaceIncludeSuite) TestRejectsNestedInclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The nested ref is never followed — the error fires on its presence — so a
	// literal one saves standing up a second git service.
	baseRef := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("dagger.toml", `
[[include]]
source = "github.com/acme/deeper@v1"
`))

	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "`+baseRef+`"
`)

	stderr, err := ctr.With(workspaceIncludeConfigFail()).Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, "nested includes are not supported")
}

func (WorkspaceIncludeSuite) TestRejectsMissingIncludeTarget(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// A repository with no config where the include points.
	baseRef := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().
		WithNewFile("README.md", "no config here"))

	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "`+baseRef+`"
`)

	stderr, err := ctr.With(workspaceIncludeConfigFail()).Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, "dagger.toml")
	require.Contains(t, stderr, "no such file")
}

func (WorkspaceIncludeSuite) TestIncludesAPathInTheSameWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := workspaceBase(t, c).
		WithNewFile("/work/common/base.toml", "[modules.greeter]\nsource = \"github.com/acme/greeter@v1\"\n").
		WithNewFile("/work/common/dagger.toml", "[modules.greeter]\nsource = \"github.com/acme/greeter@v1\"\n")
	for _, tc := range []struct {
		name   string
		source string
	}{
		{name: "beside the including config", source: "common/base.toml"},
		// The spelling that works unchanged from a config in a subdirectory.
		{name: "from the workspace root", source: "/common/base.toml"},
		{name: "a directory reaches its dagger.toml", source: "common"},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			out, err := base.
				WithNewFile("/work/dagger.toml", "[[include]]\nsource = \""+tc.source+"\"\n").
				With(workspaceIncludeConfig()).
				Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, out, `source = "github.com/acme/greeter@v1"`)
			require.Contains(t, out, "# included: "+tc.source)
		})
	}
}

func (WorkspaceIncludeSuite) TestRejectsMoreThanOneInclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The shape allows several; resolving several does not, for now.
	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "common/base.toml"

[[include]]
source = "common/extra.toml"
`).
		WithNewFile("/work/common/base.toml", "[modules]\n").
		WithNewFile("/work/common/extra.toml", "[modules]\n")

	stderr, err := ctr.With(workspaceIncludeConfigFail()).Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, "declares 2 includes")
	require.Contains(t, stderr, "only 1 is supported")
}

func (WorkspaceIncludeSuite) TestRejectsOverrideOfAModuleTheIncludeDoesNotProvide(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("dagger.toml", `[modules.greeter]
source = "github.com/acme/greeter@v1"
`))

	ctr := workspaceIncludeConsumer(t, c, `
[modules.missing.settings]
greeting = "hola"

[[include]]
source = "`+baseRef+`"
`)

	stderr, err := ctr.WithExec([]string{"dagger", "check", "-l"}, core.ContainerWithExecOpts{
		Expect: core.ReturnTypeFailure,
	}).Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, `module "missing" has no source`)
}

func (WorkspaceIncludeSuite) TestAnEnvOverlaySuppliesTheSource(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceIncludeConsumer(t, c, `
[modules.greeter.settings]
tone = "formal"

[env.dev.modules.greeter]
source = ".dagger/modules/greeter"
`).
		WithNewFile("/work/.dagger/modules/greeter/dagger.json", `{"name":"greeter","sdk":{"source":"dang"}}`).
		WithNewFile("/work/.dagger/modules/greeter/main.dang", workspaceSelectionDangSource("Greeter", "identify", "from the env"))

	out, err := ctr.WithExec([]string{"dagger", "--env", "dev", "functions"}).Stdout(ctx)
	require.NoError(t, err, "completeness is checked after the env overlay")
	require.Contains(t, out, "greeter")

	stderr, err := ctr.WithExec([]string{"dagger", "functions"}, core.ContainerWithExecOpts{
		Expect: core.ReturnTypeFailure,
	}).Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, `module "greeter" has no source`)

	_, err = ctr.With(workspaceIncludeConfig()).Stdout(ctx)
	require.NoError(t, err, "reads do not require a complete config")
}

func (WorkspaceIncludeSuite) TestAStagedEntryWithoutASourceFailsToLoad(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The staged-overlay loader, not the session one: without the check, the
	// empty source would load the workspace root under the entry's name.
	ctr := workspaceIncludeConsumer(t, c, "")
	stderr, err := ctr.WithExec([]string{"dagger", "query"}, core.ContainerWithExecOpts{
		Stdin:  fmt.Sprintf(`{currentWorkspace{withNewFile(path:"dagger.toml",contents:%q){artifacts{filterTypes(types:["Check"]){one{uri}}}}}}`, "[modules.missing.settings]\ngreeting = \"hola\"\n"),
		Expect: core.ReturnTypeFailure,
	}).Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, `module "missing" has no source`)
}

func (WorkspaceIncludeSuite) TestIncludedEnvIsSelectable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("dagger.toml", `[modules.greeter]
source = "github.com/acme/greeter@v1"

[modules.greeter.settings]
greeting = "hello"

[env.ci.modules.greeter.settings]
greeting = "ci hello"
`))

	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "`+baseRef+`"
`)

	// Read the overlay itself rather than selecting it: --env is temporarily
	// disabled across commands upstream. What matters here is that the env the
	// included config defines crossed the boundary at all.
	out, err := ctr.WithExec(
		[]string{"dagger", "workspace", "config", "env.ci.modules.greeter.settings.greeting"},
	).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "ci hello", strings.TrimSpace(out))
}

func (WorkspaceIncludeSuite) TestSetsTheIncludeThroughTheCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The config shape is an array of tables, but the CLI addresses the single
	// supported include as one value — which is the only way a user sets one
	// without hand-editing dagger.toml.
	ctr := workspaceIncludeConsumer(t, c, "ignore = [\"node_modules\"]\n").
		WithNewFile("/work/common/base.toml", `[modules.greeter]
source = "github.com/acme/greeter@v1"
`).
		With(workspaceIncludeConfig("include", "common/base.toml"))

	written, err := ctr.File("/work/dagger.toml").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, written, "[[include]]")
	require.Contains(t, written, `source = "common/base.toml"`)
	require.Contains(t, written, `ignore = ["node_modules"]`, "a bare key must not be swallowed by the include table")

	// Reading it back reports this workspace's own value, and the effective
	// view now carries what the include provides.
	out, err := ctr.With(workspaceIncludeConfig("include")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "common/base.toml", strings.TrimSpace(out))

	out, err = ctr.With(workspaceIncludeConfig()).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "github.com/acme/greeter@v1"`)
	require.Contains(t, out, "# included: common/base.toml")
}

func (WorkspaceIncludeSuite) TestRepairsABrokenIncludeThroughTheCLI(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "./missing.toml"
`).WithNewFile("/work/base.toml", `[modules.greeter]
source = "github.com/acme/greeter@v1"
`)

	for _, args := range [][]string{{"module", "list"}, {"check", "-l"}} {
		stderr, err := ctr.WithExec(
			append([]string{"dagger"}, args...),
			core.ContainerWithExecOpts{
				Expect: core.ReturnTypeFailure,
			},
		).Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, `workspace include "./missing.toml"`, "dagger %s", strings.Join(args, " "))
	}

	out, err := ctr.
		With(workspaceIncludeConfig("include", "./base.toml")).
		With(workspaceIncludeConfig()).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "github.com/acme/greeter@v1"`)
}

func (WorkspaceIncludeSuite) TestABrokenIncludeIsReportedBeforeTheEnvItStarves(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "./missing.toml"

[env.dev.modules.greeter.settings]
greeting = "hola"
`).WithNewFile("/work/base.toml", "[modules.greeter]\nsource = \"github.com/acme/greeter@v1\"\n")

	stderr, err := ctr.WithExec([]string{"dagger", "--env", "dev", "functions"}, core.ContainerWithExecOpts{
		Expect: core.ReturnTypeFailure,
	}).Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, `workspace include "./missing.toml"`)
	require.NotContains(t, stderr, "unknown module")

	out, err := ctr.
		With(workspaceIncludeConfig("include", "./base.toml")).
		WithExec([]string{"dagger", "--env", "dev", "workspace", "config", "modules.greeter.settings.greeting"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hola", strings.TrimSpace(out))
}

func (WorkspaceIncludeSuite) TestWritesStayLocal(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().WithNewFile("dagger.toml", `[modules.greeter]
source = "github.com/acme/greeter@v1"

[modules.greeter.settings]
greeting = "hello"
`))

	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "`+baseRef+`"
`)

	t.Run("a setting write records only the override", func(ctx context.Context, t *testctx.T) {
		written, err := ctr.
			With(workspaceIncludeConfig("modules.greeter.settings.greeting", "hola")).
			File("/work/dagger.toml").
			Contents(ctx)
		require.NoError(t, err)

		require.Contains(t, written, `source = "`+baseRef+`"`)
		require.Contains(t, written, `greeting = "hola"`)
		require.NotContains(t, written, `source = ""`, "an override must not write out an empty source")
		require.NotContains(t, written, "github.com/acme/greeter", "the inherited ref stays in the included config")
	})

	t.Run("uninstalling an included module names the include", func(ctx context.Context, t *testctx.T) {
		stderr, err := ctr.WithExec(
			[]string{"dagger", "uninstall", "greeter"},
			core.ContainerWithExecOpts{
				Expect: core.ReturnTypeFailure,
			},
		).Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "comes from the included config")
	})
}

func (WorkspaceIncludeSuite) TestGitIncludeIsPinnedInTheLockfile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	moduleRef := workspaceIncludeModuleRepo(ctx, t, c, "greeter", "Greeter", "hello from the base")
	baseConfig := func(greeting string) string {
		return `[modules.greeter]
source = "` + moduleRef + `"
entrypoint = true

[modules.greeter.settings]
greeting = "` + greeting + `"
`
	}
	// main moves past the commit the lock will pin, so reading the pinned
	// config proves the include is not re-resolved.
	repo := core.NewQuery(c).Container().From(alpineImage).
		WithExec([]string{"apk", "add", "git"}).
		WithWorkdir("/repo").
		WithExec([]string{"git", "init", "-b", "main"}).
		WithNewFile("/repo/dagger.toml", baseConfig("pinned")).
		WithExec([]string{"sh", "-c", "git add -A && git -c user.email=t@example.com -c user.name=t commit -qm pinned"}).
		WithNewFile("/repo/dagger.toml", baseConfig("moved")).
		WithExec([]string{"sh", "-c", "git -c user.email=t@example.com -c user.name=t commit -qam moved"})
	pinned, err := repo.WithExec([]string{"git", "rev-parse", "HEAD~1"}).Stdout(ctx)
	require.NoError(t, err)
	moved, err := repo.WithExec([]string{"git", "rev-parse", "HEAD"}).Stdout(ctx)
	require.NoError(t, err)
	pinned, moved = strings.TrimSpace(pinned), strings.TrimSpace(moved)
	baseRef := workspaceSelectionRemoteRef(ctx, t, c, repo.Directory("/repo"))

	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "`+baseRef+`"
`)

	// Locking is on by default, so an ordinary run resolves the include's ref
	// and records it like any other git lookup.
	resolved := ctr.WithExec(
		[]string{"dagger", "--progress=report", "--silent", "call", "identify"},
		core.ContainerWithExecOpts{
			UseEntrypoint: true,
		},
	)
	lock, err := resolved.File("/work/dagger.lock").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, lock, `"git-sha"`, "the include is pinned like any other git ref")
	// The lock entry names the repository as the resolver canonicalizes it:
	// no scheme, no .git suffix.
	repoIdentity := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(baseRef, "@main"), "http://"), ".git")
	require.Contains(t, lock, repoIdentity, "the entry names the include repository")
	require.Contains(t, lock, moved)

	pinnedRun := resolved.
		WithNewFile("/work/dagger.lock", strings.ReplaceAll(lock, moved, pinned)).
		With(workspaceIncludeConfig())
	out, err := pinnedRun.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `greeting = "pinned"`, "a recorded pin is reused instead of re-resolving main")

	t.Run("a staged lock edit repins it", func(ctx context.Context, t *testctx.T) {
		out, err := pinnedRun.With(workspaceSelectionDaggerQuery(
			fmt.Sprintf(`{currentWorkspace{withNewFile(path: "dagger.lock", contents: %q){configRead}}}`, lock),
		)).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "moved")
		require.NotContains(t, out, "pinned")
	})

	t.Run("a frozen workspace resolves through its own lock, not the caller's", func(ctx context.Context, t *testctx.T) {
		// One session reads both: the caller's lock pins the older commit, the
		// frozen workspace's own lock pins the newer one.
		out, err := pinnedRun.With(workspaceSelectionDaggerQuery(fmt.Sprintf(
			`{caller: currentWorkspace{configRead} frozen: directory{withNewFile(path: "dagger.toml", contents: %q){withNewFile(path: "dagger.lock", contents: %q){asWorkspace{configRead}}}}}`,
			"[[include]]\nsource = \""+baseRef+"\"\n", lock,
		))).Stdout(ctx)
		require.NoError(t, err)
		var res struct {
			Caller struct{ ConfigRead string }
			Frozen struct {
				WithNewFile struct {
					WithNewFile struct {
						AsWorkspace struct{ ConfigRead string }
					}
				}
			}
		}
		require.NoError(t, json.Unmarshal([]byte(out), &res))
		require.Contains(t, res.Caller.ConfigRead, `greeting = "pinned"`)
		require.Contains(t, res.Frozen.WithNewFile.WithNewFile.AsWorkspace.ConfigRead, `greeting = "moved"`)
	})

	// Without the pin the include follows main. `dagger lock update` cannot
	// show this here: it re-resolves every entry over https, and these test
	// servers only speak http.
	out, err = pinnedRun.
		WithoutFile("/work/dagger.lock").
		With(workspaceIncludeConfig()).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `greeting = "moved"`)
}

func (WorkspaceIncludeSuite) TestAUserLevelSourceKeepsWhatTheIncludeSets(ctx context.Context, t *testctx.T) {
	workdir := newWorkspaceConfigWorkdir(ctx, t, "[[include]]\nsource = \"./base.toml\"\n")
	require.NoError(t, os.WriteFile(filepath.Join(workdir, "base.toml"), []byte("[modules.api]\nsource = \"/opt/api\"\n\n[modules.api.settings]\nreplicas = 3\n"), 0o600))
	gitCmd := exec.Command("git", "remote", "add", "origin", "git@github.com:acme/include-user-supplier.git")
	gitCmd.Dir = workdir
	gitOutput, err := gitCmd.CombinedOutput()
	require.NoError(t, err, string(gitOutput))
	userConfigPath := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(userConfigPath, []byte(`[workspaces."github.com/acme/include-user-supplier".modules.api]
source = "./api"
`), 0o600))

	out, err := hostDaggerUserConfigExec(ctx, t, workdir, userConfigPath, "workspace", "config", "modules.api.settings.replicas")
	require.NoError(t, err)
	require.Equal(t, "3", strings.TrimSpace(string(out)))
}

func (WorkspaceIncludeSuite) TestAConsumerSkipsTheChecksOfAnInheritedModule(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	withModule := func(consumer string) *core.Container {
		return workspaceIncludeConsumer(t, c, consumer).
			WithNewFile("/work/common/dagger.toml", "[modules.example]\nsource = \"../shared/example\"\n").
			WithNewFile("/work/shared/example/dagger-module.toml", "name = \"example\"\nengineVersion = \"v1.0.0-beta.20\"\n\n[runtime]\nsource = \"dang\"\n").
			WithNewFile("/work/shared/example/main.dang", "type Example { pub verify: Void @check { null } }")
	}

	out, err := withModule("[[include]]\nsource = \"./common\"\n").WithExec([]string{"dagger", "check", "-l"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "verify")

	skipping := withModule("[[include]]\nsource = \"./common\"\n\n[modules.example]\ncheck.skip = [\"*\"]\n")
	out, err = skipping.WithExec([]string{"dagger", "check", "-l"}).Stdout(ctx)
	require.NoError(t, err)
	require.Empty(t, strings.TrimSpace(out), "every check of the module is skipped")

	// Skipping excludes the module's checks, not the module: it still loads.
	out, err = skipping.WithExec([]string{"dagger", "functions"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "example")
}
