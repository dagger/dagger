package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Each mount is an empty directory of its own, bound writable. Releasing it
// returns before its removal ends, and the directory goes after.
func TestDiskTempMount(t *testing.T) {
	removing := make(chan string, 2)
	proceed := make(chan struct{})
	removeDiskTempDir = func(dir string) error {
		removing <- dir
		<-proceed
		return os.RemoveAll(dir)
	}
	t.Cleanup(func() { removeDiskTempDir = os.RemoveAll })

	mnt := &execDiskTempMount{parent: filepath.Join(t.TempDir(), DiskTempMountDir)}
	mount := func() (string, func() error) {
		t.Helper()
		ref, err := mnt.Mount(t.Context(), false)
		require.NoError(t, err)
		mounts, release, err := ref.Mount()
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		require.Equal(t, "bind", mounts[0].Type)
		require.NotContains(t, mounts[0].Options, "ro")
		dir := mounts[0].Source
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Empty(t, entries)
		info, err := os.Stat(dir)
		require.NoError(t, err)
		require.Equal(t, os.ModeDir|os.ModeSticky|0o777, info.Mode())
		return dir, release
	}

	first, releaseFirst := mount()
	require.NoError(t, os.WriteFile(filepath.Join(first, "file"), []byte("data"), 0o644))
	second, releaseSecond := mount()
	require.NotEqual(t, first, second)

	require.NoError(t, releaseFirst())
	require.Equal(t, first, <-removing)
	require.DirExists(t, first, "the release returned while the removal waits")
	require.DirExists(t, second)

	close(proceed)
	require.Eventually(t, func() bool {
		_, err := os.Stat(first)
		return os.IsNotExist(err)
	}, 10*time.Second, 10*time.Millisecond)
	require.DirExists(t, second, "only the released mount is removed")

	require.NoError(t, releaseSecond())
	require.Equal(t, second, <-removing)
	require.Eventually(t, func() bool {
		_, err := os.Stat(second)
		return os.IsNotExist(err)
	}, 10*time.Second, 10*time.Millisecond)
}
