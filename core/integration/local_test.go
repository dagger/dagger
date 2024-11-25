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
* symlinks + hardlinks
* exclude
* concurrent change during load? if possible?
* Ensure include/exclude can't escape synced dir
 */

func (LocalDirSuite) TestLocalImportParallel(ctx context.Context, t *testctx.T) {
	fullDir := fstest.Apply(
		fstest.CreateDir("/a1", 0o755),
		fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o644),
		fstest.CreateDir("/a1/b1", 0o755),
		fstest.CreateFile("/a1/b1/f1.zip", []byte("2"), 0o644),
		fstest.CreateFile("/a1/b1/f2.rar", []byte("3"), 0o644),
		fstest.Symlink("/a1/b2/f4.zip", "/a1/b1/link.f4.zip"),
		fstest.CreateDir("/a1/b2", 0o755),
		fstest.CreateFile("/a1/b2/f3.txt", []byte("4"), 0o644),
		fstest.CreateFile("/a1/b2/f4.zip", []byte("5"), 0o644),
		fstest.Symlink("/a1/f1.txt", "/a1/b2/link.f1.rar"), // purposeful different extension

		fstest.CreateDir("/a2", 0o755),
		fstest.CreateFile("/a2/f1.zip", []byte("6"), 0o644),
		fstest.Link("/a1/f1.txt", "/a2/hardlink.f1.txt"),
		fstest.CreateDir("/a2/b1", 0o755),
		fstest.CreateFile("/a2/b1/f1.txt", []byte("7"), 0o644),
		fstest.CreateFile("/a2/b1/f2.txt", []byte("8"), 0o644),
		fstest.CreateDir("/a2/b2", 0o755),
		fstest.CreateFile("/a2/b2/f3.rar", []byte("9"), 0o644),
		fstest.CreateFile("/a2/b2/f4.zip", []byte("10"), 0o644),
		fstest.Link("/a2/b1/f1.txt", "/a2/b2/hardlink.f1.txt"), // purposeful different extension
	)

	root := t.TempDir()
	err := fullDir.Apply(root)
	require.NoError(t, err)

	c1 := connect(ctx, t)
	c2 := connect(ctx, t)

	var eg errgroup.Group
	startCh := make(chan struct{})

	var dgst1A string
	eg.Go(func() error {
		<-startCh

		inDir := c1.Host().Directory(root)

		var err error
		dgst1A, err = inDir.Digest(ctx)
		require.NoError(t, err)

		outDir := t.TempDir()
		_, err = inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fullDir)
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c1.Host().Directory(root, dagger.HostDirectoryOpts{
			Include: []string{"**.txt"},
		})

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("/a1", 0o755),
			fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o644),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f3.txt", []byte("4"), 0o644),

			fstest.CreateDir("/a2", 0o755),
			fstest.CreateDir("/a2/b1", 0o755),
			fstest.CreateFile("/a2/b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("/a2/b1/f2.txt", []byte("8"), 0o644),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c1.Host().Directory(filepath.Join(root, "a1/b1"))

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.zip", []byte("2"), 0o644),
			fstest.CreateFile("f2.rar", []byte("3"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "link.f4.zip"),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c1.Host().Directory(filepath.Join(root, "a2"))

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.zip", []byte("6"), 0o644),
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("b1/f2.txt", []byte("8"), 0o644),
			fstest.CreateDir("b2", 0o755),
			fstest.CreateFile("b2/f3.rar", []byte("9"), 0o644),
			fstest.CreateFile("b2/f4.zip", []byte("10"), 0o644),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c1.Host().Directory(filepath.Join(root, "a1"), dagger.HostDirectoryOpts{
			Include: []string{"**.rar"},
		})

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f2.rar", []byte("3"), 0o644),
			fstest.CreateDir("b2", 0o755),
			fstest.Symlink("/a1/f1.txt", "b2/link.f1.rar"),
		))
		require.NoError(t, err)
		return nil
	})

	var dgst2A string
	eg.Go(func() error {
		<-startCh

		inDir := c2.Host().Directory(root)

		var err error
		dgst2A, err = inDir.Digest(ctx)
		require.NoError(t, err)

		outDir := t.TempDir()
		_, err = inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fullDir)
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c2.Host().Directory(root, dagger.HostDirectoryOpts{
			Include: []string{"**.zip"},
		})

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("/a1", 0o755),
			fstest.CreateDir("/a1/b1", 0o755),
			fstest.CreateFile("/a1/b1/f1.zip", []byte("2"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "/a1/b1/link.f4.zip"),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f4.zip", []byte("5"), 0o644),

			fstest.CreateDir("/a2", 0o755),
			fstest.CreateFile("/a2/f1.zip", []byte("6"), 0o644),
			fstest.CreateDir("/a2/b2", 0o755),
			fstest.CreateFile("/a2/b2/f4.zip", []byte("10"), 0o644),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c2.Host().Directory(filepath.Join(root, "a2/b1"))

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("f2.txt", []byte("8"), 0o644),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c2.Host().Directory(filepath.Join(root, "a1"))

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.txt", []byte("1"), 0o644),
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f1.zip", []byte("2"), 0o644),
			fstest.CreateFile("b1/f2.rar", []byte("3"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "b1/link.f4.zip"),
			fstest.CreateDir("b2", 0o755),
			fstest.CreateFile("b2/f3.txt", []byte("4"), 0o644),
			fstest.CreateFile("b2/f4.zip", []byte("5"), 0o644),
			fstest.Symlink("/a1/f1.txt", "b2/link.f1.rar"),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c2.Host().Directory(filepath.Join(root, "a2"), dagger.HostDirectoryOpts{
			Include: []string{"**.zip"},
		})

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.zip", []byte("6"), 0o644),
			fstest.CreateDir("b2", 0o755),
			fstest.CreateFile("b2/f4.zip", []byte("10"), 0o644),
		))
		require.NoError(t, err)
		return nil
	})

	close(startCh)
	err = eg.Wait()
	require.NoError(t, err)

	require.Equal(t, dgst1A, dgst2A)

	require.NoError(t, c1.Close())
	require.NoError(t, c2.Close())

	fullDirChanges := fstest.Apply(
		fstest.RemoveAll("/a2/b2"),
		fstest.Remove("/a1/b1/f1.zip"),
		fstest.CreateFile("/a1/b1/f1.new.zip", []byte("11"), 0o644),
		fstest.Remove("/a1/b2/f3.txt"),
		fstest.CreateFile("/a1/b2/f3.txt", []byte("12"), 0o644),
	)
	require.NoError(t, fullDirChanges.Apply(root))
	newFullDir := fstest.Apply(append([]fstest.Applier{fullDir}, fullDirChanges)...)

	c3 := connect(ctx, t)
	c4 := connect(ctx, t)

	eg = errgroup.Group{}
	startCh = make(chan struct{})

	var dgst3A string
	eg.Go(func() error {
		<-startCh

		inDir := c3.Host().Directory(root)

		var err error
		dgst3A, err = inDir.Digest(ctx)
		require.NoError(t, err)

		outDir := t.TempDir()
		_, err = inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, newFullDir)
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c3.Host().Directory(root, dagger.HostDirectoryOpts{
			Include: []string{"**.txt"},
		})

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("/a1", 0o755),
			fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o644),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f3.txt", []byte("12"), 0o644),

			fstest.CreateDir("/a2", 0o755),
			fstest.CreateDir("/a2/b1", 0o755),
			fstest.CreateFile("/a2/b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("/a2/b1/f2.txt", []byte("8"), 0o644),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c3.Host().Directory(filepath.Join(root, "a1/b1"))

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.new.zip", []byte("11"), 0o644),
			fstest.CreateFile("f2.rar", []byte("3"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "link.f4.zip"),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c3.Host().Directory(filepath.Join(root, "a2"))

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.zip", []byte("6"), 0o644),
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("b1/f2.txt", []byte("8"), 0o644),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c3.Host().Directory(filepath.Join(root, "a1"), dagger.HostDirectoryOpts{
			Include: []string{"**.rar"},
		})

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f2.rar", []byte("3"), 0o644),
			fstest.CreateDir("b2", 0o755),
			fstest.Symlink("/a1/f1.txt", "b2/link.f1.rar"),
		))
		require.NoError(t, err)
		return nil
	})

	var dgst4A string
	eg.Go(func() error {
		<-startCh

		inDir := c4.Host().Directory(root)

		var err error
		dgst4A, err = inDir.Digest(ctx)
		require.NoError(t, err)

		outDir := t.TempDir()
		_, err = inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, newFullDir)
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c4.Host().Directory(root, dagger.HostDirectoryOpts{
			Include: []string{"**.zip"},
		})

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("/a1", 0o755),
			fstest.CreateDir("/a1/b1", 0o755),
			fstest.CreateFile("/a1/b1/f1.new.zip", []byte("11"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "/a1/b1/link.f4.zip"),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f4.zip", []byte("5"), 0o644),

			fstest.CreateDir("/a2", 0o755),
			fstest.CreateFile("/a2/f1.zip", []byte("6"), 0o644),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c4.Host().Directory(filepath.Join(root, "a2/b1"))

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("f2.txt", []byte("8"), 0o644),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c4.Host().Directory(filepath.Join(root, "a1"))

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.txt", []byte("1"), 0o644),
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f1.new.zip", []byte("11"), 0o644),
			fstest.CreateFile("b1/f2.rar", []byte("3"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "b1/link.f4.zip"),
			fstest.CreateDir("b2", 0o755),
			fstest.CreateFile("b2/f3.txt", []byte("12"), 0o644),
			fstest.CreateFile("b2/f4.zip", []byte("5"), 0o644),
			fstest.Symlink("/a1/f1.txt", "b2/link.f1.rar"),
		))
		require.NoError(t, err)
		return nil
	})
	eg.Go(func() error {
		<-startCh

		inDir := c4.Host().Directory(filepath.Join(root, "a2"), dagger.HostDirectoryOpts{
			Include: []string{"**.zip"},
		})

		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f1.zip", []byte("6"), 0o644),
		))
		require.NoError(t, err)
		return nil
	})

	close(startCh)
	err = eg.Wait()
	require.NoError(t, err)

	require.Equal(t, dgst3A, dgst4A)
}
