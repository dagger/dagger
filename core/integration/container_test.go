package core

import (
	"context"
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
		WithNewFile("main.go", dagger.DirectoryWithNewFileOpts{
			Contents: `package main
import "fmt"
import "os"
func main() {
	for _, env := range os.Environ() {
		fmt.Println(env)
	}
}`,
		})

	t.Run("default Dockerfile location", func(t *testing.T) {
		src := contextDir.
			WithNewFile("Dockerfile", dagger.DirectoryWithNewFileOpts{
				Contents: `FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`,
			})

		env, err := c.Container().Build(src).Exec().Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("custom Dockerfile location", func(t *testing.T) {
		src := contextDir.
			WithNewFile("subdir/Dockerfile.whee", dagger.DirectoryWithNewFileOpts{
				Contents: `FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`,
			})

		env, err := c.Container().Build(src, dagger.ContainerBuildOpts{
			Dockerfile: "subdir/Dockerfile.whee",
		}).Exec().Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with default Dockerfile location", func(t *testing.T) {
		src := contextDir.
			WithNewFile("Dockerfile", dagger.DirectoryWithNewFileOpts{
				Contents: `FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`,
			})

		sub := c.Directory().WithDirectory("subcontext", src).Directory("subcontext")

		env, err := c.Container().Build(sub).Exec().Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})

	t.Run("subdirectory with custom Dockerfile location", func(t *testing.T) {
		src := contextDir.
			WithNewFile("subdir/Dockerfile.whee", dagger.DirectoryWithNewFileOpts{
				Contents: `FROM golang:1.18.2-alpine
WORKDIR /src
COPY main.go .
RUN go mod init hello
RUN go build -o /usr/bin/goenv main.go
ENV FOO=bar
CMD goenv
`,
			})

		sub := c.Directory().WithDirectory("subcontext", src).Directory("subcontext")

		env, err := c.Container().Build(sub, dagger.ContainerBuildOpts{
			Dockerfile: "subdir/Dockerfile.whee",
		}).Exec().Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, env, "FOO=bar\n")
	})
}

func TestContainerWithFS(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	alpine316 := c.Container().From("alpine:3.16.2")

	alpine316ReleaseStr, err := alpine316.File("/etc/alpine-release").Contents(ctx)
	require.NoError(t, err)

	alpine316ReleaseStr = strings.TrimSpace(alpine316ReleaseStr)
	dir := alpine316.FS()
	exitCode, err := c.Container().WithEnvVariable("ALPINE_RELEASE", alpine316ReleaseStr).WithFS(dir).Exec(dagger.ContainerExecOpts{
		Args: []string{
			"/bin/sh",
			"-c",
			"test -f /etc/alpine-release && test \"$(head -n 1 /etc/alpine-release)\" = \"$ALPINE_RELEASE\"",
		},
	}).ExitCode(ctx)

	require.NoError(t, err)
	require.Equal(t, exitCode, 0)

	alpine315 := c.Container().From("alpine:3.15.6")

	varVal := "testing123"

	alpine315WithVar := alpine315.WithEnvVariable("DAGGER_TEST", varVal)
	varValResp, err := alpine315WithVar.EnvVariable(ctx, "DAGGER_TEST")
	require.NoError(t, err)

	require.Equal(t, varVal, varValResp)

	alpine315ReplacedFS := alpine315WithVar.WithFS(dir)

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
				Exec struct {
					ExitCode *int
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "alpine:3.16.2") {
					exec(args: ["true"]) {
						exitCode
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.NotNil(t, res.Container.From.Exec.ExitCode)
	require.Equal(t, 0, *res.Container.From.Exec.ExitCode)

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
						exec(args: ["false"]) {
							exitCode
						}
					}
				}
			}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, res.Container.From.Exec.ExitCode, 1)
	*/
}

