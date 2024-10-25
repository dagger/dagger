package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
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

/* TODO: new tests
* Ensure include/exclude can't escape synced dir
* Excluding a file doesn't remove it from cache (tough, but very nice to have)
 */

func (DirectorySuite) TestTODO(ctx context.Context, t *testctx.T) {
	tmpdir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(tmpdir, "FOOFOO"), []byte("1"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpdir, "BARBAR"), []byte("2"), 0o644))

	c := connect(ctx, t)

	hostDir1 := c.Host().Directory(tmpdir)
	out1, err := c.Container().From(alpineImage).
		WithMountedDirectory("/mnt", hostDir1).
		WithExec([]string{"ls", "/mnt"}).
		Stdout(ctx)
	require.NoError(t, err)
	var _ = out1

	out, err := exec.CommandContext(ctx, "docker", "exec", "dagger-engine.dev", "sh", "-c",
		"ls -la /var/lib/dagger/worker/snapshots/snapshots/2/fs/tmp/*/*",
	).CombinedOutput()
	t.Log(string(out))

	require.NoError(t, c.Close())
	c = connect(ctx, t)

	hostDir2 := c.Host().Directory(tmpdir, dagger.HostDirectoryOpts{
		Exclude: []string{"FOOFOO"},
	})
	out2, err := c.Container().From(alpineImage).
		WithMountedDirectory("/mnt", hostDir2).
		WithExec([]string{"ls", "/mnt"}).
		Stdout(ctx)
	require.NoError(t, err)
	var _ = out2

	out, err = exec.CommandContext(ctx, "docker", "exec", "dagger-engine.dev", "sh", "-c",
		"ls -la /var/lib/dagger/worker/snapshots/snapshots/2/fs/tmp/*/*",
	).CombinedOutput()
	t.Log(string(out))

	require.NotEqual(t, out1, out2)

	require.NoError(t, c.Close())
	c = connect(ctx, t)

	require.NoError(t, os.Remove(filepath.Join(tmpdir, "FOOFOO")))

	hostDir3 := c.Host().Directory(tmpdir, dagger.HostDirectoryOpts{
		Exclude: []string{"FOOFOO"},
	})
	out3, err := c.Container().From(alpineImage).
		WithMountedDirectory("/mnt", hostDir3).
		WithExec([]string{"ls", "/mnt"}).
		Stdout(ctx)
	require.NoError(t, err)
	var _ = out3

	out, err = exec.CommandContext(ctx, "docker", "exec", "dagger-engine.dev", "sh", "-c",
		"ls -la /var/lib/dagger/worker/snapshots/snapshots/2/fs/tmp/*/*",
	).CombinedOutput()
	t.Log(string(out))

	require.Equal(t, out2, out3)

	require.NoError(t, c.Close())
	c = connect(ctx, t)

	hostDir4 := c.Host().Directory(tmpdir)
	out4, err := c.Container().From(alpineImage).
		WithMountedDirectory("/mnt", hostDir4).
		WithExec([]string{"ls", "/mnt"}).
		Stdout(ctx)
	require.NoError(t, err)
	var _ = out4

	out, err = exec.CommandContext(ctx, "docker", "exec", "dagger-engine.dev", "sh", "-c",
		"ls -la /var/lib/dagger/worker/snapshots/snapshots/2/fs/tmp/*/*",
	).CombinedOutput()
	t.Log(string(out))

	require.Equal(t, out2, out4)
}
