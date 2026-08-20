package workspace

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSDKRoundTripFresh(t *testing.T) {
	cfg := &Config{
		Modules: map[string]ModuleEntry{
			"mymod": {Source: ".dagger/modules/mymod"},
			"go-sdk": {
				Source:   "github.com/dagger/go-sdk",
				Pin:      "abcdef",
				Settings: map[string]any{"strict-build": true},
			},
		},
		SDKs: map[string]SDKEntry{
			"go": {
				Module: "go-sdk",
				Claimed: SDKClaims{
					Modules: []string{".dagger/modules/mymod", "libs/shared"},
					Clients: []SDKManagedClient{
						{
							Path:   "./lib/cli",
							Module: ".dagger/modules/cli",
							Options: map[string]string{
								"package-name": "@my-app/dagger-cli-client",
							},
						},
					},
				},
			},
		},
	}

	raw := SerializeConfig(cfg)
	parsed, err := ParseConfig(raw)
	require.NoError(t, err)
	require.Equal(t, cfg.Modules["go-sdk"], parsed.Modules["go-sdk"])
	require.Equal(t, cfg.SDKs["go"], parsed.SDKs["go"])

	updated, err := UpdateConfigBytes(raw, parsed)
	require.NoError(t, err)
	require.Contains(t, string(updated), "[sdks.go]")
	require.Contains(t, string(updated), "[sdks.go.claimed]")
	require.Contains(t, string(updated), `modules = [`)
	require.Contains(t, string(updated), `{ path = "./lib/cli", module = ".dagger/modules/cli", package-name = "@my-app/dagger-cli-client" }`)
}

func TestLegacySDKConfigMigratesOnWriteAndPreservesOtherComments(t *testing.T) {
	original := []byte(`# Top-level comment
ignore = ["*.bak"]

# Modules section
[modules.mymod]
source = ".dagger/modules/mymod"  # inline comment

[modules.go-sdk]
source = "github.com/old/go-sdk"

[[modules.go-sdk.as-sdk.modules]]
path = ".dagger/modules/mymod"

[[modules.go-sdk.as-sdk.clients]]
path = "lib/client"
module = "github.com/acme/api@main"
pin = "abc123"

[[modules.go-sdk.as-sdk.clients]]
path = "lib/ssh-client"
module = "git@github.com:acme/api"
pin = "def456"
`)

	cfg, err := ParseConfig(original)
	require.NoError(t, err)
	require.Equal(t, SDKEntry{
		Module: "go-sdk",
		Claimed: SDKClaims{
			Modules: []string{".dagger/modules/mymod"},
			Clients: []SDKManagedClient{
				{
					Path:   "lib/client",
					Module: "github.com/acme/api@abc123",
				},
				{
					Path:   "lib/ssh-client",
					Module: "git@github.com:acme/api@def456",
				},
			},
		},
	}, cfg.SDKs["go"])

	out, err := UpdateConfigBytes(original, cfg)
	require.NoError(t, err)

	s := string(out)
	for _, want := range []string{
		"# Top-level comment",
		"# Modules section",
		"# inline comment",
		"[modules.go-sdk]",
		`source = "github.com/old/go-sdk"`,
		"[sdks.go]",
		"[sdks.go.claimed]",
		`".dagger/modules/mymod"`,
		`module = "github.com/acme/api@abc123"`,
		`module = "git@github.com:acme/api@def456"`,
	} {
		require.True(t, strings.Contains(s, want), "missing %q in:\n%s", want, s)
	}
}

func TestDottedSDKConfigCanonicalizesOnWrite(t *testing.T) {
	original := []byte(`sdks.go.module = "go-sdk"
sdks.go.claimed.modules = ["modules/api"]
sdks.go.claimed.clients = [{ path = "clients/api", module = "modules/api" }]

# Keep this comment
[modules.go-sdk]
source = "github.com/dagger/go-sdk"
`)

	cfg, err := ParseConfig(original)
	require.NoError(t, err)
	out, err := UpdateConfigBytes(original, cfg)
	require.NoError(t, err)

	roundTripped, err := ParseConfig(out)
	require.NoError(t, err)
	require.Equal(t, cfg, roundTripped)
	require.Contains(t, string(out), "# Keep this comment")
	require.Contains(t, string(out), "[sdks.go.claimed]")
}
