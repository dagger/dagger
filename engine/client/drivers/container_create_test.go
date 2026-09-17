package drivers

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/client/imageload"
)

func TestImageDriverCreateEnablesLoopbackDebugListener(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	backend := &captureContainerBackend{}
	driver := &imageDriver{backend: backend}

	target, wasRunning, err := driver.create(ctx, containerCreateOpts{
		imageRef: "registry.example.com/dagger-engine:v0.21.0",
		port:     1234,
	}, &DriverOpts{})
	require.NoError(t, err)
	require.False(t, wasRunning)
	require.Equal(t, "dagger-engine-v0.21.0", target.Host)

	require.Equal(t, "dagger-engine-v0.21.0", backend.runName)
	require.Equal(t, []string{
		"--debug",
		"--debugaddr",
		defaultDebugListenerAddress,
		"--addr",
		"tcp://0.0.0.0:1234",
	}, backend.runOpts.args)
	require.Equal(t, []string{"1234:1234"}, backend.runOpts.ports)
}

// An engine that already exists is started and used without listing the
// host's containers first. Leftovers are swept afterwards, in the
// background, by identity: the engine in use is never removed, even under a
// name that no longer points at it.
func TestImageDriverCreateStartsExistingEngineWithoutListing(t *testing.T) {
	t.Parallel()

	backend := &existingEngineBackend{
		ids: map[string]string{
			"dagger-engine-v0.21.0": "id-current",
			"dagger-engine-v0.20.0": "id-old",
			"dagger-engine-v0.19.0": "id-current", // a stale name for the engine in use
		},
		running: map[string]bool{"dagger-engine-v0.21.0": true},
		all:     []string{"dagger-engine-v0.20.0", "dagger-engine-v0.21.0", "dagger-engine-v0.19.0", "unrelated"},
		lsGate:  make(chan struct{}),
	}
	driver := &imageDriver{backend: backend}

	target, wasRunning, err := driver.create(t.Context(), containerCreateOpts{
		imageRef: "registry.example.com/dagger-engine:v0.21.0",
		cleanup:  true,
	}, &DriverOpts{})
	require.NoError(t, err)
	require.True(t, wasRunning)
	require.Equal(t, "dagger-engine-v0.21.0", target.Host)
	require.Empty(t, backend.runName, "no container is run")
	require.Empty(t, backend.started, "a running engine is not started again")
	// create returned while the listing was still blocked: it did not wait.
	require.Empty(t, backend.removedIDs())

	close(backend.lsGate)
	require.Eventually(t, func() bool { return len(backend.removedIDs()) == 1 }, 5*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"id-old"}, backend.removedIDs())
}

// A new engine still lists and sweeps before the command proceeds, as before.
func TestImageDriverCreateSweepsBeforeRunningNewEngine(t *testing.T) {
	t.Parallel()

	backend := &existingEngineBackend{
		ids:    map[string]string{"dagger-engine-v0.20.0": "id-old"},
		all:    []string{"dagger-engine-v0.20.0"},
		lsGate: make(chan struct{}),
	}
	close(backend.lsGate)
	driver := &imageDriver{backend: backend}

	_, wasRunning, err := driver.create(t.Context(), containerCreateOpts{
		imageRef: "registry.example.com/dagger-engine:v0.21.0",
		cleanup:  true,
	}, &DriverOpts{})
	require.NoError(t, err)
	require.False(t, wasRunning)
	require.Equal(t, "dagger-engine-v0.21.0", backend.runName)
	require.Equal(t, []string{"dagger-engine-v0.20.0"}, backend.removedIDs())
}

// A stopped engine is started, and reported as not having been running, so
// no tunnel is dialed into it before it is up.
func TestImageDriverCreateStartsStoppedEngine(t *testing.T) {
	t.Parallel()

	backend := &existingEngineBackend{
		ids:    map[string]string{"dagger-engine-v0.21.0": "id-current"},
		lsGate: make(chan struct{}),
	}
	close(backend.lsGate)
	driver := &imageDriver{backend: backend}

	_, wasRunning, err := driver.create(t.Context(), containerCreateOpts{
		imageRef: "registry.example.com/dagger-engine:v0.21.0",
		cleanup:  true,
	}, &DriverOpts{})
	require.NoError(t, err)
	require.False(t, wasRunning)
	require.Empty(t, backend.runName)
	require.Equal(t, []string{"dagger-engine-v0.21.0"}, backend.started)

	// Provision follows create: no pre-dialed tunnels for an engine that was
	// not running.
	connector, err := driver.Provision(t.Context(), &url.URL{Scheme: "image", Host: "registry.example.com", Path: "/dagger-engine:v0.21.0"}, &DriverOpts{})
	require.NoError(t, err)
	require.Nil(t, connector.(containerConnector).warm)
}

