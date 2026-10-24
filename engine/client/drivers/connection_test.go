package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/internal/cloud"
	"github.com/dagger/dagger/internal/cloud/auth"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type connectionCacheBackend struct {
	captureContainerBackend
	available, starts int
}

func (b *connectionCacheBackend) Available(context.Context) (bool, error) {
	b.available++
	return true, nil
}

func (b *connectionCacheBackend) ContainerStart(context.Context, string) error {
	b.starts++
	return nil
}

func connectionCacheFixture(t *testing.T) (string, *url.URL, *connectionCacheBackend) {
	t.Helper()
	backend := &connectionCacheBackend{}
	scheme := "test-connection-cache"
	register(scheme, &imageDriver{backend: backend})
	t.Cleanup(func() { delete(drivers, scheme) })
	target, err := url.Parse(scheme + "://registry.example.com/engine:v1?cleanup=false")
	require.NoError(t, err)
	return t.TempDir(), target, backend
}

func acceptConnection(context.Context, Connector) error { return nil }

func TestConnectionCacheSlidingTTL(t *testing.T) {
	dir, target, backend := connectionCacheFixture(t)
	now := time.Now()
	clock := func() time.Time { return now }
	var previous *warmTunnels
	for i, advance := range []time.Duration{0, 9 * time.Second, 9 * time.Second, 10 * time.Second} {
		now = now.Add(advance)
		_, connector, err := connectCached(t.Context(), dir, target, &DriverOpts{ClientID: fmt.Sprint(i)}, acceptConnection, clock)
		require.NoError(t, err)
		container := connector.(containerConnector)
		require.Equal(t, "dagger-engine-v1", container.host)
		require.NotSame(t, previous, container.warm, "each invocation owns new tunnels")
		previous = container.warm
		closeConnector(connector)
		want := 1
		if i == 3 {
			want = 2
		}
		require.Equal(t, want, backend.available)
		require.Equal(t, want, backend.starts)
	}
}

func TestConnectionCacheFailedEndpointReprovisions(t *testing.T) {
	dir, target, backend := connectionCacheFixture(t)
	_, connector, err := connectCached(t.Context(), dir, target, &DriverOpts{}, acceptConnection, time.Now)
	require.NoError(t, err)
	closeConnector(connector)
	attempts := 0
	var rejected containerConnector
	_, connector, err = connectCached(t.Context(), dir, target, &DriverOpts{}, func(ctx context.Context, connector Connector) error {
		attempts++
		if attempts == 1 {
			rejected = connector.(containerConnector)
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			require.LessOrEqual(t, time.Until(deadline), cachedConnectionTimeout)
			return errors.New("engine has stopped")
		}
		return nil
	}, time.Now)
	require.NoError(t, err)
	closeConnector(connector)
	require.Equal(t, 2, attempts)
	require.Equal(t, 2, backend.starts)
	select {
	case <-rejected.warm.done:
	default:
		t.Fatal("failed cached connector was not closed")
	}
	// The replacement is cached too.
	_, connector, err = connectCached(t.Context(), dir, target, &DriverOpts{}, acceptConnection, time.Now)
	require.NoError(t, err)
	closeConnector(connector)
	require.Equal(t, 2, backend.starts)
}

func TestConnectionCacheDoesNotSaveFailures(t *testing.T) {
	dir, target, backend := connectionCacheFixture(t)
	connectErr := errors.New("engine did not answer")
	_, _, err := connectCached(t.Context(), dir, target, &DriverOpts{}, func(context.Context, Connector) error { return connectErr }, time.Now)
	require.ErrorIs(t, err, connectErr)
	_, connector, err := connectCached(t.Context(), dir, target, &DriverOpts{}, acceptConnection, time.Now)
	require.NoError(t, err)
	closeConnector(connector)
	require.Equal(t, 2, backend.starts)
}

func TestConnectionCacheCancellationDoesNotReprovision(t *testing.T) {
	dir, target, backend := connectionCacheFixture(t)
	_, connector, err := connectCached(t.Context(), dir, target, &DriverOpts{}, acceptConnection, time.Now)
	require.NoError(t, err)
	closeConnector(connector)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, _, err = connectCached(ctx, dir, target, &DriverOpts{}, func(context.Context, Connector) error {
		cancel()
		return ctx.Err()
	}, time.Now)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, backend.starts)
}

