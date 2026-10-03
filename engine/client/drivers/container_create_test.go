package drivers

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
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

	target, err := driver.create(ctx, containerCreateOpts{
		imageRef: "registry.example.com/dagger-engine:v0.21.0",
		port:     1234,
	}, &DriverOpts{})
	require.NoError(t, err)
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

// An engine that already exists is started by name and connected to
// without listing the host's containers first; engines of other versions
// are still removed, in the background, and the engine in use never is.
func TestImageDriverCreateStartsExistingEngineWithoutListing(t *testing.T) {
	t.Parallel()

	backend := &existingEngineBackend{
		containers:      []string{"dagger-engine-v0.20.0", "dagger-engine-v0.21.0", "unrelated"},
		cleanupDeadline: make(chan time.Time, 1),
	}
	driver := &imageDriver{backend: backend}

	target, err := driver.create(t.Context(), containerCreateOpts{
		imageRef: "registry.example.com/dagger-engine:v0.21.0",
		cleanup:  true,
	}, &DriverOpts{})
	require.NoError(t, err)
	require.Equal(t, "dagger-engine-v0.21.0", target.Host)

	// Starting the known name is the existence check; no inspect or list is on
	// the connection's critical path.
	require.Equal(t, []string{"start dagger-engine-v0.21.0"}, backend.callsBefore("ls"))
	select {
	case deadline := <-backend.cleanupDeadline:
		require.LessOrEqual(t, time.Until(deadline), leftoverEngineCleanupTimeout)
		require.Greater(t, time.Until(deadline), time.Duration(0))
	case <-time.After(5 * time.Second):
		t.Fatal("background cleanup did not start")
	}

	require.Eventually(t, func() bool {
		return slices.Contains(backend.calls(), "remove dagger-engine-v0.20.0")
	}, 5*time.Second, 10*time.Millisecond)
	require.NotContains(t, backend.calls(), "remove dagger-engine-v0.21.0")
	require.NotContains(t, backend.calls(), "remove unrelated")
	require.Empty(t, backend.runName, "no new engine should have been run")
}

// existingEngineBackend is a host on which the engine already exists. It
// records the backend calls, in order.
type existingEngineBackend struct {
	captureContainerBackend
	containers      []string
	cleanupDeadline chan time.Time

	mu  sync.Mutex
	log []string
}

func (b *existingEngineBackend) record(call string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.log = append(b.log, call)
}

func (b *existingEngineBackend) calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.log)
}

// callsBefore returns the calls made before the first one starting with
// prefix (all of them, if none did).
func (b *existingEngineBackend) callsBefore(prefix string) []string {
	calls := b.calls()
	for i, call := range calls {
		if strings.HasPrefix(call, prefix) {
			return calls[:i]
		}
	}
	return calls
}

func (b *existingEngineBackend) ContainerExists(_ context.Context, name string) (bool, error) {
	b.record("exists " + name)
	return slices.Contains(b.containers, name), nil
}

func (b *existingEngineBackend) ContainerStart(_ context.Context, name string) error {
	b.record("start " + name)
	return nil
}

func (b *existingEngineBackend) ContainerLs(ctx context.Context) ([]string, error) {
	b.record("ls")
	if deadline, ok := ctx.Deadline(); ok && b.cleanupDeadline != nil {
		b.cleanupDeadline <- deadline
	}
	return b.containers, nil
}

func (b *existingEngineBackend) ContainerRemove(_ context.Context, name string) error {
	b.record("remove " + name)
	return nil
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

func (b *captureContainerBackend) ContainerStart(context.Context, string) error {
	return errors.New("no such container")
}

func (b *captureContainerBackend) ContainerExists(context.Context, string) (bool, error) {
	return false, nil
}

func (b *captureContainerBackend) ContainerLs(context.Context) ([]string, error) {
	return nil, nil
}
