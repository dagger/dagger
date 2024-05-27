package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (DirectorySuite) TestLocalImportsAcrossSessions(ctx context.Context, t *testctx.T) {
	tmpdir := t.TempDir()

	c1 := connect(ctx, t)

	fileName := "afile"
	err := os.WriteFile(filepath.Join(tmpdir, fileName), []byte("1"), 0o644)
	require.NoError(t, err)

	hostDir1 := c1.Host().Directory(tmpdir)

	out1, err := c1.Container().From(alpineImage).
		WithMountedDirectory("/mnt", hostDir1).
		WithExec([]string{"cat", "/mnt/" + fileName}).
		Stdout(ctx)
	require.NoError(t, err)
	out1 = strings.TrimSpace(out1)
	require.Equal(t, "1", out1)

	// repeat with new session but overwrite the host file before it's loaded

	err = os.WriteFile(filepath.Join(tmpdir, fileName), []byte("2"), 0o644)
	require.NoError(t, err)
	// just do a sanity check the file contents are what we just wrote
	contents, err := os.ReadFile(filepath.Join(tmpdir, fileName))
	require.NoError(t, err)
	require.Equal(t, "2", string(contents))

	c2 := connect(ctx, t)

	hostDir2 := c2.Host().Directory(tmpdir)

	out2, err := c2.Container().From(alpineImage).
		WithMountedDirectory("/mnt", hostDir2).
		WithExec([]string{"cat", "/mnt/" + fileName}).
		Stdout(ctx)
	require.NoError(t, err)
	out2 = strings.TrimSpace(out2)
	require.Equal(t, "2", out2)
}
