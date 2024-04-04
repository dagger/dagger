package core

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"

	"dagger.io/dagger"
)

func TestHostWorkdir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "foo"), []byte("bar"), 0600)
	require.NoError(t, err)

	c, ctx := connect(t, dagger.WithWorkdir(dir))

	t.Run("contains the workdir's content", func(t *testing.T) {
		contents, err := c.Container().
			From(alpineImage).
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
			From(alpineImage).
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

	c, ctx := connect(t, dagger.WithWorkdir(dir))

	t.Run("exclude", func(t *testing.T) {
		wd := c.Host().Directory(".", dagger.HostDirectoryOpts{
			Exclude: []string{"*.rar"},
		})

		contents, err := c.Container().
			From(alpineImage).
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
			From(alpineImage).
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
			From(alpineImage).
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
			From(alpineImage).
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
			From(alpineImage).
			WithMountedDirectory("/host", wd).
			WithExec([]string{"ls", "/host"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "", contents)
	})
}

func TestHostDirectoryRelative(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-file"), []byte("hello"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "some-dir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-dir", "sub-file"), []byte("goodbye"), 0600))

	c, ctx := connect(t, dagger.WithWorkdir(dir))

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

func TestHostSetSecretFile(t *testing.T) {
	t.Parallel()

	// Generate 512000 random bytes (non UTF-8)
	// This is our current limit: secrets break at 512001 bytes
	data := make([]byte, 512000)
	_, err := rand.Read(data)
	if err != nil {
		panic(err)
	}

	// Compute the MD5 hash of the data
	hash := md5.Sum(data)
	hashStr := hex.EncodeToString(hash[:])

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-file"), data, 0600))

	c, ctx := connect(t, dagger.WithWorkdir(dir))

	t.Run("non utf8 binary data is properly set as secret", func(t *testing.T) {
		secret := c.Host().SetSecretFile("mysecret", filepath.Join(dir, "some-file"))

		output, err := c.Container().From(alpineImage).
			WithEnvVariable("CACHEBUST", identity.NewID()).
			WithMountedSecret("/mysecret", secret).
			WithExec([]string{"md5sum", "/mysecret"}).
			Stdout(ctx)

		require.NoError(t, err)

		// Extract the MD5 hash from the command output
		hashStrCmd := strings.Split(output, " ")[0]

		require.Equal(t, hashStr, hashStrCmd)
	})
}

func TestHostDirectoryAbsolute(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-file"), []byte("hello"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "some-dir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-dir", "sub-file"), []byte("goodbye"), 0600))

	c, ctx := connect(t, dagger.WithWorkdir(dir))

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

func TestHostFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "subdir", "d.txt"), []byte("hello world"), 0600))

	c, ctx := connect(t)

	t.Run("get simple file", func(t *testing.T) {
		content, err := c.Host().File(filepath.Join(dir, "a.txt")).Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "1", content)
	})

	t.Run("get nested file", func(t *testing.T) {
		content, err := c.Host().File(filepath.Join(dir, "subdir", "d.txt")).Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "hello world", content)
	})
}
