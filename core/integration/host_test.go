package core

import (
	"context"
	"os"
	"path"
	"testing"

	"dagger.io/dagger/api"
	"dagger.io/dagger/sdk/go/dagger"
	"github.com/stretchr/testify/require"
)

func TestHostWorkdir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	err := os.WriteFile(path.Join(dir, "foo"), []byte("bar"), 0600)
	require.NoError(t, err)

	ctx := context.Background()
	c, err := dagger.Connect(ctx, dagger.WithWorkdir(dir))
	require.NoError(t, err)
	defer c.Close()

	wdID, err := c.Core().Host().Workdir().ID(ctx)
	require.NoError(t, err)

	t.Run("contains the workdir's content", func(t *testing.T) {
		contents, err := c.Core().Container().
			From("alpine:3.16.2").
			WithMountedDirectory("/host", wdID).
			Exec(api.ContainerExecOpts{
				Args: []string{"ls", "/host"},
			}).Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo\n", contents)
	})

	t.Run("updates on each call", func(t *testing.T) {
		err := os.WriteFile(path.Join(dir, "fizz"), []byte("buzz"), 0600)
		require.NoError(t, err)

		contents, err := c.Core().Container().
			From("alpine:3.16.2").
			WithMountedDirectory("/host", wdID).
			Exec(api.ContainerExecOpts{
				Args: []string{"ls", "/host"},
			}).Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "fizz\nfoo\n", contents)
	})
}

func TestHostDirectoryRelative(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(path.Join(dir, "some-file"), []byte("hello"), 0600))
	require.NoError(t, os.MkdirAll(path.Join(dir, "some-dir"), 0755))
	require.NoError(t, os.WriteFile(path.Join(dir, "some-dir", "sub-file"), []byte("goodbye"), 0600))

	ctx := context.Background()
	c, err := dagger.Connect(ctx, dagger.WithWorkdir(dir))
	require.NoError(t, err)
	defer c.Close()

	t.Run(". is same as workdir", func(t *testing.T) {
		wdID1, err := c.Core().Host().Directory(".").ID(ctx)
		require.NoError(t, err)

		wdID2, err := c.Core().Host().Workdir().ID(ctx)
		require.NoError(t, err)

		require.Equal(t, wdID1, wdID2)
	})

	t.Run("./foo is relative to workdir", func(t *testing.T) {
		contents, err := c.Core().Host().Directory("some-dir").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"sub-file"}, contents)
	})

	t.Run("../ does not allow escaping", func(t *testing.T) {
		_, err := c.Core().Host().Directory("../").ID(ctx)
		require.Error(t, err)

		// don't reveal the workdir location
		require.NotContains(t, err, dir)
	})
}

func TestHostDirectoryReadWrite(t *testing.T) {
	t.Parallel()

	dir1 := t.TempDir()
	err := os.WriteFile(path.Join(dir1, "foo"), []byte("bar"), 0600)
	require.NoError(t, err)

	dir2 := t.TempDir()

	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	srcID, err := c.Core().Host().Directory(dir1).ID(ctx)
	require.NoError(t, err)

	exported, err := c.Core().Directory(api.DirectoryOpts{ID: srcID}).Export(ctx, dir2)
	require.NoError(t, err)
	require.True(t, exported)

	content, err := os.ReadFile(path.Join(dir2, "foo"))
	require.NoError(t, err)
	require.Equal(t, "bar", string(content))
}

func TestHostVariable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	require.NoError(t, os.Setenv("HELLO_TEST", "hello"))

	secret := c.Core().Host().Variable("HELLO_TEST")

	varValue, err := secret.Value(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", varValue)

	varSecret, err := secret.Secret().ID(ctx)
	require.NoError(t, err)

	env, err := c.Core().Container().
		From("alpine:3.16.2").
		WithSecretVariable("SECRET", varSecret).
		Exec(api.ContainerExecOpts{
			Args: []string{"env"},
		}).Stdout().Contents(ctx)
	require.NoError(t, err)

	require.Contains(t, env, "SECRET=hello")
}
