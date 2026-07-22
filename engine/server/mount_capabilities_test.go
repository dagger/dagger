package server

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestRunRecursiveReadOnlyProbe(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var mounts []string
	var unmounts []string
	var setattrPath string
	var setattrFlags uint
	var setattr unix.MountAttr
	err := runRecursiveReadOnlyProbe(root, recursiveReadOnlyProbeOps{
		mount: func(_, target, _ string, _ uintptr, _ string) error {
			mounts = append(mounts, target)
			return nil
		},
		mountSetattr: func(_ int, path string, flags uint, attr *unix.MountAttr) error {
			setattrPath = path
			setattrFlags = flags
			setattr = *attr
			return nil
		},
		unmount: func(target string, _ int) error {
			unmounts = append(unmounts, target)
			return nil
		},
	})
	require.NoError(t, err)
	source := filepath.Join(root, "source")
	nested := filepath.Join(source, "nested")
	target := filepath.Join(root, "target")
	require.Equal(t, []string{source, nested, target}, mounts)
	require.Equal(t, target, setattrPath)
	require.Equal(t, uint(unix.AT_RECURSIVE), setattrFlags)
	require.Equal(t, uint64(unix.MOUNT_ATTR_RDONLY), setattr.Attr_set)
	require.Equal(t, []string{target, nested, source}, unmounts)
}

func TestRunRecursiveReadOnlyProbeCleansUpAfterUnsupportedSetattr(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("mount_setattr blocked")
	root := t.TempDir()
	var unmounts []string
	err := runRecursiveReadOnlyProbe(root, recursiveReadOnlyProbeOps{
		mount: func(_, _, _ string, _ uintptr, _ string) error { return nil },
		mountSetattr: func(_ int, _ string, _ uint, _ *unix.MountAttr) error {
			return wantErr
		},
		unmount: func(target string, _ int) error {
			unmounts = append(unmounts, target)
			return nil
		},
	})
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, []string{
		filepath.Join(root, "target"),
		filepath.Join(root, "source", "nested"),
		filepath.Join(root, "source"),
	}, unmounts)
}
