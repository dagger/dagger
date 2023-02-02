package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestHostWorkdir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "foo"), []byte("bar"), 0600)
	require.NoError(t, err)

	ctx := context.Background()
	c, err := dagger.Connect(ctx, dagger.WithWorkdir(dir))
	require.NoError(t, err)
	defer c.Close()

	t.Run("contains the workdir's content", func(t *testing.T) {
		contents, err := c.Container().
			From("alpine:3.16.2").
			WithMountedDirectory("/host", c.Host().Directory(".")).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\n", contents)
	})

	t.Run("does NOT re-sync on each call", func(t *testing.T) {
		err := os.WriteFile(filepath.Join(dir, "fizz"), []byte("buzz"), 0600)
		require.NoError(t, err)

		contents, err := c.Container().
			From("alpine:3.16.2").
			WithMountedDirectory("/host", c.Host().Directory(".")).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\n", contents)
	})
}

func TestHostWorkdirExcludeInclude(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("2"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.txt.rar"), []byte("3"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "sub-file"), []byte("goodbye"), 0600))

	ctx := context.Background()
	c, err := dagger.Connect(ctx, dagger.WithWorkdir(dir))
	require.NoError(t, err)
	defer c.Close()

	t.Run("exclude", func(t *testing.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Exclude: []string{"*.rar"},
		})

		contents, err := c.Container().
			From("alpine:3.16.2").
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a.txt\nb.txt\nsubdir\n", contents)
	})

	t.Run("exclude directory", func(t *testing.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Exclude: []string{"subdir"},
		})

		contents, err := c.Container().
			From("alpine:3.16.2").
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a.txt\nb.txt\nc.txt.rar\n", contents)
	})

	t.Run("include", func(t *testing.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Include: []string{"*.rar"},
		})

		contents, err := c.Container().
			From("alpine:3.16.2").
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "c.txt.rar\n", contents)
	})

	t.Run("exclude overrides include", func(t *testing.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Include: []string{"*.txt"},
			Exclude: []string{"b.txt"},
		})

		contents, err := c.Container().
			From("alpine:3.16.2").
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "a.txt\n", contents)
	})

	t.Run("include does not override exclude", func(t *testing.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Include: []string{"a.txt"},
			Exclude: []string{"*.txt"},
		})

		contents, err := c.Container().
			From("alpine:3.16.2").
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", contents)
	})
}

func TestHostDirectoryRelative(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-file"), []byte("hello"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "some-dir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-dir", "sub-file"), []byte("goodbye"), 0600))

	ctx := context.Background()
	c, err := dagger.Connect(ctx, dagger.WithWorkdir(dir))
	require.NoError(t, err)
	defer c.Close()

	t.Run(". is same as workdir", func(t *testing.T) {
		wdID1, err := c.Host().Directory(".").ID(ctx)
		require.NoError(t, err)

		wdID2, err := c.Host().Directory(".").ID(ctx)
		require.NoError(t, err)

		require.Equal(t, wdID1, wdID2)
	})

	t.Run("./foo is relative to workdir", func(t *testing.T) {
		contents, err := c.Host().Directory("some-dir").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"sub-file"}, contents)
	})

	t.Run("../ does not allow escaping", func(t *testing.T) {
		_, err := c.Host().Directory("../").ID(ctx)
		require.Error(t, err)

		// don't reveal the workdir location
		require.NotContains(t, err.Error(), dir)
	})
}

func TestHostDirectoryAbsolute(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-file"), []byte("hello"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "some-dir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-dir", "sub-file"), []byte("goodbye"), 0600))

	ctx := context.Background()
	c, err := dagger.Connect(ctx, dagger.WithWorkdir(dir))
	require.NoError(t, err)
	defer c.Close()

	entries, err := c.Host().Directory(filepath.Join(dir, "some-dir")).Entries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"sub-file"}, entries)
}

func TestHostDirectoryExcludeInclude(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("2"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c.txt.rar"), []byte("3"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "d.txt"), []byte("1"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "e.txt"), []byte("2"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "f.txt.rar"), []byte("3"), 0600))

	c, ctx := connect(t)
	defer c.Close()

	t.Run("exclude", func(t *testing.T) {
		entries, err := c.Host().Directory(dir, dagger.HostDirectoryOpts{
			Exclude: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt", "b.txt", "subdir"}, entries)
	})

	t.Run("include", func(t *testing.T) {
		entries, err := c.Host().Directory(dir, dagger.HostDirectoryOpts{
			Include: []string{"*.rar"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"c.txt.rar"}, entries)
	})

	t.Run("exclude overrides include", func(t *testing.T) {
		entries, err := c.Host().Directory(dir, dagger.HostDirectoryOpts{
			Include: []string{"*.txt"},
			Exclude: []string{"b.txt"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a.txt"}, entries)
	})

	t.Run("include does not override exclude", func(t *testing.T) {
		entries, err := c.Host().Directory(dir, dagger.HostDirectoryOpts{
			Include: []string{"a.txt"},
			Exclude: []string{"*.txt"},
		}).Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{}, entries)
	})
}

func TestHostVariable(t *testing.T) {
	t.Parallel()

	require.NoError(t, os.Setenv("HELLO_TEST", "hello"))

	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	secret := c.Host().EnvVariable("HELLO_TEST")

	varValue, err := secret.Value(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", varValue)

	env, err := c.Container().
		From("alpine:3.16.2").
		WithSecretVariable("SECRET", secret.Secret()).
		WithExec([]string{"env"}).
		Stdout(ctx)
	require.NoError(t, err)

	require.Contains(t, env, "SECRET=***")
}
