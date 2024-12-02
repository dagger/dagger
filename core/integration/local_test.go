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

func (LocalDirSuite) TestLocalImportParallel(ctx context.Context, t *testctx.T) {
	fullDir := fstest.Apply(
		fstest.CreateDir("/a1", 0o755),
		fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o600),
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
		fstest.Symlink("/a1/f1.txt", "/a2/link.f1.txt"),
		fstest.CreateDir("/a2/b1", 0o755),
		fstest.CreateFile("/a2/b1/f1.txt", []byte("7"), 0o644),
		fstest.CreateFile("/a2/b1/f2.txt", []byte("8"), 0o644),
		fstest.CreateDir("/a2/b2", 0o700),
		fstest.CreateFile("/a2/b2/f3.rar", []byte("9"), 0o644),
		fstest.CreateFile("/a2/b2/f4.zip", []byte("10"), 0o644),
		fstest.Symlink("/a2/b1/f1.txt", "/a2/b2/link.f1.rar"), // purposeful different extension
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

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("/a1", 0o755),
			fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o600),
			fstest.CreateDir("/a1/b1", 0o755),
			fstest.CreateFile("/a1/b1/f1.zip", []byte("2"), 0o644),
			fstest.CreateFile("/a1/b1/f2.rar", []byte("3"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "/a1/b1/link.f4.zip"),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f3.txt", []byte("4"), 0o644),
			fstest.CreateFile("/a1/b2/f4.zip", []byte("5"), 0o644),
			fstest.Symlink("/a1/f1.txt", "/a1/b2/link.f1.rar"),

			fstest.CreateDir("/a2", 0o755),
			fstest.CreateFile("/a2/f1.zip", []byte("6"), 0o644),
			fstest.Symlink("/a1/f1.txt", "/a2/link.f1.txt"),
			fstest.CreateDir("/a2/b1", 0o755),
			fstest.CreateFile("/a2/b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("/a2/b1/f2.txt", []byte("8"), 0o644),
			fstest.CreateDir("/a2/b2", 0o700),
			fstest.CreateFile("/a2/b2/f3.rar", []byte("9"), 0o644),
			fstest.CreateFile("/a2/b2/f4.zip", []byte("10"), 0o644),
			fstest.Symlink("/a2/b1/f1.txt", "/a2/b2/link.f1.rar"),
		))
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
			fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o600),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f3.txt", []byte("4"), 0o644),

			fstest.CreateDir("/a2", 0o755),
			fstest.Symlink("/a1/f1.txt", "/a2/link.f1.txt"),
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
			fstest.Symlink("/a1/f1.txt", "link.f1.txt"),
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("b1/f2.txt", []byte("8"), 0o644),
			fstest.CreateDir("b2", 0o700),
			fstest.CreateFile("b2/f3.rar", []byte("9"), 0o644),
			fstest.CreateFile("b2/f4.zip", []byte("10"), 0o644),
			fstest.Symlink("/a2/b1/f1.txt", "b2/link.f1.rar"),
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

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("/a1", 0o755),
			fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o600),
			fstest.CreateDir("/a1/b1", 0o755),
			fstest.CreateFile("/a1/b1/f1.zip", []byte("2"), 0o644),
			fstest.CreateFile("/a1/b1/f2.rar", []byte("3"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "/a1/b1/link.f4.zip"),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f3.txt", []byte("4"), 0o644),
			fstest.CreateFile("/a1/b2/f4.zip", []byte("5"), 0o644),
			fstest.Symlink("/a1/f1.txt", "/a1/b2/link.f1.rar"),

			fstest.CreateDir("/a2", 0o755),
			fstest.CreateFile("/a2/f1.zip", []byte("6"), 0o644),
			fstest.Symlink("/a1/f1.txt", "/a2/link.f1.txt"),
			fstest.CreateDir("/a2/b1", 0o755),
			fstest.CreateFile("/a2/b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("/a2/b1/f2.txt", []byte("8"), 0o644),
			fstest.CreateDir("/a2/b2", 0o700),
			fstest.CreateFile("/a2/b2/f3.rar", []byte("9"), 0o644),
			fstest.CreateFile("/a2/b2/f4.zip", []byte("10"), 0o644),
			fstest.Symlink("/a2/b1/f1.txt", "/a2/b2/link.f1.rar"),
		))
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
			fstest.CreateDir("/a2/b2", 0o700),
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
			fstest.CreateFile("f1.txt", []byte("1"), 0o600),
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
			fstest.CreateDir("b2", 0o700),
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
		fstest.Remove("/a1/b1/f1.zip"),
		fstest.CreateFile("/a1/b1/f1.new.zip", []byte("11"), 0o644),
		fstest.Remove("/a1/b2/f3.txt"),
		fstest.CreateFile("/a1/b2/f3.txt", []byte("12"), 0o644),
		fstest.Remove("/a2/link.f1.txt"),
		fstest.Symlink("/a1/b2/f3.txt", "/a2/link.f1.txt"),
		fstest.RemoveAll("/a2/b2"),
		fstest.Chmod("/a1/b2/f4.zip", 0o600),
		fstest.Chmod("/a2", 0o700),
	)
	require.NoError(t, fullDirChanges.Apply(root))

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

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("/a1", 0o755),
			fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o600),
			fstest.CreateDir("/a1/b1", 0o755),
			fstest.CreateFile("/a1/b1/f1.new.zip", []byte("11"), 0o644),
			fstest.CreateFile("/a1/b1/f2.rar", []byte("3"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "/a1/b1/link.f4.zip"),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f3.txt", []byte("12"), 0o644),
			fstest.CreateFile("/a1/b2/f4.zip", []byte("5"), 0o600),
			fstest.Symlink("/a1/f1.txt", "/a1/b2/link.f1.rar"),

			fstest.CreateDir("/a2", 0o700),
			fstest.CreateFile("/a2/f1.zip", []byte("6"), 0o644),
			fstest.Symlink("/a1/b2/f3.txt", "/a2/link.f1.txt"),
			fstest.CreateDir("/a2/b1", 0o755),
			fstest.CreateFile("/a2/b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("/a2/b1/f2.txt", []byte("8"), 0o644),
		))
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
			fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o600),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f3.txt", []byte("12"), 0o644),

			fstest.CreateDir("/a2", 0o700),
			fstest.Symlink("/a1/b2/f3.txt", "/a2/link.f1.txt"),
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
			fstest.Symlink("/a1/b2/f3.txt", "link.f1.txt"),
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

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateDir("/a1", 0o755),
			fstest.CreateFile("/a1/f1.txt", []byte("1"), 0o600),
			fstest.CreateDir("/a1/b1", 0o755),
			fstest.CreateFile("/a1/b1/f1.new.zip", []byte("11"), 0o644),
			fstest.CreateFile("/a1/b1/f2.rar", []byte("3"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "/a1/b1/link.f4.zip"),
			fstest.CreateDir("/a1/b2", 0o755),
			fstest.CreateFile("/a1/b2/f3.txt", []byte("12"), 0o644),
			fstest.CreateFile("/a1/b2/f4.zip", []byte("5"), 0o600),
			fstest.Symlink("/a1/f1.txt", "/a1/b2/link.f1.rar"),

			fstest.CreateDir("/a2", 0o700),
			fstest.CreateFile("/a2/f1.zip", []byte("6"), 0o644),
			fstest.Symlink("/a1/b2/f3.txt", "/a2/link.f1.txt"),
			fstest.CreateDir("/a2/b1", 0o755),
			fstest.CreateFile("/a2/b1/f1.txt", []byte("7"), 0o644),
			fstest.CreateFile("/a2/b1/f2.txt", []byte("8"), 0o644),
		))
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
			fstest.CreateFile("/a1/b2/f4.zip", []byte("5"), 0o600),

			fstest.CreateDir("/a2", 0o700),
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
			fstest.CreateFile("f1.txt", []byte("1"), 0o600),
			fstest.CreateDir("b1", 0o755),
			fstest.CreateFile("b1/f1.new.zip", []byte("11"), 0o644),
			fstest.CreateFile("b1/f2.rar", []byte("3"), 0o644),
			fstest.Symlink("/a1/b2/f4.zip", "b1/link.f4.zip"),
			fstest.CreateDir("b2", 0o755),
			fstest.CreateFile("b2/f3.txt", []byte("12"), 0o644),
			fstest.CreateFile("b2/f4.zip", []byte("5"), 0o600),
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

	require.Equal(t, dgst1A, dgst2A)
	require.NotEqual(t, dgst2A, dgst3A)
	require.Equal(t, dgst3A, dgst4A)
}

