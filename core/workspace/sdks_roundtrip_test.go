package workspace

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSDKScopesRoundTrip(t *testing.T) {
	cfg := &Config{
		Modules: map[string]ModuleEntry{
			"go-sdk": {Source: "github.com/dagger/go-sdk"},
		},
		SDKs: map[string]SDKEntry{
			"go": {
				Module: "go-sdk",
				Scopes: map[string]SDKScope{
					".": {
						IsModule: true,
						Name:     "payments",
						Clients:  []string{"database", "github.com/acme/cache"},
						Settings: map[string]any{"legacy-module-compat": false},
					},
					"apps/web": {
						Clients: []string{"github.com/acme/payments"},
					},
				},
			},
		},
	}

	raw := SerializeConfig(cfg)
	parsed, err := ParseConfig(raw)
	require.NoError(t, err)
	require.Equal(t, cfg, parsed)

	text := string(raw)
	require.Contains(t, text, "[sdks.go.scopes.\".\"]")
	require.Contains(t, text, "[sdks.go.scopes.\"apps/web\"]")
	require.Contains(t, text, "is-module = true")
	require.Contains(t, text, `name = "payments"`)
	require.Contains(t, text, `  "github.com/acme/cache",`)
	require.Contains(t, text, "[sdks.go.scopes.\".\".settings]")
	require.Contains(t, text, "legacy-module-compat = false")
}
