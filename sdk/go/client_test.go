package dagger

import (
	"context"
	"testing"

	"dagger.io/dagger/api"
	"github.com/stretchr/testify/require"
)

func TestDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, err := Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	dir := c.Core().Directory()

	contents, err := dir.
		WithNewFile("/hello.txt", api.DirectoryWithNewFileOpts{
			Contents: "world",
		}).
		File("/hello.txt").
		Contents(ctx)

	require.NoError(t, err)
	require.Equal(t, "world", contents)
}

func TestGit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c, err := Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	tree := c.Core().Git("github.com/dagger/dagger").
		Branch("main").
		Tree()

	files, err := tree.Entries(ctx)
	require.NoError(t, err)
	require.Contains(t, files, "README.md")

	readmeFile := tree.File("README.md")

	readme, err := readmeFile.Contents(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, readme)
	require.Contains(t, readme, "Dagger")

	readmeID, err := readmeFile.ID(ctx)
	require.NoError(t, err)

	otherReadme, err := c.Core().File(readmeID).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, readme, otherReadme)
}

func TestContainer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c, err := Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	alpine := c.
		Core().
		Container().
		From("alpine:3.16.2")

	contents, err := alpine.
		FS().
		File("/etc/alpine-release").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", contents)

	stdout, err := alpine.Exec(api.ContainerExecOpts{
		Args: []string{"cat", "/etc/alpine-release"},
	}).Stdout().Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", stdout)

	// Ensure we can grab the container ID back and re-run the same query
	id, err := alpine.ID(ctx)
	require.NoError(t, err)
	c.
		Core().
		Container(api.ContainerOpts{
			ID: id,
		}).
		FS().
		File("/etc/alpine-release").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", contents)
}
