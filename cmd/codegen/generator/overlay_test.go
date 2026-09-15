package generator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/psanford/memfs"
	"github.com/stretchr/testify/require"
)

func TestApplyRemovesPathsBeforeWritingOverlay(t *testing.T) {
	outDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outDir, "stale.gen.go"), []byte("stale"), 0o600))

	overlay := memfs.New()
	require.NoError(t, overlay.WriteFile("current.gen.go", []byte("current"), 0o600))

	require.NoError(t, Apply(t.Context(), &GeneratedState{
		Overlay:     overlay,
		RemovePaths: []string{"stale.gen.go"},
	}, outDir))

	_, err := os.Stat(filepath.Join(outDir, "stale.gen.go"))
	require.ErrorIs(t, err, os.ErrNotExist)
	current, err := os.ReadFile(filepath.Join(outDir, "current.gen.go"))
	require.NoError(t, err)
	require.Equal(t, "current", string(current))
}

func TestApplyRejectsRemovalOutsideOutputDirectory(t *testing.T) {
	require.ErrorContains(t, Apply(t.Context(), &GeneratedState{
		Overlay:     memfs.New(),
		RemovePaths: []string{"../outside"},
	}, t.TempDir()), "outside output directory")
}
