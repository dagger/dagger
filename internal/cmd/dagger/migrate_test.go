package daggercmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceRootFromCwd(t *testing.T) {
	t.Run("local workspace address", func(t *testing.T) {
		got, err := localWorkspaceAddressPath("file:///tmp/repo/services/api")
		require.NoError(t, err)
		require.Equal(t, filepath.Join(string(filepath.Separator), "tmp", "repo", "services", "api"), got)
	})

	t.Run("rejects non-local workspace address", func(t *testing.T) {
		_, err := localWorkspaceAddressPath("git://github.com/dagger/dagger")
		require.ErrorContains(t, err, "requires a local file workspace")
	})

	t.Run("root cwd", func(t *testing.T) {
		root := t.TempDir()
		got, err := workspaceRootFromCwd(root, "/")
		require.NoError(t, err)
		require.Equal(t, root, got)
	})

	t.Run("nested cwd", func(t *testing.T) {
		root := t.TempDir()
		wd := filepath.Join(root, "services", "api")
		got, err := workspaceRootFromCwd(wd, "/"+filepath.ToSlash(filepath.Join("services", "api")))
		require.NoError(t, err)
		require.Equal(t, root, got)
	})

	t.Run("legacy relative cwd", func(t *testing.T) {
		root := t.TempDir()
		wd := filepath.Join(root, "services", "api")
		got, err := workspaceRootFromCwd(wd, filepath.ToSlash(filepath.Join("services", "api")))
		require.NoError(t, err)
		require.Equal(t, root, got)
	})

	t.Run("rejects escaping cwd", func(t *testing.T) {
		_, err := workspaceRootFromCwd(t.TempDir(), "../outside")
		require.ErrorContains(t, err, "escapes workspace root")
	})

	t.Run("rejects public escaping cwd", func(t *testing.T) {
		_, err := workspaceRootFromCwd(t.TempDir(), "/../outside")
		require.ErrorContains(t, err, "escapes workspace root")
	})

	t.Run("rejects cwd that is not working directory suffix", func(t *testing.T) {
		root := t.TempDir()
		wd := filepath.Join(root, "other")
		_, err := workspaceRootFromCwd(wd, "/services/api")
		require.ErrorContains(t, err, "is not within workspace cwd")
	})
}

func TestMigrateCommandFlags(t *testing.T) {
	require.NotNil(t, migrateCmd.Flags().Lookup("force"))
	require.Nil(t, migrateCmd.Flags().Lookup("list"))
}