func TestContainerExecStdoutStderr(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Exec struct {
					Stdout, Stderr struct {
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
					exec(args: ["sh", "-c", "echo hello; echo goodbye >/dev/stderr"]) {
						stdout {
							contents
						}

						stderr {
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.Exec.Stdout.Contents, "hello\n")
	require.Equal(t, res.Container.From.Exec.Stderr.Contents, "goodbye\n")
}

func TestContainerExecStdin(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Exec struct {
					Stdout struct {
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
					exec(args: ["cat"], stdin: "hello") {
						stdout {
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.Exec.Stdout.Contents, "hello")
}

func TestContainerExecRedirectStdoutStderr(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Exec struct {
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
					exec(
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
	require.Equal(t, res.Container.From.Exec.Out.Contents, "hello\n")
	require.Equal(t, res.Container.From.Exec.Err.Contents, "goodbye\n")

	err = testutil.Query(
		`{
			container {
				from(address: "alpine:3.16.2") {
					exec(
						args: ["sh", "-c", "echo hello; echo goodbye >/dev/stderr"],
						redirectStdout: "out",
						redirectStderr: "err"
					) {
						stdout {
							contents
						}

						stderr {
							contents
						}
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
				Stdout, Stderr *struct {
					Contents string
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "alpine:3.16.2") {
					stdout {
						contents
					}

					stderr {
						contents
					}
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
					Exec struct {
						Stdout struct {
							Contents string
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
					withWorkdir(path: "/usr") {
						exec(args: ["pwd"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithWorkdir.Exec.Stdout.Contents, "/usr\n")
}

func TestContainerExecWithUser(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				User string

				WithUser struct {
					User string
					Exec struct {
						Stdout struct {
							Contents string
						}
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
						exec(args: ["whoami"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "daemon", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\n", res.Container.From.WithUser.Exec.Stdout.Contents)
	})

	t.Run("user and group name", func(t *testing.T) {
		err := testutil.Query(
			`{
			container {
				from(address: "alpine:3.16.2") {
					user
					withUser(name: "daemon:floppy") {
						user
						exec(args: ["sh", "-c", "whoami; groups"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "daemon:floppy", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\nfloppy\n", res.Container.From.WithUser.Exec.Stdout.Contents)
	})

	t.Run("user ID", func(t *testing.T) {
		err := testutil.Query(
			`{
			container {
				from(address: "alpine:3.16.2") {
					user
					withUser(name: "2") {
						user
						exec(args: ["whoami"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "2", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\n", res.Container.From.WithUser.Exec.Stdout.Contents)
	})

	t.Run("user and group ID", func(t *testing.T) {
		err := testutil.Query(
			`{
			container {
				from(address: "alpine:3.16.2") {
					user
					withUser(name: "2:11") {
						user
						exec(args: ["sh", "-c", "whoami; groups"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "2:11", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\nfloppy\n", res.Container.From.WithUser.Exec.Stdout.Contents)
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
					Exec       struct {
						Stdout struct {
							Contents string
						}
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
						exec(args: ["echo $HOME"]) {
							stdout {
								contents
							}
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
	require.Equal(t, "/root\n", res.Container.From.WithEntrypoint.Exec.Stdout.Contents)
	require.Empty(t, res.Container.From.WithEntrypoint.WithEntrypoint.Entrypoint)
}

func TestContainerWithDefaultArgs(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Entrypoint  []string
				DefaultArgs []string
				Exec        struct {
					Stdout struct {
						Contents string
					}
				}
				WithDefaultArgs struct {
					Entrypoint  []string
					DefaultArgs []string
				}
				WithEntrypoint struct {
					Entrypoint  []string
					DefaultArgs []string
					Exec        struct {
						Stdout struct {
							Contents string
						}
					}
					WithDefaultArgs struct {
						Entrypoint  []string
						DefaultArgs []string
						Exec        struct {
							Stdout struct {
								Contents string
							}
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

						exec(args: ["echo $HOME"]) {
							stdout {
								contents
							}
						}

						withDefaultArgs(args: ["id"]) {
							entrypoint
							defaultArgs

							exec(args: []) {
								stdout {
									contents
								}
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
		require.Equal(t, "/root\n", res.Container.From.WithEntrypoint.Exec.Stdout.Contents)
	})

	t.Run("with default args set", func(t *testing.T) {
		require.Equal(t, []string{"sh", "-c"}, res.Container.From.WithEntrypoint.WithDefaultArgs.Entrypoint)
		require.Equal(t, []string{"id"}, res.Container.From.WithEntrypoint.WithDefaultArgs.DefaultArgs)

		require.Equal(t, "uid=0(root) gid=0(root) groups=0(root),1(bin),2(daemon),3(sys),4(adm),6(disk),10(wheel),11(floppy),20(dialout),26(tape),27(video)\n", res.Container.From.WithEntrypoint.WithDefaultArgs.Exec.Stdout.Contents)
	})
}

func TestContainerExecWithEnvVariable(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithEnvVariable struct {
					Exec struct {
						Stdout struct {
							Contents string
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
					withEnvVariable(name: "FOO", value: "bar") {
						exec(args: ["env"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Contains(t, res.Container.From.WithEnvVariable.Exec.Stdout.Contents, "FOO=bar\n")
}

func TestContainerVariables(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				EnvVariables []schema.EnvVariable
				Exec         struct {
					Stdout struct {
						Contents string
					}
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
					exec(args: ["env"]) {
						stdout {
							contents
						}
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
	require.Contains(t, res.Container.From.Exec.Stdout.Contents, "GOPATH=/go\n")
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
					Exec         struct {
						Stdout struct {
							Contents string
						}
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
						exec(args: ["env"]) {
							stdout {
								contents
							}
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
	require.NotContains(t, res.Container.From.WithoutEnvVariable.Exec.Stdout.Contents, "GOLANG_VERSION")
}

func TestContainerEnvVariablesReplace(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithEnvVariable struct {
					EnvVariables []schema.EnvVariable
					Exec         struct {
						Stdout struct {
							Contents string
						}
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
						exec(args: ["env"]) {
							stdout {
								contents
							}
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
	require.Contains(t, res.Container.From.WithEnvVariable.Exec.Stdout.Contents, "GOPATH=/gone\n")
}

func TestContainerWorkdir(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Workdir string
				Exec    struct {
					Stdout struct {
						Contents string
					}
				}
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					workdir
					exec(args: ["pwd"]) {
						stdout {
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.Workdir, "/go")
	require.Equal(t, res.Container.From.Exec.Stdout.Contents, "/go\n")
}

func TestContainerWithWorkdir(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithWorkdir struct {
					Workdir string
					Exec    struct {
						Stdout struct {
							Contents string
						}
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
						exec(args: ["pwd"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithWorkdir.Workdir, "/usr")
	require.Equal(t, res.Container.From.WithWorkdir.Exec.Stdout.Contents, "/usr\n")
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
					Exec struct {
						Stdout struct {
							Contents string
						}

						Exec struct {
							Stdout struct {
								Contents string
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
						exec(args: ["cat", "/mnt/some-file"]) {
							stdout {
								contents
							}

							exec(args: ["cat", "/mnt/some-dir/sub-file"]) {
								stdout {
									contents
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
	require.Equal(t, "some-content", execRes.Container.From.WithMountedDirectory.Exec.Stdout.Contents)
	require.Equal(t, "sub-content", execRes.Container.From.WithMountedDirectory.Exec.Exec.Stdout.Contents)
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
					Exec struct {
						Exec struct {
							Stdout struct {
								Contents string
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
						exec(args: ["sh", "-c", "echo >> /mnt/sub-file; echo -n more-content >> /mnt/sub-file"]) {
							exec(args: ["cat", "/mnt/sub-file"]) {
								stdout {
									contents
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
	require.Equal(t, "sub-content\nmore-content", execRes.Container.From.WithMountedDirectory.Exec.Exec.Stdout.Contents)
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
					Exec struct {
						Stdout struct {
							Contents string
						}
						Exec struct {
							Exec struct {
								Stdout struct {
									Contents string
								}
								WithMountedDirectory struct {
									Exec struct {
										Stdout struct {
											Contents string
										}
										Exec struct {
											Stdout struct {
												Contents string
											}
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
						exec(args: ["cat", "/mnt/some-file"]) {
							# original content
							stdout { contents }

							exec(args: ["sh", "-c", "echo >> /mnt/some-file; echo -n more-content >> /mnt/some-file"]) {
								exec(args: ["cat", "/mnt/some-file"]) {
									# modified content should propagate
									stdout { contents }

									withMountedDirectory(path: "/mnt", source: $id) {
										exec(args: ["cat", "/mnt/some-file"]) {
											# should be back to the original content
											stdout { contents }

											exec(args: ["cat", "/mnt/some-file"]) {
												# original content override should propagate
												stdout { contents }
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
		execRes.Container.From.WithMountedDirectory.Exec.Stdout.Contents)

	require.Equal(t,
		"some-content\nmore-content",
		execRes.Container.From.WithMountedDirectory.Exec.Exec.Exec.Stdout.Contents)

	require.Equal(t,
		"some-content",
		execRes.Container.From.WithMountedDirectory.Exec.Exec.Exec.WithMountedDirectory.Exec.Stdout.Contents)

	require.Equal(t,
		"some-content",
		execRes.Container.From.WithMountedDirectory.Exec.Exec.Exec.WithMountedDirectory.Exec.Exec.Stdout.Contents)
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
					Exec struct {
						Stdout struct {
							Contents string
						}
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
						exec(args: ["cat", "/mnt/file"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	require.Equal(t, "sub-content", execRes.Container.From.WithMountedFile.Exec.Stdout.Contents)
}

func TestContainerWithMountedCache(t *testing.T) {
	t.Parallel()

	cacheID := newCache(t)

	execRes := struct {
		Container struct {
			From struct {
				WithEnvVariable struct {
					WithMountedCache struct {
						Exec struct {
							Stdout struct {
								Contents string
							}
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
							exec(args: ["sh", "-c", "echo $RAND >> /mnt/cache/file; cat /mnt/cache/file"]) {
								stdout {
									contents
								}
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
	require.Equal(t, rand1+"\n", execRes.Container.From.WithEnvVariable.WithMountedCache.Exec.Stdout.Contents)

	rand2 := identity.NewID()
	err = testutil.Query(query, &execRes, &testutil.QueryOptions{Variables: map[string]any{
		"cache": cacheID,
		"rand":  rand2,
	}})
	require.NoError(t, err)
	require.Equal(t, rand1+"\n"+rand2+"\n", execRes.Container.From.WithEnvVariable.WithMountedCache.Exec.Stdout.Contents)
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
						Exec struct {
							Stdout struct {
								Contents string
							}
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
							exec(args: ["sh", "-c", "echo $RAND >> /mnt/cache/sub-file; cat /mnt/cache/sub-file"]) {
								stdout {
									contents
								}
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
	require.Equal(t, "initial-content\n"+rand1+"\n", execRes.Container.From.WithEnvVariable.WithMountedCache.Exec.Stdout.Contents)

	rand2 := identity.NewID()
	err = testutil.Query(query, &execRes, &testutil.QueryOptions{Variables: map[string]any{
		"init":  initialID,
		"rand":  rand2,
		"cache": cacheID,
	}})
	require.NoError(t, err)
	require.Equal(t, "initial-content\n"+rand1+"\n"+rand2+"\n", execRes.Container.From.WithEnvVariable.WithMountedCache.Exec.Stdout.Contents)
}

func TestContainerWithMountedTemp(t *testing.T) {
	t.Parallel()

	execRes := struct {
		Container struct {
			From struct {
				WithMountedTemp struct {
					Exec struct {
						Stdout struct {
							Contents string
						}
					}
				}
			}
		}
	}{}

	err := testutil.Query(`{
			container {
				from(address: "alpine:3.16.2") {
					withMountedTemp(path: "/mnt/tmp") {
						exec(args: ["grep", "/mnt/tmp", "/proc/mounts"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &execRes, nil)
	require.NoError(t, err)
	require.Contains(t, execRes.Container.From.WithMountedTemp.Exec.Stdout.Contents, "tmpfs /mnt/tmp tmpfs")
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
				WithMountedTemp struct {
					Mounts               []string
					WithMountedDirectory struct {
						Mounts []string
						Exec   struct {
							Stdout struct {
								Contents string
							}
							WithoutMount struct {
								Mounts []string
								Exec   struct {
									Stdout struct {
										Contents string
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
					withMountedTemp(path: "/mnt/tmp") {
						mounts
						withMountedDirectory(path: "/mnt/dir", source: $id) {
							mounts
							exec(args: ["ls", "/mnt/dir"]) {
								stdout {
									contents
								}
								withoutMount(path: "/mnt/dir") {
									mounts
									exec(args: ["ls", "/mnt/dir"]) {
										stdout {
											contents
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
	require.Equal(t, []string{"/mnt/tmp"}, execRes.Container.From.WithMountedTemp.Mounts)
	require.Equal(t, []string{"/mnt/tmp", "/mnt/dir"}, execRes.Container.From.WithMountedTemp.WithMountedDirectory.Mounts)
	require.Equal(t, "some-dir\nsome-file\n", execRes.Container.From.WithMountedTemp.WithMountedDirectory.Exec.Stdout.Contents)
	require.Equal(t, "", execRes.Container.From.WithMountedTemp.WithMountedDirectory.Exec.WithoutMount.Exec.Stdout.Contents)
	require.Equal(t, []string{"/mnt/tmp"}, execRes.Container.From.WithMountedTemp.WithMountedDirectory.Exec.WithoutMount.Mounts)
}

func TestContainerReplacedMounts(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)
	defer c.Close()

	lower := c.Directory().WithNewFile("some-file", dagger.DirectoryWithNewFileOpts{
		Contents: "lower-content",
	})

	upper := c.Directory().WithNewFile("some-file", dagger.DirectoryWithNewFileOpts{
		Contents: "upper-content",
	})

	ctr := c.Container().
		From("alpine:3.16.2").
		WithMountedDirectory("/mnt/dir", lower)

	t.Run("initial content is lower", func(t *testing.T) {
		mnts, err := ctr.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt/dir"}, mnts)

		out, err := ctr.Exec(dagger.ContainerExecOpts{
			Args: []string{"cat", "/mnt/dir/some-file"},
		}).Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "lower-content", out)
	})

	replaced := ctr.WithMountedDirectory("/mnt/dir", upper)

	t.Run("mounts of same path are replaced", func(t *testing.T) {
		mnts, err := replaced.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt/dir"}, mnts)

		out, err := replaced.Exec(dagger.ContainerExecOpts{
			Args: []string{"cat", "/mnt/dir/some-file"},
		}).Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "upper-content", out)
	})

	t.Run("removing a replaced mount does not reveal previous mount", func(t *testing.T) {
		removed := replaced.WithoutMount("/mnt/dir")
		mnts, err := removed.Mounts(ctx)
		require.NoError(t, err)
		require.Empty(t, mnts)
	})

	clobberedDir := c.Directory().WithNewFile("some-file", dagger.DirectoryWithNewFileOpts{
		Contents: "clobbered-content",
	})
	clobbered := replaced.WithMountedDirectory("/mnt", clobberedDir)

	t.Run("replacing parent of a mount clobbers child", func(t *testing.T) {
		mnts, err := clobbered.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt"}, mnts)

		out, err := clobbered.Exec(dagger.ContainerExecOpts{
			Args: []string{"cat", "/mnt/some-file"},
		}).Stdout().Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "clobbered-content", out)
	})

	clobberedSubDir := c.Directory().WithNewFile("some-file", dagger.DirectoryWithNewFileOpts{
		Contents: "clobbered-sub-content",
	})
	clobberedSub := clobbered.WithMountedDirectory("/mnt/dir", clobberedSubDir)

	t.Run("restoring mount under clobbered mount", func(t *testing.T) {
		mnts, err := clobberedSub.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt", "/mnt/dir"}, mnts)

		out, err := clobberedSub.Exec(dagger.ContainerExecOpts{
			Args: []string{"cat", "/mnt/dir/some-file"},
		}).Stdout().Contents(ctx)
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
						Exec struct {
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
							exec(args: ["sh", "-c", "echo hello >> /mnt/dir/overlap/another-file"]) {
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

	writtenID := writeRes.Container.From.WithMountedDirectory.WithMountedDirectory.Exec.Directory.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					Exec struct {
						Stdout struct {
							Contents string
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
						exec(args: ["cat", "/mnt/dir/another-file"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "hello\n", execRes.Container.From.WithMountedDirectory.Exec.Stdout.Contents)
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
					Exec struct {
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
						exec(args: ["sh", "-c", "echo more-content >> /mnt/dir/sub-dir/sub-file"]) {
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

	writtenID := writeRes.Container.From.WithMountedDirectory.Exec.Directory.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					Exec struct {
						Stdout struct {
							Contents string
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
						exec(args: ["cat", "/mnt/dir/sub-file"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "sub-content\nmore-content\n", execRes.Container.From.WithMountedDirectory.Exec.Stdout.Contents)
}

func TestContainerFile(t *testing.T) {
	t.Parallel()

	id := newDirWithFile(t, "some-file", "some-content-")

	writeRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithMountedDirectory struct {
						Exec struct {
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
							exec(args: ["sh", "-c", "echo -n appended >> /mnt/dir/overlap/some-file"]) {
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

	writtenID := writeRes.Container.From.WithMountedDirectory.WithMountedDirectory.Exec.File.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedFile struct {
					Exec struct {
						Stdout struct {
							Contents string
						}
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
						exec(args: ["cat", "/mnt/file"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "some-content-appended", execRes.Container.From.WithMountedFile.Exec.Stdout.Contents)
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
					Exec struct {
						Stdout struct {
							Contents string
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
					withMountedDirectory(path: "/mnt/etc", source: $id) {
						exec(args: ["cat", "/mnt/etc/alpine-release"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": etcID,
		}})
	require.NoError(t, err)

	require.Equal(t, "3.16.2\n", execRes.Container.From.WithMountedDirectory.Exec.Stdout.Contents)
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
				Exec struct {
					WithWorkdir struct {
						WithWorkdir struct {
							Workdir              string
							WithMountedDirectory struct {
								WithMountedTemp struct {
									WithMountedCache struct {
										Mounts []string
										Exec   struct {
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
					exec(args: ["mkdir", "-p", "/mnt/sub"]) {
						withWorkdir(path: "/mnt") {
							withWorkdir(path: "sub") {
								workdir
								withMountedDirectory(path: "dir", source: $id) {
									withMountedTemp(path: "tmp") {
										withMountedCache(path: "cache", cache: $cache) {
											mounts
											exec(args: ["touch", "dir/another-file"]) {
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
		writeRes.Container.From.Exec.WithWorkdir.WithWorkdir.WithMountedDirectory.WithMountedTemp.WithMountedCache.Mounts)

	require.Equal(t,
		[]string{"/mnt/sub/dir", "/mnt/sub/tmp"},
		writeRes.Container.From.Exec.WithWorkdir.WithWorkdir.WithMountedDirectory.WithMountedTemp.WithMountedCache.WithoutMount.Mounts)

	writtenID := writeRes.Container.From.Exec.WithWorkdir.WithWorkdir.WithMountedDirectory.WithMountedTemp.WithMountedCache.Exec.Directory.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					Exec struct {
						Stdout struct {
							Contents string
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
						exec(args: ["ls", "/mnt/dir"]) {
							stdout {
								contents
							}
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "another-file\nsome-file\n", execRes.Container.From.WithMountedDirectory.Exec.Stdout.Contents)
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
					Exec struct {
						From struct {
							Exec struct {
								Exec struct {
									Stdout struct {
										Contents string
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
				from(address: "node:18.10.0-alpine") {
					withMountedDirectory(path: "/mnt", source: $id) {
						exec(args: ["sh", "-c", "node --version >> /mnt/versions"]) {
							from(address: "golang:1.18.2-alpine") {
								exec(args: ["sh", "-c", "go version >> /mnt/versions"]) {
									exec(args: ["cat", "/mnt/versions"]) {
										stdout {
											contents
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
	require.Contains(t, execRes.Container.From.WithMountedDirectory.Exec.From.Exec.Exec.Stdout.Contents, "v18.10.0\n")
	require.Contains(t, execRes.Container.From.WithMountedDirectory.Exec.From.Exec.Exec.Stdout.Contents, "go version go1.18.2")
}

func TestContainerPublish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	startRegistry(ctx, c, t)

	testRef := "127.0.0.1:5000/testimagepush:latest"
	pushedRef, err := c.Container().
		From("alpine:3.16.2").
		Publish(ctx, testRef)
	require.NoError(t, err)
	require.NotEqual(t, testRef, pushedRef)
	require.Contains(t, pushedRef, "@sha256:")

	contents, err := c.Container().
		From(pushedRef).FS().File("/etc/alpine-release").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, contents, "3.16.2\n")
}

func TestExecFromScratch(t *testing.T) {
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	startRegistry(ctx, c, t)

	// execute it from scratch, where there is no default platform, make sure it works and can be pushed
	execBusybox := c.Container().
		// /bin/busybox is a static binary
		WithMountedFile("/busybox", c.Container().From("busybox:musl").File("/bin/busybox")).
		Exec(dagger.ContainerExecOpts{Args: []string{"/busybox"}})

	_, err = execBusybox.Stdout().Contents(ctx)
	require.NoError(t, err)
	_, err = execBusybox.Publish(ctx, "127.0.0.1:5000/testexecfromscratch:latest")
	require.NoError(t, err)
}

func TestContainerMultipleMounts(t *testing.T) {
	c, ctx := connect(t)
	defer c.Close()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "one"), []byte("1"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "two"), []byte("2"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "three"), []byte("3"), 0600))

	one := c.Host().Directory(dir).File("one")
	two := c.Host().Directory(dir).File("two")
	three := c.Host().Directory(dir).File("three")

	build := c.Container().From("alpine:3.16.2").
		WithMountedFile("/example/one", one).
		WithMountedFile("/example/two", two).
		WithMountedFile("/example/three", three)

	build = build.Exec(dagger.ContainerExecOpts{
		Args: []string{"ls", "/example/one", "/example/two", "/example/three"},
	})

	build = build.Exec(dagger.ContainerExecOpts{
		Args: []string{"cat", "/example/one", "/example/two", "/example/three"},
	})

	out, err := build.Stdout().Contents(ctx)
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

	startRegistry(ctx, c, t)

	variants := make([]*dagger.Container, 0, len(platformToUname))
	for platform := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From("alpine:3.16.2").
			Exec(dagger.ContainerExecOpts{
				Args: []string{"uname", "-m"},
			})

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
