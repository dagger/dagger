package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/core/workspace"
	"github.com/stretchr/testify/require"
)

func TestParseWorkspaceRemoteRef(t *testing.T) {
	t.Parallel()

	t.Run("supports address fragment ref", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.CloneRef)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, ".", ref.WorkspaceSubdir)
	})

	t.Run("supports address fragment ref and subdir", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger#main:toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "https://github.com/dagger/dagger", ref.CloneRef)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, "toolchains/changelog", ref.WorkspaceSubdir)
	})

	t.Run("supports legacy at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "github.com/dagger/dagger/toolchains/changelog@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, "toolchains/changelog", ref.WorkspaceSubdir)
	})

	t.Run("preserves legacy https at-ref syntax", func(t *testing.T) {
		t.Parallel()

		ref, err := ParseWorkspaceRemoteRef(context.Background(), "https://github.com/dagger/dagger@main")
		require.NoError(t, err)
		require.Equal(t, "main", ref.Version)
		require.Equal(t, ".", ref.WorkspaceSubdir)
	})
}

func TestNormalizeWorkspaceRemoteSubdir(t *testing.T) {
	t.Parallel()

	t.Run("empty becomes dot", func(t *testing.T) {
		t.Parallel()
		got, err := NormalizeWorkspaceRemoteSubdir("")
		require.NoError(t, err)
		require.Equal(t, ".", got)
	})

	t.Run("absolute gets normalized to relative", func(t *testing.T) {
		t.Parallel()
		got, err := NormalizeWorkspaceRemoteSubdir("/toolchains/changelog")
		require.NoError(t, err)
		require.Equal(t, "toolchains/changelog", got)
	})

	t.Run("rejects escaping paths", func(t *testing.T) {
		t.Parallel()
		_, err := NormalizeWorkspaceRemoteSubdir("../outside")
		require.ErrorContains(t, err, "outside repository")
	})
}

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

	// The shape `dagger setup` writes for a migrated SDK. Nothing named php
	// exists in the included tree, so this pins the classification rather than
	// the stat — and with it the reasoning that lets setupResolveMigratedSDKs
	// keep writing back what configRead returned: it only rewrites entries of
	// this shape, and an entry of this shape is never inherited from an include.
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
