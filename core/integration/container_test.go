package core

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/containerd/containerd/platforms"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/moby/buildkit/identity"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/testutil"
)

func TestContainerScratch(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			ID     string
			Rootfs struct {
				Entries []string
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				id
				rootfs {
					entries
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Empty(t, res.Container.Rootfs.Entries)
}

func TestContainerFrom(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				File struct {
					Contents string
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
                    file(path: "/etc/alpine-release") {
                        contents
                    }
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.File.Contents, "3.18.2\n")
}

func TestContainerBuild(t *testing.T) {
	c, ctx := connect(t)

	contextDir := c.Directory().
		WithNewFile("main.go",
			`package main
import "fmt"
import "os"
func main() {
	for _, env := range os.Environ() {
		fmt.Println(env)
	}
}`)

	t.Run("default Dockerfile location", func(t *testing.T) {
		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)

		env, err := c.Container().Build(src).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("custom Dockerfile location", func(t *testing.T) {
		src := contextDir.
			WithNewFile("subdir/Dockerfile.whee",
				`FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)

		env, err := c.Container().Build(src, dagger.ContainerBuildOpts{
			Dockerfile: "subdir/Dockerfile.whee",
		}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with default Dockerfile location", func(t *testing.T) {
		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)

		sub := c.Directory().WithDirectory("subcontext", src).Directory("subcontext")

		env, err := c.Container().Build(sub).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with custom Dockerfile location", func(t *testing.T) {
		src := contextDir.
			WithNewFile("subdir/Dockerfile.whee",
				`FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`)

		sub := c.Directory().WithDirectory("subcontext", src).Directory("subcontext")

		env, err := c.Container().Build(sub, dagger.ContainerBuildOpts{
			Dockerfile: "subdir/Dockerfile.whee",
		}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("with build args", func(t *testing.T) {
		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
ARG FOOARG=bar
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=$FOOARG
CMD goenv
`)

		env, err := c.Container().Build(src).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")

		env, err = c.Container().Build(src, dagger.ContainerBuildOpts{BuildArgs: []dagger.BuildArg{{Name: "FOOARG", Value: "barbar"}}}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=barbar\n")
	})

	t.Run("with target", func(t *testing.T) {
		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine AS base
CMD echo "base"

FROM base AS stage1
CMD echo "stage1"

FROM base AS stage2
CMD echo "stage2"
`)

		output, err := c.Container().Build(src).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage2\n")

		output, err = c.Container().Build(src, dagger.ContainerBuildOpts{Target: "stage1"}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage1\n")
		require.NotContains(t, output, "stage2\n")
	})

	t.Run("with build secrets", func(t *testing.T) {
		sec := c.SetSecret("my-secret", "barbar")

		src := contextDir.
			WithNewFile("Dockerfile",
				`FROM golang:1.18.2-alpine
WORKDIR /src
RUN --mount=type=secret,id=my-secret test "$(cat /run/secrets/my-secret)" = "barbar"
RUN --mount=type=secret,id=my-secret cp /run/secrets/my-secret  /secret
CMD cat /secret
`)

		stdout, err := c.Container().Build(src, dagger.ContainerBuildOpts{
			Secrets: []*dagger.Secret{sec},
		}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, stdout, "***")
	})

	t.Run("just build, don't execute", func(t *testing.T) {
		src := contextDir.
			WithNewFile("Dockerfile", "FROM "+alpineImage+"\nCMD false")

		_, err := c.Container().Build(src).Sync(ctx)
		require.NoError(t, err)

		// unless there's a WithExec
		_, err = c.Container().Build(src).WithExec(nil).Sync(ctx)
		require.NotEmpty(t, err)
	})

	t.Run("just build, short-circuit", func(t *testing.T) {
		src := contextDir.
			WithNewFile("Dockerfile", "FROM "+alpineImage+"\nRUN false")

		_, err := c.Container().Build(src).Sync(ctx)
		require.NotEmpty(t, err)
	})
}

func TestContainerWithRootFS(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	alpine316 := c.Container().From(alpineImage)

	alpine316ReleaseStr, err := alpine316.File("/etc/alpine-release").Contents(ctx)
	require.NoError(t, err)

	alpine316ReleaseStr = strings.TrimSpace(alpine316ReleaseStr)
	dir := alpine316.Rootfs()
	_, err = c.Container().WithEnvVariable("ALPINE_RELEASE", alpine316ReleaseStr).WithRootfs(dir).WithExec([]string{
		"/bin/sh",
		"-c",
		"test -f /etc/alpine-release && test \"$(head -n 1 /etc/alpine-release)\" = \"$ALPINE_RELEASE\"",
	}).Sync(ctx)

	require.NoError(t, err)

	alpine315 := c.Container().From(alpineImage)

	varVal := "testing123"

	alpine315WithVar := alpine315.WithEnvVariable("DAGGER_TEST", varVal)
	varValResp, err := alpine315WithVar.EnvVariable(ctx, "DAGGER_TEST")
	require.NoError(t, err)

	require.Equal(t, varVal, varValResp)

	alpine315ReplacedFS := alpine315WithVar.WithRootfs(dir)

	varValResp, err = alpine315ReplacedFS.EnvVariable(ctx, "DAGGER_TEST")
	require.NoError(t, err)
	require.Equal(t, varVal, varValResp)

	releaseStr, err := alpine315ReplacedFS.File("/etc/alpine-release").Contents(ctx)
	require.NoError(t, err)

	require.Equal(t, "3.18.2\n", releaseStr)
}

//go:embed testdata/hello.go
var helloSrc string

func TestContainerWithRootFSSubdir(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	hello := c.Directory().WithNewFile("main.go", helloSrc).File("main.go")

	ctr := c.Container().
		From(golangImage).
		WithMountedFile("/src/main.go", hello).
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"go", "build", "-o", "/out/hello", "/src/main.go"})

	out, err := c.Container().
		WithRootfs(ctr.Directory("/out")).
		WithExec([]string{"/hello"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "Hello, world!\n", out)
}

func TestContainerExecSync(t *testing.T) {
	t.Parallel()

	// A successful sync doesn't prove anything. As soon as you call other
	// leaves to check things, they could be the ones triggering execution.
	// Still, sync can be useful for short-circuiting.
	err := testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["false"]) {
						sync
					}
				}
			}
		}`, nil, nil)
	require.Contains(t, err.Error(), `process "false" did not complete successfully`)
}

func TestContainerExecStdoutStderr(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithExec struct {
					Stdout string
					Stderr string
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["sh", "-c", "echo hello; echo goodbye >/dev/stderr"]) {
						stdout
						stderr
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithExec.Stdout, "hello\n")
	require.Equal(t, res.Container.From.WithExec.Stderr, "goodbye\n")
}

func TestContainerExecStdin(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithExec struct {
					Stdout string
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["cat"], stdin: "hello") {
						stdout
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithExec.Stdout, "hello")
}

func TestContainerExecRedirectStdoutStderr(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithExec struct {
					Out, Err struct {
						Contents string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(
						args: ["sh", "-c", "echo hello; echo goodbye >/dev/stderr"],
						redirectStdout: "out",
						redirectStderr: "err"
					) {
						out: file(path: "out") {
							contents
						}

						err: file(path: "err") {
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithExec.Out.Contents, "hello\n")
	require.Equal(t, res.Container.From.WithExec.Err.Contents, "goodbye\n")

	c, ctx := connect(t)

	execWithMount := c.Container().From(alpineImage).
		WithMountedDirectory("/mnt", c.Directory()).
		WithExec([]string{"sh", "-c", "echo hello; echo goodbye >/dev/stderr"}, dagger.ContainerWithExecOpts{
			RedirectStdout: "/mnt/out",
			RedirectStderr: "/mnt/err",
		})

	stdout, err := execWithMount.File("/mnt/out").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello\n", stdout)
	stderr, err := execWithMount.File("/mnt/err").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "goodbye\n", stderr)

	_, err = execWithMount.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello\n", stdout)

	_, err = execWithMount.Stderr(ctx)
	require.NoError(t, err)
	require.Equal(t, "goodbye\n", stderr)
}

func TestContainerExecWithWorkdir(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithWorkdir struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
					withWorkdir(path: "/usr") {
						withExec(args: ["pwd"]) {
							stdout
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithWorkdir.WithExec.Stdout, "/usr\n")
}

func TestContainerExecWithoutWorkdir(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	res, err := c.Container().
		From(alpineImage).
		WithWorkdir("/usr").
		WithoutWorkdir().
		WithExec([]string{"pwd"}).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "/\n", res)
}

func TestContainerExecWithUser(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				User string

				WithUser struct {
					User     string
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}

	t.Run("user name", func(t *testing.T) {
		err := testutil.Query(
			`{
			container {
				from(address: "`+alpineImage+`") {
					user
					withUser(name: "daemon") {
						user
						withExec(args: ["whoami"]) {
							stdout
						}
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "daemon", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\n", res.Container.From.WithUser.WithExec.Stdout)
	})

	t.Run("user and group name", func(t *testing.T) {
		err := testutil.Query(
			`{
			container {
				from(address: "`+alpineImage+`") {
					user
					withUser(name: "daemon:floppy") {
						user
						withExec(args: ["sh", "-c", "whoami; groups"]) {
							stdout
						}
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "daemon:floppy", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\nfloppy\n", res.Container.From.WithUser.WithExec.Stdout)
	})

	t.Run("user ID", func(t *testing.T) {
		err := testutil.Query(
			`{
			container {
				from(address: "`+alpineImage+`") {
					user
					withUser(name: "2") {
						user
						withExec(args: ["whoami"]) {
							stdout
						}
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "2", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\n", res.Container.From.WithUser.WithExec.Stdout)
	})

	t.Run("user and group ID", func(t *testing.T) {
		err := testutil.Query(
			`{
			container {
				from(address: "`+alpineImage+`") {
					user
					withUser(name: "2:11") {
						user
						withExec(args: ["sh", "-c", "whoami; groups"]) {
							stdout
						}
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "2:11", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\nfloppy\n", res.Container.From.WithUser.WithExec.Stdout)
	})

	t.Run("stdin", func(t *testing.T) {
		err := testutil.Query(
			`{
			container {
				from(address: "`+alpineImage+`") {
					withUser(name: "daemon") {
						withExec(args: ["sh"], stdin: "whoami") {
							stdout
						}
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, "daemon\n", res.Container.From.WithUser.WithExec.Stdout)
	})
}

func TestContainerExecWithoutUser(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	res, err := c.Container().
		From(alpineImage).
		WithUser("daemon").
		WithoutUser().
		WithExec([]string{"whoami"}).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "root\n", res)
}

func TestContainerExecWithEntrypoint(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	base := c.Container().From(alpineImage)
	withEntry := base.WithEntrypoint([]string{"sh"})

	t.Run("before", func(t *testing.T) {
		before, err := base.Entrypoint(ctx)
		require.NoError(t, err)
		require.Empty(t, before)
	})

	t.Run("after", func(t *testing.T) {
		after, err := withEntry.Entrypoint(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"sh"}, after)
	})

	t.Run("used", func(t *testing.T) {
		used, err := withEntry.WithExec([]string{"-c", "echo $HOME"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/root\n", used)
	})

	t.Run("prepended to exec", func(t *testing.T) {
		_, err := withEntry.WithExec([]string{"sh", "-c", "echo $HOME"}).Sync(ctx)
		require.Error(t, err)
		require.ErrorContains(t, err, "can't open 'sh'")
	})

	t.Run("skipped", func(t *testing.T) {
		skipped, err := withEntry.WithExec([]string{"sh", "-c", "echo $HOME"}, dagger.ContainerWithExecOpts{
			SkipEntrypoint: true,
		}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/root\n", skipped)
	})

	t.Run("unset default args", func(t *testing.T) {
		removed, err := base.
			WithDefaultArgs([]string{"foobar"}).
			WithEntrypoint([]string{"echo"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "\n", removed)
	})

	t.Run("kept default args", func(t *testing.T) {
		kept, err := base.
			WithDefaultArgs([]string{"foobar"}).
			WithEntrypoint([]string{"echo"}, dagger.ContainerWithEntrypointOpts{
				KeepDefaultArgs: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar\n", kept)
	})

	t.Run("cleared", func(t *testing.T) {
		withoutEntry := withEntry.WithEntrypoint(nil)
		removed, err := withoutEntry.Entrypoint(ctx)
		require.NoError(t, err)
		require.Empty(t, removed)
	})
}

func TestContainerExecWithoutEntrypoint(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	t.Run("cleared entrypoint", func(t *testing.T) {
		res, err := c.Container().
			From(alpineImage).
			// if not unset this would return an error
			WithEntrypoint([]string{"foo"}).
			WithoutEntrypoint().
			WithExec([]string{"echo", "-n", "foobar"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar", res)
	})

	t.Run("cleared entrypoint with default args", func(t *testing.T) {
		res, err := c.Container().
			From(alpineImage).
			WithEntrypoint([]string{"foo"}).
			WithDefaultArgs([]string{"echo", "-n", "foobar"}).
			WithoutEntrypoint().
			Stdout(ctx)
		require.ErrorContains(t, err, "no command has been set")
		require.Empty(t, res)
	})

	t.Run("cleared entrypoint without default args", func(t *testing.T) {
		res, err := c.Container().
			From(alpineImage).
			WithEntrypoint([]string{"foo"}).
			WithDefaultArgs([]string{"echo", "-n", "foobar"}).
			WithoutEntrypoint(dagger.ContainerWithoutEntrypointOpts{
				KeepDefaultArgs: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar", res)
	})
}

func TestContainerWithDefaultArgs(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Entrypoint  []string
				DefaultArgs []string
				WithExec    struct {
					Stdout string
				}
				WithDefaultArgs struct {
					Entrypoint  []string
					DefaultArgs []string
				}
				WithEntrypoint struct {
					Entrypoint  []string
					DefaultArgs []string
					WithExec    struct {
						Stdout string
					}
					WithDefaultArgs struct {
						Entrypoint  []string
						DefaultArgs []string
						WithExec    struct {
							Stdout string
						}
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
					entrypoint
					defaultArgs
					withDefaultArgs(args: []) {
						entrypoint
						defaultArgs
					}

					withEntrypoint(args: ["sh", "-c"]) {
						entrypoint
						defaultArgs

						withExec(args: ["echo $HOME"]) {
							stdout
						}

						withDefaultArgs(args: ["id"]) {
							entrypoint
							defaultArgs

							withExec(args: []) {
								stdout
							}
						}
					}
				}
			}
		}`, &res, nil)
	t.Run("default alpine (no entrypoint)", func(t *testing.T) {
		require.NoError(t, err)
		require.Empty(t, res.Container.From.Entrypoint)
		require.Equal(t, []string{"/bin/sh"}, res.Container.From.DefaultArgs)
	})

	t.Run("with nil default args", func(t *testing.T) {
		require.Empty(t, res.Container.From.WithDefaultArgs.Entrypoint)
		require.Empty(t, res.Container.From.WithDefaultArgs.DefaultArgs)
	})

	t.Run("with entrypoint set", func(t *testing.T) {
		require.Equal(t, []string{"sh", "-c"}, res.Container.From.WithEntrypoint.Entrypoint)
		require.Empty(t, res.Container.From.WithEntrypoint.DefaultArgs)
	})

	t.Run("with exec args", func(t *testing.T) {
		require.Equal(t, "/root\n", res.Container.From.WithEntrypoint.WithExec.Stdout)
	})

	t.Run("with default args set", func(t *testing.T) {
		require.Equal(t, []string{"sh", "-c"}, res.Container.From.WithEntrypoint.WithDefaultArgs.Entrypoint)
		require.Equal(t, []string{"id"}, res.Container.From.WithEntrypoint.WithDefaultArgs.DefaultArgs)

		require.Equal(t, "uid=0(root) gid=0(root) groups=0(root),1(bin),2(daemon),3(sys),4(adm),6(disk),10(wheel),11(floppy),20(dialout),26(tape),27(video)\n", res.Container.From.WithEntrypoint.WithDefaultArgs.WithExec.Stdout)
	})
}

func TestContainerExecWithoutDefaultArgs(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	res, err := c.Container().
		From(alpineImage).
		WithEntrypoint([]string{"echo", "-n"}).
		WithDefaultArgs([]string{"foo"}).
		WithoutDefaultArgs().
		WithExec([]string{}).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "", res)
}

func TestContainerExecWithEnvVariable(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithEnvVariable struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
					withEnvVariable(name: "FOO", value: "bar") {
						withExec(args: ["env"]) {
							stdout
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Contains(t, res.Container.From.WithEnvVariable.WithExec.Stdout, "FOO=bar\n")
}

func TestContainerVariables(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				EnvVariables []schema.EnvVariable
				WithExec     struct {
					Stdout string
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					envVariables {
						name
						value
					}
					withExec(args: ["env"]) {
						stdout
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, []schema.EnvVariable{
		{Name: "PATH", Value: "/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		{Name: "GOLANG_VERSION", Value: "1.18.2"},
		{Name: "GOPATH", Value: "/go"},
	}, res.Container.From.EnvVariables)
	require.Contains(t, res.Container.From.WithExec.Stdout, "GOPATH=/go\n")
}

func TestContainerVariable(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				EnvVariable *string
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					envVariable(name: "GOLANG_VERSION")
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.NotNil(t, res.Container.From.EnvVariable)
	require.Equal(t, "1.18.2", *res.Container.From.EnvVariable)

	err = testutil.Query(
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					envVariable(name: "UNKNOWN")
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Nil(t, res.Container.From.EnvVariable)
}

func TestContainerWithoutVariable(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithoutEnvVariable struct {
					EnvVariables []schema.EnvVariable
					WithExec     struct {
						Stdout string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					withoutEnvVariable(name: "GOLANG_VERSION") {
						envVariables {
							name
							value
						}
						withExec(args: ["env"]) {
							stdout
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithoutEnvVariable.EnvVariables, []schema.EnvVariable{
		{Name: "PATH", Value: "/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		{Name: "GOPATH", Value: "/go"},
	})
	require.NotContains(t, res.Container.From.WithoutEnvVariable.WithExec.Stdout, "GOLANG_VERSION")
}

func TestContainerEnvVariablesReplace(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithEnvVariable struct {
					EnvVariables []schema.EnvVariable
					WithExec     struct {
						Stdout string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					withEnvVariable(name: "GOPATH", value: "/gone") {
						envVariables {
							name
							value
						}
						withExec(args: ["env"]) {
							stdout
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithEnvVariable.EnvVariables, []schema.EnvVariable{
		{Name: "PATH", Value: "/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		{Name: "GOLANG_VERSION", Value: "1.18.2"},
		{Name: "GOPATH", Value: "/gone"},
	})
	require.Contains(t, res.Container.From.WithEnvVariable.WithExec.Stdout, "GOPATH=/gone\n")
}

func TestContainerWithEnvVariableExpand(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	t.Run("add env var without expansion", func(t *testing.T) {
		out, err := c.Container().
			From(alpineImage).
			WithEnvVariable("FOO", "foo:$PATH").
			WithExec([]string{"printenv", "FOO"}).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "foo:$PATH\n", out)
	})

	t.Run("add env var with expansion", func(t *testing.T) {
		out, err := c.Container().
			From(alpineImage).
			WithEnvVariable("USER_PATH", "/opt").
			WithEnvVariable(
				"PATH",
				"${USER_PATH}/bin:$PATH",
				dagger.ContainerWithEnvVariableOpts{
					Expand: true,
				},
			).
			WithExec([]string{"printenv", "PATH"}).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t,
			"/opt/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n",
			out,
		)
	})
}

func TestContainerLabel(t *testing.T) {
	c, ctx := connect(t)

	t.Run("container with new label", func(t *testing.T) {
		label, err := c.Container().From(alpineImage).WithLabel("FOO", "BAR").Label(ctx, "FOO")

		require.NoError(t, err)
		require.Contains(t, label, "BAR")
	})

	// implementing this test as GraphQL query until
	// https://github.com/dagger/dagger/issues/4398 gets resolved
	t.Run("container labels", func(t *testing.T) {
		res := struct {
			Container struct {
				From struct {
					Labels []schema.Label
				}
			}
		}{}

		err := testutil.Query(
			`{
				container {
				  from(address: "nginx") {
					labels {
					  name
					  value
					}
				  }
				}
			  }`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, []schema.Label{
			{Name: "maintainer", Value: "NGINX Docker Maintainers <docker-maint@nginx.com>"},
		}, res.Container.From.Labels)
	})

	t.Run("container without label", func(t *testing.T) {
		label, err := c.Container().From("nginx").WithoutLabel("maintainer").Label(ctx, "maintainer")

		require.NoError(t, err)
		require.Empty(t, label)
	})

	t.Run("container replace label", func(t *testing.T) {
		label, err := c.Container().From("nginx").WithLabel("maintainer", "bar").Label(ctx, "maintainer")

		require.NoError(t, err)
		require.Contains(t, label, "bar")
	})

	t.Run("container with new label - nil panics", func(t *testing.T) {
		label, err := c.Container().WithLabel("FOO", "BAR").Label(ctx, "FOO")

		require.NoError(t, err)
		require.Contains(t, label, "BAR")
	})

	t.Run("container label - nil panics", func(t *testing.T) {
		label, err := c.Container().Label(ctx, "FOO")

		require.NoError(t, err)
		require.Empty(t, label)
	})

	t.Run("container without label - nil panics", func(t *testing.T) {
		label, err := c.Container().WithoutLabel("maintainer").Label(ctx, "maintainer")

		require.NoError(t, err)
		require.Empty(t, label)
	})

	// implementing this test as GraphQL query until
	// https://github.com/dagger/dagger/issues/4398 gets resolved
	t.Run("container labels - nil panics", func(t *testing.T) {
		res := struct {
			Container struct {
				From struct {
					Labels []schema.Label
				}
			}
		}{}

		err := testutil.Query(
			`{
				container {
				  labels {
					name
					value
				  }
				}
			  }`, &res, nil)
		require.NoError(t, err)
		require.Empty(t, res.Container.From.Labels)
	})
}

func TestContainerWorkdir(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Workdir  string
				WithExec struct {
					Stdout string
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
			  from(address: "golang:1.18.2-alpine") {
				workdir
				withExec(args: ["pwd"]) {
				  stdout
				}
			  }
			}
		  }`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.Workdir, "/go")
	require.Equal(t, res.Container.From.WithExec.Stdout, "/go\n")
}

func TestContainerWithWorkdir(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithWorkdir struct {
					Workdir  string
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					withWorkdir(path: "/usr") {
						workdir
						withExec(args: ["pwd"]) {
							stdout
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithWorkdir.Workdir, "/usr")
	require.Equal(t, res.Container.From.WithWorkdir.WithExec.Stdout, "/usr\n")
}

func TestContainerWithMountedDirectory(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					ID string
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
						id
					}
				}
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.WithNewFile.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Stdout string

						WithExec struct {
							Stdout string
						}
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt", source: $id) {
						withExec(args: ["cat", "/mnt/some-file"]) {
							stdout

							withExec(args: ["cat", "/mnt/some-dir/sub-file"]) {
								stdout
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	require.Equal(t, "some-content", execRes.Container.From.WithMountedDirectory.WithExec.Stdout)
	require.Equal(t, "sub-content", execRes.Container.From.WithMountedDirectory.WithExec.WithExec.Stdout)
}

func TestContainerWithMountedDirectorySourcePath(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Directory struct {
						ID string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
						directory(path: "some-dir") {
							id
						}
					}
				}
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.WithNewFile.Directory.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						WithExec struct {
							Stdout string
						}
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt", source: $id) {
						withExec(args: ["sh", "-c", "echo >> /mnt/sub-file; echo -n more-content >> /mnt/sub-file"]) {
							withExec(args: ["cat", "/mnt/sub-file"]) {
								stdout
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	require.Equal(t, "sub-content\nmore-content", execRes.Container.From.WithMountedDirectory.WithExec.WithExec.Stdout)
}

func TestContainerWithMountedDirectoryPropagation(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}{}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					id
				}
			}
		}`, &dirRes, nil, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Stdout   string
						WithExec struct {
							WithExec struct {
								Stdout               string
								WithMountedDirectory struct {
									WithExec struct {
										Stdout   string
										WithExec struct {
											Stdout string
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt", source: $id) {
						withExec(args: ["cat", "/mnt/some-file"]) {
							# original content
							stdout
							withExec(args: ["sh", "-c", "echo >> /mnt/some-file; echo -n more-content >> /mnt/some-file"]) {
								withExec(args: ["cat", "/mnt/some-file"]) {
									# modified content should propagate
									stdout
									withMountedDirectory(path: "/mnt", source: $id) {
										withExec(args: ["cat", "/mnt/some-file"]) {
											# should be back to the original content
											stdout

											withExec(args: ["cat", "/mnt/some-file"]) {
												# original content override should propagate
												stdout
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}}, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)

	require.Equal(t,
		"some-content",
		execRes.Container.From.WithMountedDirectory.WithExec.Stdout)

	require.Equal(t,
		"some-content\nmore-content",
		execRes.Container.From.WithMountedDirectory.WithExec.WithExec.WithExec.Stdout)

	require.Equal(t,
		"some-content",
		execRes.Container.From.WithMountedDirectory.WithExec.WithExec.WithExec.WithMountedDirectory.WithExec.Stdout)

	require.Equal(t,
		"some-content",
		execRes.Container.From.WithMountedDirectory.WithExec.WithExec.WithExec.WithMountedDirectory.WithExec.WithExec.Stdout)
}

func TestContainerWithMountedFile(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				File struct {
					ID core.FileID
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
					file(path: "some-dir/sub-file") {
						id
					}
				}
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.File.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedFile struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: FileID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedFile(path: "/mnt/file", source: $id) {
						withExec(args: ["cat", "/mnt/file"]) {
							stdout
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	require.Equal(t, "sub-content", execRes.Container.From.WithMountedFile.WithExec.Stdout)
}

func TestContainerWithMountedCache(t *testing.T) {
	t.Parallel()

	cacheID := newCache(t)

	execRes := struct {
		Container struct {
			From struct {
				WithEnvVariable struct {
					WithMountedCache struct {
						WithExec struct {
							Stdout string
						}
					}
				}
			}
		}
	}{}

	query := `query Test($cache: CacheVolumeID!, $rand: String!) {
			container {
				from(address: "` + alpineImage + `") {
					withEnvVariable(name: "RAND", value: $rand) {
						withMountedCache(path: "/mnt/cache", cache: $cache) {
							withExec(args: ["sh", "-c", "echo $RAND >> /mnt/cache/file; cat /mnt/cache/file"]) {
								stdout
							}
						}
					}
				}
			}
		}`

	rand1 := identity.NewID()
	err := testutil.Query(query, &execRes, &testutil.QueryOptions{Variables: map[string]any{
		"cache": cacheID,
		"rand":  rand1,
	}})
	require.NoError(t, err)
	require.Equal(t, rand1+"\n", execRes.Container.From.WithEnvVariable.WithMountedCache.WithExec.Stdout)

	rand2 := identity.NewID()
	err = testutil.Query(query, &execRes, &testutil.QueryOptions{Variables: map[string]any{
		"cache": cacheID,
		"rand":  rand2,
	}})
	require.NoError(t, err)
	require.Equal(t, rand1+"\n"+rand2+"\n", execRes.Container.From.WithEnvVariable.WithMountedCache.WithExec.Stdout)
}

func TestContainerWithMountedCacheFromDirectory(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				Directory struct {
					ID core.DirectoryID
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-dir/sub-file", contents: "initial-content\n") {
					directory(path: "some-dir") {
						id
					}
				}
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	initialID := dirRes.Directory.WithNewFile.Directory.ID

	cacheID := newCache(t)

	execRes := struct {
		Container struct {
			From struct {
				WithEnvVariable struct {
					WithMountedCache struct {
						WithExec struct {
							Stdout string
						}
					}
				}
			}
		}
	}{}

	query := `query Test($cache: CacheVolumeID!, $rand: String!, $init: DirectoryID!) {
			container {
				from(address: "` + alpineImage + `") {
					withEnvVariable(name: "RAND", value: $rand) {
						withMountedCache(path: "/mnt/cache", cache: $cache, source: $init) {
							withExec(args: ["sh", "-c", "echo $RAND >> /mnt/cache/sub-file; cat /mnt/cache/sub-file"]) {
								stdout
							}
						}
					}
				}
			}
		}`

	rand1 := identity.NewID()
	err = testutil.Query(query, &execRes, &testutil.QueryOptions{Variables: map[string]any{
		"init":  initialID,
		"rand":  rand1,
		"cache": cacheID,
	}})
	require.NoError(t, err)
	require.Equal(t, "initial-content\n"+rand1+"\n", execRes.Container.From.WithEnvVariable.WithMountedCache.WithExec.Stdout)

	rand2 := identity.NewID()
	err = testutil.Query(query, &execRes, &testutil.QueryOptions{Variables: map[string]any{
		"init":  initialID,
		"rand":  rand2,
		"cache": cacheID,
	}})
	require.NoError(t, err)
	require.Equal(t, "initial-content\n"+rand1+"\n"+rand2+"\n", execRes.Container.From.WithEnvVariable.WithMountedCache.WithExec.Stdout)
}

func TestContainerWithMountedTemp(t *testing.T) {
	t.Parallel()

	execRes := struct {
		Container struct {
			From struct {
				WithMountedTemp struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}

	err := testutil.Query(`{
			container {
				from(address: "`+alpineImage+`") {
					withMountedTemp(path: "/mnt/tmp") {
						withExec(args: ["grep", "/mnt/tmp", "/proc/mounts"]) {
							stdout
						}
					}
				}
			}
		}`, &execRes, nil)
	require.NoError(t, err)
	require.Contains(t, execRes.Container.From.WithMountedTemp.WithExec.Stdout, "tmpfs /mnt/tmp tmpfs")
}

func TestContainerWithDirectory(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	dir := c.Directory().
		WithNewFile("some-file", "some-content").
		WithNewFile("some-dir/sub-file", "sub-content").
		Directory("some-dir")

	ctr := c.Container().
		From(alpineImage).
		WithWorkdir("/workdir").
		WithDirectory("with-dir", dir)

	contents, err := ctr.WithExec([]string{"cat", "with-dir/sub-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "sub-content", contents)

	contents, err = ctr.WithExec([]string{"cat", "/workdir/with-dir/sub-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "sub-content", contents)

	// Test with a mount
	mount := c.Directory().
		WithNewFile("mounted-file", "mounted-content")

	ctr = c.Container().
		From(alpineImage).
		WithWorkdir("/workdir").
		WithMountedDirectory("mnt/mount", mount).
		WithDirectory("mnt/mount/dst/with-dir", dir)
	contents, err = ctr.WithExec([]string{"cat", "mnt/mount/mounted-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "mounted-content", contents)

	contents, err = ctr.WithExec([]string{"cat", "mnt/mount/dst/with-dir/sub-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "sub-content", contents)

	// Test with a relative mount
	mnt := c.Directory().WithNewDirectory("/a/b/c")
	ctr = c.Container().
		From(alpineImage).
		WithMountedDirectory("/mnt", mnt)
	dir = c.Directory().
		WithNewDirectory("/foo").
		WithNewFile("/foo/some-file", "some-content")
	ctr = ctr.WithDirectory("/mnt/a/b/foo", dir)
	contents, err = ctr.WithExec([]string{"cat", "/mnt/a/b/foo/foo/some-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", contents)
}

func TestContainerWithFile(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	file := c.Directory().
		WithNewFile("some-file", "some-content").
		File("some-file")

	ctr := c.Container().
		From(alpineImage).
		WithWorkdir("/workdir").
		WithFile("target-file", file)

	contents, err := ctr.WithExec([]string{"cat", "target-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", contents)

	contents, err = ctr.WithExec([]string{"cat", "/workdir/target-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", contents)
}

func TestContainerWithFiles(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	file1 := c.Directory().
		WithNewFile("first-file", "file1 content").
		File("first-file")
	file2 := c.Directory().
		WithNewFile("second-file", "file2 content").
		File("second-file")
	files := []*dagger.File{file1, file2}

	ctr := c.Container().
		From(alpineImage).
		WithFiles("myfiles", files)

	contents, err := ctr.WithExec([]string{"cat", "/myfiles/first-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "file1 content", contents)

	contents, err = ctr.WithExec([]string{"cat", "/myfiles/second-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "file2 content", contents)
}

func TestContainerWithNewFile(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := c.Container().
		From(alpineImage).
		WithWorkdir("/workdir").
		WithNewFile("some-file", dagger.ContainerWithNewFileOpts{
			Contents: "some-content",
		})

	contents, err := ctr.WithExec([]string{"cat", "some-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", contents)

	contents, err = ctr.WithExec([]string{"cat", "/workdir/some-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", contents)
}

func TestContainerMountsWithoutMount(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	scratchID, err := c.Directory().ID(ctx)
	require.NoError(t, err)

	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					ID string
				}
			}
		}
	}{}

	err = testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
						id
					}
				}
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.WithNewFile.ID

	execRes := struct {
		Container struct {
			From struct {
				WithDirectory struct {
					WithMountedTemp struct {
						Mounts               []string
						WithMountedDirectory struct {
							Mounts   []string
							WithExec struct {
								Stdout       string
								WithoutMount struct {
									Mounts   []string
									WithExec struct {
										Stdout string
									}
								}
							}
						}
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!, $scratch: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withDirectory(path: "/mnt/dir", directory: $scratch) {
						withMountedTemp(path: "/mnt/tmp") {
							mounts
							withMountedDirectory(path: "/mnt/dir", source: $id) {
								mounts
								withExec(args: ["ls", "/mnt/dir"]) {
									stdout
									withoutMount(path: "/mnt/dir") {
										mounts
										withExec(args: ["ls", "/mnt/dir"]) {
											stdout
										}
									}
								}
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id":      id,
			"scratch": scratchID,
		}})
	require.NoError(t, err)
	require.Equal(t, []string{"/mnt/tmp"}, execRes.Container.From.WithDirectory.WithMountedTemp.Mounts)
	require.Equal(t, []string{"/mnt/tmp", "/mnt/dir"}, execRes.Container.From.WithDirectory.WithMountedTemp.WithMountedDirectory.Mounts)
	require.Equal(t, "some-dir\nsome-file\n", execRes.Container.From.WithDirectory.WithMountedTemp.WithMountedDirectory.WithExec.Stdout)
	require.Equal(t, "", execRes.Container.From.WithDirectory.WithMountedTemp.WithMountedDirectory.WithExec.WithoutMount.WithExec.Stdout)
	require.Equal(t, []string{"/mnt/tmp"}, execRes.Container.From.WithDirectory.WithMountedTemp.WithMountedDirectory.WithExec.WithoutMount.Mounts)
}

func TestContainerReplacedMounts(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	lower := c.Directory().WithNewFile("some-file", "lower-content")

	upper := c.Directory().WithNewFile("some-file", "upper-content")

	ctr := c.Container().
		From(alpineImage).
		WithMountedDirectory("/mnt/dir", lower)

	t.Run("initial content is lower", func(t *testing.T) {
		mnts, err := ctr.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt/dir"}, mnts)

		out, err := ctr.WithExec([]string{"cat", "/mnt/dir/some-file"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "lower-content", out)
	})

	replaced := ctr.WithMountedDirectory("/mnt/dir", upper)

	t.Run("mounts of same path are replaced", func(t *testing.T) {
		mnts, err := replaced.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt/dir"}, mnts)

		out, err := replaced.WithExec([]string{"cat", "/mnt/dir/some-file"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "upper-content", out)
	})

	t.Run("removing a replaced mount does not reveal previous mount", func(t *testing.T) {
		removed := replaced.WithoutMount("/mnt/dir")
		mnts, err := removed.Mounts(ctx)
		require.NoError(t, err)
		require.Empty(t, mnts)
	})

	clobberedDir := c.Directory().WithNewFile("some-file", "clobbered-content")
	clobbered := replaced.WithMountedDirectory("/mnt", clobberedDir)

	t.Run("replacing parent of a mount clobbers child", func(t *testing.T) {
		mnts, err := clobbered.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt"}, mnts)

		out, err := clobbered.WithExec([]string{"cat", "/mnt/some-file"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "clobbered-content", out)
	})

	clobberedSubDir := c.Directory().WithNewFile("some-file", "clobbered-sub-content")
	clobberedSub := clobbered.WithMountedDirectory("/mnt/dir", clobberedSubDir)

	t.Run("restoring mount under clobbered mount", func(t *testing.T) {
		mnts, err := clobberedSub.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt", "/mnt/dir"}, mnts)

		out, err := clobberedSub.WithExec([]string{"cat", "/mnt/dir/some-file"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "clobbered-sub-content", out)
	})
}

func TestContainerDirectory(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					ID core.DirectoryID
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
						id
					}
				}
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.WithNewFile.ID

	writeRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithMountedDirectory struct {
						WithExec struct {
							Directory struct {
								ID core.DirectoryID
							}
						}
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/dir", source: $id) {
						withMountedDirectory(path: "/mnt/dir/overlap", source: $id) {
							withExec(args: ["sh", "-c", "echo hello >> /mnt/dir/overlap/another-file"]) {
								directory(path: "/mnt/dir/overlap") {
									id
								}
							}
						}
					}
				}
			}
		}`, &writeRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)

	writtenID := writeRes.Container.From.WithMountedDirectory.WithMountedDirectory.WithExec.Directory.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/dir", source: $id) {
						withExec(args: ["cat", "/mnt/dir/another-file"]) {
							stdout
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "hello\n", execRes.Container.From.WithMountedDirectory.WithExec.Stdout)
}

func TestContainerDirectoryErrors(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					ID core.DirectoryID
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
						id
					}
				}
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.WithNewFile.ID

	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/dir", source: $id) {
						directory(path: "/mnt/dir/some-file") {
							id
						}
					}
				}
			}
		}`, nil, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "path /mnt/dir/some-file is a file, not a directory")

	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/dir", source: $id) {
						directory(path: "/mnt/dir/bogus") {
							id
						}
					}
				}
			}
		}`, nil, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus: no such file or directory")

	err = testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
					withMountedTemp(path: "/mnt/tmp") {
						directory(path: "/mnt/tmp/bogus") {
							id
						}
					}
				}
			}
		}`, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus: cannot retrieve path from tmpfs")

	cacheID := newCache(t)
	err = testutil.Query(
		`query Test($cache: CacheVolumeID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedCache(path: "/mnt/cache", cache: $cache) {
						directory(path: "/mnt/cache/bogus") {
							id
						}
					}
				}
			}
		}`, nil, &testutil.QueryOptions{Variables: map[string]any{
			"cache": cacheID,
		}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus: cannot retrieve path from cache")
}

func TestContainerDirectorySourcePath(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				Directory struct {
					ID core.DirectoryID
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-dir/sub-dir/sub-file", contents: "sub-content\n") {
					directory(path: "some-dir") {
						id
					}
				}
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.Directory.ID

	writeRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Directory struct {
							ID core.DirectoryID
						}
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/dir", source: $id) {
						withExec(args: ["sh", "-c", "echo more-content >> /mnt/dir/sub-dir/sub-file"]) {
							directory(path: "/mnt/dir/sub-dir") {
								id
							}
						}
					}
				}
			}
		}`, &writeRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)

	writtenID := writeRes.Container.From.WithMountedDirectory.WithExec.Directory.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/dir", source: $id) {
						withExec(args: ["cat", "/mnt/dir/sub-file"]) {
							stdout
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "sub-content\nmore-content\n", execRes.Container.From.WithMountedDirectory.WithExec.Stdout)
}

func TestContainerFile(t *testing.T) {
	t.Parallel()

	id := newDirWithFile(t, "some-file", "some-content-")

	writeRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithMountedDirectory struct {
						WithExec struct {
							File struct {
								ID core.FileID
							}
						}
					}
				}
			}
		}
	}{}
	err := testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/dir", source: $id) {
						withMountedDirectory(path: "/mnt/dir/overlap", source: $id) {
							withExec(args: ["sh", "-c", "echo -n appended >> /mnt/dir/overlap/some-file"]) {
								file(path: "/mnt/dir/overlap/some-file") {
									id
								}
							}
						}
					}
				}
			}
		}`, &writeRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)

	writtenID := writeRes.Container.From.WithMountedDirectory.WithMountedDirectory.WithExec.File.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedFile struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: FileID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedFile(path: "/mnt/file", source: $id) {
						withExec(args: ["cat", "/mnt/file"]) {
							stdout
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "some-content-appended", execRes.Container.From.WithMountedFile.WithExec.Stdout)
}

func TestContainerFileErrors(t *testing.T) {
	t.Parallel()

	id := newDirWithFile(t, "some-file", "some-content")

	err := testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/dir", source: $id) {
						file(path: "/mnt/dir/bogus") {
							id
						}
					}
				}
			}
		}`, nil, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus: no such file or directory")

	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/dir", source: $id) {
						file(path: "/mnt/dir") {
							id
						}
					}
				}
			}
		}`, nil, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "path /mnt/dir is a directory, not a file")

	err = testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
					withMountedTemp(path: "/mnt/tmp") {
						file(path: "/mnt/tmp/bogus") {
							id
						}
					}
				}
			}
		}`, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus: cannot retrieve path from tmpfs")

	cacheID := newCache(t)
	err = testutil.Query(
		`query Test($cache: CacheVolumeID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedCache(path: "/mnt/cache", cache: $cache) {
						file(path: "/mnt/cache/bogus") {
							id
						}
					}
				}
			}
		}`, nil, &testutil.QueryOptions{Variables: map[string]any{
			"cache": cacheID,
		}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "bogus: cannot retrieve path from cache")

	err = testutil.Query(
		`query Test($secret: SecretID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedSecret(path: "/sekret", source: $secret) {
						file(path: "/sekret") {
							contents
						}
					}
				}
			}
		}`, nil, &testutil.QueryOptions{Secrets: map[string]string{
			"secret": "some-secret",
		}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "sekret: no such file or directory")
}

func TestContainerFSDirectory(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Container struct {
			From struct {
				Directory struct {
					ID core.DirectoryID
				}
			}
		}
	}{}
	err := testutil.Query(
		`{
			container {
				from(address: "`+alpineImage+`") {
					directory(path: "/etc") {
						id
					}
				}
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	etcID := dirRes.Container.From.Directory.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/etc", source: $id) {
						withExec(args: ["cat", "/mnt/etc/alpine-release"]) {
							stdout
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": etcID,
		}})
	require.NoError(t, err)

	require.Equal(t, "3.18.2\n", execRes.Container.From.WithMountedDirectory.WithExec.Stdout)
}

func TestContainerRelativePaths(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}{}

	err := testutil.Query(
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					id
				}
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.ID

	writeRes := struct {
		Container struct {
			From struct {
				WithExec struct {
					WithWorkdir struct {
						WithWorkdir struct {
							Workdir              string
							WithMountedDirectory struct {
								WithMountedTemp struct {
									WithMountedCache struct {
										Mounts   []string
										WithExec struct {
											Directory struct {
												ID core.DirectoryID
											}
										}
										WithoutMount struct {
											Mounts []string
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}{}

	cacheID := newCache(t)
	err = testutil.Query(
		`query Test($id: DirectoryID!, $cache: CacheVolumeID!) {
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["mkdir", "-p", "/mnt/sub"]) {
						withWorkdir(path: "/mnt") {
							withWorkdir(path: "sub") {
								workdir
								withMountedDirectory(path: "dir", source: $id) {
									withMountedTemp(path: "tmp") {
										withMountedCache(path: "cache", cache: $cache) {
											mounts
											withExec(args: ["touch", "dir/another-file"]) {
												directory(path: "dir") {
													id
												}
											}
											withoutMount(path: "cache") {
												mounts
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}`, &writeRes, &testutil.QueryOptions{Variables: map[string]any{
			"id":    id,
			"cache": cacheID,
		}})
	require.NoError(t, err)

	require.Equal(t,
		[]string{"/mnt/sub/dir", "/mnt/sub/tmp", "/mnt/sub/cache"},
		writeRes.Container.From.WithExec.WithWorkdir.WithWorkdir.WithMountedDirectory.WithMountedTemp.WithMountedCache.Mounts)

	require.Equal(t,
		[]string{"/mnt/sub/dir", "/mnt/sub/tmp"},
		writeRes.Container.From.WithExec.WithWorkdir.WithWorkdir.WithMountedDirectory.WithMountedTemp.WithMountedCache.WithoutMount.Mounts)

	writtenID := writeRes.Container.From.WithExec.WithWorkdir.WithWorkdir.WithMountedDirectory.WithMountedTemp.WithMountedCache.WithExec.Directory.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withMountedDirectory(path: "/mnt/dir", source: $id) {
						withExec(args: ["ls", "/mnt/dir"]) {
							stdout
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "another-file\nsome-file\n", execRes.Container.From.WithMountedDirectory.WithExec.Stdout)
}

func TestContainerMultiFrom(t *testing.T) {
	t.Parallel()

	dirRes := struct {
		Directory struct {
			ID core.DirectoryID
		}
	}{}

	err := testutil.Query(
		`{
			directory {
				id
			}
		}`, &dirRes, nil)
	require.NoError(t, err)

	id := dirRes.Directory.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						From struct {
							WithExec struct {
								WithExec struct {
									Stdout string
								}
							}
						}
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query Test($id: DirectoryID!) {
			container {
				from(address: "node:18.10.0-alpine") {
					withMountedDirectory(path: "/mnt", source: $id) {
						withExec(args: ["sh", "-c", "node --version >> /mnt/versions"]) {
							from(address: "golang:1.18.2-alpine") {
								withExec(args: ["sh", "-c", "go version >> /mnt/versions"]) {
									withExec(args: ["cat", "/mnt/versions"]) {
										stdout
									}
								}
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	require.Contains(t, execRes.Container.From.WithMountedDirectory.WithExec.From.WithExec.WithExec.Stdout, "v18.10.0\n")
	require.Contains(t, execRes.Container.From.WithMountedDirectory.WithExec.From.WithExec.WithExec.Stdout, "go version go1.18.2")
}

func TestContainerPublish(t *testing.T) {
	c, ctx := connect(t)

	testRef := registryRef("container-publish")

	entrypoint := []string{"echo", "im-a-entrypoint"}
	ctr := c.Container().From(alpineImage).
		WithEntrypoint(entrypoint)
	pushedRef, err := ctr.Publish(ctx, testRef)
	require.NoError(t, err)
	require.NotEqual(t, testRef, pushedRef)
	require.Contains(t, pushedRef, "@sha256:")

	pulledCtr := c.Container().From(pushedRef)
	contents, err := pulledCtr.File("/etc/alpine-release").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, "3.18.2\n")

	output, err := pulledCtr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "im-a-entrypoint\n", output)
}

func TestExecFromScratch(t *testing.T) {
	c, ctx := connect(t)

	// execute it from scratch, where there is no default platform, make sure it works and can be pushed
	execBusybox := c.Container().
		// /bin/busybox is a static binary
		WithMountedFile("/busybox", c.Container().From("busybox:musl").File("/bin/busybox")).
		WithExec([]string{"/busybox"})

	_, err := execBusybox.Stdout(ctx)
	require.NoError(t, err)
	_, err = execBusybox.Publish(ctx, registryRef("from-scratch"))
	require.NoError(t, err)
}

func TestContainerMultipleMounts(t *testing.T) {
	c, ctx := connect(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "one"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "two"), []byte("2"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "three"), []byte("3"), 0o600))

	one := c.Host().Directory(dir).File("one")
	two := c.Host().Directory(dir).File("two")
	three := c.Host().Directory(dir).File("three")

	build := c.Container().From(alpineImage).
		WithMountedFile("/example/one", one).
		WithMountedFile("/example/two", two).
		WithMountedFile("/example/three", three)

	build = build.WithExec([]string{"ls", "/example/one", "/example/two", "/example/three"})

	build = build.WithExec([]string{"cat", "/example/one", "/example/two", "/example/three"})

	out, err := build.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "123", out)
}

func TestContainerExport(t *testing.T) {
	t.Parallel()

	wd := t.TempDir()
	dest := t.TempDir()

	c, ctx := connect(t, dagger.WithWorkdir(wd))

	entrypoint := []string{"sh", "-c", "im-a-entrypoint"}
	ctr := c.Container().From(alpineImage).
		WithEntrypoint(entrypoint)

	t.Run("to absolute dir", func(t *testing.T) {
		for _, useAsTarball := range []bool{true, false} {
			t.Run(fmt.Sprintf("useAsTarball=%t", useAsTarball), func(t *testing.T) {
				imagePath := filepath.Join(dest, "image.tar")

				if useAsTarball {
					tarFile := ctr.AsTarball()
					ok, err := tarFile.Export(ctx, imagePath)
					require.NoError(t, err)
					require.True(t, ok)
				} else {
					ok, err := ctr.Export(ctx, imagePath)
					require.NoError(t, err)
					require.True(t, ok)
				}

				stat, err := os.Stat(imagePath)
				require.NoError(t, err)
				require.NotZero(t, stat.Size())
				require.EqualValues(t, 0o600, stat.Mode().Perm())

				entries := tarEntries(t, imagePath)
				require.Contains(t, entries, "oci-layout")
				require.Contains(t, entries, "index.json")

				// a single-platform image includes a manifest.json, making it
				// compatible with docker load
				require.Contains(t, entries, "manifest.json")

				dockerManifestBytes := readTarFile(t, imagePath, "manifest.json")
				// NOTE: this is what buildkit integ tests do, use a one-off struct rather than actual defined type
				var dockerManifest []struct {
					Config string
				}
				require.NoError(t, json.Unmarshal(dockerManifestBytes, &dockerManifest))
				require.Len(t, dockerManifest, 1)
				configPath := dockerManifest[0].Config
				configBytes := readTarFile(t, imagePath, configPath)
				var img ocispecs.Image
				require.NoError(t, json.Unmarshal(configBytes, &img))
				require.Equal(t, entrypoint, img.Config.Entrypoint)
			})
		}
	})

	t.Run("to workdir", func(t *testing.T) {
		ok, err := ctr.Export(ctx, "./image.tar")
		require.NoError(t, err)
		require.True(t, ok)

		stat, err := os.Stat(filepath.Join(wd, "image.tar"))
		require.NoError(t, err)
		require.NotZero(t, stat.Size())
		require.EqualValues(t, 0o600, stat.Mode().Perm())

		entries := tarEntries(t, filepath.Join(wd, "image.tar"))
		require.Contains(t, entries, "oci-layout")
		require.Contains(t, entries, "index.json")
		require.Contains(t, entries, "manifest.json")
	})

	t.Run("to subdir", func(t *testing.T) {
		ok, err := ctr.Export(ctx, "./foo/image.tar")
		require.NoError(t, err)
		require.True(t, ok)

		entries := tarEntries(t, filepath.Join(wd, "foo", "image.tar"))
		require.Contains(t, entries, "oci-layout")
		require.Contains(t, entries, "index.json")
		require.Contains(t, entries, "manifest.json")
	})

	t.Run("to outer dir", func(t *testing.T) {
		ok, err := ctr.Export(ctx, "../")
		require.Error(t, err)
		require.False(t, ok)
	})
}

// NOTE: more test coverage of Container.AsTarball are in TestContainerExport and TestContainerMultiPlatformExport
func TestContainerAsTarball(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	ctr := c.Container().From(alpineImage)
	output, err := ctr.
		WithMountedFile("/foo.tar", ctr.AsTarball()).
		WithExec([]string{"apk", "add", "file"}).
		WithExec([]string{"file", "/foo.tar"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "/foo.tar: POSIX tar archive\n", output)
}

func TestContainerImport(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	t.Run("OCI", func(t *testing.T) {
		pf, err := c.DefaultPlatform(ctx)
		require.NoError(t, err)

		platform, err := platforms.Parse(string(pf))
		require.NoError(t, err)

		config := map[string]any{
			"contents": map[string]any{
				"keyring": []string{
					"https://packages.wolfi.dev/os/wolfi-signing.rsa.pub",
				},
				"repositories": []string{
					"https://packages.wolfi.dev/os",
				},
				"packages": []string{
					"wolfi-base",
				},
			},
			"cmd": "/bin/sh -l",
			"environment": map[string]string{
				"FOO": "bar",
			},
			"archs": []string{
				platform.Architecture,
			},
		}

		cfgYaml, err := yaml.Marshal(config)
		require.NoError(t, err)

		apko := c.Container().
			From("cgr.dev/chainguard/apko:latest").
			WithNewFile("config.yml", dagger.ContainerWithNewFileOpts{
				Contents: string(cfgYaml),
			})

		imageFile := apko.
			WithExec([]string{
				"build",
				"config.yml", "latest", "output.tar",
			}).
			File("output.tar")

		imported := c.Container().Import(imageFile)

		out, err := imported.WithExec([]string{"sh", "-c", "echo $FOO"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar\n", out)
	})

	t.Run("Docker", func(t *testing.T) {
		out, err := c.Container().
			Import(c.Container().From(alpineImage).WithEnvVariable("FOO", "bar").AsTarball(dagger.ContainerAsTarballOpts{
				MediaTypes: dagger.Dockermediatypes,
			})).
			WithExec([]string{"sh", "-c", "echo $FOO"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar\n", out)
	})
}

func TestContainerFromIDPlatform(t *testing.T) {
	c, ctx := connect(t)

	var desiredPlatform dagger.Platform = "linux/arm64"

	id, err := c.Container(dagger.ContainerOpts{
		Platform: desiredPlatform,
	}).From(alpineImage).ID(ctx)
	require.NoError(t, err)

	platform, err := c.Container(dagger.ContainerOpts{
		ID: id,
	}).Platform(ctx)
	require.NoError(t, err)
	require.Equal(t, desiredPlatform, platform)
}

func TestContainerMultiPlatformExport(t *testing.T) {
	for _, useAsTarball := range []bool{true, false} {
		t.Run(fmt.Sprintf("useAsTarball=%t", useAsTarball), func(t *testing.T) {
			c, ctx := connect(t)

			variants := make([]*dagger.Container, 0, len(platformToUname))
			for platform, uname := range platformToUname {
				ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
					From(alpineImage).
					WithExec([]string{"uname", "-m"}).
					WithEntrypoint([]string{"echo", uname})
				variants = append(variants, ctr)
			}

			dest := filepath.Join(t.TempDir(), "image.tar")

			if useAsTarball {
				tarFile := c.Container().AsTarball(dagger.ContainerAsTarballOpts{
					PlatformVariants: variants,
				})
				ok, err := tarFile.Export(ctx, dest)
				require.NoError(t, err)
				require.True(t, ok)
			} else {
				ok, err := c.Container().Export(ctx, dest, dagger.ContainerExportOpts{
					PlatformVariants: variants,
				})
				require.NoError(t, err)
				require.True(t, ok)
			}

			entries := tarEntries(t, dest)
			require.Contains(t, entries, "oci-layout")
			// multi-platform images don't contain a manifest.json
			require.NotContains(t, entries, "manifest.json")

			indexBytes := readTarFile(t, dest, "index.json")
			var index ocispecs.Index
			require.NoError(t, json.Unmarshal(indexBytes, &index))
			// index is nested (search "nested index" in spec here):
			// https://github.com/opencontainers/image-spec/blob/main/image-index.md
			nestedIndexDigest := index.Manifests[0].Digest
			indexBytes = readTarFile(t, dest, "blobs/sha256/"+nestedIndexDigest.Encoded())
			index = ocispecs.Index{}
			require.NoError(t, json.Unmarshal(indexBytes, &index))

			// make sure all the platforms we expected are there
			exportedPlatforms := make(map[string]struct{})
			for _, desc := range index.Manifests {
				require.NotNil(t, desc.Platform)
				platformStr := platforms.Format(*desc.Platform)
				exportedPlatforms[platformStr] = struct{}{}

				manifestDigest := desc.Digest
				manifestBytes := readTarFile(t, dest, "blobs/sha256/"+manifestDigest.Encoded())
				var manifest ocispecs.Manifest
				require.NoError(t, json.Unmarshal(manifestBytes, &manifest))
				configDigest := manifest.Config.Digest
				configBytes := readTarFile(t, dest, "blobs/sha256/"+configDigest.Encoded())
				var config ocispecs.Image
				require.NoError(t, json.Unmarshal(configBytes, &config))
				require.Equal(t, []string{"echo", platformToUname[dagger.Platform(platformStr)]}, config.Config.Entrypoint)
			}
			for platform := range platformToUname {
				delete(exportedPlatforms, string(platform))
			}
			require.Empty(t, exportedPlatforms)
		})
	}
}

// Multiplatform publish is also tested in more complicated scenarios in platform_test.go
func TestContainerMultiPlatformPublish(t *testing.T) {
	c, ctx := connect(t)

	variants := make([]*dagger.Container, 0, len(platformToUname))
	for platform, uname := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(alpineImage).
			WithExec([]string{"uname", "-m"}).
			WithEntrypoint([]string{"echo", uname})
		variants = append(variants, ctr)
	}

	testRef := registryRef("container-multiplatform-publish")

	publishedRef, err := c.Container().Publish(ctx, testRef, dagger.ContainerPublishOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)

	for platform, uname := range platformToUname {
		output, err := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(publishedRef).
			WithExec(nil).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, uname+"\n", output)
	}
}

func TestContainerMultiPlatformImport(t *testing.T) {
	c, ctx := connect(t)

	variants := make([]*dagger.Container, 0, len(platformToUname))
	for platform := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(alpineImage)

		variants = append(variants, ctr)
	}

	tmp := t.TempDir()
	imagePath := filepath.Join(tmp, "image.tar")

	ok, err := c.Container().Export(ctx, imagePath, dagger.ContainerExportOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)
	require.True(t, ok)

	for platform, uname := range platformToUname {
		imported := c.Container(dagger.ContainerOpts{Platform: platform}).
			Import(c.Host().Directory(tmp).File("image.tar"))

		out, err := imported.WithExec([]string{"uname", "-m"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, uname+"\n", out)
	}
}

func TestContainerWithDirectoryToMount(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	mnt := c.Directory().
		WithNewDirectory("/top/sub-dir/sub-file").
		Directory("/top") // <-- the important part!
	ctr := c.Container().
		From(alpineImage).
		WithMountedDirectory("/mnt", mnt)

	dir := c.Directory().
		WithNewFile("/copied-file", "some-content")

	ctr = ctr.WithDirectory("/mnt/sub-dir/copied-dir", dir)

	contents, err := ctr.WithExec([]string{"find", "/mnt"}).Stdout(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"/mnt",
		"/mnt/sub-dir",
		"/mnt/sub-dir/sub-file",
		"/mnt/sub-dir/copied-dir",
		"/mnt/sub-dir/copied-dir/copied-file",
	}, strings.Split(strings.Trim(contents, "\n"), "\n"))
}

func TestContainerExecError(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	outMsg := "THIS SHOULD GO TO STDOUT"
	encodedOutMsg := base64.StdEncoding.EncodeToString([]byte(outMsg))
	errMsg := "THIS SHOULD GO TO STDERR"
	encodedErrMsg := base64.StdEncoding.EncodeToString([]byte(errMsg))

	t.Run("includes output of failed exec in error", func(t *testing.T) {
		_, err := c.Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", fmt.Sprintf(
				`echo %s | base64 -d >&1; echo %s | base64 -d >&2; exit 1`, encodedOutMsg, encodedErrMsg,
			)}).
			Sync(ctx)

		var exErr *dagger.ExecError

		require.ErrorAs(t, err, &exErr)
		require.Equal(t, outMsg, exErr.Stdout)
		require.Equal(t, errMsg, exErr.Stderr)
	})

	t.Run("includes output of failed exec in error when redirects are enabled", func(t *testing.T) {
		_, err := c.Container().
			From(alpineImage).
			WithExec(
				[]string{"sh", "-c", fmt.Sprintf(
					`echo %s | base64 -d >&1; echo %s | base64 -d >&2; exit 1`, encodedOutMsg, encodedErrMsg,
				)},
				dagger.ContainerWithExecOpts{
					RedirectStdout: "/out",
					RedirectStderr: "/err",
				},
			).
			Sync(ctx)

		var exErr *dagger.ExecError

		require.ErrorAs(t, err, &exErr)
		require.Equal(t, outMsg, exErr.Stdout)
		require.Equal(t, errMsg, exErr.Stderr)
	})

	t.Run("truncates output past a maximum size", func(t *testing.T) {
		// fill a byte buffer with a string that is slightly over the size of the max output
		// size, then base64 encode it
		var stdoutBuf bytes.Buffer
		for i := 0; i < buildkit.MaxExecErrorOutputBytes+50; i++ {
			stdoutBuf.WriteByte('a')
		}
		stdoutStr := stdoutBuf.String()
		encodedOutMsg := base64.StdEncoding.EncodeToString(stdoutBuf.Bytes())

		var stderrBuf bytes.Buffer
		for i := 0; i < buildkit.MaxExecErrorOutputBytes+50; i++ {
			stderrBuf.WriteByte('b')
		}
		stderrStr := stderrBuf.String()
		encodedErrMsg := base64.StdEncoding.EncodeToString(stderrBuf.Bytes())

		truncMsg := fmt.Sprintf(buildkit.TruncationMessage, 50)

		_, err := c.Container().
			From(alpineImage).
			WithDirectory("/", c.Directory().
				WithNewFile("encout", encodedOutMsg).
				WithNewFile("encerr", encodedErrMsg),
			).
			WithExec([]string{"sh", "-c", "base64 -d encout >&1; base64 -d encerr >&2; exit 1"}).
			Sync(ctx)

		var exErr *dagger.ExecError

		require.ErrorAs(t, err, &exErr)
		require.Equal(t, truncMsg+stdoutStr[:buildkit.MaxExecErrorOutputBytes-len(truncMsg)], exErr.Stdout)
		require.Equal(t, truncMsg+stderrStr[:buildkit.MaxExecErrorOutputBytes-len(truncMsg)], exErr.Stderr)
	})
}

func TestContainerWithRegistryAuth(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	testRef := privateRegistryRef("container-with-registry-auth")
	container := c.Container().From(alpineImage)

	// Push without credentials should fail
	_, err := container.Publish(ctx, testRef)
	require.Error(t, err)

	pushedRef, err := container.
		WithRegistryAuth(
			privateRegistryHost,
			"john",
			c.SetSecret("this-secret", "xFlejaPdjrt25Dvr"),
		).
		Publish(ctx, testRef)

	require.NoError(t, err)
	require.NotEqual(t, testRef, pushedRef)
	require.Contains(t, pushedRef, "@sha256:")
}

func TestContainerImageRef(t *testing.T) {
	t.Parallel()

	t.Run("should test query returning imageRef", func(t *testing.T) {
		res := struct {
			Container struct {
				From struct {
					ImageRef string
				}
			}
		}{}

		err := testutil.Query(
			`{
				container {
					from(address: "`+alpineImage+`") {
						imageRef
					}
				}
			}`, &res, nil)
		require.NoError(t, err)
		require.Contains(t, res.Container.From.ImageRef, "docker.io/library/alpine:3.18.2@sha256:")
	})

	t.Run("should throw error after the container image modification with exec", func(t *testing.T) {
		res := struct {
			Container struct {
				From struct {
					ImageRef string
				}
			}
		}{}

		err := testutil.Query(
			`{
				container {
					from(address:"hello-world") {
						withExec(args:["/hello"]) {
							imageRef
						}
					}
				}
			}`, &res, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
	})

	t.Run("should throw error after the container image modification with exec", func(t *testing.T) {
		res := struct {
			Container struct {
				From struct {
					ImageRef string
				}
			}
		}{}

		err := testutil.Query(
			`{
				container {
					from(address:"hello-world") {
						withExec(args:["/hello"]) {
							imageRef
						}
					}
				}
			}`, &res, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
	})

	t.Run("should throw error after the container image modification with directory", func(t *testing.T) {
		c, ctx := connect(t)

		dir := c.Directory().
			WithNewFile("some-file", "some-content").
			WithNewFile("some-dir/sub-file", "sub-content").
			Directory("some-dir")

		ctr := c.Container().
			From(alpineImage).
			WithWorkdir("/workdir").
			WithDirectory("with-dir", dir)

		_, err := ctr.ImageRef(ctx)

		require.Error(t, err)
		require.Contains(t, err.Error(), "Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
	})
}

func TestContainerBuildNilContextError(t *testing.T) {
	t.Parallel()

	// regression test, this previously caused the engine to panic
	err := testutil.Query(
		`{
			container {
				build(context: "") {
					id
				}
			}
		}`, &map[any]any{}, nil)
	require.ErrorContains(t, err, "cannot decode empty string as ID")
}

func TestContainerInsecureRootCapabilites(t *testing.T) {
	c, ctx := connect(t)

	// This isn't exhaustive, but it's the major important ones. Being exhaustive
	// is trickier since the full list of caps is host dependent based on the kernel version.
	privilegedCaps := []string{
		"cap_sys_admin",
		"cap_net_admin",
		"cap_sys_module",
		"cap_sys_ptrace",
		"cap_sys_boot",
		"cap_sys_rawio",
		"cap_sys_resource",
	}

	for _, capSet := range []string{"CapPrm", "CapEff", "CapBnd"} {
		out, err := c.Container().From(alpineImage).
			WithExec([]string{"apk", "add", "libcap"}).
			WithExec([]string{"sh", "-c", "capsh --decode=$(grep " + capSet + " /proc/self/status | awk '{print $2}')"}).
			Stdout(ctx)
		require.NoError(t, err)
		for _, privCap := range privilegedCaps {
			require.NotContains(t, out, privCap)
		}
	}

	for _, capSet := range []string{"CapPrm", "CapEff", "CapBnd", "CapInh", "CapAmb"} {
		out, err := c.Container().From(alpineImage).
			WithExec([]string{"apk", "add", "libcap"}).
			WithExec([]string{"sh", "-c", "capsh --decode=$(grep " + capSet + " /proc/self/status | awk '{print $2}')"}, dagger.ContainerWithExecOpts{
				InsecureRootCapabilities: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		for _, privCap := range privilegedCaps {
			require.Contains(t, out, privCap)
		}
	}
}

func TestContainerInsecureRootCapabilitesWithService(t *testing.T) {
	c, ctx := connect(t)

	// verify the root capabilities setting works by executing dockerd with it and
	// testing it can startup, create containers and bind mount from its filesystem to
	// them.
	dockerd := c.Container().From("docker:23.0.1-dind").
		WithMountedCache("/var/lib/docker", c.CacheVolume("docker-lib"), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.Private,
		}).
		WithMountedCache("/tmp", c.CacheVolume("share-tmp")).
		WithExposedPort(2375).
		WithExec([]string{
			"dockerd",
			"--host=tcp://0.0.0.0:2375",
			"--tls=false",
		}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).AsService()

	dockerHost, err := dockerd.Endpoint(ctx, dagger.ServiceEndpointOpts{
		Scheme: "tcp",
	})
	require.NoError(t, err)

	randID := identity.NewID()
	out, err := c.Container().From("docker:23.0.1-cli").
		WithMountedCache("/tmp", c.CacheVolume("share-tmp")).
		WithServiceBinding("docker", dockerd).
		WithEnvVariable("DOCKER_HOST", dockerHost).
		WithExec([]string{"sh", "-e", "-c", strings.Join([]string{
			fmt.Sprintf("echo %s-from-outside > /tmp/from-outside", randID),
			"docker run --rm -v /tmp:/tmp alpine cat /tmp/from-outside",
			fmt.Sprintf("docker run --rm -v /tmp:/tmp alpine sh -c 'echo %s-from-inside > /tmp/from-inside'", randID),
			"cat /tmp/from-inside",
		}, "\n")}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("%s-from-outside\n%s-from-inside\n", randID, randID), out)
}

func TestContainerNoExec(t *testing.T) {
	c, ctx := connect(t)

	stdout, err := c.Container().From(alpineImage).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "", stdout)

	stderr, err := c.Container().From(alpineImage).Stderr(ctx)
	require.NoError(t, err)
	require.Equal(t, "", stderr)

	_, err = c.Container().
		From(alpineImage).
		WithoutDefaultArgs().
		Stdout(ctx)

	require.Error(t, err)
	require.Contains(t, err.Error(), "no command has been set")
}

func TestContainerWithMountedFileOwner(t *testing.T) {
	c, ctx := connect(t)

	t.Run("simple file", func(t *testing.T) {
		tmp := t.TempDir()

		err := os.WriteFile(filepath.Join(tmp, "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		file := c.Host().Directory(tmp).File("message.txt")

		testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithMountedFile(name, file, dagger.ContainerWithMountedFileOpts{
				Owner: owner,
			})
		})
	})

	t.Run("file from subdirectory", func(t *testing.T) {
		tmp := t.TempDir()

		err := os.Mkdir(filepath.Join(tmp, "subdir"), 0o755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(tmp, "subdir", "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		file := c.Host().Directory(tmp).Directory("subdir").File("message.txt")

		testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithMountedFile(name, file, dagger.ContainerWithMountedFileOpts{
				Owner: owner,
			})
		})
	})
}

func TestContainerWithMountedDirectoryOwner(t *testing.T) {
	c, ctx := connect(t)

	t.Run("simple directory", func(t *testing.T) {
		tmp := t.TempDir()

		err := os.WriteFile(filepath.Join(tmp, "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		dir := c.Host().Directory(tmp)

		testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithMountedDirectory(name, dir, dagger.ContainerWithMountedDirectoryOpts{
				Owner: owner,
			})
		})
	})

	t.Run("subdirectory", func(t *testing.T) {
		tmp := t.TempDir()

		err := os.Mkdir(filepath.Join(tmp, "subdir"), 0o755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(tmp, "subdir", "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		dir := c.Host().Directory(tmp).Directory("subdir")

		testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithMountedDirectory(name, dir, dagger.ContainerWithMountedDirectoryOpts{
				Owner: owner,
			})
		})
	})

	t.Run("permissions", func(t *testing.T) {
		dir := c.Directory().
			WithNewDirectory("perms", dagger.DirectoryWithNewDirectoryOpts{
				Permissions: 0o745,
			}).
			WithNewFile("perms/foo", "whee", dagger.DirectoryWithNewFileOpts{
				Permissions: 0o645,
			}).
			Directory("perms")

		ctr := c.Container().From(alpineImage).
			WithExec([]string{"adduser", "-D", "inherituser"}).
			WithExec([]string{"adduser", "-u", "1234", "-D", "auser"}).
			WithExec([]string{"addgroup", "-g", "4321", "agroup"}).
			WithUser("inherituser").
			WithMountedDirectory("/data", dir, dagger.ContainerWithMountedDirectoryOpts{
				Owner: "auser:agroup",
			})

		out, err := ctr.WithExec([]string{"stat", "-c", "%a:%U:%G", "/data"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "745:auser:agroup\n", out)

		out, err = ctr.WithExec([]string{"stat", "-c", "%a:%U:%G", "/data/foo"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "645:auser:agroup\n", out)
	})
}

func TestContainerWithFileOwner(t *testing.T) {
	c, ctx := connect(t)

	t.Run("simple file", func(t *testing.T) {
		tmp := t.TempDir()

		err := os.WriteFile(filepath.Join(tmp, "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		file := c.Host().Directory(tmp).File("message.txt")

		testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithFile(name, file, dagger.ContainerWithFileOpts{
				Owner: owner,
			})
		})
	})

	t.Run("file from subdirectory", func(t *testing.T) {
		tmp := t.TempDir()

		err := os.Mkdir(filepath.Join(tmp, "subdir"), 0o755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(tmp, "subdir", "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		file := c.Host().Directory(tmp).Directory("subdir").File("message.txt")

		testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithFile(name, file, dagger.ContainerWithFileOpts{
				Owner: owner,
			})
		})
	})
}

func TestContainerWithDirectoryOwner(t *testing.T) {
	c, ctx := connect(t)

	t.Run("simple directory", func(t *testing.T) {
		tmp := t.TempDir()

		err := os.WriteFile(filepath.Join(tmp, "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		dir := c.Host().Directory(tmp)

		testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithDirectory(name, dir, dagger.ContainerWithDirectoryOpts{
				Owner: owner,
			})
		})
	})

	t.Run("subdirectory", func(t *testing.T) {
		tmp := t.TempDir()

		err := os.Mkdir(filepath.Join(tmp, "subdir"), 0o755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(tmp, "subdir", "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		dir := c.Host().Directory(tmp).Directory("subdir")

		testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithDirectory(name, dir, dagger.ContainerWithDirectoryOpts{
				Owner: owner,
			})
		})
	})
}

func TestContainerWithNewFileOwner(t *testing.T) {
	c, ctx := connect(t)

	testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
		return ctr.WithNewFile(name, dagger.ContainerWithNewFileOpts{
			Owner: owner,
		})
	})
}

func TestContainerWithMountedCacheOwner(t *testing.T) {
	c, ctx := connect(t)

	cache := c.CacheVolume("test")

	testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
		return ctr.WithMountedCache(name, cache, dagger.ContainerWithMountedCacheOpts{
			Owner: owner,
		})
	})

	t.Run("permissions (empty)", func(t *testing.T) {
		ctr := c.Container().From(alpineImage).
			WithExec([]string{"adduser", "-D", "inherituser"}).
			WithExec([]string{"adduser", "-u", "1234", "-D", "auser"}).
			WithExec([]string{"addgroup", "-g", "4321", "agroup"}).
			WithUser("inherituser").
			WithMountedCache("/data", cache, dagger.ContainerWithMountedCacheOpts{
				Owner: "auser:agroup",
			})

		out, err := ctr.WithExec([]string{"stat", "-c", "%a:%U:%G", "/data"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "755:auser:agroup\n", out)
	})

	t.Run("permissions (source)", func(t *testing.T) {
		dir := c.Directory().
			WithNewDirectory("perms", dagger.DirectoryWithNewDirectoryOpts{
				Permissions: 0o745,
			}).
			WithNewFile("perms/foo", "whee", dagger.DirectoryWithNewFileOpts{
				Permissions: 0o645,
			}).
			Directory("perms")

		ctr := c.Container().From(alpineImage).
			WithExec([]string{"adduser", "-D", "inherituser"}).
			WithExec([]string{"adduser", "-u", "1234", "-D", "auser"}).
			WithExec([]string{"addgroup", "-g", "4321", "agroup"}).
			WithUser("inherituser").
			WithMountedCache("/data", cache, dagger.ContainerWithMountedCacheOpts{
				Source: dir,
				Owner:  "auser:agroup",
			})

		out, err := ctr.WithExec([]string{"stat", "-c", "%a:%U:%G", "/data"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "745:auser:agroup\n", out)

		out, err = ctr.WithExec([]string{"stat", "-c", "%a:%U:%G", "/data/foo"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "645:auser:agroup\n", out)
	})
}

func TestContainerWithMountedSecretOwner(t *testing.T) {
	c, ctx := connect(t)

	secret := c.SetSecret("test", "hunter2")

	testOwnership(ctx, t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
		return ctr.WithMountedSecret(name, secret, dagger.ContainerWithMountedSecretOpts{
			Owner: owner,
		})
	})
}

func TestContainerParallelMutation(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			A struct {
				EnvVariable string
			}
			B string
		}
	}{}

	err := testutil.Query(
		`{
			container {
				a: withEnvVariable(name: "FOO", value: "BAR") {
					envVariable(name: "FOO")
				}
				b: envVariable(name: "FOO")
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.A.EnvVariable, "BAR")
	require.Empty(t, res.Container.B, "BAR")
}

func TestContainerForceCompression(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		compression          dagger.ImageLayerCompression
		expectedOCIMediaType string
	}{
		{
			dagger.Gzip,
			"application/vnd.oci.image.layer.v1.tar+gzip",
		},
		{
			dagger.Zstd,
			"application/vnd.oci.image.layer.v1.tar+zstd",
		},
		{
			dagger.Uncompressed,
			"application/vnd.oci.image.layer.v1.tar",
		},
		{
			dagger.Estargz,
			"application/vnd.oci.image.layer.v1.tar+gzip",
		},
	} {
		tc := tc
		t.Run(string(tc.compression), func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			ref := registryRef("testcontainerpublishforcecompression" + strings.ToLower(string(tc.compression)))
			_, err := c.Container().
				From(alpineImage).
				Publish(ctx, ref, dagger.ContainerPublishOpts{
					ForcedCompression: tc.compression,
				})
			require.NoError(t, err)

			parsedRef, err := name.ParseReference(ref, name.Insecure)
			require.NoError(t, err)

			imgDesc, err := remote.Get(parsedRef, remote.WithTransport(http.DefaultTransport))
			require.NoError(t, err)
			img, err := imgDesc.Image()
			require.NoError(t, err)
			layers, err := img.Layers()
			require.NoError(t, err)
			for _, layer := range layers {
				mediaType, err := layer.MediaType()
				require.NoError(t, err)
				require.EqualValues(t, tc.expectedOCIMediaType, mediaType)
			}

			tarPath := filepath.Join(t.TempDir(), "export.tar")
			_, err = c.Container().
				From(alpineImage).
				Export(ctx, tarPath, dagger.ContainerExportOpts{
					ForcedCompression: tc.compression,
				})
			require.NoError(t, err)

			// check that docker compatible manifest is present
			dockerManifestBytes := readTarFile(t, tarPath, "manifest.json")
			require.NotNil(t, dockerManifestBytes)

			indexBytes := readTarFile(t, tarPath, "index.json")
			var index ocispecs.Index
			require.NoError(t, json.Unmarshal(indexBytes, &index))

			manifestDigest := index.Manifests[0].Digest
			manifestBytes := readTarFile(t, tarPath, "blobs/sha256/"+manifestDigest.Encoded())
			var manifest ocispecs.Manifest
			require.NoError(t, json.Unmarshal(manifestBytes, &manifest))
			for _, layer := range manifest.Layers {
				require.EqualValues(t, tc.expectedOCIMediaType, layer.MediaType)
			}
		})
	}
}

func TestContainerMediaTypes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		mediaTypes           dagger.ImageMediaTypes
		expectedOCIMediaType string
	}{
		{
			"", // use default
			"application/vnd.oci.image.layer.v1.tar+gzip",
		},
		{
			dagger.Ocimediatypes,
			"application/vnd.oci.image.layer.v1.tar+gzip",
		},
		{
			dagger.Dockermediatypes,
			"application/vnd.docker.image.rootfs.diff.tar.gzip",
		},
	} {
		tc := tc
		t.Run(string(tc.mediaTypes), func(t *testing.T) {
			t.Parallel()

			c, ctx := connect(t)

			ref := registryRef("testcontainerpublishmediatypes" + strings.ToLower(string(tc.mediaTypes)))
			_, err := c.Container().
				From(alpineImage).
				Publish(ctx, ref, dagger.ContainerPublishOpts{
					MediaTypes: tc.mediaTypes,
				})
			require.NoError(t, err)

			parsedRef, err := name.ParseReference(ref, name.Insecure)
			require.NoError(t, err)

			imgDesc, err := remote.Get(parsedRef, remote.WithTransport(http.DefaultTransport))
			require.NoError(t, err)
			img, err := imgDesc.Image()
			require.NoError(t, err)
			layers, err := img.Layers()
			require.NoError(t, err)
			for _, layer := range layers {
				mediaType, err := layer.MediaType()
				require.NoError(t, err)
				require.EqualValues(t, tc.expectedOCIMediaType, mediaType)
			}

			for _, useAsTarball := range []bool{true, false} {
				useAsTarball := useAsTarball
				t.Run(fmt.Sprintf("useAsTarball=%t", useAsTarball), func(t *testing.T) {
					tarPath := filepath.Join(t.TempDir(), "export.tar")
					if useAsTarball {
						_, err := c.Container().
							From(alpineImage).
							AsTarball(dagger.ContainerAsTarballOpts{
								MediaTypes: tc.mediaTypes,
							}).
							Export(ctx, tarPath)
						require.NoError(t, err)
					} else {
						_, err := c.Container().
							From(alpineImage).
							Export(ctx, tarPath, dagger.ContainerExportOpts{
								MediaTypes: tc.mediaTypes,
							})
						require.NoError(t, err)
					}

					// check that docker compatible manifest is present
					dockerManifestBytes := readTarFile(t, tarPath, "manifest.json")
					require.NotNil(t, dockerManifestBytes)

					indexBytes := readTarFile(t, tarPath, "index.json")
					var index ocispecs.Index
					require.NoError(t, json.Unmarshal(indexBytes, &index))

					manifestDigest := index.Manifests[0].Digest
					manifestBytes := readTarFile(t, tarPath, "blobs/sha256/"+manifestDigest.Encoded())
					var manifest ocispecs.Manifest
					require.NoError(t, json.Unmarshal(manifestBytes, &manifest))
					for _, layer := range manifest.Layers {
						require.EqualValues(t, tc.expectedOCIMediaType, layer.MediaType)
					}
				})
			}
		})
	}
}

func TestContainerBuildMergesWithParent(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	// Create a builder container
	builderCtr := c.Directory().WithNewFile("Dockerfile",
		`FROM `+alpineImage+`
ENV FOO=BAR
LABEL "com.example.test-should-replace"="foo"
EXPOSE 8080
`,
	)

	// Create a container with envs variables and labels
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

	res := struct {
		Container struct {
			ExposedPorts []core.Port
		}
	}{}

	err = testutil.Query(`
        query Test($id: ContainerID!) {
            container(id: $id) {
                exposedPorts {
                    port
                    protocol
                    description
                }
            }
        }`,
		&res,
		&testutil.QueryOptions{
			Variables: map[string]interface{}{
				"id": cid,
			},
		},
	)
	require.NoError(t, err)
	require.Len(t, res.Container.ExposedPorts, 2)

	// random order since ImageConfig.ExposedPorts is a map
	for _, p := range res.Container.ExposedPorts {
		require.Equal(t, core.NetworkProtocolTCP, p.Protocol)
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

func TestContainerFromMergesWithParent(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	// Create a container with envs and pull alpine image on it
	testCtr := c.Container().
		WithEnvVariable("FOO", "BAR").
		WithEnvVariable("PATH", "/replace/me").
		WithLabel("moby.buildkit.frontend.caps", "replace-me").
		WithLabel("com.example.test-should-exist", "exist").
		WithExposedPort(5000).
		From("docker/dockerfile:1.5")

	envShouldExist, err := testCtr.EnvVariable(ctx, "FOO")
	require.NoError(t, err)
	require.Equal(t, "BAR", envShouldExist)

	envShouldBeReplaced, err := testCtr.EnvVariable(ctx, "PATH")
	require.NoError(t, err)
	require.Equal(t, "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", envShouldBeReplaced)

	labelShouldExist, err := testCtr.Label(ctx, "com.example.test-should-exist")
	require.NoError(t, err)
	require.Equal(t, "exist", labelShouldExist)

	existingLabelFromImageShouldExist, err := testCtr.Label(ctx, "moby.buildkit.frontend.network.none")
	require.NoError(t, err)
	require.Equal(t, "true", existingLabelFromImageShouldExist)

	labelShouldBeReplaced, err := testCtr.Label(ctx, "moby.buildkit.frontend.caps")
	require.NoError(t, err)
	require.Equal(t, "moby.buildkit.frontend.inputs,moby.buildkit.frontend.subrequests,moby.buildkit.frontend.contexts", labelShouldBeReplaced)

	ports, err := testCtr.ExposedPorts(ctx)
	require.NoError(t, err)

	port, err := ports[0].Port(ctx)
	require.NoError(t, err)
	require.Equal(t, 5000, port)
}

func TestContainerImageLoadCompatibility(t *testing.T) {
	t.Parallel()
	c, ctx := connect(t)

	for i, dockerVersion := range []string{"20.10", "23.0", "24.0"} {
		dockerVersion := dockerVersion
		port := 2375 + i
		dockerd := c.Container().From(fmt.Sprintf("docker:%s-dind", dockerVersion)).
			WithMountedCache("/var/lib/docker", c.CacheVolume(t.Name()+"-"+dockerVersion+"-docker-lib"), dagger.ContainerWithMountedCacheOpts{
				Sharing: dagger.Private,
			}).
			WithExposedPort(port).
			WithExec([]string{
				"dockerd",
				"--host=tcp://0.0.0.0:" + strconv.Itoa(port),
				"--tls=false",
			}, dagger.ContainerWithExecOpts{
				InsecureRootCapabilities: true,
			}).
			AsService()

		dockerHost, err := dockerd.Endpoint(ctx, dagger.ServiceEndpointOpts{
			Scheme: "tcp",
		})
		require.NoError(t, err)

		for _, mediaType := range []dagger.ImageMediaTypes{dagger.Ocimediatypes, dagger.Dockermediatypes} {
			mediaType := mediaType
			for _, compression := range []dagger.ImageLayerCompression{dagger.Gzip, dagger.Zstd, dagger.Uncompressed} {
				compression := compression
				t.Run(fmt.Sprintf("%s-%s-%s-%s", t.Name(), dockerVersion, mediaType, compression), func(t *testing.T) {
					t.Parallel()
					tmpdir := t.TempDir()
					tmpfile := filepath.Join(tmpdir, fmt.Sprintf("test-%s-%s-%s.tar", dockerVersion, mediaType, compression))
					_, err := c.Container().From(alpineImage).
						// we need a unique image, otherwise docker load skips it after the first tar load
						WithExec([]string{"sh", "-c", "echo '" + string(compression) + string(mediaType) + "' > /foo"}).
						Export(ctx, tmpfile, dagger.ContainerExportOpts{
							MediaTypes:        mediaType,
							ForcedCompression: compression,
						})
					require.NoError(t, err)

					randID := identity.NewID()
					ctr := c.Container().From(fmt.Sprintf("docker:%s-cli", dockerVersion)).
						WithEnvVariable("CACHEBUST", randID).
						WithServiceBinding("docker", dockerd).
						WithEnvVariable("DOCKER_HOST", dockerHost).
						WithMountedFile(path.Join("/", path.Base(tmpfile)), c.Host().File(tmpfile)).
						WithExec([]string{"docker", "load", "-i", "/" + path.Base(tmpfile)})

					output, err := ctr.Stdout(ctx)
					if dockerVersion == "20.10" && compression == dagger.Zstd {
						// zstd support in docker wasn't added until 23, so sanity check that it fails
						require.Error(t, err)
					} else {
						require.NoError(t, err)
						_, imageID, ok := strings.Cut(output, "sha256:")
						require.True(t, ok)
						imageID = strings.TrimSpace(imageID)

						_, err = ctr.WithExec([]string{"docker", "run", "--rm", imageID, "echo", "hello"}).Sync(ctx)
						require.NoError(t, err)
					}

					// also check that buildkit can load+run it too
					_, err = c.Container().
						Import(c.Host().File(tmpfile)).
						WithExec([]string{"echo", "hello"}).
						Sync(ctx)
					require.NoError(t, err)
				})
			}
		}
	}
}

func TestContainerWithMountedSecretMode(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	t.Cleanup(func() { c.Close() })

	secret := c.SetSecret("test", "secret")

	ctr := c.Container().From(alpineImage).WithMountedSecret("/secret", secret, dagger.ContainerWithMountedSecretOpts{
		Mode:  0o666,
		Owner: "root:root",
	})

	perms, err := ctr.WithExec([]string{"sh", "-c", "stat /secret "}).Stdout(ctx)
	require.Contains(t, perms, "0666/-rw-rw-rw-")
	require.NoError(t, err)
}
