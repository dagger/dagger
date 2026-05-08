package dagger

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"dagger.io/dagger/engineconn"
	"github.com/stretchr/testify/require"
)

// TestWithWorkspace verifies that the public Go SDK option stores the opaque
// workspace ref in engine connection config.
func TestWithWorkspace(t *testing.T) {
	t.Parallel()

	cfg := &engineconn.Config{}
	WithWorkspace("github.com/acme/ws").setClientOpt(cfg)

	require.Equal(t, "github.com/acme/ws", cfg.Workspace)
}

func TestWithLoadWorkspaceModules(t *testing.T) {
	t.Parallel()

	cfg := &engineconn.Config{}
	WithLoadWorkspaceModules().setClientOpt(cfg)

	require.True(t, cfg.LoadWorkspaceModules)
}

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

	otherReadme, err := c.LoadFileFromID(readmeID).Contents(ctx)
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
	contents, err = c.
		LoadContainerFromID(id).
		File("/etc/alpine-release").
		Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", contents)
}

// lockedBuffer captures Connect log output safely because session startup logs
// can be written from multiple goroutines.
type lockedBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestConnectOption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	buf := new(lockedBuffer)
	// NO_COLOR disables ANSI color codes in progress output so we can
	// match log content reliably without having to strip escape sequences.
	c, err := Connect(ctx, WithLogOutput(buf), WithEnvironmentVariable("NO_COLOR", "1"))
	require.NoError(t, err)
	defer c.Close()

	_, err = c.
		Container().
		From("alpine:3.16.1").
		File("/etc/alpine-release").
		Contents(ctx)
	require.NoError(t, err)

	logOutput := buf.String()
	require.NotEmpty(t, logOutput)

	wants := []string{
		"Creating new Engine session",
		"Establishing connection to Engine",
		"Container.from(address: \"alpine:3.16.1\")",
		"Container.file(path: \"/etc/alpine-release\")",
		"Container.file DONE",
		"File.contents DONE",
	}

	for _, want := range wants {
		require.Contains(t, logOutput, want)
	}
}

func TestContainerWith(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	env := func(c *Container) *Container {
		return c.WithEnvVariable("FOO", "bar")
	}

	secret := func(token string, client *Client) WithContainerFunc {
		return func(c *Container) *Container {
			return c.WithSecretVariable("TOKEN", client.SetSecret("TOKEN", token))
		}
	}

	c, err := Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	_, err = c.
		Container().
		From("alpine:3.16.2").
		With(env).
		With(secret("baz", c)).
		WithExec([]string{"sh", "-c", "test $FOO = bar && test $TOKEN = baz"}).
		Sync(ctx)

	require.NoError(t, err)
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

		require.NotContains(t, exErr.Message(), outMsg)
		require.NotContains(t, exErr.Message(), errMsg)

		if _, ok := err.(*ExecError); !ok {
			t.Fatal("unable to cast error type, check potential wrapping")
		}
	})

	t.Run("no output", func(t *testing.T) {
		_, err = c.
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
