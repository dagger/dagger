//go:build !windows
// +build !windows

package copy

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestCopyDevicesAndFifo(t *testing.T) {
	requiresRoot(t)

	t1 := t.TempDir()

	err := mknod(filepath.Join(t1, "char"), unix.S_IFCHR|0444, int(unix.Mkdev(1, 9)))
	require.NoError(t, err)

	err = mknod(filepath.Join(t1, "block"), unix.S_IFBLK|0441, int(unix.Mkdev(3, 2)))
	require.NoError(t, err)

	err = mknod(filepath.Join(t1, "socket"), unix.S_IFSOCK|0555, 0)
	require.NoError(t, err)

	err = unix.Mkfifo(filepath.Join(t1, "fifo"), 0555)
	require.NoError(t, err)

	t2 := t.TempDir()

	err = Copy(context.TODO(), t1, ".", t2, ".")
	require.NoError(t, err)

	fi, err := os.Lstat(filepath.Join(t2, "char"))
	require.NoError(t, err)
	assert.Equal(t, os.ModeCharDevice, fi.Mode()&os.ModeCharDevice)
	assert.Equal(t, os.FileMode(0444), fi.Mode()&0777)

	fi, err = os.Lstat(filepath.Join(t2, "block"))
	require.NoError(t, err)
	assert.Equal(t, os.ModeDevice, fi.Mode()&os.ModeDevice)
	assert.Equal(t, os.FileMode(0441), fi.Mode()&0777)

	fi, err = os.Lstat(filepath.Join(t2, "fifo"))
	require.NoError(t, err)
	assert.Equal(t, os.ModeNamedPipe, fi.Mode()&os.ModeNamedPipe)
	assert.Equal(t, os.FileMode(0555), fi.Mode()&0777)

	fi, err = os.Lstat(filepath.Join(t2, "socket"))
	require.NoError(t, err)
	assert.NotEqual(t, os.ModeSocket, fi.Mode()&os.ModeSocket) // socket copied as stub
	assert.Equal(t, os.FileMode(0555), fi.Mode()&0777)
}

func TestCopySetuid(t *testing.T) {
	requiresRoot(t)

	t1 := t.TempDir()

	err := mknod(filepath.Join(t1, "char"), unix.S_IFCHR|0444, int(unix.Mkdev(1, 9)))
	require.NoError(t, err)

	t2 := t.TempDir()

	err = Copy(context.TODO(), t1, ".", t2, ".")
	require.NoError(t, err)

	fi, err := os.Lstat(filepath.Join(t2, "char"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0444), fi.Mode().Perm())
	assert.Equal(t, os.FileMode(0), fi.Mode()&os.ModeSetuid)
	assert.Equal(t, os.FileMode(0), fi.Mode()&os.ModeSetgid)
	assert.Equal(t, os.FileMode(0), fi.Mode()&os.ModeSticky)

	t3 := t.TempDir()

	p := 0444 | syscall.S_ISUID
	err = Copy(context.TODO(), t1, ".", t3, ".", WithCopyInfo(CopyInfo{
		Mode: &p,
	}))
	require.NoError(t, err)

	fi, err = os.Lstat(filepath.Join(t3, "char"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0444), fi.Mode().Perm())
	assert.Equal(t, os.ModeSetuid, fi.Mode()&os.ModeSetuid)
	assert.Equal(t, os.FileMode(0), fi.Mode()&os.ModeSetgid)
	assert.Equal(t, os.FileMode(0), fi.Mode()&os.ModeSticky)
}
