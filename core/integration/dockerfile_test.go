package core

import (
	"context"
	"fmt"
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

	toContainerBuildOpts := func(opts []dagger.DirectoryDockerBuildOpts) (newOpts []dagger.ContainerBuildOpts) {
		if len(opts) == 0 {
			return nil
		}
		for _, opt := range opts {
			newOpt := dagger.ContainerBuildOpts{}
			if opt.Dockerfile != "" {
				newOpt.Dockerfile = opt.Dockerfile
			}
			if len(opt.BuildArgs) > 0 {
				newOpt.BuildArgs = opt.BuildArgs
			}
			if opt.Target != "" {
				newOpt.Target = opt.Target
			}
			if len(opt.Secrets) > 0 {
				newOpt.Secrets = opt.Secrets
			}
			newOpts = append(newOpts, newOpt)
		}
		return newOpts
	}

	testCases := []struct {
		name     string
		buildDir func(*dagger.Directory, ...dagger.DirectoryDockerBuildOpts) *dagger.Container
	}{
		{
			name: "container build",
			buildDir: func(dir *dagger.Directory, opts ...dagger.DirectoryDockerBuildOpts) *dagger.Container {
				//nolint:staticcheck
				return c.Container().Build(dir, toContainerBuildOpts(opts)...)
			},
		},
		{
			name: "directory build",
			buildDir: func(dir *dagger.Directory, opts ...dagger.DirectoryDockerBuildOpts) *dagger.Container {
				return dir.DockerBuild(opts...)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
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
				env, err := tc.buildDir(dir).WithExec(nil).Stdout(ctx)
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
				env, err := tc.buildDir(dir).WithExec(nil).Stdout(ctx)
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
				env, err := tc.buildDir(dir).WithExec(nil).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, env, "FOO=bar\n")
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
				env, err := tc.buildDir(dir, opts).WithExec(nil).Stdout(ctx)
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
				env, err := tc.buildDir(sub).WithExec(nil).Stdout(ctx)
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
				env, err := tc.buildDir(sub, opts).WithExec(nil).Stdout(ctx)
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
				env, err := tc.buildDir(dir).WithExec(nil).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, env, "FOO=bar\n")

				opts := dagger.DirectoryDockerBuildOpts{
					BuildArgs: []dagger.BuildArg{{Name: "FOOARG", Value: "barbar"}},
				}
				env, err = tc.buildDir(dir, opts).WithExec(nil).Stdout(ctx)
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
				output, err := tc.buildDir(dir).WithExec(nil).Stdout(ctx)
				require.NoError(t, err)
				require.Contains(t, output, "stage2\n")

				opts := dagger.DirectoryDockerBuildOpts{Target: "stage1"}
				output, err = tc.buildDir(dir, opts).WithExec(nil).Stdout(ctx)
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

					stdout, err := tc.buildDir(dir, opts).WithExec(nil).Stdout(ctx)
					require.NoError(t, err)
					require.Contains(t, stdout, "***")
					require.Contains(t, stdout, "BARBAR")
				})

				t.Run("remote frontend", func(ctx context.Context, t *testctx.T) {
					dir := baseDir.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile)
					opts := dagger.DirectoryDockerBuildOpts{Secrets: []*dagger.Secret{sec}}

					stdout, err := tc.buildDir(dir, opts).WithExec(nil).Stdout(ctx)
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

					stdout, err := tc.buildDir(dir).WithExec(nil).Stdout(ctx)
					require.NoError(t, err)
					require.Contains(t, stdout, "foofoo")
					require.Contains(t, stdout, "FOOFOO")
				})

				t.Run("remote frontend", func(ctx context.Context, t *testctx.T) {
					dir := baseDir.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile)

					stdout, err := tc.buildDir(dir).WithExec(nil).Stdout(ctx)
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

				_, err := tc.buildDir(dir).Sync(ctx)
				require.NoError(t, err)
			})

			t.Run("just build, don't execute", func(ctx context.Context, t *testctx.T) {
				dir := baseDir.
					WithNewFile("Dockerfile", "FROM "+alpineImage+"\nCMD false")

				_, err := tc.buildDir(dir).Sync(ctx)
				require.NoError(t, err)

				_, err = tc.buildDir(dir).WithExec(nil).Sync(ctx)
				require.NotEmpty(t, err)
			})

			t.Run("just build, short-circuit", func(ctx context.Context, t *testctx.T) {
				dir := baseDir.
					WithNewFile("Dockerfile", "FROM "+alpineImage+"\nRUN false")

				_, err := tc.buildDir(dir).Sync(ctx)
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
				content, err := tc.buildDir(dir).Directory("/src").File("foo").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "foo-contents", content)

				cts, err := tc.buildDir(dir).Directory("/src").File(".dockerignore").Contents(ctx)
				require.ErrorContains(t, err, "/src/.dockerignore: no such file or directory", fmt.Sprintf("cts is %s", cts))

				_, err = tc.buildDir(dir).Directory("/src").File("Dockerfile").Contents(ctx)
				require.ErrorContains(t, err, "/src/Dockerfile: no such file or directory")

				_, err = tc.buildDir(dir).Directory("/src").File("bar").Contents(ctx)
				require.ErrorContains(t, err, "/src/bar: no such file or directory")

				_, err = tc.buildDir(dir).Directory("/src").File("baz").Contents(ctx)
				require.ErrorContains(t, err, "/src/baz: no such file or directory")

				content, err = tc.buildDir(dir).Directory("/src").File("bay").Contents(ctx)
				require.NoError(t, err)
				require.Equal(t, "bay-contents", content)
			})
		})
	}
}

