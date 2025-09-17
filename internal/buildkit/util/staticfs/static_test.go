package staticfs

import (
	"context"
	"io"
	iofs "io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil/types"
)

func TestStatic(t *testing.T) {
	fs := NewFS()
	fs.Add("foo", types.Stat{Mode: 0644}, []byte("foofoo"))
	fs.Add("bar", types.Stat{Mode: 0444}, []byte("barbarbar"))

	rc, err := fs.Open("bar")
	require.NoError(t, err)
	require.NoError(t, rc.Close())

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("barbarbar"), data)

	_, err = fs.Open("abc")
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))

	var files []string
	err = fs.Walk(context.TODO(), "", func(path string, entry iofs.DirEntry, err error) error {
		require.NoError(t, err)
		info, err := entry.Info()
		require.NoError(t, err)
		switch path {
		case "foo":
			require.Equal(t, int64(6), info.Size())
			require.Equal(t, os.FileMode(0644), info.Mode())
		case "bar":
			require.Equal(t, int64(9), info.Size())
			require.Equal(t, os.FileMode(0444), info.Mode())
		default:
			require.Fail(t, "unexpected path", path)
		}
		files = append(files, path)
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, []string{"bar", "foo"}, files)

	fs.Add("abc", types.Stat{Mode: 0444}, []byte("abcabcabc"))

	rc, err = fs.Open("abc")
	require.NoError(t, err)

	data, err = io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("abcabcabc"), data)
	require.NoError(t, rc.Close())

	rc, err = fs.Open("foo")
	require.NoError(t, err)

	data, err = io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("foofoo"), data)
	require.NoError(t, rc.Close())
}
