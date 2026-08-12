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
			append([]string{"dagger", "--progress=report", "--silent", "workspace", "config"}, args...),
		)
	}
}

func workspaceIncludeConfigFail(args ...string) core.WithContainerFunc {
	return func(ctr *core.Container) *core.Container {
		return ctr.WithExec(
			append([]string{"dagger", "--progress=report", "workspace", "config"}, args...),
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
		require.Contains(t, out, "entrypoint = true", "other fields are inherited")
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

	// The include entry is the workspace entrypoint, so its functions are
	// proxied onto the root rather than namespaced under the module name.
	t.Run("loads a module the include contributes", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.With(workspaceSelectionDaggerCall("identify")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello from the base", strings.TrimSpace(out))
	})
}

func (WorkspaceIncludeSuite) TestLocalModulesAreNotInherited(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	baseRef := workspaceSelectionRemoteRef(ctx, t, c, core.NewQuery(c).Directory().
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
		[]string{"dagger", "--progress=report", "--silent", "workspace", "config", "env.ci.modules.greeter.settings.greeting"},
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
			[]string{"dagger", "--progress=report", "uninstall", "greeter"},
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
