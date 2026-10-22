package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// An export builds up to 4 layers at once, and all exports together up to
// 4: the most the serial exports built at once.
func TestExportLayerBuilds(t *testing.T) {
	builds := exportRefConfig.Builds
	require.Equal(t, 4, builds.Workers)
	require.NotNil(t, builds.Gate)
	require.True(t, builds.Gate.TryAcquire(4))
	require.False(t, builds.Gate.TryAcquire(1))
	builds.Gate.Release(4)
}
