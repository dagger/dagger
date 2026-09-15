package gogenerator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/psanford/memfs"
	"github.com/stretchr/testify/require"
)

func TestFindStaleDependencyBindings(t *testing.T) {
	outDir := t.TempDir()
	bindingsDir := filepath.Join("internal", "dagger")
	require.NoError(t, os.MkdirAll(filepath.Join(outDir, bindingsDir), 0o755))

	generated := []byte(daggerGeneratedHeader + "\n\npackage dagger\n")
	for _, name := range []string{"dagger.gen.go", "current.gen.go", "stale-b.gen.go", "stale-a.gen.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(outDir, bindingsDir, name), generated, 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(outDir, bindingsDir, "handwritten.gen.go"), []byte("package dagger\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outDir, bindingsDir, "notes.txt"), generated, 0o600))

	overlay := memfs.New()
	require.NoError(t, overlay.MkdirAll(bindingsDir, 0o755))
	require.NoError(t, overlay.WriteFile(filepath.Join(bindingsDir, "current.gen.go"), generated, 0o600))

	stale, err := findStaleDependencyBindings(outDir, bindingsDir, overlay)
	require.NoError(t, err)
	require.Equal(t, []string{
		filepath.Join(bindingsDir, "stale-a.gen.go"),
		filepath.Join(bindingsDir, "stale-b.gen.go"),
	}, stale)
}

func TestFindStaleDependencyBindingsMissingDirectory(t *testing.T) {
	stale, err := findStaleDependencyBindings(t.TempDir(), filepath.Join("internal", "dagger"), memfs.New())
	require.NoError(t, err)
	require.Empty(t, stale)
}
