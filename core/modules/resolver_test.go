package modules

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatchVersion(t *testing.T) {
	vers := []string{"v1.0.0", "v1.0.1", "v2.0.0", "path/v1.0.1"}

	match := "v1.0.1"

	match1, err := matchVersion(vers, match, "")
	require.NoError(t, err)
	require.Equal(t, "v1.0.1", match1)

	match2, err := matchVersion(vers, match, "path")
	require.NoError(t, err)
	require.Equal(t, "path/v1.0.1", match2)
}

func TestIsSemver(t *testing.T) {
	require.True(t, isSemver("v1.0.0"))
	require.True(t, isSemver("v2.0.1"))
	require.False(t, isSemver("1.0.0"))
	require.False(t, isSemver("v1.0"))
	require.False(t, isSemver("v1"))
	require.False(t, isSemver("foo"))
}