func TestConnectionCacheUnavailableOrCorrupt(t *testing.T) {
	for _, kind := range []string{"unavailable", "corrupt", "future"} {
		t.Run(kind, func(t *testing.T) {
			dir, target, backend := connectionCacheFixture(t)
			key, err := connectionCacheKey(target, &DriverOpts{})
			require.NoError(t, err)
			switch kind {
			case "unavailable":
				dir = filepath.Join(dir, "file")
				require.NoError(t, os.WriteFile(dir, nil, 0o600))
			case "corrupt":
				require.NoError(t, os.WriteFile(filepath.Join(dir, key+".json"), []byte("{"), 0o600))
			case "future":
				writeConnectionCache(filepath.Join(dir, key+".json"), cachedConnection{
					LastUsed: time.Now().Add(time.Hour), Driver: 0,
					Container: &cachedContainer{Host: "wrong-engine"},
				})
			}
			_, connector, err := connectCached(t.Context(), dir, target, &DriverOpts{}, acceptConnection, time.Now)
			require.NoError(t, err)
			closeConnector(connector)
			require.Equal(t, "dagger-engine-v1", connector.(containerConnector).host)
			require.Equal(t, 1, backend.starts)
		})
	}
}

func TestConnectionCacheCloud(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Equal(t, "/v1/engines", r.URL.Path)
		require.Equal(t, "org-one", r.Header.Get("X-Dagger-Org"))
		w.WriteHeader(http.StatusCreated)
		require.NoError(t, json.NewEncoder(w).Encode(cloud.EngineSpec{
			URL: "engine.example.com:443", InstanceID: "engine-one",
			CertSerialized: &cloud.SerializableCertificate{PrivateKey: []byte("private-key")},
		}))
	}))
	defer server.Close()
	t.Setenv("DAGGER_CLOUD_URL", server.URL)
	dir := filepath.Join(t.TempDir(), "connections")
	target, err := url.Parse("dagger-cloud://")
	require.NoError(t, err)
	opts := &DriverOpts{Module: "module", Function: "check", ClientID: "first", CloudAuth: &auth.Cloud{
		Token: &oauth2.Token{AccessToken: "secret", TokenType: "Basic"}, Org: &auth.Org{ID: "org-one"},
	}}
	for _, id := range []string{"first", "second"} {
		opts.ClientID = id
		_, connector, err := connectCached(t.Context(), dir, target, opts, acceptConnection, time.Now)
		require.NoError(t, err)
		require.Equal(t, "engine-one", connector.EngineID())
		require.Equal(t, []byte("private-key"), connector.(*DaggerCloudConnector).EngineSpec.CertSerialized.PrivateKey)
	}
	require.EqualValues(t, 1, requests.Load())
	key, err := connectionCacheKey(target, opts)
	require.NoError(t, err)
	path := filepath.Join(dir, key+".json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret")
	require.NotContains(t, string(data), "client_id")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		info, err = os.Stat(dir)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	}
	// A changed Cloud routing input must provision again.
	opts.Function = "build"
	_, _, err = connectCached(t.Context(), dir, target, opts, acceptConnection, time.Now)
	require.NoError(t, err)
	require.EqualValues(t, 2, requests.Load())
}

func TestConnectionCacheKeyInputs(t *testing.T) {
	target, err := url.Parse("dagger-cloud://")
	require.NoError(t, err)
	base := func() *DriverOpts {
		return &DriverOpts{ClientID: "first", CloudAuth: &auth.Cloud{
			Token: &oauth2.Token{AccessToken: "token", TokenType: "Basic"}, Org: &auth.Org{ID: "org"},
		}}
	}
	key, err := connectionCacheKey(target, base())
	require.NoError(t, err)
	for name, mutate := range map[string]func(*DriverOpts){
		"token":    func(o *DriverOpts) { o.CloudAuth.Token.AccessToken = "other" },
		"org":      func(o *DriverOpts) { o.CloudAuth.Org.ID = "other" },
		"module":   func(o *DriverOpts) { o.Module = "other" },
		"function": func(o *DriverOpts) { o.Function = "other" },
		"exec":     func(o *DriverOpts) { o.ExecCmd = []string{"echo", "hello"} },
	} {
		t.Run(name, func(t *testing.T) {
			opts := base()
			mutate(opts)
			other, err := connectionCacheKey(target, opts)
			require.NoError(t, err)
			require.NotEqual(t, key, other)
		})
	}
	t.Run("API", func(t *testing.T) {
		t.Setenv("DAGGER_CLOUD_URL", "https://other.example.com")
		other, err := connectionCacheKey(target, base())
		require.NoError(t, err)
		require.NotEqual(t, key, other)
	})
	t.Run("local runtime", func(t *testing.T) {
		_, target, _ := connectionCacheFixture(t)
		dockerConfig := t.TempDir()
		t.Setenv("DOCKER_CONFIG", dockerConfig)
		key, err := connectionCacheKey(target, base())
		require.NoError(t, err)
		opts := base()
		opts.ClientID, opts.Module, opts.Function = "next", "module", "check"
		other, err := connectionCacheKey(target, opts)
		require.NoError(t, err)
		require.Equal(t, key, other, "local commands share an endpoint")
		require.NoError(t, os.WriteFile(filepath.Join(dockerConfig, "config.json"), []byte(`{"currentContext":"other"}`), 0o600))
		other, err = connectionCacheKey(target, base())
		require.NoError(t, err)
		require.NotEqual(t, key, other)
		key = other
		t.Setenv("DOCKER_HOST", "tcp://other.example.com:2376")
		other, err = connectionCacheKey(target, base())
		require.NoError(t, err)
		require.NotEqual(t, key, other)
	})
}

