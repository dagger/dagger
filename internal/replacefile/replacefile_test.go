package replacefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenameReplaces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tmp, dest := filepath.Join(dir, "tmp"), filepath.Join(dir, "dest")
	require.NoError(t, os.WriteFile(dest, []byte("old"), 0o600))
	require.NoError(t, os.WriteFile(tmp, []byte("new"), 0o600))
	require.NoError(t, Rename(tmp, dest))
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "new", string(got))
	_, err = os.Stat(tmp)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// When the rename keeps failing, as on Windows over a file another process
// holds open, the destination is rewritten in place after bounded retries.
func TestRenameFallsBackAfterRetries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	tmp, dest := filepath.Join(dir, "tmp"), filepath.Join(dir, "dest")
	require.NoError(t, os.WriteFile(dest, []byte("old contents"), 0o600))
	require.NoError(t, os.WriteFile(tmp, []byte("new"), 0o600))
	calls := 0
	failing := func(string, string) error {
		calls++
		return errors.New("sharing violation")
	}
	require.NoError(t, rename(tmp, dest, failing, true))
	require.Equal(t, renameRetries+1, calls)
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "new", string(got))
	_, err = os.Stat(tmp)
	require.ErrorIs(t, err, os.ErrNotExist)

	require.Error(t, rename(tmp, dest, failing, false), "no retry off Windows")
}
