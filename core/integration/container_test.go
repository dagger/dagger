package core

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func TestContainerScratch(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			ID string
			Fs struct {
				Entries []string
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				id
				fs {
					entries
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Empty(t, res.Container.Fs.Entries)
}

func TestContainerFrom(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Fs struct {
					File struct {
						Contents string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "alpine:3.16.2") {
					fs {
						file(path: "/etc/alpine-release") {
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.Fs.File.Contents, "3.16.2\n")
}

func TestContainerBuild(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

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

		env, err := c.Container().Build(src).WithExec([]string{}).Stdout(ctx)
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
		}).WithExec([]string{}).Stdout(ctx)
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

		env, err := c.Container().Build(sub).WithExec([]string{}).Stdout(ctx)
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
		}).WithExec([]string{}).Stdout(ctx)
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

		env, err := c.Container().Build(src).WithExec([]string{}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")

		env, err = c.Container().Build(src, dagger.ContainerBuildOpts{BuildArgs: []dagger.BuildArg{{Name: "FOOARG", Value: "barbar"}}}).WithExec([]string{}).Stdout(ctx)
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

		output, err := c.Container().Build(src).WithExec([]string{}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage2\n")

		output, err = c.Container().Build(src, dagger.ContainerBuildOpts{Target: "stage1"}).WithExec([]string{}).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, output, "stage1\n")
		require.NotContains(t, output, "stage2\n")
	})
}

func TestContainerWithRootFS(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	alpine316 := c.Container().From("alpine:3.16.2")

	alpine316ReleaseStr, err := alpine316.File("/etc/alpine-release").Contents(ctx)
	require.NoError(t, err)

	alpine316ReleaseStr = strings.TrimSpace(alpine316ReleaseStr)
	dir := alpine316.Rootfs()
	exitCode, err := c.Container().WithEnvVariable("ALPINE_RELEASE", alpine316ReleaseStr).WithRootfs(dir).WithExec([]string{
		"/bin/sh",
		"-c",
		"test -f /etc/alpine-release && test \"$(head -n 1 /etc/alpine-release)\" = \"$ALPINE_RELEASE\"",
	}).ExitCode(ctx)

	require.NoError(t, err)
	require.Equal(t, exitCode, 0)

	alpine315 := c.Container().From("alpine:3.15.6")

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

	require.Equal(t, "3.16.2\n", releaseStr)
}

