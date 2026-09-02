package core

import (
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

const testIncludeCommit = "0123456789abcdef0123456789abcdef01234567"

// testGitAddresser stands in for an include resolved from
// https://github.com/acme/base, whose config sits at the repository root.
func testGitAddresser() includedModuleAddresser {
	return gitIncludedModuleAddresser("https://github.com/acme/base", testIncludeCommit, ".")
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

	dropped := addressIncludedModules(testGitAddresser(), cfg)

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

	address := gitIncludedModuleAddresser("https://github.com/acme/base", testIncludeCommit, "dagger/common")
	require.Empty(t, addressIncludedModules(address, cfg))

	require.Equal(t, "https://github.com/acme/base/dagger/common/ci@"+testIncludeCommit, cfg.Modules["ci"].Source)
	require.Equal(t, "https://github.com/acme/base/dagger/tools/lint@"+testIncludeCommit, cfg.Modules["sibling"].Source)
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

	require.Empty(t, addressIncludedModules(testGitAddresser(), cfg))
	require.Equal(t, "vanity.example.com/acme/toolchain", cfg.Modules["toolchain"].Source)
}

func TestAddressIncludedModulesDropsABuiltinSDKInstall(t *testing.T) {
	t.Parallel()

	// The shape `dagger setup` writes for a migrated SDK: a runtime name the
	// engine resolves in-process, not a path. It is also the reasoning that
	// lets setupResolveMigratedSDKs keep writing back what configRead
	// returned: it only rewrites entries of this shape, and an entry of this
	// shape is never inherited from an include.
	cfg, err := workspace.ParseConfig([]byte(`[modules.php]
source = "php"

[modules.php.as-sdk]
name = "php"

[modules.go]
source = "go@v1.2.3"

[modules.go.as-sdk]
name = "go"
`))
	require.NoError(t, err)

	dropped := addressIncludedModules(testGitAddresser(), cfg)
	require.Len(t, dropped, 2)
	require.Contains(t, dropped[0], "built-in SDK runtime")
	require.NotContains(t, cfg.Modules, "php")
	require.NotContains(t, cfg.Modules, "go")
}

func TestAddressIncludedModulesKeepsADirectoryNamedAfterAnSDK(t *testing.T) {
	t.Parallel()

	// Without as-sdk the entry is an ordinary module that happens to live in a
	// directory called "go", and the loader reads it that way too. Dropping it
	// on the name alone would lose a module nobody said was a runtime.
	cfg, err := workspace.ParseConfig([]byte(`[modules.compiler]
source = "go"
`))
	require.NoError(t, err)

	require.Empty(t, addressIncludedModules(testGitAddresser(), cfg))
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

	require.Len(t, addressIncludedModules(testGitAddresser(), cfg), 2)
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

	require.Len(t, addressIncludedModules(testGitAddresser(), cfg), 1)
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

	require.Len(t, addressIncludedModules(testGitAddresser(), cfg), 1)
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

	require.Empty(t, addressIncludedModules(testGitAddresser(), cfg))
	require.Equal(t, "https://github.com/acme/base/ci@"+testIncludeCommit, cfg.Modules["ci"].Source)
	require.Empty(t, cfg.Env["prod"].Modules["ci"].Pin, "the pin described the source that was replaced")
}

func TestAddressIncludedModulesDropsTheIncludedRootItself(t *testing.T) {
	t.Parallel()

	// The tree the config came from is a workspace, not a module.
	cfg, err := workspace.ParseConfig([]byte(`[modules.self]
source = "."
`))
	require.NoError(t, err)

	require.Equal(t, []string{"self (no address outside the config it came from)"}, addressIncludedModules(testGitAddresser(), cfg))
}

func TestAddressIncludedModulesDropsAnAbsolutePath(t *testing.T) {
	t.Parallel()

	// An absolute path addresses the machine the config was authored on.
	cfg, err := workspace.ParseConfig([]byte(`[modules.abs]
source = "/opt/ci"
`))
	require.NoError(t, err)

	require.Equal(t, []string{"abs (no address outside the config it came from)"}, addressIncludedModules(testGitAddresser(), cfg))
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

	require.Equal(t, []string{"MyTool (no address outside the config it came from)"}, addressIncludedModules(testGitAddresser(), cfg))
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

	require.Equal(t, []string{"env.ci.modules.local-ci (no address outside the config it came from)"}, addressIncludedModules(testGitAddresser(), cfg))
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
		{source: "..", ok: false},
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

	// The classifier is workspace.IsLocalRef, the same one the module loader
	// reaches through ResolveModuleEntrySource. These cases pin the agreement,
	// including the dotted path that only reads as local because the dot is not
	// in the host segment.
	for _, tc := range []struct {
		source string
		local  bool
	}{
		{source: "", local: false},
		{source: "modules/ci", local: true},
		{source: "./ci", local: true},
		{source: "../shared/ci", local: true},
		{source: "common/.dagger/mymod", local: true},
		{source: "github.com/acme/toolchain@v1", local: false},
		{source: "https://example.com/acme/mod.git", local: false},
		{source: "vanity.example.com/acme/toolchain", local: false},
	} {
		require.Equal(t, tc.local, includedSourceIsLocal(tc.source), "source %q", tc.source)
	}
}

func TestIncludedSourceIsLocalIgnoresThePin(t *testing.T) {
	t.Parallel()

	// A pin makes the shared classifier read any ref as git, so the sanitizer
	// deliberately never passes one: otherwise `source = "./ci", pin = "…"`
	// would survive and then resolve against the consuming workspace.
	require.True(t, includedSourceIsLocal("./ci"))
	require.False(t, workspace.IsLocalRef("./ci", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
}

func TestIncludeSourceIsGit(t *testing.T) {
	t.Parallel()

	// The rule is workspace.IsLocalRef, the classifier every other ref in a
	// workspace config is read with. These cases pin the spellings the
	// reference page advertises, in both directions.
	for _, tc := range []struct {
		source string
		git    bool
	}{
		{source: "github.com/acme/base@v1.2.0", git: true},
		{source: "github.com/acme/base/common/base.toml@v1.2.0", git: true},
		{source: "github.com/acme/base", git: true},
		{source: "https://example.com/acme/base.git#main:base.toml", git: true},
		{source: "ssh://git@example.com/acme/base.git#v1:base.toml", git: true},
		{source: "git@github.com:acme/base.git", git: true},
		{source: "vanity.example.com/acme/base", git: true},
		{source: "common/base.toml", git: false},
		{source: "dagger/base.toml", git: false},
		{source: "./base.toml", git: false},
		{source: "../shared/base.toml", git: false},
		{source: "/common/base.toml", git: false},
		{source: "common", git: false},
	} {
		require.Equal(t, tc.git, IncludeSourceIsGit(tc.source), "source %q", tc.source)
	}
}

func TestResolveIncludePath(t *testing.T) {
	t.Parallel()

	src := IncludeSource{ConfigDir: "services/payment"}

	for _, tc := range []struct {
		source string
		want   string
	}{
		{source: "base.toml", want: "services/payment/base.toml"},
		{source: "./base.toml", want: "services/payment/base.toml"},
		{source: "../shared/base.toml", want: "services/shared/base.toml"},
		{source: "/common/base.toml", want: "common/base.toml"},
		// A path typed on Windows: filepath.ToSlash is a no-op on the engine
		// reading it, so the separators are normalized explicitly.
		{source: `common\base.toml`, want: "services/payment/common/base.toml"},
		{source: `\common\base.toml`, want: "common/base.toml"},
	} {
		got, err := src.resolveIncludePath(tc.source)
		require.NoError(t, err, "source %q", tc.source)
		require.Equal(t, tc.want, got, "source %q", tc.source)
	}

	_, err := src.resolveIncludePath("../../../etc/base.toml")
	require.ErrorContains(t, err, "escapes the workspace root")
}

func TestIncludeConfigPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		source string
		want   string
	}{
		{source: ".", want: "dagger.toml"},
		{source: "common", want: "common/dagger.toml"},
		{source: "common/base.toml", want: "common/base.toml"},
		// A mistyped extension still names a file: appending dagger.toml to it
		// would report a missing directory rather than the typo.
		{source: "dagger.tml", want: "dagger.tml"},
	} {
		require.Equal(t, tc.want, includeConfigPath(tc.source), "source %q", tc.source)
	}
}