// The connector dials its tunnels ahead of time, all at once, and hands
// them out in order; past those it dials on demand, and a pre-dial that
// failed is replaced by a fresh dial.
func TestContainerConnectorPreDialsTunnels(t *testing.T) {
	t.Parallel()

	backend := &dialCountingBackend{failFirst: 1}
	driver := &containerDriver{backend: backend}
	connector, err := driver.Provision(t.Context(), &url.URL{Scheme: "container", Host: "engine"}, &DriverOpts{})
	require.NoError(t, err)

	// All four tunnels are in flight before anyone asked for one.
	require.Eventually(t, func() bool { return backend.dials.Load() == warmTunnelCount }, 5*time.Second, 10*time.Millisecond)

	for range warmTunnelCount + 1 {
		conn, err := connector.Connect(t.Context())
		require.NoError(t, err)
		require.NotNil(t, conn)
		conn.Close()
	}
	// Four pre-dials, one of which failed and was redone, plus one on demand.
	require.Equal(t, int32(warmTunnelCount+2), backend.dials.Load())
}

type dialCountingBackend struct {
	captureContainerBackend
	dials     atomic.Int32
	failFirst int32
}

func (b *dialCountingBackend) ContainerDial(context.Context, string, []string) (net.Conn, error) {
	n := b.dials.Add(1)
	if n <= b.failFirst {
		return nil, fmt.Errorf("dial %d failed", n)
	}
	client, server := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, server) }()
	return client, nil
}

// existingEngineBackend fakes a runtime holding the containers in ids, of
// which those in running are up.
type existingEngineBackend struct {
	captureContainerBackend
	ids     map[string]string
	running map[string]bool
	all     []string
	lsGate  chan struct{}

	mu      sync.Mutex
	started []string
	removed []string
}

func (b *existingEngineBackend) ContainerStart(_ context.Context, name string) error {
	if _, ok := b.ids[name]; !ok {
		return fmt.Errorf("no such container: %s", name)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.started = append(b.started, name)
	return nil
}

func (b *existingEngineBackend) ContainerInspect(_ context.Context, name string) (string, bool, error) {
	id, ok := b.ids[name]
	if !ok {
		return "", false, fmt.Errorf("no such container: %s", name)
	}
	return id, b.running[name], nil
}

func (b *existingEngineBackend) ContainerLs(ctx context.Context) ([]string, error) {
	select {
	case <-b.lsGate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return slices.Clone(b.all), nil
}

func (b *existingEngineBackend) ContainerRemove(_ context.Context, ref string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removed = append(b.removed, ref)
	return nil
}

func (b *existingEngineBackend) removedIDs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.removed)
}

type captureContainerBackend struct {
	runName string
	runOpts runOpts
}

func (b *captureContainerBackend) Available(context.Context) (bool, error) {
	return true, nil
}

func (b *captureContainerBackend) ImagePull(context.Context, string) error {
	return nil
}

func (b *captureContainerBackend) ImageExists(context.Context, string) (bool, error) {
	return true, nil
}

func (b *captureContainerBackend) ImageRemove(context.Context, string) error {
	return nil
}

func (b *captureContainerBackend) ImageLoader(context.Context) imageload.Backend {
	return nil
}

func (b *captureContainerBackend) ContainerRun(_ context.Context, name string, opts runOpts) error {
	b.runName = name
	b.runOpts = opts
	return nil
}

func (b *captureContainerBackend) ContainerExec(context.Context, string, []string) (string, string, error) {
	return "", "", nil
}

func (b *captureContainerBackend) ContainerDial(context.Context, string, []string) (net.Conn, error) {
	return nil, nil
}

func (b *captureContainerBackend) ContainerRemove(context.Context, string) error {
	return nil
}

// No container exists yet in this fake: starting one fails as the runtime
// would, so create goes on to run it.
func (b *captureContainerBackend) ContainerStart(_ context.Context, name string) error {
	return fmt.Errorf("no such container: %s", name)
}

func (b *captureContainerBackend) ContainerExists(context.Context, string) (bool, error) {
	return false, nil
}

func (b *captureContainerBackend) ContainerInspect(_ context.Context, name string) (string, bool, error) {
	return "", false, fmt.Errorf("no such container: %s", name)
}

func (b *captureContainerBackend) ContainerLs(context.Context) ([]string, error) {
	return nil, nil
}
