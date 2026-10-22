package main

import (
	"testing"
	"time"

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
			t.Setenv(envRemoteCacheEphemeral, "")
			settings, on := remoteCacheSettings(tc.cfg)
			if tc.url == "" {
				require.False(t, on)
				require.Zero(t, settings)
				return
			}
			require.True(t, on)
			require.Equal(t, remotecache.Config{URL: tc.url, Token: tc.token, EngineVersion: engine.Version, EngineName: engineName, Lookups: true, LookupWait: remotecache.DefaultLookupWait}, settings)
		})
	}
}

// Lookups are on by default, with the default wait; the environment turns
// them off, or sets the wait, 0 asking without waiting. An invalid wait keeps
// the default.
func TestRemoteCacheLookupSettings(t *testing.T) {
	t.Setenv(envRemoteCacheURL, "https://env.example")
	t.Setenv("DAGGER_CLOUD_TOKEN", "engine-token")
	for _, tc := range []struct {
		name    string
		lookups string
		wait    string
		on      bool
		want    time.Duration
	}{
		{name: "default", on: true, want: 10 * time.Second},
		{name: "off", lookups: "0", on: false, want: 10 * time.Second},
		{name: "a wait", wait: "3s", on: true, want: 3 * time.Second},
		{name: "no wait", wait: "0", on: true, want: 0},
		{name: "an invalid wait", wait: "soon", on: true, want: 10 * time.Second},
		{name: "a negative wait", wait: "-1s", on: true, want: 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envRemoteCacheLookups, tc.lookups)
			t.Setenv(envRemoteCacheLookupWait, tc.wait)
			settings, on := remoteCacheSettings(config.Config{})
			require.True(t, on)
			require.Equal(t, tc.on, settings.Lookups)
			require.Equal(t, tc.want, settings.LookupWait)
		})
	}
}

// An engine whose state does not outlive its run says so when
// _EXPERIMENTAL_DAGGER_REMOTE_CACHE_EPHEMERAL is true, as strconv.ParseBool
// reads it; any other value leaves it a lasting engine.
func TestRemoteCacheEphemeral(t *testing.T) {
	for _, tc := range []struct {
		env       string
		ephemeral bool
	}{
		{env: "", ephemeral: false},
		{env: "1", ephemeral: true},
		{env: "true", ephemeral: true},
		{env: "0", ephemeral: false},
		{env: "false", ephemeral: false},
		{env: "yes please", ephemeral: false},
	} {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv(envRemoteCacheURL, "https://env.example")
			t.Setenv("DAGGER_CLOUD_TOKEN", "engine-token")
			t.Setenv(envRemoteCacheEphemeral, tc.env)
			settings, on := remoteCacheSettings(config.Config{})
			require.True(t, on)
			require.Equal(t, tc.ephemeral, settings.Ephemeral)
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
