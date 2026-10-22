package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/config"
	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/server"
)

// envRemoteCacheURL sets the remote cache service's URL. It wins over the
// engine config's remoteCache.url.
const envRemoteCacheURL = "_EXPERIMENTAL_DAGGER_REMOTE_CACHE_URL"

// envRemoteCacheLookups set to 0 stops the engine asking the service for the
// calls its cache misses. envRemoteCacheLookupWait sets how long an
// evaluation waits for a merge the service's answer names, as a duration; 0
// asks without waiting.
const (
	envRemoteCacheLookups    = "_EXPERIMENTAL_DAGGER_REMOTE_CACHE_LOOKUPS"
	envRemoteCacheLookupWait = "_EXPERIMENTAL_DAGGER_REMOTE_CACHE_LOOKUP_WAIT"
)

// envRemoteCacheEphemeral, when true as strconv.ParseBool reads it, tells the
// service that the engine's state does not outlive this run of it, as for a
// nested engine a check starts as a service, so that the service can forget
// its cache once it disconnects.
const envRemoteCacheEphemeral = "_EXPERIMENTAL_DAGGER_REMOTE_CACHE_EPHEMERAL"

// remoteCacheSettings returns the connection to the remote cache service, and
// whether it is on: when the service URL is set, by the environment or the
// engine config, and DAGGER_CLOUD_TOKEN holds an engine token. A token alone
// does nothing, since clients forward it into every engine they provision.
// DAGGER_CLOUD_TOKEN=oidc fetches an OIDC token, which is not an engine
// token, so the connection stays off.
func remoteCacheSettings(cfg config.Config) (remotecache.Config, bool) {
	url := os.Getenv(envRemoteCacheURL)
	if url == "" && cfg.RemoteCache != nil {
		url = cfg.RemoteCache.URL
	}
	if url == "" {
		return remotecache.Config{}, false
	}
	token := os.Getenv("DAGGER_CLOUD_TOKEN")
	switch token {
	case "":
		slog.Warn("remote cache off: DAGGER_CLOUD_TOKEN holds no engine token")
		return remotecache.Config{}, false
	case "oidc":
		slog.Warn("remote cache off: an OIDC token is not an engine token")
		return remotecache.Config{}, false
	}
	lookupWait := remotecache.DefaultLookupWait
	if v := os.Getenv(envRemoteCacheLookupWait); v != "" {
		wait, err := time.ParseDuration(v)
		if err != nil || wait < 0 {
			slog.Warn("remote cache: invalid lookup wait; using the default", "value", v, "default", lookupWait)
		} else {
			lookupWait = wait
		}
	}
	return remotecache.Config{
		URL:           url,
		Token:         token,
		EngineVersion: engine.Version,
		EngineName:    engineName,
		Lookups:       os.Getenv(envRemoteCacheLookups) != "0",
		LookupWait:    lookupWait,
		Ephemeral:     remoteCacheEphemeral(),
	}, true
}

// remoteCacheEphemeral reports whether envRemoteCacheEphemeral is true. A
// value strconv.ParseBool does not read leaves the engine a lasting one.
func remoteCacheEphemeral() bool {
	value := os.Getenv(envRemoteCacheEphemeral)
	if value == "" {
		return false
	}
	ephemeral, err := strconv.ParseBool(value)
	if err != nil {
		slog.Warn("ignoring invalid "+envRemoteCacheEphemeral, "value", value, "error", err)
		return false
	}
	return ephemeral
}

// newRemoteCacheIntegration returns the server's remote cache integration, or
// nil when it is off. When it is on, the engine exports its cache events too:
// the service learns the engine's cache from them.
func newRemoteCacheIntegration(cfg *config.Config) *server.RemoteCacheIntegrationConfig {
	settings, ok := remoteCacheSettings(*cfg)
	if !ok {
		return nil
	}
	cfg.Telemetry.EngineEvents = true
	return &server.RemoteCacheIntegrationConfig{Run: func(ctx context.Context, adapter *server.RemoteCacheAdapter) error {
		// The engine's logger is set once its config is loaded, before Run.
		settings.Logger = slog.Default()
		return remotecache.Run(ctx, adapter, settings)
	}}
}
