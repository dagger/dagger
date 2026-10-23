package core

import (
	"context"

	"testing"

	"github.com/dagger/dagger/core/gitref"
	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

const testIncludeCommit = "0123456789abcdef0123456789abcdef01234567"

// testGitAddresser stands in for an include resolved from
// https://github.com/acme/base, whose config sits at the repository root.
func testGitAddresser() includedModuleAddresser {
	return gitIncludedModuleAddresser(
		"https://github.com/acme/base",
		gitref.NoSelector,
		testIncludeCommit,
		".",
	)
}

func TestAddressIncludedModules(t *testing.T) {
	t.Parallel()

	cfg, err := workspace.ParseConfig([]byte(`[modules.ci]
source = "modules/ci"

[modules.pinned-local]
source = "./ci"
pin = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

[modules.dotted-local]
source = "modules/foo.bar"

[modules.escaping]
source = "../outside/ci"

[modules.toolchain]
source = "github.com/acme/toolchain@v1"

[modules.scheme]
source = "https://example.com/acme/mod.git"

[env.ci.modules.ci.settings]
greeting = "hello"

[env.ci.modules.toolchain.settings]
version = "1.24"

[env.ci.modules.extra-local]
source = "modules/extra"

[ports.3000]
backendService = "ci:web"
backendPort = 8080

[ports.4000]
backendService = "toolchain:api"
backendPort = 9090

[ports.5000]
backendService = "escaping:web"
backendPort = 7070
`))
	require.NoError(t, err)

	dropped := addressIncludedModules(testGitAddresser(), cfg, nil)

	// Only what has no address in the included repository is left out.
	require.Equal(t, []string{"escaping (no address outside the config it came from)"}, dropped)

	// The included config's own modules become refs into its repository, at the
	// commit its config was read from.
	require.Equal(t, "https://github.com/acme/base/modules/ci@"+testIncludeCommit, cfg.Modules["ci"].Source)
	require.Equal(t, "https://github.com/acme/base/ci@"+testIncludeCommit, cfg.Modules["pinned-local"].Source)
	require.Empty(t, cfg.Modules["pinned-local"].Pin, "the pin described the source that was replaced")
	require.Equal(t, "https://github.com/acme/base/modules/foo.bar@"+testIncludeCommit, cfg.Modules["dotted-local"].Source,
		"a dot outside the host segment is a path, not a ref")

	// Sources that already address something outside the tree are untouched.
	require.Equal(t, "github.com/acme/toolchain@v1", cfg.Modules["toolchain"].Source)
	require.Equal(t, "https://example.com/acme/mod.git", cfg.Modules["scheme"].Source)

	// Env overlays follow their module, whether they install one or only
	// configure it.
	require.Equal(t, "https://github.com/acme/base/modules/extra@"+testIncludeCommit, cfg.Env["ci"].Modules["extra-local"].Source)
	require.Contains(t, cfg.Env["ci"].Modules, "ci")
	require.Contains(t, cfg.Env["ci"].Modules, "toolchain")

	// Ports keep pointing at modules that survived, and lose the one whose
	// module did not.
	require.Contains(t, cfg.Ports, "3000")
	require.Contains(t, cfg.Ports, "4000")
	require.NotContains(t, cfg.Ports, "5000")
}

func TestAddressIncludedModulesFromAConfigInASubdirectory(t *testing.T) {
	t.Parallel()

	// The included config is not at the repository root, so its own paths are
	// relative to where it sits rather than to the clone.
	cfg, err := workspace.ParseConfig([]byte(`[modules.ci]
source = "ci"

[modules.sibling]
source = "../tools/lint"
`))
	require.NoError(t, err)

	address := gitIncludedModuleAddresser(
		"https://github.com/acme/base",
		gitref.NoSelector,
		testIncludeCommit,
		"dagger/common",
	)
	require.Empty(t, addressIncludedModules(address, cfg, nil))

	require.Equal(t, "https://github.com/acme/base/dagger/common/ci@"+testIncludeCommit, cfg.Modules["ci"].Source)
	require.Equal(t, "https://github.com/acme/base/dagger/tools/lint@"+testIncludeCommit, cfg.Modules["sibling"].Source)
}

func TestGitIncludedModuleAddresserPreservesSelector(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		selector gitref.SelectorType
		want     string
	}{
		{
			name:     "module version",
			selector: gitref.ModuleVersionSelector,
			want:     "https://git.example.org/team/project.git/config/ci@" + testIncludeCommit,
		},
		{
			name:     "Git URL",
			selector: gitref.GitRefSelector,
			want:     "https://git.example.org/team/project.git#" + testIncludeCommit + ":config/ci",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address := gitIncludedModuleAddresser(
				"https://git.example.org/team/project.git",
				tc.selector,
				testIncludeCommit,
				"config",
			)
			got, ok := address("./ci")
			require.True(t, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestAddressIncludedModulesKeepsAVanityRemote(t *testing.T) {
	t.Parallel()

	// A schemeless remote whose host carries the dot: kept as written, and
	// without asking the network — classification is syntactic, so it cannot
	// depend on reachability.
	cfg, err := workspace.ParseConfig([]byte(`[modules.toolchain]
source = "vanity.example.com/acme/toolchain"
`))
	require.NoError(t, err)

	require.Empty(t, addressIncludedModules(testGitAddresser(), cfg, nil))
	require.Equal(t, "vanity.example.com/acme/toolchain", cfg.Modules["toolchain"].Source)
}

func TestAddressIncludedModulesDropsABuiltinSDKInstall(t *testing.T) {
	t.Parallel()

	// An SDK provider named by a runtime the engine resolves in-process, not a
	// path.
	cfg, err := workspace.ParseConfig([]byte(`[modules.php]
source = "php"

[modules.go]
source = "go@v1.2.3"

[sdks.php]
module = "php"

[sdks.go]
module = "go"
`))
	require.NoError(t, err)

	dropped := addressIncludedModules(testGitAddresser(), cfg, nil)
	require.Len(t, dropped, 2)
	require.Contains(t, dropped[0], "built-in SDK runtime")
	require.NotContains(t, cfg.Modules, "php")
	require.NotContains(t, cfg.Modules, "go")
}

func TestAddressIncludedModulesKeepsADirectoryNamedAfterAnSDK(t *testing.T) {
	t.Parallel()

	// With no [sdks.*] naming it as a provider, the entry is an ordinary module
	// that happens to live in a directory called "go", and the loader reads it
	// that way too. Dropping it on the name alone would lose a module nobody
	// said was a runtime.
	cfg, err := workspace.ParseConfig([]byte(`[modules.compiler]
source = "go"
`))
	require.NoError(t, err)

	require.Empty(t, addressIncludedModules(testGitAddresser(), cfg, nil))
	require.Equal(t, "https://github.com/acme/base/go@"+testIncludeCommit, cfg.Modules["compiler"].Source)
}

func TestAddressIncludedModulesDropsAPathAGitRefCannotSpell(t *testing.T) {
	t.Parallel()

	// "@" and "#" both mean "version" in a ref, so a path carrying either
	// would be cut short by the parser and resolve somewhere else entirely.
	cfg, err := workspace.ParseConfig([]byte(`[modules.scoped]
source = "modules/@scope/tool"

[modules.fragment]
source = "modules/a#b"
`))
	require.NoError(t, err)

	require.Len(t, addressIncludedModules(testGitAddresser(), cfg, nil), 2)
	require.NotContains(t, cfg.Modules, "scoped")
	require.NotContains(t, cfg.Modules, "fragment")
}

func TestAddressIncludedModulesDropsAWindowsAbsolutePath(t *testing.T) {
	t.Parallel()

	// A UNC path reaches a Linux engine with backslashes: normalizing after the
	// absolute check would read it as relative and rebase it under the included
	// config's directory.
	cfg, err := workspace.ParseConfig([]byte(`[modules.unc]
source = '\\server\share\mod'
`))
	require.NoError(t, err)

	require.Len(t, addressIncludedModules(testGitAddresser(), cfg, nil), 1)
	require.NotContains(t, cfg.Modules, "unc")
}

func TestAddressIncludedModulesKeepsAPortAnEnvOverlayStillInstalls(t *testing.T) {
	t.Parallel()

	// The base entry has no address here, but the env overlay installs the same
	// module with one that does — so the port still has something to reach.
	cfg, err := workspace.ParseConfig([]byte(`[modules.api]
source = "../../outside"

[env.ci.modules.api]
source = "modules/api"

[ports.3000]
backendService = "api:web"
backendPort = 8080
`))
	require.NoError(t, err)

	require.Len(t, addressIncludedModules(testGitAddresser(), cfg, nil), 1)
	require.Equal(t, "https://github.com/acme/base/modules/api@"+testIncludeCommit, cfg.Env["ci"].Modules["api"].Source)
	require.Contains(t, cfg.Ports, "3000")
}

func TestAddressIncludedModulesClearsAPinOnlyOverlayOfAReaddressedModule(t *testing.T) {
	t.Parallel()

	// A lone pin updates the base entry's pin, and that entry now names
	// something else entirely.
	cfg, err := workspace.ParseConfig([]byte(`[modules.ci]
source = "./ci"

[env.prod.modules.ci]
pin = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`))
	require.NoError(t, err)

	require.Empty(t, addressIncludedModules(testGitAddresser(), cfg, nil))
	require.Equal(t, "https://github.com/acme/base/ci@"+testIncludeCommit, cfg.Modules["ci"].Source)
	require.Empty(t, cfg.Env["prod"].Modules["ci"].Pin, "the pin described the source that was replaced")
}

func TestAddressIncludedModulesAddressesARootModule(t *testing.T) {
	t.Parallel()

	// A repository whose root is itself a module, installed with source = ".".
	cfg, err := workspace.ParseConfig([]byte(`[modules.app]
source = "."
`))
	require.NoError(t, err)

	require.Empty(t, addressIncludedModules(testGitAddresser(), cfg, nil))
	require.Equal(t, "https://github.com/acme/base@"+testIncludeCommit, cfg.Modules["app"].Source)
}

func TestAddressIncludedModulesKeepsWhatTheConsumerInstalls(t *testing.T) {
	t.Parallel()

	included := `[modules.api]
source = "/opt/api"

[modules.api.settings]
replicas = 3

[env.prod.modules.web.settings]
tier = "gold"

[ports.8080]
backendService = "api:http"
backendPort = 80
`
	consumer, err := workspace.ParseConfig([]byte(`[modules.api]
source = "./api"

[modules.web]
source = "./web"
`))
	require.NoError(t, err)

	cfg, err := workspace.ParseConfig([]byte(included))
	require.NoError(t, err)
	require.Empty(t, addressIncludedModules(testGitAddresser(), cfg, consumer))
	require.Contains(t, cfg.Ports, "8080")
	require.Equal(t, "gold", cfg.Env["prod"].Modules["web"].Settings["tier"])

	merged, err := workspace.MergeIncludedConfig(cfg, consumer, map[string]bool{})
	require.NoError(t, err)
	require.Equal(t, "./api", merged.Modules["api"].Source)
	require.EqualValues(t, 3, merged.Modules["api"].Settings["replicas"])

	// Without the consumer installing them, the same entries dangle.
	cfg, err = workspace.ParseConfig([]byte(included))
	require.NoError(t, err)
	require.Equal(t, []string{"api (no address outside the config it came from)"}, addressIncludedModules(testGitAddresser(), cfg, nil))
	require.NotContains(t, cfg.Modules, "api")
	require.NotContains(t, cfg.Ports, "8080")
	require.Empty(t, cfg.Env["prod"].Modules)
}

func TestAddressIncludedModulesDropsAnAbsolutePath(t *testing.T) {
	t.Parallel()

	// An absolute path addresses the machine the config was authored on.
	cfg, err := workspace.ParseConfig([]byte(`[modules.abs]
source = "/opt/ci"
`))
	require.NoError(t, err)

	require.Equal(t, []string{"abs (no address outside the config it came from)"}, addressIncludedModules(testGitAddresser(), cfg, nil))
}

func TestAddressIncludedModulesCascadesToPortsOfNonCanonicalNames(t *testing.T) {
	t.Parallel()

	// backendService names the module CLI-cased, which is not how the config
	// spells the key it was declared under.
	cfg, err := workspace.ParseConfig([]byte(`[modules.MyTool]
source = "../../my-tool"

[ports.3000]
backendService = "my-tool:web"
backendPort = 8080
`))
	require.NoError(t, err)

	require.Equal(t, []string{"MyTool (no address outside the config it came from)"}, addressIncludedModules(testGitAddresser(), cfg, nil))
	require.NotContains(t, cfg.Ports, "3000")
}

func TestAddressIncludedModulesCascadesToPortsOfEnvOnlyInstalls(t *testing.T) {
	t.Parallel()

	// The module is installed by an env overlay only, so dropping that overlay
	// leaves nothing for the port to forward to.
	cfg, err := workspace.ParseConfig([]byte(`[env.ci.modules.local-ci]
source = "/opt/ci"

[ports.3000]
backendService = "local-ci:web"
backendPort = 8080

[ports.4000]
backendService = "toolchain:api"
backendPort = 9090
`))
	require.NoError(t, err)

	require.Equal(t, []string{"env.ci.modules.local-ci (no address outside the config it came from)"}, addressIncludedModules(testGitAddresser(), cfg, nil))
	require.NotContains(t, cfg.Ports, "3000")
	require.Contains(t, cfg.Ports, "4000")
}

func TestPathIncludedModuleAddresser(t *testing.T) {
	t.Parallel()

	// The monorepo shape: common/dagger.toml installs the modules under
	// shared/, and project-a/dagger.toml includes it. Both paths are
	// workspace-relative, so only what they are relative to changes.
	address := pathIncludedModuleAddresser("common", "project-a")

	for _, tc := range []struct {
		source string
		want   string
		ok     bool
	}{
		{source: "../shared/tester", want: "../shared/tester", ok: true},
		{source: "modules/ci", want: "../common/modules/ci", ok: true},
		// Dot-prefixed even when the target sits below the consumer, so the
		// result can never read back as a git ref.
		{source: "../project-a/ci", want: "./ci", ok: true},
		{source: "../shared.v2/ci", want: "../shared.v2/ci", ok: true},
		{source: "../../elsewhere", ok: false},
		{source: "/opt/ci", ok: false},
		// The workspace root is itself a module.
		{source: "..", want: "..", ok: true},
	} {
		got, ok := address(tc.source)
		require.Equal(t, tc.ok, ok, "source %q", tc.source)
		if tc.ok {
			require.Equal(t, tc.want, got, "source %q", tc.source)
			require.True(t, workspace.IsLocalRef(got, ""), "%q must read back as a path", got)
		}
	}
}

func TestPathIncludedModuleAddresserFromTheWorkspaceRoot(t *testing.T) {
	t.Parallel()

	// A consuming config at the workspace root: the rewritten path is the
	// workspace-relative one, still dot-prefixed.
	address := pathIncludedModuleAddresser("common", "")
	got, ok := address("../shared/tester")
	require.True(t, ok)
	require.Equal(t, "./shared/tester", got)
}

func TestIncludedSourceIsLocal(t *testing.T) {
	t.Parallel()

	// The classifier is workspace.IsLocalRef, whose own tests cover the
	// spellings. The pin is deliberately empty: IsLocalRef reads any pinned ref
	// as git, which would let `source = "./ci", pin = "…"` through.
	require.True(t, includedSourceIsLocal("./ci"))
	require.False(t, workspace.IsLocalRef("./ci", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	require.False(t, includedSourceIsLocal(""), "an empty source names nothing")
}

func TestIncludeSourceIsGit(t *testing.T) {
	t.Parallel()

	// The spellings the reference page advertises for an include source.
	for _, tc := range []struct {
		source string
		git    bool
	}{
		{source: "github.com/acme/base/common/base.toml@v1.2.0", git: true},
		{source: "https://example.com/acme/base.git#main:base.toml", git: true},
		{source: "ssh://git@example.com/acme/base.git#v1:base.toml", git: true},
		{source: "base.toml", git: true},
		{source: "./base.toml", git: false},
		{source: "common/base.toml", git: false},
		{source: "/common/base.toml", git: false},
	} {
		require.Equal(t, tc.git, IncludeSourceIsGit(tc.source), "source %q", tc.source)
	}
}

func TestLoadIncludedConfigResolvesPaths(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		source string
		want   string
	}{
		{source: "./base.toml", want: "services/payment/base.toml"},
		{source: "../shared/base.toml", want: "services/shared/base.toml"},
		{source: "/common/base.toml", want: "common/base.toml"},
		{source: "common", want: "services/payment/common/dagger.toml"},
		{source: "/", want: "dagger.toml"},
		// A mistyped extension still names a file: appending dagger.toml to it
		// would report a missing directory rather than the typo.
		{source: "./dagger.tml", want: "services/payment/dagger.tml"},
		{source: `\common\base.toml`, want: "common/base.toml"},
	} {
		var read string
		src := IncludeSource{
			ConfigDir: "services/payment",
			ReadWorkspaceFile: func(_ context.Context, wsPath string) ([]byte, error) {
				read = wsPath
				return nil, nil
			},
		}
		_, err := LoadIncludedConfig(t.Context(), src, tc.source, nil)
		require.NoError(t, err, "source %q", tc.source)
		require.Equal(t, tc.want, read, "source %q", tc.source)
	}

	_, err := LoadIncludedConfig(t.Context(), IncludeSource{
		ConfigDir:         "services/payment",
		ReadWorkspaceFile: func(context.Context, string) ([]byte, error) { return nil, nil },
	}, "../../../etc/base.toml", nil)
	require.ErrorContains(t, err, "escapes the workspace root")
}

func TestLoadIncludedConfigRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	src := IncludeSource{
		ConfigDir: ".",
		ReadWorkspaceFile: func(_ context.Context, wsPath string) ([]byte, error) {
			require.Equal(t, "common/base.toml", wsPath)
			return []byte("[modules.ci]\nsource = \"github.com/acme/ci@v1\"\nsorce = \"typo\"\n"), nil
		},
	}
	_, err := LoadIncludedConfig(t.Context(), src, "./common/base.toml", nil)
	require.ErrorContains(t, err, `workspace include "./common/base.toml": common/base.toml:3:1: unknown field modules.ci.sorce`)
}

func TestAddressIncludedModulesKeepsPortsOfAModuleAnEnvStillInstalls(t *testing.T) {
	t.Parallel()

	for range 50 {
		cfg, err := workspace.ParseConfig([]byte(`[env.a.modules.web]
source = "web"

[env.b.modules.web]
source = "/abs/web"

[env.c.modules.web]
source = "/abs/web"

[ports.3000]
backendService = "web:server"
backendPort = 80
`))
		require.NoError(t, err)

		addressIncludedModules(testGitAddresser(), cfg, nil)
		require.Contains(t, cfg.Ports, "3000", "env a still installs web")
	}
}

func TestApplyIncludesGraftsSettingsWhereAnEnvInstalls(t *testing.T) {
	t.Parallel()

	const included = `[modules.api]
source = "/opt/api"

[modules.api.settings]
replicas = 3
tone = "formal"

[ports.8080]
backendService = "api:http"
backendPort = 80
`
	for _, tc := range []struct {
		name     string
		included string
		local    string
		user     *workspace.UserWorkspaceOverlay
		replicas int
	}{
		{name: "an env-only consumer gets the include's default", local: "[env.dev.modules.api]\nsource = \"./api\"\n", replicas: 3},
		{name: "a consumer base setting wins", local: "[modules.api.settings]\nreplicas = 5\n\n[env.dev.modules.api]\nsource = \"./api\"\n", replicas: 5},
		// A user-level setting applies to a module the base config has.
		{name: "a user-level base setting wins", local: "[modules.api.settings]\nregion = \"eu\"\n\n[env.dev.modules.api]\nsource = \"./api\"\n", user: &workspace.UserWorkspaceOverlay{
			Modules: map[string]workspace.EnvModuleOverlay{"api": {Settings: map[string]any{"replicas": 5}}},
		}, replicas: 5},
		{name: "a consumer env setting wins", local: "[env.dev.modules.api]\nsource = \"./api\"\n\n[env.dev.modules.api.settings]\nreplicas = 7\n", replicas: 7},
		{name: "the include's own env installs it", included: "[env.dev.modules.api]\nsource = \"./api\"\n", replicas: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := IncludeSource{
				ConfigDir: ".",
				ReadWorkspaceFile: func(context.Context, string) ([]byte, error) {
					return []byte(included + "\n" + tc.included), nil
				},
			}
			local, err := workspace.ParseConfig([]byte("[[include]]\nsource = \"./base.toml\"\n\n" + tc.local))
			require.NoError(t, err)
			keys, err := workspace.ExplicitConfigKeys([]byte(tc.local))
			require.NoError(t, err)

			merged, err := ApplyIncludes(t.Context(), src, local, keys, tc.user)
			require.NoError(t, err)
			require.Empty(t, merged.Modules["api"].Source, "outside dev nothing installs it")
			require.Contains(t, merged.Ports, "8080")
			merged, err = workspace.ApplyUserOverlay(merged, tc.user)
			require.NoError(t, err)
			dev, err := workspace.ApplyEnvOverlay(merged, "dev")
			require.NoError(t, err)
			require.Equal(t, "./api", dev.Modules["api"].Source)
			require.EqualValues(t, tc.replicas, dev.Modules["api"].Settings["replicas"])
			require.Equal(t, "formal", dev.Modules["api"].Settings["tone"], "a key nothing else sets is inherited")
		})
	}
}

func TestApplyIncludesCountsAUserLevelSource(t *testing.T) {
	t.Parallel()

	src := IncludeSource{
		ConfigDir: ".",
		ReadWorkspaceFile: func(context.Context, string) ([]byte, error) {
			return []byte(`[modules.api]
source = "/opt/api"

[modules.api.settings]
replicas = 3

[env.prod.modules.web.settings]
tier = "gold"
`), nil
		},
	}
	local, err := workspace.ParseConfig([]byte("[[include]]\nsource = \"./base.toml\"\n"))
	require.NoError(t, err)
	user := &workspace.UserWorkspaceOverlay{
		Modules: map[string]workspace.EnvModuleOverlay{
			"api": {Source: "./api"},
			"web": {Source: "./web"},
		},
	}

	merged, err := ApplyIncludes(t.Context(), src, local, map[string]bool{}, user)
	require.NoError(t, err)
	effective, err := workspace.ApplyUserOverlay(merged, user)
	require.NoError(t, err)
	require.Equal(t, "./api", effective.Modules["api"].Source)
	require.EqualValues(t, 3, effective.Modules["api"].Settings["replicas"])
	require.Equal(t, "gold", effective.Env["prod"].Modules["web"].Settings["tier"])
}
