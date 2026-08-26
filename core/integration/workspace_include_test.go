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

// workspaceIncludeMonorepoIn runs a command from dir inside the consuming
// repository, which is what a monorepo's per-project workspace looks like: the
// git root is the workspace, and each project's dagger.toml sits below it.
func workspaceIncludeMonorepoIn(dir string, args ...string) dagger.WithContainerFunc {
	return func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithWorkdir(dir).WithExec(
			append([]string{"dagger", "--progress=report"}, args...),
			dagger.ContainerWithExecOpts{
				UseEntrypoint:                 true,
				ExperimentalPrivilegedNesting: true,
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

[modules.dagger-dang-sdk.as-sdk]
name = "dang"

[[modules.dagger-dang-sdk.as-sdk.modules]]
path = "tester"

[[modules.dagger-dang-sdk.as-sdk.modules]]
path = "builder"
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

	t.Run("dagger installed lists the shared modules", func(ctx context.Context, t *testctx.T) {
		out, err := monorepo.With(workspaceIncludeMonorepoIn("/work/project-a", "installed")).Stdout(ctx)
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
			With(workspaceIncludeMonorepoIn("/work/project-a", "--silent", "workspace", "config")).
			Stdout(ctx)
		require.NoError(t, err)
		// Relative to project-a, not to common, and dot-prefixed so it can
		// never read back as a git ref.
		require.Contains(t, out, `source = "../shared/tester"`)
		require.Contains(t, out, "# included: ../common")
	})

	t.Run("a project overrides a shared module's settings without repeating its source", func(ctx context.Context, t *testctx.T) {
		out, err := monorepo.
			With(workspaceIncludeMonorepoIn("/work/project-b", "--silent", "workspace", "config")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, `platform = "arm64"`)
		require.Contains(t, out, `source = "../shared/builder"`)
	})
}

func (WorkspaceIncludeSuite) TestLocalModulesOfAGitIncludeAreInherited(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().
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
	baseRepo := c.Directory().
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
	require.Contains(t, out, "/dagger/modules/ci@", "a local entry is addressed against the config's own directory")

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
	baseRef := workspaceSelectionRemoteRef(ctx, t, c, c.Directory().
		WithNewFile("dagger.toml", `[modules.dagger-dang-sdk]
source = "dang"

[modules.dagger-dang-sdk.as-sdk]
name = "dang"

[modules.escaping]
source = "../outside/ci"

[modules.remote-tool]
source = "github.com/acme/tool@v1"
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
		// Reported without --silent, which suppresses progress output. The
		// warning has to reach a command that skips workspace modules
		// entirely, which is exactly what `workspace config` does.
		out, err := ctr.WithExec(
			[]string{"dagger", "--progress=plain", "workspace", "config"},
			dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
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

func (WorkspaceIncludeSuite) TestIncludesARootRelativeConfig(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// A leading "/" means the workspace root, the same rule every other path a
	// workspace resolves follows — which is the spelling that works unchanged
	// from a config in a subdirectory.
	ctr := workspaceIncludeConsumer(t, c, `
[[include]]
source = "/common/base.toml"
`).
		WithNewFile("/work/common/base.toml", `[modules.greeter]
source = "github.com/acme/greeter@v1"
`)

	out, err := ctr.With(workspaceIncludeConfig()).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "github.com/acme/greeter@v1"`)
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

	// Locking is on by default, so an ordinary run resolves the include's ref
	// and records it like any other git lookup.
	resolved := ctr.WithExec(
		[]string{"dagger", "--progress=report", "--silent", "call", "identify"},
		dagger.ContainerWithExecOpts{
			UseEntrypoint:                 true,
			ExperimentalPrivilegedNesting: true,
		},
	)
	lock, err := resolved.File("/work/dagger.lock").Contents(ctx)
	require.NoError(t, err)
	// "git-sha" is the immutable-resolution operation the lockfile records for
	// a git lookup; the include gets one because it resolves like any other.
	require.Contains(t, lock, `"git-sha"`, "the include is pinned like any other git ref")
	require.Contains(t, lock, strings.TrimSuffix(baseRef, "@main"), "the entry names the include repository")

	// A later run reuses the recorded pin rather than re-resolving: the lock is
	// left exactly as it was, and the merged config still resolves.
	replayed := resolved.WithExec(
		[]string{"dagger", "--progress=report", "--silent", "workspace", "config"},
		dagger.ContainerWithExecOpts{ExperimentalPrivilegedNesting: true},
	)
	out, err := replayed.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `source = "`+moduleRef+`"`)

	after, err := replayed.File("/work/dagger.lock").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, lock, after, "a pinned include is not re-resolved on the next run")
}
