package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestLocalImportsAcrossSessions(t *testing.T) {
	ctx := context.Background()

	tmpdir := t.TempDir()

	c1, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	require.NoError(t, err)
	t.Cleanup(func() { c1.Close() })

	fileName := "afile"
	err = os.WriteFile(filepath.Join(tmpdir, fileName), []byte("1"), 0644)
	require.NoError(t, err)

	hostDir1 := c1.Host().Directory(tmpdir)

	out1, err := c1.Container().From("alpine:3.16.2").
		WithMountedDirectory("/mnt", hostDir1).
		WithExec([]string{"cat", "/mnt/" + fileName}).
		Stdout(ctx)
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

	c2, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	require.NoError(t, err)
	t.Cleanup(func() { c2.Close() })

	hostDir2 := c2.Host().Directory(tmpdir)

	out2, err := c2.Container().From("alpine:3.18").
		WithMountedDirectory("/mnt", hostDir2).
		WithExec([]string{"cat", "/mnt/" + fileName}).
		Stdout(ctx)
	require.NoError(t, err)
	out2 = strings.TrimSpace(out2)
	require.Equal(t, "2", out2)
}
