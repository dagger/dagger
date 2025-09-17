package util

import (
	"path"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tonistiigi/fsutil"
)

// TestSetErrorPath ensures that modifying the os.PathError from fsutil.Stat
// modifies the error message appropriately.
//
// This method of modifying the path error is an implementation
// detail of how the error is returned. It isn't necessarily the
// case that this will always work if the underlying library changes.
func TestSetErrorPath(t *testing.T) {
	// Temporary directory to reduce the chance of using stat on a real file.
	dir := t.TempDir()

	// Random path that shouldn't exist.
	fpath := path.Join(dir, "a/b/c")
	_, err := fsutil.Stat(fpath)
	require.Error(t, err)

	require.ErrorContains(t, err, "a/b/c")
	// Set the path in the error to a new path.
	replaceErrorPath(err, "/my/new/path")
	require.NotContains(t, err.Error(), "a/b/c")
	require.Contains(t, err.Error(), "/my/new/path")
}
