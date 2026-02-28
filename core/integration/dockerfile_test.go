package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

type DockerfileSuite struct{}

func TestDockerfile(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(DockerfileSuite{})
}

func (DockerfileSuite) TestDockerBuild(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	contextDir := c.Container().
		From(golangImage).
		WithWorkdir("/src").
		WithExec([]string{"go", "mod", "init", "hello"}).
		WithNewFile("main.go",
			`package main
import "fmt"
import "os"
func main() {
	for _, env := range os.Environ() {
		fmt.Println(env)
	}
}`).
		Directory(".")
	baseDir := contextDir

	t.Run("default Dockerfile location", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		env, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("with syntax pragma", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`# syntax = docker/dockerfile:1
FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		env, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("with old syntax pragma", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`# syntax = docker/dockerfile:1.7
FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		env, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("with unknown syntax pragma", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`# syntax = example.com/custom/dockerfile:1
FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		_, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, `syntax frontend "example.com/custom/dockerfile:1" is unsupported`)
	})

	t.Run("custom Dockerfile location", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("subdir/Dockerfile.whee",
				`FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		opts := dagger.DirectoryDockerBuildOpts{Dockerfile: "subdir/Dockerfile.whee"}
		env, err := dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with default Dockerfile location", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		sub := c.Directory().WithDirectory("subcontext", dir).Directory("subcontext")
		env, err := sub.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with custom Dockerfile location", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("subdir/Dockerfile.whee",
				`FROM `+golangImage+`
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)
		sub := c.Directory().WithDirectory("subcontext", dir).Directory("subcontext")
		opts := dagger.DirectoryDockerBuildOpts{Dockerfile: "subdir/Dockerfile.whee"}
		env, err := sub.DockerBuild(opts).WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("with build args", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`FROM `+golangImage+`
ARG FOOARG=bar
WORKDIR /src
COPY main.go .
RUN go build -o /usr/bin/goenv main.go
ENV FOO=$FOOARG
CMD goenv
`)
		env, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")

		opts := dagger.DirectoryDockerBuildOpts{
			BuildArgs: []dagger.BuildArg{{Name: "FOOARG", Value: "barbar"}},
		}
		env, err = dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=barbar\n")
	})

	t.Run("with target", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile",
				`FROM `+golangImage+` AS base
CMD echo "base"

FROM base AS stage1
CMD echo "stage1"

FROM base AS stage2
CMD echo "stage2"
`)
		output, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage2\n")

		opts := dagger.DirectoryDockerBuildOpts{Target: "stage1"}
		output, err = dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage1\n")
		require.NotContains(t, output, "stage2\n")
	})

	t.Run("with build secrets", func(ctx context.Context, t *testctx.T) {
		sec := c.SetSecret("my-secret", "barbar")

		dockerfile := `FROM ` + alpineImage + `
WORKDIR /src
RUN --mount=type=secret,id=my-secret,required=true test "$(cat /run/secrets/my-secret)" = "barbar"
RUN --mount=type=secret,id=my-secret,required=true cp /run/secrets/my-secret /secret
CMD cat /secret && (cat /secret | tr "[a-z]" "[A-Z]")
`

		t.Run("builtin frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", dockerfile)
			opts := dagger.DirectoryDockerBuildOpts{Secrets: []*dagger.Secret{sec}}

			stdout, err := dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})

		t.Run("remote frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile)
			opts := dagger.DirectoryDockerBuildOpts{Secrets: []*dagger.Secret{sec}}

			stdout, err := dir.DockerBuild(opts).WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})
	})

	t.Run("with unknown build secrets", func(ctx context.Context, t *testctx.T) {
		dockerfile := `FROM ` + alpineImage + `
WORKDIR /src
RUN --mount=type=secret,id=my-secret echo "foofoo" > /secret 
CMD cat /secret && (cat /secret | tr "[a-z]" "[A-Z]")
`

		t.Run("builtin frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", dockerfile)

			stdout, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "foofoo")
			require.Contains(t, stdout, "FOOFOO")
		})

		t.Run("remote frontend", func(ctx context.Context, t *testctx.T) {
			dir := baseDir.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile)

			stdout, err := dir.DockerBuild().WithExec(nil).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "foofoo")
			require.Contains(t, stdout, "FOOFOO")
		})
	})

	t.Run("prevent duplicate secret transform", func(ctx context.Context, t *testctx.T) {
		sec := c.SetSecret("my-secret", "barbar")

		// src is a directory that has a secret dependency in its build graph
		dir := c.Container().
			From(alpineImage).
			WithWorkdir("/src").
			WithMountedSecret("/run/secret", sec).
			WithExec([]string{"cat", "/run/secret"}).
			WithNewFile("Dockerfile", `
			FROM alpine
			COPY / /
			`).
			Directory("/src")

		_, err := dir.DockerBuild().Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("just build, don't execute", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile", "FROM "+alpineImage+"\nCMD false")

		_, err := dir.DockerBuild().Sync(ctx)
		require.NoError(t, err)

		_, err = dir.DockerBuild().WithExec(nil).Sync(ctx)
		require.NotEmpty(t, err)
	})

	t.Run("just build, short-circuit", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("Dockerfile", "FROM "+alpineImage+"\nRUN false")

		_, err := dir.DockerBuild().Sync(ctx)
		require.NotEmpty(t, err)
	})

	t.Run("confirm .dockerignore compatibility with docker", func(ctx context.Context, t *testctx.T) {
		dir := baseDir.
			WithNewFile("foo", "foo-contents").
			WithNewFile("bar", "bar-contents").
			WithNewFile("baz", "baz-contents").
			WithNewFile("bay", "bay-contents").
			WithNewFile("Dockerfile",
				`FROM `+golangImage+`
	WORKDIR /src
	COPY . .
	`).
			WithNewFile(".dockerignore", `
	ba*
	Dockerfile
	!bay
	.dockerignore
	`)
		content, err := dir.DockerBuild().Directory("/src").File("foo").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "foo-contents", content)

		cts, err := dir.DockerBuild().Directory("/src").File(".dockerignore").Contents(ctx)
		require.ErrorContains(t, err, ".dockerignore: no such file or directory", fmt.Sprintf("cts is %s", cts))

		_, err = dir.DockerBuild().Directory("/src").File("Dockerfile").Contents(ctx)
		require.ErrorContains(t, err, "Dockerfile: no such file or directory")

		_, err = dir.DockerBuild().Directory("/src").File("bar").Contents(ctx)
		require.ErrorContains(t, err, "bar: no such file or directory")

		_, err = dir.DockerBuild().Directory("/src").File("baz").Contents(ctx)
		require.ErrorContains(t, err, "baz: no such file or directory")

		content, err = dir.DockerBuild().Directory("/src").File("bay").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "bay-contents", content)
	})

	t.Run("onbuild command is published", func(ctx context.Context, t *testctx.T) {
		testRef := registryRef("dockerfile-publish")

		pushedRef, err := baseDir.
			WithNewFile("Dockerfile",
				`FROM `+golangImage+`
	ONBUILD COPY some-file-that-might-exist .
	`).DockerBuild().Publish(ctx, testRef)

		// The initial build doesn't depend on some-file-that-might-exist existing
		require.NoError(t, err)
		require.Contains(t, pushedRef, "@sha256:")

		// However, when we perform a second build that references the above Dockerfile
		// it should return an error since some-file-that-might-exist doesn't actually exist
		_, err = baseDir.
			WithNewFile("Dockerfile",
				`FROM `+pushedRef+`
	`).DockerBuild().Sync(ctx)
		require.ErrorContains(t, err, "\"/some-file-that-might-exist\": not found")

		// Test again, after some-file-that-might-exist is created.
		s, err := baseDir.
			WithNewFile("some-file-that-might-exist", "existence is futile").
			WithNewFile("Dockerfile",
				`FROM `+pushedRef+`
	`).DockerBuild().File("some-file-that-might-exist").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "existence is futile", s)
	})
}

func (DockerfileSuite) TestBuildMergesWithParent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Create a container with envs variables and labels
	testCtr := c.Directory().WithNewFile("Dockerfile",
		`FROM `+alpineImage+`
ENV FOO=BAR
LABEL "com.example.test"="foo"
EXPOSE 8080
`,
	).DockerBuild()

	env, err := testCtr.EnvVariable(ctx, "FOO")
	require.NoError(t, err)
	require.Equal(t, "BAR", env)

	labelShouldExist, err := testCtr.Label(ctx, "com.example.test")
	require.NoError(t, err)
	require.Equal(t, "foo", labelShouldExist)

	// FIXME: Pretty clunky to work with lists of objects from the SDK
	// so test the exposed ports with a query string for now.
	cid, err := testCtr.ID(ctx)
	require.NoError(t, err)

	res, err := testutil.QueryWithClient[struct {
		Container struct {
			ExposedPorts []core.Port
		} `json:"loadContainerFromID"`
	}](c, t, `
        query Test($id: ContainerID!) {
            loadContainerFromID(id: $id) {
                exposedPorts {
                    port
                    protocol
                    description
                }
            }
        }`,
		&testutil.QueryOptions{
			Variables: map[string]any{
				"id": cid,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, res.Container.ExposedPorts, 1)

	// random order since ImageConfig.ExposedPorts is a map
	for _, p := range res.Container.ExposedPorts {
		require.Equalf(t, core.NetworkProtocolTCP, p.Protocol, "unexpected protocol for port %d", p.Port)
		switch p.Port {
		case 8080:
			require.Nil(t, p.Description)
		default:
			t.Fatalf("unexpected port %d", p.Port)
		}
	}
}

func (DockerfileSuite) TestDockerBuildSSH(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Set up a local unix socket echo server
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "test.sock")

	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { l.Close() })

	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
					panic(err)
				}
				return
			}

			n, err := io.Copy(conn, conn)
			if err != nil {
				t.Logf("copy: %s", err)
				panic(err)
			}

			t.Logf("copied %d bytes", n)

			err = conn.Close()
			if err != nil {
				t.Logf("close: %s", err)
				panic(err)
			}
		}
	}()

	sockID, err := c.Host().UnixSocket(sock).ID(ctx)
	require.NoError(t, err)

	dockerfile := `FROM ` + alpineImage + `
RUN apk add netcat-openbsd
RUN --mount=type=ssh sh -c 'echo -n hello | nc -w1 -N -U $SSH_AUTH_SOCK > /result'
`

	t.Run("builtin frontend", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", dockerfile)
		dirID, err := dir.ID(ctx)
		require.NoError(t, err)

		res, err := testutil.QueryWithClient[struct {
			LoadDirectoryFromID struct {
				DockerBuild struct {
					File struct {
						Contents string
					}
				}
			} `json:"loadDirectoryFromID"`
		}](c, t, `query Test($dir: DirectoryID!, $sock: SocketID!) {
			loadDirectoryFromID(id: $dir) {
				dockerBuild(ssh: $sock) {
					file(path: "/result") {
						contents
					}
				}
			}
		}`, &testutil.QueryOptions{
			Variables: map[string]any{
				"dir":  dirID,
				"sock": sockID,
			},
		})
		require.NoError(t, err)
		require.Equal(t, "hello", res.LoadDirectoryFromID.DockerBuild.File.Contents)
	})

	t.Run("remote frontend", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile)
		dirID, err := dir.ID(ctx)
		require.NoError(t, err)

		res, err := testutil.QueryWithClient[struct {
			LoadDirectoryFromID struct {
				DockerBuild struct {
					File struct {
						Contents string
					}
				}
			} `json:"loadDirectoryFromID"`
		}](c, t, `query Test($dir: DirectoryID!, $sock: SocketID!) {
			loadDirectoryFromID(id: $dir) {
				dockerBuild(ssh: $sock) {
					file(path: "/result") {
						contents
					}
				}
			}
		}`, &testutil.QueryOptions{
			Variables: map[string]any{
				"dir":  dirID,
				"sock": sockID,
			},
		})
		require.NoError(t, err)
		require.Equal(t, "hello", res.LoadDirectoryFromID.DockerBuild.File.Contents)
	})

	t.Run("without ssh socket fails", func(ctx context.Context, t *testctx.T) {
		dir := c.Directory().WithNewFile("Dockerfile", dockerfile)
		_, err := dir.DockerBuild().Sync(ctx)
		require.Error(t, err)
	})
}