func TestContainerExecExitCode(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithExec struct {
					ExitCode *int
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "alpine:3.16.2") {
					withExec(args: ["true"]) {
						exitCode
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.NotNil(t, res.Container.From.WithExec.ExitCode)
	require.Equal(t, 0, *res.Container.From.WithExec.ExitCode)

	/*
		It's not currently possible to get a nonzero exit code back because
		Buildkit raises an error.

		We could perhaps have the shim mask the exit status and always exit 0, but
		we would have to be careful not to let that happen in a big chained LLB
		since it would prevent short-circuiting.

		We could only do it when the user requests the exitCode, but then we would
		actually need to run the command _again_ since we'd need some way to tell
		the shim what to do.

		Hmm...

		err = testutil.Query(
			`{
				container {
					from(address: "alpine:3.16.2") {
						withExec(args: ["false"]) {
							exitCode
						}
					}
				}
			}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, res.Container.From.WithExec.ExitCode, 1)
	*/
}

func TestContainerRun(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	err := c.Container().
		From("alpine:3.16.2").
		WithExec([]string{"true"}).
		Run(ctx)
	require.NoError(t, err)

	err = c.Container().
		From("alpine:3.16.2").
		WithExec([]string{"false"}).
		Run(ctx)
	require.Error(t, err)

	err = c.Container().
		From("alpine:3.16.2").
		WithExec([]string{"sh", "-exc", "echo hello; echo goodbye >/dev/stderr; exit 42"}).
		Run(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exit code: 42")
	require.Contains(t, err.Error(), "hello")
	require.Contains(t, err.Error(), "goodbye")
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
	defer c.Close()

	execWithMount := c.Container().From("alpine:3.16.2").
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

	err = testutil.Query(
		`{
			container {
				from(address: "alpine:3.16.2") {
					exec(
						args: ["sh", "-c", "echo hello; echo goodbye >/dev/stderr"],
						redirectStdout: "out",
						redirectStderr: "err"
					) {
						stdout
						stderr
					}
				}
			}
		}`, &res, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stdout: no such file or directory")
	require.Contains(t, err.Error(), "stderr: no such file or directory")
}

func TestContainerNullStdoutStderr(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Stdout *string
				Stderr *string
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "alpine:3.16.2") {
					stdout
					stderr
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Nil(t, res.Container.From.Stdout)
	require.Nil(t, res.Container.From.Stderr)
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
}

func TestContainerExecWithEntrypoint(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Entrypoint     []string
				WithEntrypoint struct {
					Entrypoint []string
					WithExec   struct {
						Stdout string
					}
					WithEntrypoint struct {
						Entrypoint []string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "alpine:3.16.2") {
					entrypoint
					withEntrypoint(args: ["sh", "-c"]) {
						entrypoint
						withExec(args: ["echo $HOME"]) {
							stdout
						}

						withEntrypoint(args: []) {
							entrypoint
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Empty(t, res.Container.From.Entrypoint)
	require.Equal(t, []string{"sh", "-c"}, res.Container.From.WithEntrypoint.Entrypoint)
	require.Equal(t, "/root\n", res.Container.From.WithEntrypoint.WithExec.Stdout)
	require.Empty(t, res.Container.From.WithEntrypoint.WithEntrypoint.Entrypoint)
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
				from(address: "alpine:3.16.2") {
					entrypoint
					defaultArgs
					withDefaultArgs {
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
		require.Equal(t, []string{"/bin/sh"}, res.Container.From.WithEntrypoint.DefaultArgs)
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
				from(address: "alpine:3.16.2") {
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

func TestContainerLabel(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	t.Run("container with new label", func(t *testing.T) {
		label, err := c.Container().From("alpine:3.16.2").WithLabel("FOO", "BAR").Label(ctx, "FOO")

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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
		}`, &dirRes, nil)
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
				from(address: "alpine:3.16.2") {
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
		}})
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
				from(address: "alpine:3.16.2") {
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

	query := `query Test($cache: CacheID!, $rand: String!) {
			container {
				from(address: "alpine:3.16.2") {
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
					ID core.FileID
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

	query := `query Test($cache: CacheID!, $rand: String!, $init: DirectoryID!) {
			container {
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
	defer c.Close()

	dir := c.Directory().
		WithNewFile("some-file", "some-content").
		WithNewFile("some-dir/sub-file", "sub-content").
		Directory("some-dir")

	ctr := c.Container().
		From("alpine:3.16.2").
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
		From("alpine:3.16.2").
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
		From("alpine:3.16.2").
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
	defer c.Close()

	file := c.Directory().
		WithNewFile("some-file", "some-content").
		File("some-file")

	ctr := c.Container().
		From("alpine:3.16.2").
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

func TestContainerWithNewFile(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	ctr := c.Container().
		From("alpine:3.16.2").
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
		`query Test($id: DirectoryID!) {
			container {
				from(address: "alpine:3.16.2") {
					withDirectory(path: "/mnt/dir", directory: "") {
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
			"id": id,
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
	defer c.Close()

	lower := c.Directory().WithNewFile("some-file", "lower-content")

	upper := c.Directory().WithNewFile("some-file", "upper-content")

	ctr := c.Container().
		From("alpine:3.16.2").
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
		`query Test($cache: CacheID!) {
			container {
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
		`query Test($cache: CacheID!) {
			container {
				from(address: "alpine:3.16.2") {
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

	secretID := newSecret(t, "some-secret")
	err = testutil.Query(
		`query Test($secret: SecretID!) {
			container {
				from(address: "alpine:3.16.2") {
					withMountedSecret(path: "/sekret", source: $secret) {
						file(path: "/sekret") {
							contents
						}
					}
				}
			}
		}`, nil, &testutil.QueryOptions{Variables: map[string]any{
			"secret": secretID,
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
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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

	require.Equal(t, "3.16.2\n", execRes.Container.From.WithMountedDirectory.WithExec.Stdout)
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
		`query Test($id: DirectoryID!, $cache: CacheID!) {
			container {
				from(address: "alpine:3.16.2") {
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
				from(address: "alpine:3.16.2") {
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
	defer c.Close()

	testRef := registryRef("container-publish")
	pushedRef, err := c.Container().
		From("alpine:3.16.2").
		Publish(ctx, testRef)
	require.NoError(t, err)
	require.NotEqual(t, testRef, pushedRef)
	require.Contains(t, pushedRef, "@sha256:")

	contents, err := c.Container().
		From(pushedRef).Rootfs().File("/etc/alpine-release").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, "3.16.2\n")
}

func TestExecFromScratch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	// execute it from scratch, where there is no default platform, make sure it works and can be pushed
	execBusybox := c.Container().
		// /bin/busybox is a static binary
		WithMountedFile("/busybox", c.Container().From("busybox:musl").File("/bin/busybox")).
		WithExec([]string{"/busybox"})

	_, err = execBusybox.Stdout(ctx)
	require.NoError(t, err)
	_, err = execBusybox.Publish(ctx, registryRef("from-scratch"))
	require.NoError(t, err)
}

func TestContainerMultipleMounts(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "one"), []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "two"), []byte("2"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "three"), []byte("3"), 0o600))

	one := c.Host().Directory(dir).File("one")
	two := c.Host().Directory(dir).File("two")
	three := c.Host().Directory(dir).File("three")

	build := c.Container().From("alpine:3.16.2").
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

	ctx := context.Background()

	wd := t.TempDir()
	dest := t.TempDir()

	c, err := dagger.Connect(ctx, dagger.WithWorkdir(wd))
	require.NoError(t, err)
	defer c.Close()

	ctr := c.Container().From("alpine:3.16.2")

	t.Run("to absolute dir", func(t *testing.T) {
		imagePath := filepath.Join(dest, "image.tar")

		ok, err := ctr.Export(ctx, imagePath)
		require.NoError(t, err)
		require.True(t, ok)

		entries := tarEntries(t, imagePath)
		require.Contains(t, entries, "oci-layout")
		require.Contains(t, entries, "index.json")

		// a single-platform image can include a manifest.json, making it
		// compatible with docker load
		require.Contains(t, entries, "manifest.json")
	})

	t.Run("to workdir", func(t *testing.T) {
		ok, err := ctr.Export(ctx, "./image.tar")
		require.NoError(t, err)
		require.True(t, ok)

		entries := tarEntries(t, filepath.Join(wd, "image.tar"))
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

func TestContainerMultiPlatformExport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)
	defer c.Close()

	variants := make([]*dagger.Container, 0, len(platformToUname))
	for platform := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From("alpine:3.16.2").
			WithExec([]string{"uname", "-m"})

		variants = append(variants, ctr)
	}

	dest := filepath.Join(t.TempDir(), "image.tar")

	ok, err := c.Container().Export(ctx, dest, dagger.ContainerExportOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)
	require.True(t, ok)

	entries := tarEntries(t, dest)
	require.Contains(t, entries, "oci-layout")
	require.Contains(t, entries, "index.json")

	// multi-platform images don't contain a manifest.json
	require.NotContains(t, entries, "manifest.json")
}

func TestContainerWithDirectoryToMount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)
	defer c.Close()

	mnt := c.Directory().
		WithNewDirectory("/top/sub-dir/sub-file").
		Directory("/top") // <-- the important part!
	ctr := c.Container().
		From("alpine:3.16.2").
		WithMountedDirectory("/mnt", mnt)

	dir := c.Directory().
		WithNewFile("/copied-file", "some-content")

	ctr = ctr.WithDirectory("/mnt/sub-dir/copied-dir", dir)

	contents, err := ctr.WithExec([]string{"find", "/mnt"}).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{
		"/mnt",
		"/mnt/sub-dir",
		"/mnt/sub-dir/sub-file",
		"/mnt/sub-dir/copied-dir",
		"/mnt/sub-dir/copied-dir/copied-file",
	}, strings.Split(strings.Trim(contents, "\n"), "\n"))
}

//go:embed testdata/socket-echo.go
var echoSocketSrc string

func TestContainerWithUnixSocket(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)

	tmp := t.TempDir()
	sock := filepath.Join(tmp, "test.sock")

	l, err := net.Listen("unix", sock)
	require.NoError(t, err)

	defer l.Close()

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					t.Logf("accept: %s", err)
					panic(err)
				}
				return
			}

			n, err := io.Copy(c, c)
			if err != nil {
				t.Logf("hello: %s", err)
				panic(err)
			}

			t.Logf("copied %d bytes", n)

			err = c.Close()
			if err != nil {
				t.Logf("close: %s", err)
				panic(err)
			}
		}
	}()

	echo := c.Directory().WithNewFile("main.go", echoSocketSrc).File("main.go")

	ctr := c.Container().
		From("golang:1.20.0-alpine").
		WithMountedFile("/src/main.go", echo).
		WithUnixSocket("/tmp/test.sock", c.Host().UnixSocket(sock)).
		WithExec([]string{"go", "run", "/src/main.go", "/tmp/test.sock", "hello"})

	stdout, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello\n", stdout)

	t.Run("socket can be removed", func(t *testing.T) {
		without := ctr.WithoutUnixSocket("/tmp/test.sock").
			WithExec([]string{"ls", "/tmp"})

		stdout, err = without.Stdout(ctx)
		require.NoError(t, err)
		require.Empty(t, stdout)
	})

	t.Run("replaces existing socket at same path", func(t *testing.T) {
		repeated := ctr.WithUnixSocket("/tmp/test.sock", c.Host().UnixSocket(sock))

		stdout, err := repeated.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", stdout)

		without := repeated.WithoutUnixSocket("/tmp/test.sock").
			WithExec([]string{"ls", "/tmp"})

		stdout, err = without.Stdout(ctx)
		require.NoError(t, err)
		require.Empty(t, stdout)
	})
}

func TestContainerExecError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))
	require.NoError(t, err)

	outMsg := "THIS SHOULD GO TO STDOUT"
	encodedOutMsg := base64.StdEncoding.EncodeToString([]byte(outMsg))
	errMsg := "THIS SHOULD GO TO STDERR"
	encodedErrMsg := base64.StdEncoding.EncodeToString([]byte(errMsg))

	t.Run("includes output of failed exec in error", func(t *testing.T) {
		_, err = c.Container().
			From("alpine:3.16.2").
			WithExec([]string{"sh", "-c", fmt.Sprintf(
				`echo %s | base64 -d >&1; echo %s | base64 -d >&2; exit 1`, encodedOutMsg, encodedErrMsg,
			)}).
			ExitCode(ctx)
		require.Error(t, err)

		require.Contains(t, err.Error(), outMsg)
		require.Contains(t, err.Error(), errMsg)
	})

	t.Run("includes output of failed exec in error when redirects are enabled", func(t *testing.T) {
		_, err = c.Container().
			From("alpine:3.16.2").
			WithExec(
				[]string{"sh", "-c", fmt.Sprintf(
					`echo %s | base64 -d >&1; echo %s | base64 -d >&2; exit 1`, encodedOutMsg, encodedErrMsg,
				)},
				dagger.ContainerWithExecOpts{
					RedirectStdout: "/out",
					RedirectStderr: "/err",
				},
			).
			ExitCode(ctx)
		require.Error(t, err)

		require.Contains(t, err.Error(), outMsg)
		require.Contains(t, err.Error(), errMsg)
	})
}

func TestContainerWithRegistryAuth(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))

	require.NoError(t, err)
	defer c.Close()

	testRef := privateRegistryRef("container-with-registry-auth")
	container := c.Container().From("alpine:3.16.2")

	// Push without credentials should fail
	_, err = container.Publish(ctx, testRef)
	require.Error(t, err)

	pushedRef, err := container.
		WithRegistryAuth(
			privateRegistryHost,
			"john",
			c.Container().
				WithNewFile("secret.txt", dagger.ContainerWithNewFileOpts{Contents: "xFlejaPdjrt25Dvr"}).
				File("secret.txt").
				Secret(),
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
					from(address: "alpine:3.16.2") {
						imageRef
					}
				}
			}`, &res, nil)
		require.NoError(t, err)
		require.Contains(t, res.Container.From.ImageRef, "docker.io/library/alpine:3.16.2@sha256:")
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
						exec(args:["/hello"]) {
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
		defer c.Close()

		dir := c.Directory().
			WithNewFile("some-file", "some-content").
			WithNewFile("some-dir/sub-file", "sub-content").
			Directory("some-dir")

		ctr := c.Container().
			From("alpine:3.16.2").
			WithWorkdir("/workdir").
			WithDirectory("with-dir", dir)

		_, err := ctr.ImageRef(ctx)

		require.Error(t, err)
		require.Contains(t, err.Error(), "Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
	})
}

func TestContainerBuildNilContextError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	c, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stdout))

	require.NoError(t, err)
	defer c.Close()

	// regression test, this previously caused the engine to panic
	err = testutil.Query(
		`{
			container {
				build(context: "") {
					id
				}
			}
		}`, &map[any]any{}, nil)
	require.ErrorContains(t, err, "invalid nil input definition to definition op")
}

func TestContainerInsecureRootCapabilites(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	out, err := c.Container().From("alpine:3.16.2").
		WithExec([]string{"cat", "/proc/self/status"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "CapInh:\t0000000000000000")
	require.Contains(t, out, "CapPrm:\t00000000a80425fb")
	require.Contains(t, out, "CapEff:\t00000000a80425fb")
	require.Contains(t, out, "CapBnd:\t00000000a80425fb")
	require.Contains(t, out, "CapAmb:\t0000000000000000")

	out, err = c.Container().From("alpine:3.16.2").
		WithExec([]string{"cat", "/proc/self/status"}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "CapInh:\t0000003fffffffff")
	require.Contains(t, out, "CapPrm:\t0000003fffffffff")
	require.Contains(t, out, "CapEff:\t0000003fffffffff")
	require.Contains(t, out, "CapBnd:\t0000003fffffffff")
	require.Contains(t, out, "CapAmb:\t0000003fffffffff")
}

func TestContainerInsecureRootCapabilitesWithService(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// verify the root capabilities setting works by executing dockerd with it and
	// testing it can startup, create containers and bind mount from its filesystem to
	// them.
	dockerd := c.Container().From("docker:23.0.1-dind").
		WithMountedCache("/var/lib/docker", c.CacheVolume("docker-lib"), dagger.ContainerWithMountedCacheOpts{
			Sharing: dagger.Private,
		}).
		WithMountedCache("/tmp", c.CacheVolume("share-tmp")).
		WithExposedPort(2375).
		WithExec([]string{"dockerd",
			"--host=tcp://0.0.0.0:2375",
			"--tls=false",
		}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		})

	dockerHost, err := dockerd.Endpoint(ctx, dagger.ContainerEndpointOpts{
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
