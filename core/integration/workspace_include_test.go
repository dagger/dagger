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
// - lockfile_test.go: the git.ref lock entries a git include records.

import (
	"context"
	"strings"
	"testing"

	"dagger.io/dagger"
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

	return workspaceSelectionRemoteRef(ctx, t, c, c.Directory().
		WithNewFile("dagger.json", `{"name":"`+name+`","sdk":{"source":"dang"}}`).
		WithNewFile("main.dang", workspaceSelectionDangSource(typeName, "identify", result)))
}

// workspaceIncludeConsumer is the consuming workspace: a git-rooted /work — the
// boundary local workspace detection walks up to — whose dagger.toml declares
// the include.
func workspaceIncludeConsumer(t *testctx.T, c *dagger.Client, configTOML string) *dagger.Container {
	t.Helper()

	return workspaceBase(t, c).WithNewFile("/work/dagger.toml", configTOML)
}

func workspaceIncludeConfig(args ...string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithExec(
			append([]string{"dagger", "--progress=report", "--silent", "workspace", "config"}, args...),
			dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
		)
	}
}

func workspaceIncludeConfigFail(args ...string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithExec(
			append([]string{"dagger", "--progress=report", "workspace", "config"}, args...),
			dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
				Expect:                        dagger.ReturnTypeFailure,
			},
		)
	}
}

func (WorkspaceIncludeSuite) TestMergesIncludedConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	moduleRef := workspaceIncludeModuleRepo(ctx, t, c, "greeter", "Greeter", "hello from the base")
	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().WithNewFile("dagger.toml", `[modules.greeter]
source = "`+moduleRef+`"
entrypoint = true

[modules.greeter.settings]
greeting = "hello"
tone = "formal"
`))

	// The overriding entry carries no source: it inherits the module's ref from
	// the include config and only changes one setting.
	ctr := workspaceIncludeConsumer(t, c, `
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
		require.Contains(t, out, "entrypoint = true", "other fields are inherited")

		require.Contains(t, out, "# included: "+baseRef)
		require.NotContains(t, out, "[[include]]", "the applied layer must not be re-applicable from this output")
	})

	t.Run("reads the include list from the local config", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceIncludeConfig("include")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, baseRef, strings.TrimSpace(out))
	})

	// The include entry is the workspace entrypoint, so its functions are
	// proxied onto the root rather than namespaced under the module name.
	t.Run("loads a module the include contributes", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceSelectionDaggerCall("identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from the base", strings.TrimSpace(out))
	})
}

func (WorkspaceIncludeSuite) TestCurrentWorkspaceWins(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().WithNewFile("dagger.toml", `ignore = ["base-dist"]

[modules.greeter]
source = "github.com/acme/base-greeter@v1"
pin = "1111111111111111111111111111111111111111"
entrypoint = true
`))

	ctr := workspaceIncludeConsumer(t, c, `ignore = ["dist"]

[modules.greeter]
source = "github.com/acme/local-greeter@v2"
entrypoint = false

[[include]]
source = "`+baseRef+`"
`)

	out, err := ctr.With(workspaceIncludeConfig()).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "github.com/acme/local-greeter@v2"`)
	require.NotContains(t, out, "1111111111111111111111111111111111111111", "a replaced source drops the pin that belonged to it")
	require.Contains(t, out, `ignore = ["dist"]`)

	// entrypoint = false is an override, not an absent key.
	out, err = ctr.With(workspaceIncludeConfig("modules.greeter.entrypoint")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "false", strings.TrimSpace(out))
}

func (WorkspaceIncludeSuite) TestLocalModulesAreNotInherited(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().
		WithNewFile("dagger.toml", `[modules.local-ci]
source = ".dagger/modules/local-ci"

[modules.remote-tool]
source = "github.com/acme/tool@v1"
`).
		WithNewFile(".dagger/modules/local-ci/dagger.json", `{"name":"local-ci","sdk":{"source":"dang"}}`).
		WithNewFile(".dagger/modules/local-ci/main.dang", workspaceSelectionDangSource("LocalCi", "identify", "base local module")))

	// The consuming workspace has a directory at the very same path: without
	// the drop, the inherited source would resolve here instead.
	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "`+baseRef+`"
