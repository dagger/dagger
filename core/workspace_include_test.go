package core

import (
	"context"

	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestDropLocalIncludedModules(t *testing.T) {
	t.Parallel()

	cfg, err := workspace.ParseConfig([]byte(`[modules.ci]
source = "modules/ci"

[modules.pinned-local]
source = "./ci"
pin = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

[modules.dotted-local]
source = "modules/foo.bar"

[modules.toolchain]
source = "github.com/acme/toolchain@v1"

[modules.scheme]
source = "https://example.com/acme/mod.git"

[env.ci.modules.ci.settings]
greeting = "hello"

[env.ci.modules.toolchain.settings]
version = "1.24"

[env.ci.modules.extra-local]
source = "modules/ci"

[ports.3000]
backendService = "ci:web"
backendPort = 8080

[ports.4000]
backendService = "toolchain:api"
backendPort = 9090
`))
	require.NoError(t, err)

	dropped := dropLocalIncludedModules(cfg)

	require.Equal(t, []string{
		"ci",
		"dotted-local",
		"env.ci.modules.extra-local",
		"pinned-local",
	}, dropped)

	require.NotContains(t, cfg.Modules, "ci")
	require.NotContains(t, cfg.Modules, "pinned-local", "a pin must not launder a local source")
	require.NotContains(t, cfg.Modules, "dotted-local", "an ambiguous ref that is a directory in the included tree is local")
	require.Contains(t, cfg.Modules, "toolchain")
	require.Contains(t, cfg.Modules, "scheme")

	// The dropped module's env overlay goes with it; the surviving module's
	// overlay stays.
	require.NotContains(t, cfg.Env["ci"].Modules, "ci")
	require.NotContains(t, cfg.Env["ci"].Modules, "extra-local")
	require.Contains(t, cfg.Env["ci"].Modules, "toolchain")

	// Ports forwarding to a dropped module go too; the others stay.
	require.NotContains(t, cfg.Ports, "3000")
	require.Contains(t, cfg.Ports, "4000")
}

func TestDropLocalIncludedModulesKeepsAVanityRemote(t *testing.T) {
	t.Parallel()

	// A schemeless remote whose host carries the dot: kept, and without asking
	// the network — classification is syntactic, so it cannot depend on
	// reachability.
	cfg, err := workspace.ParseConfig([]byte(`[modules.toolchain]
source = "vanity.example.com/acme/toolchain"
`))
	require.NoError(t, err)

	dropped := dropLocalIncludedModules(cfg)
	require.Empty(t, dropped)
	require.Contains(t, cfg.Modules, "toolchain")
}

func TestDropLocalIncludedModulesDropsBareShortNameSource(t *testing.T) {
	t.Parallel()

	// A bare short name. Nothing named php exists in the included tree, so
	// this pins the classification rather than the stat.
	cfg, err := workspace.ParseConfig([]byte(`[modules.php]
source = "php"
`))
	require.NoError(t, err)

	dropped := dropLocalIncludedModules(cfg)
	require.Equal(t, []string{"php"}, dropped)
	require.NotContains(t, cfg.Modules, "php")
}

func TestDropLocalIncludedModulesCascadesToPortsOfNonCanonicalNames(t *testing.T) {
	t.Parallel()

	// backendService names the module CLI-cased, which is not how the config
	// spells the key it was declared under.
	cfg, err := workspace.ParseConfig([]byte(`[modules.MyTool]
source = "modules/my-tool"

[ports.3000]
backendService = "my-tool:web"
backendPort = 8080
`))
	require.NoError(t, err)

	dropped := dropLocalIncludedModules(cfg)
	require.Equal(t, []string{"MyTool"}, dropped)
	require.NotContains(t, cfg.Ports, "3000")
}

func TestDropLocalIncludedModulesCascadesToPortsOfEnvOnlyInstalls(t *testing.T) {
	t.Parallel()

	// The module is installed by an env overlay only, so dropping that overlay
	// leaves nothing for the port to forward to.
	cfg, err := workspace.ParseConfig([]byte(`[env.ci.modules.local-ci]
source = "modules/ci"

[ports.3000]
backendService = "local-ci:web"
backendPort = 8080

[ports.4000]
backendService = "toolchain:api"
backendPort = 9090
`))
	require.NoError(t, err)

	dropped := dropLocalIncludedModules(cfg)
	require.Equal(t, []string{"env.ci.modules.local-ci"}, dropped)
	require.NotContains(t, cfg.Ports, "3000")
	require.Contains(t, cfg.Ports, "4000")
}

func TestDropLocalIncludedModulesDropsADottedPath(t *testing.T) {
	t.Parallel()

	// The dot is in a path segment rather than the host, so this is a path.
	cfg, err := workspace.ParseConfig([]byte(`[modules.nested]
source = "modules/foo.bar"
`))
	require.NoError(t, err)

	dropped := dropLocalIncludedModules(cfg)
	require.Equal(t, []string{"nested"}, dropped)
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
		_, err := LoadIncludedConfig(t.Context(), src, tc.source)
		require.NoError(t, err, "source %q", tc.source)
		require.Equal(t, tc.want, read, "source %q", tc.source)
	}

	_, err := LoadIncludedConfig(t.Context(), IncludeSource{
		ConfigDir:         "services/payment",
		ReadWorkspaceFile: func(context.Context, string) ([]byte, error) { return nil, nil },
	}, "../../../etc/base.toml")
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
	_, err := LoadIncludedConfig(t.Context(), src, "./common/base.toml")
	require.ErrorContains(t, err, `workspace include "./common/base.toml": common/base.toml:3:1: unknown field modules.ci.sorce`)
}
