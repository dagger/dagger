package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocalImportsAcrossSessions(t *testing.T) {
	t.Parallel()
	tmpdir := t.TempDir()

	c1, ctx1 := connect(t)

	fileName := "afile"
	err := os.WriteFile(filepath.Join(tmpdir, fileName), []byte("1"), 0644)
	require.NoError(t, err)

	hostDir1 := c1.Host().Directory(tmpdir)

	out1, err := c1.Container().From("alpine:3.16.2").
		WithMountedDirectory("/mnt", hostDir1).
		WithExec([]string{"cat", "/mnt/" + fileName}).
		Stdout(ctx1)
	require.NoError(t, err)
	out1 = strings.TrimSpace(out1)
	require.Equal(t, "1", out1)

	// repeat with new session but overwrite the host file before it's loaded

	err = os.WriteFile(filepath.Join(tmpdir, fileName), []byte("2"), 0644)
	require.NoError(t, err)
	// just do a sanity check the file contents are what we just wrote
	contents, err := os.ReadFile(filepath.Join(tmpdir, fileName))
	require.NoError(t, err)
	require.Equal(t, "2", string(contents))

	c2, ctx2 := connect(t)

	hostDir2 := c2.Host().Directory(tmpdir)

	out2, err := c2.Container().From("alpine:3.18").
		WithMountedDirectory("/mnt", hostDir2).
		WithExec([]string{"cat", "/mnt/" + fileName}).
		Stdout(ctx2)
	require.NoError(t, err)
	out2 = strings.TrimSpace(out2)
	require.Equal(t, "2", out2)
}