`).
		WithNewFile("/work/.dagger/modules/local-ci/dagger.json", `{"name":"local-ci","sdk":{"source":"dang"}}`).
		WithNewFile("/work/.dagger/modules/local-ci/main.dang", workspaceSelectionDangSource("LocalCi", "identify", "consumer local module"))

	t.Run("drops the entry", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceIncludeConfig()).Stdout(ctx)
		require.NoError(t, err)
		require.NotContains(t, out, "local-ci", "a local module of the included config must not be usable here")
		require.Contains(t, out, `source = "github.com/acme/tool@v1"`, "remote entries still come through")
	})

	t.Run("says so", func(ctx context.Context, t *testctx.T) {
		// Reported without --silent, which suppresses progress output. The
		// warning has to reach a command that skips workspace modules
		// entirely, which is exactly what `workspace config` does.
		out, err := ctr.WithExec(
			[]string{"dagger", "--progress=plain", "workspace", "config"},
			dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
		).CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "local-ci")
		require.Contains(t, out, "Only its remote modules are inherited")
	})

	t.Run("does not resolve the same-named local directory", func(ctx context.Context, t *testctx.T) {
		_, err := ctr.With(workspaceSelectionDaggerCallFail("local-ci", "identify")).Sync(ctx)
		require.NoError(t, err)
	})
}

func (WorkspaceIncludeSuite) TestRejectsNestedInclude(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// The nested ref is never followed — the error fires on its presence — so a
	// literal one saves standing up a second git service.
	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().WithNewFile("dagger.toml", `
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
	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().
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

func (WorkspaceIncludeSuite) TestIncludesAConfigFileInTheSameWorkspace(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// A monorepo shape: the shared config sits next to the workspace that
	// includes it, addressed as a path rather than a git ref.
	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "common/base.toml"
`).
		WithNewFile("/work/common/base.toml", `[modules.greeter]
source = "github.com/acme/greeter@v1"

[modules.greeter.settings]
greeting = "hello"
tone = "formal"
`)

	out, err := ctr.With(workspaceIncludeConfig()).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "github.com/acme/greeter@v1"`)
	require.Contains(t, out, `tone = "formal"`)
	require.Contains(t, out, "# included: common/base.toml")
}

func (WorkspaceIncludeSuite) TestIncludesADirectoryConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// A source that names a directory reaches the dagger.toml inside it.
	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "common"
`).
		WithNewFile("/work/common/dagger.toml", `[modules.greeter]
source = "github.com/acme/greeter@v1"
`)

	out, err := ctr.With(workspaceIncludeConfig()).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "github.com/acme/greeter@v1"`)
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

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().WithNewFile("dagger.toml", `[modules.greeter]
source = "github.com/acme/greeter@v1"
`))

	ctr := workspaceIncludeConsumer(t, c, `
[modules.missing.settings]
greeting = "hola"

[[include]]
source = "`+baseRef+`"
`)

	stderr, err := ctr.With(workspaceIncludeConfigFail()).Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, stderr, `module "missing" has no source`)
}

func (WorkspaceIncludeSuite) TestIncludedEnvIsSelectable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().WithNewFile("dagger.toml", `[modules.greeter]
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

	out, err := ctr.WithExec(
		[]string{"dagger", "--progress=report", "--silent", "--env", "ci", "workspace", "config", "modules.greeter.settings.greeting"},
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "ci hello", strings.TrimSpace(out))
}

func (WorkspaceIncludeSuite) TestWritesStayLocal(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().WithNewFile("dagger.toml", `[modules.greeter]
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
			[]string{"dagger", "--progress=report", "uninstall", "greeter"},
			dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
				Expect:                        dagger.ReturnTypeFailure,
			},
		).Stderr(ctx)
		require.NoError(t, err)
		require.Contains(t, stderr, "comes from the included config")
	})
}

func (WorkspaceIncludeSuite) TestGitIncludeIsPinnedInTheLockfile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	moduleRef := workspaceIncludeModuleRepo(ctx, t, c, "greeter", "Greeter", "hello from the base")
	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().WithNewFile("dagger.toml", `[modules.greeter]
source = "`+moduleRef+`"
entrypoint = true
`))

	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "`+baseRef+`"
`)

	// A first run resolves the include's ref live and records it.
	resolved := ctr.WithExec(
		[]string{"dagger", "--progress=report", "--silent", "--lock=pinned", "call", "identify"},
		dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		},
	)
	lock, err := resolved.File("/work/dagger.lock").Contents(ctx)
	require.NoError(t, err)
	require.Contains(t, lock, "git.ref", "the include is pinned like any other git ref")
	require.Contains(t, lock, strings.TrimSuffix(baseRef, "@main"), "the entry names the include repository")

	// A frozen run replays that pin instead of resolving the ref again.
	out, err := resolved.WithExec(
		[]string{"dagger", "--progress=report", "--silent", "--lock=frozen", "workspace", "config"},
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "`+moduleRef+`"`)
}
