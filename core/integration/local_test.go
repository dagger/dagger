package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/containerd/continuity/fs/fstest"
	"github.com/dagger/dagger/testctx"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

type LocalDirSuite struct{}

func TestLocalDir(t *testing.T) {
	testctx.Run(testCtx, t, LocalDirSuite{}, Middleware()...)
}

func (LocalDirSuite) TestLocalImportsAcrossSessions(ctx context.Context, t *testctx.T) {
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
* load + modify in single session
* load + modify in separate sessions (serial)
* load + modify in separate sessions (parallel)
* include/exclude (serial)
* include/exclude (parallel)
* concurrent change during load? if possible?
* Ensure include/exclude can't escape synced dir
 */

func (LocalDirSuite) TestLocalImportParallel(ctx context.Context, t *testctx.T) {
	// TODO: throw in some symlinks and hardlinks for fun
	fullDir := fstest.Apply(
		fstest.CreateDir("/a1", 0o755),
		fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o644),
		fstest.CreateDir("/a1/b1", 0o755),
		fstest.CreateFile("/a1/b1/f1.zip", []byte("2"), 0o644),
		fstest.CreateFile("/a1/b1/f2.rar", []byte("3"), 0o644),
		fstest.CreateDir("/a1/b2", 0o755),
		fstest.CreateFile("/a1/b2/f3.txt", []byte("4"), 0o644),
		fstest.CreateFile("/a1/b2/f4.zip", []byte("5"), 0o644),

		fstest.CreateDir("/a2", 0o755),
		fstest.CreateFile("/a2/f1.zip", []byte("6"), 0o644),
		fstest.CreateDir("/a2/b1", 0o755),
		fstest.CreateFile("/a2/b1/f1.txt", []byte("7"), 0o644),
		fstest.CreateFile("/a2/b1/f2.txt", []byte("8"), 0o644),
		fstest.CreateDir("/a2/b2", 0o755),
		fstest.CreateFile("/a2/b2/f3.rar", []byte("9"), 0o644),
		fstest.CreateFile("/a2/b2/f4.zip", []byte("10"), 0o644),
	)

	root := t.TempDir()
	err := fullDir.Apply(root)
	require.NoError(t, err)

	c1 := connect(ctx, t)
	c2 := connect(ctx, t)

	// TODO: calc digest of loaded dirs and compare?
	// TODO: use exclude too

	var eg errgroup.Group
	startCh := make(chan struct{})

	eg.Go(func() error {
		<-startCh

		outDir := t.TempDir()
		_, err := c1.Host().Directory(root).Export(ctx, outDir)
		if err != nil {
			return err
		}

		return fstest.CheckDirectoryEqualWithApplier(outDir, fullDir)
	})
	eg.Go(func() error {
		<-startCh

		outDir := t.TempDir()
		_, err := c1.Host().Directory(root, dagger.HostDirectoryOpts{
			Include: []string{"**.txt"},
		}).Export(ctx, outDir)
		if err != nil {
			return err
		}

		return fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("/a1", 0o755),
			fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o644),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f3.txt", []byte("4"), 0o644),

			fstest.CreateDir("/a2", 0o755),
			fstest.CreateDir("/a2/b1", 0o755),
			fstest.CreateFile("/a2/b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("/a2/b1/f2.txt", []byte("8"), 0o644),
		))
	})
	eg.Go(func() error {
		<-startCh

		outDir := t.TempDir()
		_, err := c1.Host().Directory(filepath.Join(root, "a1/b1")).Export(ctx, outDir)
		if err != nil {
			return err
		}

		return fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.zip", []byte("2"), 0o644),
			fstest.CreateFile("f2.rar", []byte("3"), 0o644),
		))
	})
	eg.Go(func() error {
		<-startCh

		outDir := t.TempDir()
		_, err := c1.Host().Directory(filepath.Join(root, "a2")).Export(ctx, outDir)
		if err != nil {
			return err
		}

		return fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.zip", []byte("6"), 0o644),
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("b1/f2.txt", []byte("8"), 0o644),
			fstest.CreateDir("b2", 0o755),
			fstest.CreateFile("b2/f3.rar", []byte("9"), 0o644),
			fstest.CreateFile("b2/f4.zip", []byte("10"), 0o644),
		))
	})
	eg.Go(func() error {
		<-startCh

		outDir := t.TempDir()
		_, err := c1.Host().Directory(filepath.Join(root, "a1"), dagger.HostDirectoryOpts{
			Include: []string{"**.rar"},
		}).Export(ctx, outDir)
		if err != nil {
			return err
		}

		return fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f2.rar", []byte("3"), 0o644),
		))
	})

	eg.Go(func() error {
		<-startCh

		outDir := t.TempDir()
		_, err := c2.Host().Directory(root).Export(ctx, outDir)
		if err != nil {
			return err
		}

		return fstest.CheckDirectoryEqualWithApplier(outDir, fullDir)
	})
	eg.Go(func() error {
		<-startCh

		outDir := t.TempDir()
		_, err := c2.Host().Directory(root, dagger.HostDirectoryOpts{
			Include: []string{"**.zip"},
		}).Export(ctx, outDir)
		if err != nil {
			return err
		}

		return fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("/a1", 0o755),
			fstest.CreateDir("/a1/b1", 0o755),
			fstest.CreateFile("/a1/b1/f1.zip", []byte("2"), 0o644),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f4.zip", []byte("5"), 0o644),

			fstest.CreateDir("/a2", 0o755),
			fstest.CreateFile("/a2/f1.zip", []byte("6"), 0o644),
			fstest.CreateDir("/a2/b2", 0o755),
			fstest.CreateFile("/a2/b2/f4.zip", []byte("10"), 0o644),
		))
	})
	eg.Go(func() error {
		<-startCh

		outDir := t.TempDir()
		_, err := c2.Host().Directory(filepath.Join(root, "a2/b1")).Export(ctx, outDir)
		if err != nil {
			return err
		}

		return fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("f2.txt", []byte("8"), 0o644),
		))
	})
	eg.Go(func() error {
		<-startCh

		outDir := t.TempDir()
		_, err := c2.Host().Directory(filepath.Join(root, "a1")).Export(ctx, outDir)
		if err != nil {
			return err
		}

		return fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.txt", []byte("1"), 0o644),
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f1.zip", []byte("2"), 0o644),
			fstest.CreateFile("b1/f2.rar", []byte("3"), 0o644),
			fstest.CreateDir("b2", 0o755),
			fstest.CreateFile("b2/f3.txt", []byte("4"), 0o644),
			fstest.CreateFile("b2/f4.zip", []byte("5"), 0o644),
		))
	})
	eg.Go(func() error {
		<-startCh

		outDir := t.TempDir()
		_, err := c2.Host().Directory(filepath.Join(root, "a2"), dagger.HostDirectoryOpts{
			Include: []string{"**.zip"},
		}).Export(ctx, outDir)
		if err != nil {
			return err
		}

		return fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.zip", []byte("6"), 0o644),
			fstest.CreateDir("b2", 0o755),
			fstest.CreateFile("b2/f4.zip", []byte("10"), 0o644),
		))
	})

	close(startCh)
	err = eg.Wait()
	require.NoError(t, err)
}
