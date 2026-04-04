package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectMigrationTarget(t *testing.T) {
	t.Run("returns migration target for legacy module", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "dagger.json"), []byte("{}"), 0o644))

		target, status, err := detectMigrationTarget(root)
		require.NoError(t, err)
		require.Empty(t, status)
		require.NotNil(t, target)
		require.Equal(t, filepath.Join(root, "dagger.json"), target.ConfigPath)
		require.Equal(t, root, target.ProjectRoot)
	})

	t.Run("reports when no legacy module exists", func(t *testing.T) {
		target, status, err := detectMigrationTarget(t.TempDir())
		require.NoError(t, err)
		require.Nil(t, target)
		require.Equal(t, "No migration needed: no dagger.json found.", status)
	})

	t.Run("reports when workspace config already exists", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "dagger.json"), []byte("{}"), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".dagger"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, ".dagger", "config.toml"), []byte(""), 0o644))

		target, status, err := detectMigrationTarget(root)
		require.NoError(t, err)
		require.Nil(t, target)
		require.Equal(t, "No migration needed: workspace already initialized.", status)
	})
}

func TestFindMigratableModuleConfigs(t *testing.T) {
	root := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "api"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "services", "api", "dagger.json"), []byte("{}"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(root, "services", "ui", ".dagger"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "services", "ui", "dagger.json"), []byte("{}"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "services", "ui", ".dagger", "config.toml"), []byte(""), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git", "hooks", "dagger.json"), []byte("{}"), 0o644))

	paths, err := findMigratableModuleConfigs(root)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join("services", "api", "dagger.json")}, paths)
}
