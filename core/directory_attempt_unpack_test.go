package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagger/dagger/core/internal/layercopy"
	"github.com/stretchr/testify/require"
)

func TestCopyAttemptUnpackNonArchiveSingleFileDestDirHintCreatesDestDir(t *testing.T) {
	t.Parallel()

	srcRoot := t.TempDir()
	srcPath := filepath.Join(srcRoot, "hack", "git-checkout-tag-with-hash.sh")
	require.NoError(t, os.MkdirAll(filepath.Dir(srcPath), 0o755))
	require.NoError(t, os.WriteFile(srcPath, []byte("#!/bin/sh\necho ok\n"), 0o755))

	destRoot := t.TempDir()
	copier, err := layercopy.NewCopier(layercopy.Mount{Root: destRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	copied, err := copyAttemptUnpackNonArchiveSingleFile(
		context.Background(),
		copier,
		layercopy.Mount{Root: srcRoot},
		"hack/git-checkout-tag-with-hash.sh",
		"/usr/local/bin",
		layercopy.CopyOptions{ReplaceExisting: true},
		true,
	)
	require.NoError(t, err)
	require.True(t, copied)

	destPath := filepath.Join(destRoot, "usr", "local", "bin")
	destInfo, err := os.Stat(destPath)
	require.NoError(t, err)
	require.True(t, destInfo.IsDir())

	got, err := os.ReadFile(filepath.Join(destPath, "git-checkout-tag-with-hash.sh"))
	require.NoError(t, err)
	require.Equal(t, "#!/bin/sh\necho ok\n", string(got))
}

func TestCopyAttemptUnpackNonArchiveSingleFileNoDestDirHintCopiesToExactPath(t *testing.T) {
	t.Parallel()

	srcRoot := t.TempDir()
	srcPath := filepath.Join(srcRoot, "archive.tar")
	require.NoError(t, os.WriteFile(srcPath, []byte("not an archive"), 0o644))

	destRoot := t.TempDir()
	copier, err := layercopy.NewCopier(layercopy.Mount{Root: destRoot})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, copier.Close())
	})

	copied, err := copyAttemptUnpackNonArchiveSingleFile(
		context.Background(),
		copier,
		layercopy.Mount{Root: srcRoot},
		"archive.tar",
		"/out",
		layercopy.CopyOptions{ReplaceExisting: true},
		false,
	)
	require.NoError(t, err)
	require.True(t, copied)

	destPath := filepath.Join(destRoot, "out")
	destInfo, err := os.Stat(destPath)
	require.NoError(t, err)
	require.False(t, destInfo.IsDir())
}
