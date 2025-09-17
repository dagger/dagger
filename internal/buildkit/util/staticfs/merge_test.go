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

func TestMerge(t *testing.T) {
	fs1 := NewFS()
	fs1.Add("foo", types.Stat{Mode: 0644}, []byte("foofoo"))
	fs1.Add("bar", types.Stat{Mode: 0444}, []byte("barbarbar"))

	fs2 := NewFS()
	fs2.Add("abc", types.Stat{Mode: 0400}, []byte("abcabc"))
	fs2.Add("foo", types.Stat{Mode: 0440}, []byte("foofoofoofoo"))

	fs := NewMergeFS(fs1, fs2)

	rc, err := fs.Open("foo")
	require.NoError(t, err)

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("foofoofoofoo"), data)
	require.NoError(t, rc.Close())

	rc, err = fs.Open("bar")
	require.NoError(t, err)

	data, err = io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("barbarbar"), data)

	var files []string
	err = fs.Walk(context.TODO(), "", func(path string, entry iofs.DirEntry, err error) error {
		require.NoError(t, err)
		info, err := entry.Info()
		require.NoError(t, err)
		switch path {
		case "foo":
			require.Equal(t, int64(12), info.Size())
			require.Equal(t, os.FileMode(0440), info.Mode())
		case "bar":
			require.Equal(t, int64(9), info.Size())
			require.Equal(t, os.FileMode(0444), info.Mode())
		case "abc":
			require.Equal(t, int64(6), info.Size())
			require.Equal(t, os.FileMode(0400), info.Mode())
		default:
			require.Fail(t, "unexpected path", path)
		}
		files = append(files, path)
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, []string{"abc", "bar", "foo"}, files)

	// extra level
	fs3 := NewFS()
	fs3.Add("bax", types.Stat{Mode: 0600}, []byte("bax"))

	fs = NewMergeFS(fs, fs3)

	rc, err = fs.Open("bar")
	require.NoError(t, err)

	data, err = io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("barbarbar"), data)
	require.NoError(t, rc.Close())

	rc, err = fs.Open("bax")
	require.NoError(t, err)

	data, err = io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, []byte("bax"), data)
	require.NoError(t, rc.Close())

	_, err = fs.Open("bay")
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))

	files = nil
	err = fs.Walk(context.TODO(), "", func(path string, entry iofs.DirEntry, err error) error {
		require.NoError(t, err)
		files = append(files, path)
		return nil
	})
	require.NoError(t, err)

	require.Equal(t, []string{"abc", "bar", "bax", "foo"}, files)
}
