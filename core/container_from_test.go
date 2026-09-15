package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContainerCanUseFromContentDigest(t *testing.T) {
	t.Parallel()

	require.True(t, NewContainer(Platform{}).CanUseFromContentDigest())
	require.False(t, (&Container{}).CanUseFromContentDigest())
	require.False(t, (*Container)(nil).CanUseFromContentDigest())
}
