package drivers

import (
	"context"
	"net/url"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeBackend struct {
	*captureContainerBackend
	name       string
	available  bool
	containers []string
}

func (b *fakeBackend) Name() string { return b.name }

func (b *fakeBackend) Available(context.Context, *url.URL) (bool, error) {
	return b.available, nil
}

func (b *fakeBackend) ContainerExists(_ context.Context, name string) (bool, error) {
	return slices.Contains(b.containers, name), nil
}

func TestContainerDriverAvailableChecksContainerFromURL(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	u := func(raw string) *url.URL {
		parsed, err := url.Parse(raw)
		require.NoError(t, err)
		return parsed
	}

	t.Run("container exists", func(t *testing.T) {
		d := &containerDriver{&fakeBackend{captureContainerBackend: &captureContainerBackend{}, name: "docker", available: true, containers: []string{"my-engine"}}}
		ok, err := d.Available(ctx, u("container://my-engine"))
		require.NoError(t, err)
		require.True(t, ok)
	})

	t.Run("container missing explains why and keeps looking", func(t *testing.T) {
		d := &containerDriver{&fakeBackend{captureContainerBackend: &captureContainerBackend{}, name: "podman", available: true}}
		ok, err := d.Available(ctx, u("container://my-engine"))
		require.False(t, ok)
		unavailable, isUnavailable := err.(*UnavailableError)
		require.True(t, isUnavailable, "got %v", err)
		require.Contains(t, unavailable.Reason, "podman")
		require.Contains(t, unavailable.Reason, "my-engine")
	})

	t.Run("runtime not installed is skipped silently", func(t *testing.T) {
		d := &containerDriver{&fakeBackend{captureContainerBackend: &captureContainerBackend{}, name: "finch", available: false, containers: []string{"my-engine"}}}
		ok, err := d.Available(ctx, u("container://my-engine"))
		require.NoError(t, err)
		require.False(t, ok)
	})

	t.Run("name is required", func(t *testing.T) {
		d := &containerDriver{&fakeBackend{captureContainerBackend: &captureContainerBackend{}, name: "docker", available: true}}
		_, err := d.Available(ctx, u("container://"))
		require.Error(t, err)
	})
}
