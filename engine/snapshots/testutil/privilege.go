package testutil

import (
	"errors"
	"os"
	"testing"

	"github.com/containerd/containerd/v2/core/mount"
	"github.com/stretchr/testify/require"
)

// RequireNativeMount skips the test with the reason when the host denies the
// read-only bind mount that a native snapshot read needs. Tests that read a
// snapshot through core.MountRef (File.Contents, Directory.Entries, the
// evaluation of a subdirectory) call it first; the in-place store makes every
// other store test independent of mount privileges, and this probe marks the
// remaining ones as unexecuted, not failed, on hosts and CI runners without
// them.
func RequireNativeMount(t testing.TB) {
	t.Helper()
	source := t.TempDir()
	target, err := os.MkdirTemp("", "snapshot-mount-probe")
	require.NoError(t, err)
	defer func() { require.NoError(t, os.Remove(target)) }()

	// Probe the read-only mount used to read a snapshot, including cleanup
	// after a partial mount.
	mountErr := mount.All([]mount.Mount{{Type: "bind", Source: source, Options: []string{"rbind", "ro"}}}, target)
	unmountErr := mount.UnmountAll(target, 0)
	if errors.Is(mountErr, os.ErrPermission) {
		if !errors.Is(unmountErr, os.ErrPermission) {
			require.NoError(t, unmountErr)
		}
		t.Skipf("reading a native snapshot requires read-only bind mount privileges: %v", mountErr)
	}
	require.NoError(t, mountErr)
	require.NoError(t, unmountErr)
}
