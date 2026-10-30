//go:build linux

package layercopy

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// An overlay directory is opaque only when its opaque attribute is the one
// byte "y": a longer value, or another byte, isn't the marker.
func TestOverlayEntryStateOpaqueMarker(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value  string
		opaque bool
	}{
		{"y", true},
		{"n", false},
		{"yy", false},
		{"yes", false},
	} {
		dir := t.TempDir()
		require.NoError(t, unix.Lsetxattr(dir, "user.overlay.opaque", []byte(test.value), 0))
		isDir, hidesLower, err := overlayEntryState(dir)
		require.NoError(t, err, test.value)
		require.True(t, isDir, test.value)
		require.Equal(t, test.opaque, hidesLower, "opaque value %q", test.value)
	}
	isDir, hidesLower, err := overlayEntryState(t.TempDir())
	require.NoError(t, err)
	require.True(t, isDir)
	require.False(t, hidesLower, "no opaque value")
}
