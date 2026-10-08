package core

import (
	"context"
	"errors"
	"io"
	"testing"

	dagger "dagger.io/dagger"
	"github.com/stretchr/testify/require"
)

func TestDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()
	q := NewQuery(c)

	dir := q.Directory()

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

	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()
	q := NewQuery(c)

	tree := q.Git("github.com/dagger/dagger").
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

	otherReadme, err := Ref[*File](q, readmeID).Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, readme, otherReadme)
}

func TestContainer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()
	q := NewQuery(c)

	alpine := q.
		Container().
		From("alpine:3.16.2")

	contents, err := alpine.
		File("/etc/alpine-release").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", contents)

	stdout, err := alpine.
		WithExec([]string{"cat", "/etc/alpine-release"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", stdout)

	// Ensure we can grab the container ID back and re-run the same query
	id, err := alpine.ID(ctx)
	require.NoError(t, err)
	contents, err = Ref[*Container](q, id).
		File("/etc/alpine-release").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", contents)
}

// TODO: fix this test, it's actually broken, the result is an empty string
// We could use a buffer, however the regexp want need to be updated, the
// display of Dagger has change since.
func TestConnectOption(t *testing.T) {
	t.Skip("test broken with io.Pipe and empty string on the standard output." +
		"We need to update the test with new output and use a buffer to catch" +
		"output.")
	t.Parallel()
	ctx := context.Background()

	r, w := io.Pipe()
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(w))
	require.NoError(t, err)
	q := NewQuery(c)

	_, err = q.
		Container().
		From("alpine:3.16.1").
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

	// Empty
	// fmt.Println(string(logOutput))

	for _, want := range wants {
		// NOTE: the string is empty
		// This pass the test
		// require.Regexp(t, "", want)
		require.Regexp(t, string(logOutput), want)
	}
}

func TestContainerWith(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	env := func(c *Container) *Container {
		return c.WithEnvVariable("FOO", "bar")
	}

	secret := func(token string, q *Query) WithContainerFunc {
		return func(c *Container) *Container {
			return c.WithSecretVariable("TOKEN", q.SetSecret("TOKEN", token))
		}
	}

	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()
	q := NewQuery(c)

	_, err = q.
		Container().
		From("alpine:3.16.2").
		With(env).
		With(secret("baz", q)).
		WithExec([]string{"sh", "-c", "test $FOO = bar && test $TOKEN = baz"}).
		Sync(ctx)

	require.NoError(t, err)
}

func TestList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()
	q := NewQuery(c)

	envs, err := q.
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

	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()
	q := NewQuery(c)

	t.Run("get output and exit code", func(t *testing.T) {
		outMsg := "STDOUT HERE"
		errMsg := "STDERR HERE"
		args := []string{"sh", "-c", "cat /testout; cat /testerr >&2; exit 127"}

		_, err = q.
			Container().
			From("alpine:3.16.2").
			// don't put these in the command args so it stays out of the
			// error message
			WithDirectory("/", q.Directory().
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

		require.NotContains(t, exErr.Message(), outMsg)
		require.NotContains(t, exErr.Message(), errMsg)

		if _, ok := err.(*ExecError); !ok {
			t.Fatal("unable to cast error type, check potential wrapping")
		}
	})

	t.Run("no output", func(t *testing.T) {
		_, err = q.
			Container().
			From("alpine:3.16.2").
			WithExec([]string{"false"}).
			Sync(ctx)

		var exErr *ExecError

		require.ErrorAs(t, err, &exErr)
		require.ErrorContains(t, exErr, "exit code: 1")
		require.Equal(t, "", exErr.Stdout)
		require.Equal(t, "", exErr.Stderr)
	})

	t.Run("not an exec error", func(t *testing.T) {
		_, err = q.
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
