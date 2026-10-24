package drivers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
	telemetry "github.com/dagger/otel-go"
	"github.com/gofrs/flock"
	"go.opentelemetry.io/otel"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/replacefile"
)

const connectionCacheTTL = 10 * time.Second

// A cached engine should already be running. Bound the attempt so a stopped or
// replaced engine falls back to provisioning instead of the normal startup wait.
const cachedConnectionTimeout = 2 * time.Second

// Connect resolves an engine and calls connect to establish and validate the
// connection. Successful resolutions are shared across processes for ten seconds
// after their last use. Each call gets a new connector; callers still establish
// their own sessions.
//
// connect must release any resources it creates on failure. On success the
// caller owns those resources and the returned connector.
func Connect(ctx context.Context, target *url.URL, opts *DriverOpts, connect func(context.Context, Connector) error) (Driver, Connector, error) {
	return connectCached(ctx, filepath.Join(xdg.CacheHome, "dagger", "connections"), target, opts, connect, time.Now)
}

type cachedConnection struct {
	LastUsed time.Time `json:"lastUsed"`
	// Driver is an index in the scheme's registered drivers, so a cache hit
	// can skip runtime discovery while preserving backend and image loading.
	Driver    int               `json:"driver"`
	Container *cachedContainer  `json:"container,omitempty"`
	Cloud     *cloud.EngineSpec `json:"cloud,omitempty"`
}

type cachedContainer struct {
	Host   string     `json:"host"`
	Values url.Values `json:"values,omitempty"`
}

func connectCached(ctx context.Context, dir string, target *url.URL, opts *DriverOpts, connect func(context.Context, Connector) error, now func() time.Time) (Driver, Connector, error) {
	cacheable := false
	for _, driver := range drivers[target.Scheme] {
		switch driver.(type) {
		case *imageDriver, *containerDriver, *daggerCloudDriver:
			cacheable = true
		}
	}
	if !cacheable {
		return connectFresh(ctx, target, opts, connect)
	}
	key, err := connectionCacheKey(target, opts)
	if err != nil {
		return connectFresh(ctx, target, opts, connect)
	}
	path := filepath.Join(dir, key+".json")
	// Cache failures must never prevent a connection. Only wait briefly for
	// another invocation; it may be pulling an image or starting a Cloud engine.
	lock, err := lockConnectionCache(ctx, dir, path)
	if err != nil {
		slog.Debug("engine connection cache unavailable", "error", err)
		return connectFresh(ctx, target, opts, connect)
	}
	defer lock.Close()

	var entry cachedConnection
	if data, err := os.ReadFile(path); err == nil && json.Unmarshal(data, &entry) == nil {
		age := now().Sub(entry.LastUsed)
		if age >= 0 && age < connectionCacheTTL {
			if driver, connector := entry.restore(target.Scheme); connector != nil {
				attemptCtx, cancel := context.WithTimeout(ctx, cachedConnectionTimeout)
				err := connect(attemptCtx, connector)
				cancel()
				if err == nil {
					entry.LastUsed = now()
					writeConnectionCache(path, entry)
					slog.Debug("reused cached engine endpoint")
					return driver, connector, nil
				}
				closeConnector(connector)
				slog.Debug("cached engine connection failed; provisioning again", "error", err)
			}
		}
	}
	// Remove expired, invalid, or failed entries, including when provisioning
	// subsequently fails. The lock prevents invalidating a concurrent refresh.
	_ = os.Remove(path)
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	driver, connector, err := connectFresh(ctx, target, opts, connect)
	if err != nil {
		return nil, nil, err
	}
	if entry, ok := cacheConnection(target.Scheme, driver, connector); ok {
		entry.LastUsed = now()
		writeConnectionCache(path, entry)
	}
	return driver, connector, nil
}

func connectFresh(ctx context.Context, target *url.URL, opts *DriverOpts, connect func(context.Context, Connector) error) (Driver, Connector, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	provisionCtx, span := otel.Tracer(InstrumentationLibrary).Start(ctx, "starting engine")
	driver, err := GetDriver(provisionCtx, target.Scheme)
	if err != nil {
		telemetry.EndWithCause(span, &err)
		return nil, nil, err
	}
	connector, err := driver.Provision(provisionCtx, target, opts)
	telemetry.EndWithCause(span, &err)
	if err != nil {
		return nil, nil, err
	}
	if err := connect(ctx, connector); err != nil {
		closeConnector(connector)
		return nil, nil, err
	}
	return driver, connector, nil
}

func closeConnector(connector Connector) {
	if closer, ok := connector.(io.Closer); ok {
		_ = closer.Close()
	}
}

func lockConnectionCache(ctx context.Context, dir, path string) (*flock.Flock, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lock := flock.New(path + ".lock")
	ctx, cancel := context.WithTimeout(ctx, cachedConnectionTimeout)
	defer cancel()
	locked, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil || !locked {
		_ = lock.Close()
		return nil, fmt.Errorf("lock engine connection cache: %w", err)
	}
	return lock, nil
}

