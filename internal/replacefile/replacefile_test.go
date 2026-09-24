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
// holds open, Rename retries a few times and then returns the error, leaving
// the destination untouched.
func TestRenameFailsAfterRetriesAndPreservesDestination(t *testing.T) {
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
	require.Error(t, rename(tmp, dest, failing, true))
	require.Equal(t, renameRetries+1, calls)
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "old contents", string(got), "the destination is untouched")

	calls = 0
	require.Error(t, rename(tmp, dest, failing, false))
	require.Equal(t, 1, calls, "no retry off Windows")
}
