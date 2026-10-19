package fsutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestFilterFSLiteralIncludeXattrs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "a")
	file := filepath.Join(dir, "file")
	require.NoError(t, os.Mkdir(dir, 0o750))
	require.NoError(t, os.WriteFile(file, nil, 0o640))
	for _, path := range []string{dir, file} {
		err := unix.Setxattr(path, "user.fsutil-literal", []byte("preserved"), 0)
		if err == unix.ENOTSUP {
			t.Skip("filesystem does not support user xattrs")
		}
		require.NoError(t, err)
	}
	opt := &FilterOpt{IncludePatterns: []string{"a/file"}}
	want, err := readLiteralInclude(t, root, opt, true, "", "", "")
	require.NoError(t, err)
	got, err := readLiteralInclude(t, root, opt, false, "", "", "")
	require.NoError(t, err)
	require.Len(t, want, 2)
	for _, stat := range want {
		require.Equal(t, []byte("preserved"), stat.Xattrs["user.fsutil-literal"])
	}
	require.Equal(t, want, got)
}