func writeConnectionCache(path string, entry cachedConnection) {
	// Cloud entries contain a private TLS key. CreateTemp uses mode 0600,
	// and an atomic replacement keeps readers from seeing a partial record.
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".connection-*")
	if err != nil {
		return
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr == nil && closeErr == nil {
		if err := replacefile.Rename(f.Name(), path); err != nil {
			slog.Debug("failed to save engine connection cache", "error", err)
		}
	}
}

func cacheConnection(scheme string, driver Driver, connector Connector) (cachedConnection, bool) {
	entry := cachedConnection{Driver: -1}
	for i, candidate := range drivers[scheme] {
		if candidate == driver {
			entry.Driver = i
			break
		}
	}
	if entry.Driver < 0 {
		return entry, false
	}
	switch connector := connector.(type) {
	case containerConnector:
		entry.Container = &cachedContainer{Host: connector.host, Values: connector.values}
	case *DaggerCloudConnector:
		spec := connector.EngineSpec
		// Request metadata belongs to the invocation, not the endpoint.
		spec.EngineRequest = cloud.EngineRequest{}
		entry.Cloud = &spec
	default:
		// Direct dial drivers already have an endpoint and no provisioning
		// work to save. Do not persist their credentials or connection state.
		return entry, false
	}
	return entry, true
}

func (entry cachedConnection) restore(scheme string) (Driver, Connector) {
	candidates := drivers[scheme]
	if entry.Driver < 0 || entry.Driver >= len(candidates) {
		return nil, nil
	}
	driver := candidates[entry.Driver]
	var backend containerBackend
	switch driver := driver.(type) {
	case *imageDriver:
		backend = driver.backend
	case *containerDriver:
		backend = driver.backend
	case *daggerCloudDriver:
		if entry.Cloud != nil && entry.Cloud.URL != "" {
			return driver, &DaggerCloudConnector{EngineSpec: *entry.Cloud}
		}
	}
	if backend != nil && entry.Container != nil && entry.Container.Host != "" {
		return driver, containerConnector{
			backend: backend, host: entry.Container.Host, values: entry.Container.Values,
			warm: newWarmTunnels(),
		}
	}
	return nil, nil
}

func connectionCacheKey(target *url.URL, opts *DriverOpts) (string, error) {
	var options DriverOpts
	if opts != nil {
		options = *opts
	}
	// Every invocation generates a new ID. It is session metadata, not an
	// input to endpoint selection, and would otherwise prevent any reuse.
	options.ClientID = ""
	if target.Scheme != "dagger-cloud" {
		options.Module, options.Function, options.ExecCmd, options.CloudAuth = "", "", nil, nil
	}
	inputs := struct {
		Schema                               int
		Version, Tag, MinimumVersion, Target string
		Options                              DriverOpts
		Environment                          map[string]string
		Config                               map[string][]byte
		Cleanup                              bool
	}{
		Schema: 1, Version: engine.FullVersion(), Tag: engine.Tag,
		MinimumVersion: engine.MinimumEngineVersion, Target: target.String(),
		Options: options, Environment: map[string]string{}, Config: map[string][]byte{},
	}
	if target.Scheme == "dagger-cloud" {
		inputs.Environment["DAGGER_CLOUD_URL"] = os.Getenv("DAGGER_CLOUD_URL")
	} else {
		inputs.Cleanup = cleanupOldEngines(target.Query())
		for _, env := range os.Environ() {
			name, value, _ := strings.Cut(env, "=")
			if name == "PATH" || name == "HOME" || strings.HasPrefix(name, "XDG_") ||
				strings.HasPrefix(name, "DOCKER_") || strings.HasPrefix(name, "CONTAINER") ||
				strings.HasPrefix(name, "PODMAN_") || strings.HasPrefix(name, "FINCH_") || strings.HasPrefix(name, "NERDCTL_") {
				inputs.Environment[name] = value
			}
		}
		for _, env := range target.Query()["env"] {
			if !strings.Contains(env, "=") {
				if value, ok := os.LookupEnv(env); ok {
					inputs.Environment[env] = value
				}
			}
		}
		dockerConfig := os.Getenv("DOCKER_CONFIG")
		if dockerConfig == "" {
			dockerConfig = filepath.Join(os.Getenv("HOME"), ".docker")
		}
		// In particular, `docker context use` changes config.json without
		// changing the environment. Hash configuration; never persist secrets.
		for _, path := range []string{engineConfigPath, filepath.Join(dockerConfig, "config.json"),
			filepath.Join(xdg.ConfigHome, "containers", "containers.conf"),
			filepath.Join(xdg.ConfigHome, "containers", "podman-connections.json")} {
			data, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				return "", err
			}
			inputs.Config[path] = data
		}
	}
	data, err := json.Marshal(inputs)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}
