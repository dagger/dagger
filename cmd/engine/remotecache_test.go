package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/remotecache"
)

// The remote cache is on when its URL is set and DAGGER_CLOUD_TOKEN holds an
// engine token. The environment variable's URL wins over the engine config's.
func TestRemoteCacheSettings(t *testing.T) {
	configured := config.Config{RemoteCache: &config.RemoteCacheConfig{URL: "https://config.example"}}
	for _, tc := range []struct {
		name  string
		env   string
		token string
		cfg   config.Config
		url   string
	}{
		{name: "from the engine config", token: "engine-token", cfg: configured, url: "https://config.example"},
		{name: "from the environment", env: "https://env.example", token: "engine-token", url: "https://env.example"},
		{name: "the environment wins", env: "https://env.example", token: "engine-token", cfg: configured, url: "https://env.example"},
		{name: "a token alone does nothing", token: "engine-token"},
		{name: "a URL without a token", env: "https://env.example", cfg: configured},
		{name: "an OIDC token is not an engine token", env: "https://env.example", token: "oidc", cfg: configured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envRemoteCacheURL, tc.env)
			t.Setenv("DAGGER_CLOUD_TOKEN", tc.token)
			settings, on := remoteCacheSettings(tc.cfg)
			if tc.url == "" {
				require.False(t, on)
				require.Zero(t, settings)
				return
			}
			require.True(t, on)
			require.Equal(t, remotecache.Config{URL: tc.url, Token: tc.token, EngineVersion: engine.Version, EngineName: engineName}, settings)
		})
	}
}

// Turning the remote cache on turns the engine's cache events on too; when
// it stays off, the events keep their own switch.
func TestRemoteCacheTurnsOnEngineEvents(t *testing.T) {
	t.Setenv(envEngineEvents, "")
	t.Setenv("DAGGER_CLOUD_URL", "http://127.0.0.1:1")
	t.Setenv(envRemoteCacheURL, "https://env.example")

	t.Setenv("DAGGER_CLOUD_TOKEN", "oidc")
	var cfg config.Config
	require.Nil(t, newRemoteCacheIntegration(&cfg))
	require.False(t, cfg.Telemetry.EngineEvents)

	t.Setenv("DAGGER_CLOUD_TOKEN", "engine-token")
	integration := newRemoteCacheIntegration(&cfg)
	require.NotNil(t, integration)
	require.NotNil(t, integration.Run)
	require.True(t, cfg.Telemetry.EngineEvents)
	export := newEngineEventExport(t.Context(), resource.Empty(), cfg.Telemetry)
	require.True(t, export.Enabled())
	require.NoError(t, export.Shutdown(t.Context()))
}
