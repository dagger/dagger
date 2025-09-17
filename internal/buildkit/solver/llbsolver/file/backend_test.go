package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestRmPathNonExistentFileAllowNotFoundFalse(t *testing.T) {
	root := t.TempDir()
	err := rmPath(root, "doesnt_exist", false)
	require.Error(t, err)
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func TestRmPathNonExistentFileAllowNotFoundTrue(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, rmPath(root, "doesnt_exist", true))
}

func TestRmPathFileExists(t *testing.T) {
	root := t.TempDir()

	src := filepath.Join(root, "exists")
	file, err := os.Create(src)
	require.NoError(t, err)
	file.Close()

	require.NoError(t, rmPath(root, "exists", false))

	_, err = os.Stat(src)

	require.True(t, os.IsNotExist(err))
}