func (LocalDirSuite) TestLocalHardlinks(ctx context.Context, t *testctx.T) {
	fullDir := fstest.Apply(
		fstest.CreateDir("dirA", 0o755),
		fstest.CreateFile("dirA/a", []byte("a"), 0o644),
		fstest.CreateDir("dirB", 0o755),
		fstest.Link("dirA/a", "dirB/b"),
		fstest.Link("dirB/b", "c"),
	)

	var err error

	root1 := t.TempDir()
	require.NoError(t, fullDir.Apply(root1))
	c1 := connect(ctx, t)

	inDir1Root := c1.Host().Directory(root1)
	dgst1Root, err := inDir1Root.Digest(ctx)
	require.NoError(t, err)
	outDir1Root := t.TempDir()
	_, err = inDir1Root.Export(ctx, outDir1Root)
	require.NoError(t, err)
	require.NoError(t, fstest.CheckDirectoryEqualWithApplier(outDir1Root, fullDir))

	require.NoError(t, c1.Close())

	root2 := t.TempDir()
	require.NoError(t, fullDir.Apply(root2))
	c2 := connect(ctx, t)

	inDir2A := c2.Host().Directory(filepath.Join(root2, "dirA"))
	outDir2A := t.TempDir()
	_, err = inDir2A.Export(ctx, outDir2A)
	require.NoError(t, err)
	require.NoError(t, fstest.CheckDirectoryEqualWithApplier(outDir2A, fstest.Apply(
		fstest.CreateFile("a", []byte("a"), 0o644),
	)))

	inDir2B := c2.Host().Directory(filepath.Join(root2, "dirB"))
	outDir2B := t.TempDir()
	_, err = inDir2B.Export(ctx, outDir2B)
	require.NoError(t, err)
	require.NoError(t, fstest.CheckDirectoryEqualWithApplier(outDir2B, fstest.Apply(
		fstest.CreateFile("b", []byte("a"), 0o644),
	)))

	inDir2Root := c2.Host().Directory(root2)
	dgst2Root, err := inDir2Root.Digest(ctx)
	require.NoError(t, err)
	outDir2Root := t.TempDir()
	_, err = inDir2Root.Export(ctx, outDir2Root)
	require.NoError(t, err)
	require.NoError(t, fstest.CheckDirectoryEqualWithApplier(outDir2Root, fullDir))

	require.Equal(t, dgst1Root, dgst2Root)

	require.NoError(t, c2.Close())

	fullDirChanges := fstest.Apply(
		// replace hardlink with its own file
		fstest.Remove("dirB/b"),
		fstest.CreateFile("dirB/b", []byte("b"), 0o644),

		// rewrite hardlink to point to a different file
		fstest.Remove("c"),
		fstest.Link("dirB/b", "c"),

		// add a new hardlink
		fstest.Link("dirA/a", "z"),
	)
	require.NoError(t, fullDirChanges.Apply(root1))
	c3 := connect(ctx, t)

	inDir3Root := c3.Host().Directory(root1)
	dgst3Root, err := inDir3Root.Digest(ctx)
	require.NoError(t, err)
	outDir3Root := t.TempDir()
	_, err = inDir3Root.Export(ctx, outDir3Root)
	require.NoError(t, err)
	require.NoError(t, fstest.CheckDirectoryEqualWithApplier(outDir3Root, fstest.Apply(
		fstest.CreateDir("dirA", 0o755),
		fstest.CreateFile("dirA/a", []byte("a"), 0o644),
		fstest.CreateDir("dirB", 0o755),
		fstest.CreateFile("dirB/b", []byte("b"), 0o644),
		fstest.Link("dirB/b", "c"),
		fstest.Link("dirA/a", "z"),
	)))

	require.NotEqual(t, dgst1Root, dgst3Root)

	require.NoError(t, c3.Close())

	fullDirChanges = fstest.Apply(
		// replace hardlink with its own file that's the same as before but its own inode
		fstest.Remove("z"),
		fstest.CreateFile("z", []byte("a"), 0o644),
	)
	require.NoError(t, fullDirChanges.Apply(root1))
	c4 := connect(ctx, t)

	inDir4Root := c4.Host().Directory(root1)
	dgst4Root, err := inDir4Root.Digest(ctx)
	require.NoError(t, err)
	outDir4Root := t.TempDir()
	_, err = inDir4Root.Export(ctx, outDir4Root)
	require.NoError(t, err)
	require.NoError(t, fstest.CheckDirectoryEqualWithApplier(outDir4Root, fstest.Apply(
		fstest.CreateDir("dirA", 0o755),
		fstest.CreateFile("dirA/a", []byte("a"), 0o644),
		fstest.CreateDir("dirB", 0o755),
		fstest.CreateFile("dirB/b", []byte("b"), 0o644),
		fstest.Link("dirB/b", "c"),

		// NOTE: due to the fact that content digests don't account for hardlink status, just the actual
		// file contents whether or not it is hardlink'd elsewhere, we actually end up with the same digest
		// as the previous load and thus still export the hardlink'd "z" file as opposed to the standalone
		// one (which is otherwise identical).
		//
		// This is debatable behavior but given the various inconstencies around how hardlinks are handled
		// in different snapshotters, users can't rely on hardlink status being exactly preserved from their
		// host filesystem anyways. This behavior also seems to have existed for a while (i.e. before
		// filesync refactorizing), so calling this "expected" for the time being.
		//
		// fstest.CreateFile("z", []byte("a"), 0o644),
		fstest.Link("dirA/a", "z"),
	)))

	require.Equal(t, dgst3Root, dgst4Root)

	require.NoError(t, c4.Close())
}

