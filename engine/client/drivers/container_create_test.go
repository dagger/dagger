package drivers

import (
	"context"
	"net"
	"testing"

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
	return nil
}

func (b *captureContainerBackend) ContainerExists(context.Context, string) (bool, error) {
	return false, nil
}

func (b *captureContainerBackend) ContainerLs(context.Context) ([]string, error) {
	return nil, nil
}
