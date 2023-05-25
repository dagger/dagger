package dagger

import (
	"context"
	"errors"
	"io"
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
		WithNewFile("/hello.txt", "world").
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
	}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", stdout)

	// Ensure we can grab the container ID back and re-run the same query
	id, err := alpine.ID(ctx)
	require.NoError(t, err)
	contents, err = c.
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

	r, w := io.Pipe()
	c, err := Connect(ctx, WithLogOutput(w))
	require.NoError(t, err)

	_, err = c.
		Container().
		From("alpine:3.16.1").
		FS().
		File("/etc/alpine-release").
		Contents(ctx)
	require.NoError(t, err)

	err = c.Close()
	w.Close()
	require.NoError(t, err)

	wants := []string{
		"#1 resolve image config for docker.io/library/alpine:3.16.1",
		"#1 DONE [0-9.]+s",
		"#2 docker-image://docker.io/library/alpine:3.16.1",
		"#2 resolve docker.io/library/alpine:3.16.1 [0-9.]+s done",
		"#2 (DONE [0-9.]+s|CACHED)",
	}

	logOutput, err := io.ReadAll(r)
	require.NoError(t, err)

	for _, want := range wants {
		require.Regexp(t, string(logOutput), want)
	}
}

func TestErrorMessage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c, err := Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	_, err = c.Container().From("fake.invalid:latest").ID(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, errorHelpBlurb)

	_, err = c.Container().From("alpine:3.16.2").WithExec([]string{"false"}).Sync(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, errorHelpBlurb)
}

func TestList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c, err := Connect(ctx)
	require.NoError(t, err)

	defer c.Close()

	envs, err := c.
		Container().
		From("alpine:3.16.2").
		WithEnvVariable("FOO", "BAR").
		WithEnvVariable("BAR", "BAZ").
		EnvVariables(ctx)

	require.NoError(t, err)

	envName, err := envs[1].Name(ctx)
	require.NoError(t, err)

	envValue, err := envs[1].Value(ctx)
	require.NoError(t, err)

	require.Equal(t, "FOO", envName)
	require.Equal(t, "BAR", envValue)

	envName, err = envs[2].Name(ctx)
	require.NoError(t, err)

	envValue, err = envs[2].Value(ctx)
	require.NoError(t, err)
	require.Equal(t, "BAR", envName)
	require.Equal(t, "BAZ", envValue)
}

func TestExecError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c, err := Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	t.Run("get output and exit code", func(t *testing.T) {
		outMsg := "STDOUT HERE"
		errMsg := "STDERR HERE"
		args := []string{"sh", "-c", "cat /testout; cat /testerr >&2; exit 127"}

		_, err = c.
			Container().
			From("alpine:3.16.2").
			// don't put these in the command args so it stays out of the
			// error message
			WithDirectory("/", c.Directory().
				WithNewFile("testout", outMsg).
				WithNewFile("testerr", errMsg),
			).
			WithExec(args).
			Sync(ctx)

		var exErr *ExecError

		require.ErrorAs(t, err, &exErr)
		require.Equal(t, 127, exErr.ExitCode)
		require.Equal(t, args, exErr.Cmd)
		require.Equal(t, outMsg, exErr.Stdout)
		require.Equal(t, errMsg, exErr.Stderr)

		require.Contains(t, exErr.Error(), outMsg)
		require.Contains(t, exErr.Error(), errMsg)
		require.NotContains(t, exErr.Message(), outMsg)
		require.NotContains(t, exErr.Message(), errMsg)
	})

	t.Run("no output", func(t *testing.T) {
		_, err = c.
			Container().
			From("alpine:3.16.2").
			WithExec([]string{"false"}).
			Sync(ctx)

		var exErr *ExecError

		require.ErrorAs(t, err, &exErr)
		require.ErrorContains(t, exErr, "did not complete successfully: exit code: 1")
		require.Equal(t, "", exErr.Stdout)
		require.Equal(t, "", exErr.Stderr)
	})

	t.Run("not an exec error", func(t *testing.T) {
		_, err = c.
			Container().
			From("invalid!").
			WithExec([]string{"false"}).
			Sync(ctx)

		var exErr *ExecError

		if errors.As(err, &exErr) {
			t.Fatal("unexpected ExecError")
		}
	})
}
