package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCopyAttemptUnpackNonArchiveSingleFileDestDirHintCreatesDestDir(t *testing.T) {
	t.Parallel()

	srcRoot := t.TempDir()
	srcPath := filepath.Join(srcRoot, "hack", "git-checkout-tag-with-hash.sh")
	require.NoError(t, os.MkdirAll(filepath.Dir(srcPath), 0o755))
	require.NoError(t, os.WriteFile(srcPath, []byte("#!/bin/sh\necho ok\n"), 0o755))

	destRoot := t.TempDir()
	destPath := filepath.Join(destRoot, "usr", "local", "bin")

	copied, err := copyAttemptUnpackNonArchiveSingleFile(
		srcPath,
		"git-checkout-tag-with-hash.sh",
		destPath,
		nil,
		nil,
		true,
	)
	require.NoError(t, err)
	require.True(t, copied)

	destInfo, err := os.Stat(destPath)
	require.NoError(t, err)
	require.True(t, destInfo.IsDir())

	got, err := os.ReadFile(filepath.Join(destPath, "git-checkout-tag-with-hash.sh"))
	require.NoError(t, err)
	require.Equal(t, "#!/bin/sh\necho ok\n", string(got))
}

func TestCopyAttemptUnpackNonArchiveSingleFileNoDestDirHintCopiesToExactPath(t *testing.T) {
	t.Parallel()

	srcPath := filepath.Join(t.TempDir(), "archive.tar")
	require.NoError(t, os.WriteFile(srcPath, []byte("not an archive"), 0o644))

	destPath := filepath.Join(t.TempDir(), "out")
	copied, err := copyAttemptUnpackNonArchiveSingleFile(
		srcPath,
		"archive.tar",
		destPath,
		nil,
		nil,
		false,
	)
	require.NoError(t, err)
	require.True(t, copied)

	destInfo, err := os.Stat(destPath)
	require.NoError(t, err)
	require.False(t, destInfo.IsDir())
}
