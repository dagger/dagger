package contentutil

import (
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/reference"
	"github.com/stretchr/testify/require"
)

func TestHasSource(t *testing.T) {
	info := content.Info{
		Labels: map[string]string{
			"containerd.io/distribution.source.docker.io": "library/alpine",
		},
	}
	ref, err := reference.Parse("docker.io/library/alpine:latest")
	require.NoError(t, err)
	b, err := HasSource(info, ref)
	require.NoError(t, err)
	require.True(t, b)

	info = content.Info{
		Labels: map[string]string{
			"containerd.io/distribution.source.docker.io": "library/alpine,library/ubuntu",
		},
	}
	b, err = HasSource(info, ref)
	require.NoError(t, err)
	require.True(t, b)

	info = content.Info{}
	b, err = HasSource(info, ref)
	require.NoError(t, err)
	require.False(t, b)

	info = content.Info{Labels: map[string]string{}}
	b, err = HasSource(info, ref)
	require.NoError(t, err)
	require.False(t, b)

	info = content.Info{
		Labels: map[string]string{
			"containerd.io/distribution.source.docker.io": "library/ubuntu",
		},
	}
	b, err = HasSource(info, ref)
	require.NoError(t, err)
	require.False(t, b)

	info = content.Info{Labels: map[string]string{
		"containerd.io/distribution.source.ghcr.io": "library/alpine",
	}}
	b, err = HasSource(info, ref)
	require.NoError(t, err)
	require.False(t, b)
}
