package dagger

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, err := Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	dir := c.Directory()

	contents, err := dir.
		WithNewFile("/hello.txt", DirectoryWithNewFileOpts{
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

	tree := c.Git("github.com/dagger/dagger").
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

	otherReadme, err := c.File(readmeID).Contents(ctx)
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
		Container().
		From("alpine:3.16.2")

	contents, err := alpine.
		FS().
		File("/etc/alpine-release").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", contents)

	stdout, err := alpine.Exec(ContainerExecOpts{
		Args: []string{"cat", "/etc/alpine-release"},
	}).Stdout().Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", stdout)

	// Ensure we can grab the container ID back and re-run the same query
	id, err := alpine.ID(ctx)
	require.NoError(t, err)
	c.
		Container(ContainerOpts{
			ID: id,
		}).
		FS().
		File("/etc/alpine-release").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", contents)
}

func TestConnectOption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var b bytes.Buffer
	c, err := Connect(ctx, WithLogOutput(&b))
	require.NoError(t, err)

	_, err = c.
		Container().
		From("alpine:3.16.1").
		FS().
		File("/etc/alpine-release").
		Contents(ctx)
	require.NoError(t, err)

	err = c.Close()
	require.NoError(t, err)

	wants := []string{
		"#1 resolve image config for docker.io/library/alpine:3.16.1",
		"#1 DONE [0-9.]+s",
		"#2 docker-image://docker.io/library/alpine:3.16.1",
		"#2 resolve docker.io/library/alpine:3.16.1 [0-9.]+s done",
		"#2 (DONE [0-9.]+s|CACHED)",
	}

	for _, want := range wants {
		require.Regexp(t, want, b.String())
	}
}