// Run separate processes against the same directory, both sequentially and
// concurrently. A memory cache or an unlocked read/provision/write cannot pass.
func TestConnectionCacheProcesses(t *testing.T) {
	if dir := os.Getenv("_DAGGER_TEST_CONNECTION_CACHE_DIR"); dir != "" {
		backend := &processCacheBackend{path: filepath.Join(dir, "provisions")}
		register("test-process", &imageDriver{backend: backend})
		target, err := url.Parse("test-process://example.com/engine:v1?cleanup=false")
		require.NoError(t, err)
		_, connector, err := connectCached(t.Context(), dir, target, &DriverOpts{}, acceptConnection, time.Now)
		require.NoError(t, err)
		closeConnector(connector)
		return
	}
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprint("parallel=", parallel), func(t *testing.T) {
			dir := t.TempDir()
			executable, err := os.Executable()
			require.NoError(t, err)
			var wg sync.WaitGroup
			for range 4 {
				run := func() {
					cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestConnectionCacheProcesses$")
					cmd.Env = append(os.Environ(), "_DAGGER_TEST_CONNECTION_CACHE_DIR="+dir)
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Errorf("helper: %v\n%s", err, output)
					}
				}
				if parallel {
					wg.Go(run)
				} else {
					run()
				}
			}
			wg.Wait()
			data, err := os.ReadFile(filepath.Join(dir, "provisions"))
			require.NoError(t, err)
			require.Equal(t, "available\nstart\n", string(data))
		})
	}
}

type processCacheBackend struct {
	captureContainerBackend
	path string
}

func (b *processCacheBackend) record(value string) error {
	f, err := os.OpenFile(b.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, value)
	return errors.Join(err, f.Close())
}

func (b *processCacheBackend) Available(context.Context) (bool, error) {
	return true, b.record("available")
}

func (b *processCacheBackend) ContainerStart(context.Context, string) error {
	return b.record("start")
}

func TestConnectionCachePreservesSelectedRuntime(t *testing.T) {
	dir, target, backend := connectionCacheFixture(t)
	selected := drivers[target.Scheme][0]
	unavailable := &unavailableCacheBackend{}
	drivers[target.Scheme] = append([]Driver{&imageDriver{backend: unavailable}}, selected)
	for range 2 {
		driver, connector, err := connectCached(t.Context(), dir, target, &DriverOpts{}, acceptConnection, time.Now)
		require.NoError(t, err)
		require.Same(t, selected, driver, "image loading must use the selected runtime too")
		require.Same(t, backend, connector.(containerConnector).backend)
		closeConnector(connector)
	}
	require.Equal(t, 1, unavailable.checks)
	require.Equal(t, 1, backend.available)
}

type unavailableCacheBackend struct {
	captureContainerBackend
	checks int
}

func (b *unavailableCacheBackend) Available(context.Context) (bool, error) {
	b.checks++
	return false, nil
}

func TestConnectionCacheDirectDialBypassesCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unused")
	target, err := url.Parse("tcp://127.0.0.1:1234")
	require.NoError(t, err)
	calls := 0
	for range 2 {
		_, _, err := connectCached(t.Context(), dir, target, &DriverOpts{}, func(context.Context, Connector) error {
			calls++
			return nil
		}, time.Now)
		require.NoError(t, err)
	}
	require.Equal(t, 2, calls)
	_, err = os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist)
}