func (LocalDirSuite) TestLocalParentSymlinks(ctx context.Context, t *testctx.T) {
	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		fullDir := fstest.Apply(
			fstest.CreateDir("dirA", 0o755),
			fstest.CreateDir("dirA/dirB", 0o755),
			fstest.CreateFile("dirA/dirB/f", []byte("f"), 0o644),
			fstest.Symlink("dirA", "dirALink"),
			fstest.Symlink("../dirA/dirB", "dirA/dirBLink"),
		)

		root := t.TempDir()
		require.NoError(t, fullDir.Apply(root))
		c := connect(ctx, t)

		inDir := c.Host().Directory(filepath.Join(root, "dirALink/dirBLink"))
		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f", []byte("f"), 0o644),
		))
		require.NoError(t, err)
	})

	t.Run("nested symlinks", func(ctx context.Context, t *testctx.T) {
		fullDir := fstest.Apply(
			fstest.CreateDir("dirA", 0o755),
			fstest.CreateDir("dirA/dirB", 0o755),
			fstest.CreateFile("dirA/dirB/f", []byte("f"), 0o644),
			fstest.Symlink("dirA", "dirALink"),
			fstest.Symlink("../dirALink/dirB", "dirA/dirBLink"), // link points to a path with yet another symlink
		)

		root := t.TempDir()
		require.NoError(t, fullDir.Apply(root))
		c := connect(ctx, t)

		inDir := c.Host().Directory(filepath.Join(root, "dirALink/dirBLink"))
		outDir := t.TempDir()
		_, err := inDir.Export(ctx, outDir)
		require.NoError(t, err)

		err = fstest.CheckDirectoryEqualWithApplier(outDir, fstest.Apply(
			fstest.CreateFile("f", []byte("f"), 0o644),
		))
		require.NoError(t, err)
	})
}