func (DockerfileSuite) TestBuildNilContextError(ctx context.Context, t *testctx.T) {
	// regression test, this previously caused the engine to panic
	_, err := testutil.Query[map[any]any](t,
		`{
			container {
				build(context: "") {
					id
				}
			}
		}`, nil)
	requireErrOut(t, err, "cannot decode empty string as ID")
}

func (DockerfileSuite) TestBuildMergesWithParent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Create a builder container
	builderCtr := c.Directory().WithNewFile("Dockerfile",
		`FROM `+alpineImage+`
ENV FOO=BAR
LABEL "com.example.test-should-replace"="foo"
EXPOSE 8080
`,
	)

	// Create a container with envs variables and labels
	//nolint:staticcheck
	testCtr := c.Container().
		WithEnvVariable("BOOL", "DOG").
		WithEnvVariable("FOO", "BAZ").
		WithLabel("com.example.test-should-exist", "test").
		WithLabel("com.example.test-should-replace", "bar").
		WithExposedPort(5000, dagger.ContainerWithExposedPortOpts{
			Description: "five thousand",
		}).
		Build(builderCtr)

	envShouldExist, err := testCtr.EnvVariable(ctx, "BOOL")
	require.NoError(t, err)
	require.Equal(t, "DOG", envShouldExist)

	envShouldBeReplaced, err := testCtr.EnvVariable(ctx, "FOO")
	require.NoError(t, err)
	require.Equal(t, "BAR", envShouldBeReplaced)

	labelShouldExist, err := testCtr.Label(ctx, "com.example.test-should-exist")
	require.NoError(t, err)
	require.Equal(t, "test", labelShouldExist)

	labelShouldBeReplaced, err := testCtr.Label(ctx, "com.example.test-should-replace")
	require.NoError(t, err)
	require.Equal(t, "foo", labelShouldBeReplaced)

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
	require.Len(t, res.Container.ExposedPorts, 2)

	// random order since ImageConfig.ExposedPorts is a map
	for _, p := range res.Container.ExposedPorts {
		require.Equalf(t, core.NetworkProtocolTCP, p.Protocol, "unexpected protocol for port %d", p.Port)
		switch p.Port {
		case 8080:
			require.Nil(t, p.Description)
		case 5000:
			require.NotNil(t, p.Description)
			require.Equal(t, "five thousand", *p.Description)
		default:
			t.Fatalf("unexpected port %d", p.Port)
		}
	}
}
