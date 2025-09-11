package core

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
)

type ContainerSuite struct{}

func TestContainer(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ContainerSuite{})
}

func (ContainerSuite) TestScratch(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			ID     string
			Rootfs struct {
				Entries []string
			}
		}
	}](t,
		`{
			container {
				id
				rootfs {
					entries
				}
			}
		}`, nil)
	require.NoError(t, err)
	require.Empty(t, res.Container.Rootfs.Entries)
}

func (ContainerSuite) TestFrom(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				File struct {
					Contents string
				}
			}
		}
	}](t,
		`{
			container {
				from(address: "`+alpineImage+`") {
                    file(path: "/etc/alpine-release") {
                        contents
                    }
				}
			}
		}`, nil)
	require.NoError(t, err)

	releaseStr := res.Container.From.File.Contents
	require.Equal(t, distconsts.AlpineVersion, strings.TrimSpace(releaseStr))
}

func (ContainerSuite) TestWithRootFS(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

	require.Equal(t, distconsts.AlpineVersion, strings.TrimSpace(releaseStr))
}

//go:embed testdata/hello.go
var helloSrc string

func (ContainerSuite) TestWithRootFSSubdir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExecSync(ctx context.Context, t *testctx.T) {
	// A successful sync doesn't prove anything. As soon as you call other
	// leaves to check things, they could be the ones triggering execution.
	// Still, sync can be useful for short-circuiting.
	_, err := testutil.Query[struct {
		Container struct {
			From struct {
				WithExec struct {
					Sync string
				}
			}
		}
	}](t,
		`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["false"]) {
						sync
					}
				}
			}
		}`, nil)
	requireErrOut(t, err, `process "false" did not complete successfully`)
}

func (ContainerSuite) TestError(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		name        string
		query       string
		expectedErr *string
	}{
		{
			"with error message",
			`
			{
				container {
					from(address: "` + alpineImage + `") {
						withError(err: "error raised")
					}
				}
			}`,
			ptr("error raised"),
		},
		{
			"with empty error message",
			`
			{
				container {
					from(address: "` + alpineImage + `") {
						withError(err: "")
					}
				}
			}`,
			nil,
		},
	} {
		t.Run(tc.name, func(ctx context.Context, t *testctx.T) {
			_, err := testutil.Query[struct {
				Container struct {
					From struct {
						WithError struct{}
					}
				}
			}](t, tc.query, nil)
			if tc.expectedErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, *tc.expectedErr)
			}
		})
	}
}

func (ContainerSuite) TestExecStdoutStderr(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("stdout", func(ctx context.Context, t *testctx.T) {
		out, err := c.Container().
			From(alpineImage).
			WithExec([]string{"echo", "hello"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("stderr", func(ctx context.Context, t *testctx.T) {
		out, err := c.Container().
			From(alpineImage).
			WithExec([]string{"sh", "-c", "echo goodbye > /dev/stderr"}).
			Stderr(ctx)
		require.NoError(t, err)
		require.Equal(t, "goodbye\n", out)
	})

	t.Run("stdout without exec", func(ctx context.Context, t *testctx.T) {
		_, err := c.Container().
			From(alpineImage).
			Stdout(ctx)
		requireErrOut(t, err, "no command has been set")
	})

	t.Run("stderr without exec", func(ctx context.Context, t *testctx.T) {
		_, err := c.Container().
			From(alpineImage).
			Stderr(ctx)
		requireErrOut(t, err, "no command has been set")
	})
}

func (ContainerSuite) TestExecCombinedOutput(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().
		From(alpineImage).
		WithNewFile("/test.sh", `echo "out"
echo "err" >&2
`).
		WithExec([]string{"sh", "/test.sh"})
	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, out, "out\n")

	out, err = ctr.Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "err\n")

	out, err = ctr.CombinedOutput(ctx)
	require.NoError(t, err)
	// order is not guarantee, but we can ensure both expected lines are present
	require.Contains(t, out, "out\n")
	require.Contains(t, out, "err\n")
}

func (ContainerSuite) TestExecStdin(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				WithExec struct {
					Stdout string
				}
			}
		}
	}](t,
		`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["cat"], stdin: "hello") {
						stdout
					}
				}
			}
		}`, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithExec.Stdout, "hello")
}

func (ContainerSuite) TestExecRedirectStdin(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dir := c.Directory().WithNewFile("input.txt", "redirected stdin")
	execWithMount := c.Container().From(alpineImage).
		WithMountedDirectory("/mnt", dir).
		WithExec([]string{"cat"}, dagger.ContainerWithExecOpts{
			RedirectStdin:  "/mnt/input.txt",
			RedirectStdout: "/mnt/out",
		})

	stdout, err := execWithMount.File("/mnt/out").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "redirected stdin", stdout)
}

func (ContainerSuite) TestExecRedirectStdinSecret(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	secret := c.SetSecret("my-secret", "secret stdin")
	execWithSecret := c.Container().From(alpineImage).
		WithMountedSecret("/mnt/secret", secret).
		WithExec([]string{"sh", "-c", "cat | tr '[a-z]' '[A-Z]'"}, dagger.ContainerWithExecOpts{
			RedirectStdin:  "/mnt/secret",
			RedirectStdout: "/mnt/out",
		})

	stdout, err := execWithSecret.File("/mnt/out").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "SECRET STDIN", stdout)
}

func (ContainerSuite) TestExecRedirectStdoutStderr(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("exec", func(ctx context.Context, t *testctx.T) {
		exec := c.Container().From(alpineImage).
			WithExec([]string{"sh", "-c", "echo hello; echo goodbye >/dev/stderr"}, dagger.ContainerWithExecOpts{
				RedirectStdout: "out",
				RedirectStderr: "err",
			})

		stdout, err := exec.File("out").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", stdout)
		stderr, err := exec.File("err").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "goodbye\n", stderr)

		_, err = exec.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", stdout)

		_, err = exec.Stderr(ctx)
		require.NoError(t, err)
		require.Equal(t, "goodbye\n", stderr)
	})

	t.Run("exec with mount", func(ctx context.Context, t *testctx.T) {
		// same as above, but with a mounted directory instead
		exec := c.Container().From(alpineImage).
			WithMountedDirectory("/mnt", c.Directory()).
			WithExec([]string{"sh", "-c", "echo hello; echo goodbye >/dev/stderr"}, dagger.ContainerWithExecOpts{
				RedirectStdout: "/mnt/out",
				RedirectStderr: "/mnt/err",
			})

		stdout, err := exec.File("/mnt/out").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", stdout)
		stderr, err := exec.File("/mnt/err").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "goodbye\n", stderr)

		_, err = exec.Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", stdout)

		_, err = exec.Stderr(ctx)
		require.NoError(t, err)
		require.Equal(t, "goodbye\n", stderr)
	})
}

func (ContainerSuite) TestExecWithWorkdir(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				WithWorkdir struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](t,
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
		}`, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithWorkdir.WithExec.Stdout, "/usr\n")
}

func (ContainerSuite) TestExecWithoutWorkdir(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	res, err := c.Container().
		From(alpineImage).
		WithWorkdir("/usr").
		WithoutWorkdir().
		WithExec([]string{"pwd"}).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "/\n", res)
}

func (ContainerSuite) TestExecWithUser(ctx context.Context, t *testctx.T) {
	type resType struct {
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
	}

	t.Run("user name", func(ctx context.Context, t *testctx.T) {
		res, err := testutil.Query[resType](t,
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
		}`, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "daemon", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\n", res.Container.From.WithUser.WithExec.Stdout)
	})

	t.Run("user and group name", func(ctx context.Context, t *testctx.T) {
		res, err := testutil.Query[resType](t,
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
		}`, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "daemon:floppy", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\nfloppy\n", res.Container.From.WithUser.WithExec.Stdout)
	})

	t.Run("user ID", func(ctx context.Context, t *testctx.T) {
		res, err := testutil.Query[resType](t,
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
		}`, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "2", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\n", res.Container.From.WithUser.WithExec.Stdout)
	})

	t.Run("user and group ID", func(ctx context.Context, t *testctx.T) {
		res, err := testutil.Query[resType](t,
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
		}`, nil)
		require.NoError(t, err)
		require.Equal(t, "", res.Container.From.User)
		require.Equal(t, "2:11", res.Container.From.WithUser.User)
		require.Equal(t, "daemon\nfloppy\n", res.Container.From.WithUser.WithExec.Stdout)
	})

	t.Run("stdin", func(ctx context.Context, t *testctx.T) {
		res, err := testutil.Query[resType](t,
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
		}`, nil)
		require.NoError(t, err)
		require.Equal(t, "daemon\n", res.Container.From.WithUser.WithExec.Stdout)
	})
}

func (ContainerSuite) TestExecWithoutUser(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	res, err := c.Container().
		From(alpineImage).
		WithUser("daemon").
		WithoutUser().
		WithExec([]string{"whoami"}).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "root\n", res)
}

func (ContainerSuite) TestExecWithEntrypoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	base := c.Container().From(alpineImage)
	withEntry := base.WithEntrypoint([]string{"sh"})

	t.Run("before", func(ctx context.Context, t *testctx.T) {
		before, err := base.Entrypoint(ctx)
		require.NoError(t, err)
		require.Empty(t, before)
	})

	t.Run("after", func(ctx context.Context, t *testctx.T) {
		after, err := withEntry.Entrypoint(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"sh"}, after)
	})

	t.Run("used", func(ctx context.Context, t *testctx.T) {
		used, err := withEntry.WithExec([]string{"-c", "echo $HOME"}, dagger.ContainerWithExecOpts{
			UseEntrypoint: true,
		}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/root\n", used)
	})

	t.Run("prepended to exec", func(ctx context.Context, t *testctx.T) {
		_, err := withEntry.WithExec([]string{"sh", "-c", "echo $HOME"}, dagger.ContainerWithExecOpts{
			UseEntrypoint: true,
		}).Sync(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "can't open 'sh'")
	})

	t.Run("skipped", func(ctx context.Context, t *testctx.T) {
		skipped, err := withEntry.WithExec([]string{"sh", "-c", "echo $HOME"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/root\n", skipped)
	})

	t.Run("unset default args", func(ctx context.Context, t *testctx.T) {
		removed, err := base.
			WithDefaultArgs([]string{"foobar"}).
			WithEntrypoint([]string{"echo"}).
			WithExec(nil, dagger.ContainerWithExecOpts{
				UseEntrypoint: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "\n", removed)
	})

	t.Run("kept default args", func(ctx context.Context, t *testctx.T) {
		kept, err := base.
			WithDefaultArgs([]string{"foobar"}).
			WithEntrypoint([]string{"echo"}, dagger.ContainerWithEntrypointOpts{
				KeepDefaultArgs: true,
			}).
			WithExec(nil, dagger.ContainerWithExecOpts{
				UseEntrypoint: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar\n", kept)
	})

	t.Run("cleared", func(ctx context.Context, t *testctx.T) {
		withoutEntry := withEntry.WithEntrypoint(nil)
		removed, err := withoutEntry.Entrypoint(ctx)
		require.NoError(t, err)
		require.Empty(t, removed)
	})
}

func (ContainerSuite) TestExecWithoutEntrypoint(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("cleared entrypoint", func(ctx context.Context, t *testctx.T) {
		res, err := c.Container().
			From(alpineImage).
			// if not unset this would return an error
			WithEntrypoint([]string{"foo"}).
			WithoutEntrypoint().
			WithExec([]string{"echo", "-n", "foobar"}, dagger.ContainerWithExecOpts{
				UseEntrypoint: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar", res)
	})

	t.Run("cleared entrypoint with default args", func(ctx context.Context, t *testctx.T) {
		res, err := c.Container().
			From(alpineImage).
			WithEntrypoint([]string{"foo"}).
			WithDefaultArgs([]string{"echo", "-n", "foobar"}).
			WithoutEntrypoint().
			Stdout(ctx)
		requireErrOut(t, err, "no command has been set")
		require.Empty(t, res)
	})

	t.Run("cleared entrypoint without default args", func(ctx context.Context, t *testctx.T) {
		res, err := c.Container().
			From(alpineImage).
			WithEntrypoint([]string{"foo"}).
			WithDefaultArgs([]string{"echo", "-n", "foobar"}).
			WithoutEntrypoint(dagger.ContainerWithoutEntrypointOpts{
				KeepDefaultArgs: true,
			}).
			WithExec(nil).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "foobar", res)
	})
}

func (ContainerSuite) TestWithDefaultArgs(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
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
	}](t,
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

						withExec(args: ["echo $HOME"], useEntrypoint: true) {
							stdout
						}

						withDefaultArgs(args: ["id"]) {
							entrypoint
							defaultArgs

							withExec(args: [], useEntrypoint: true) {
								stdout
							}
						}
					}
				}
			}
		}`, nil)
	t.Run("default alpine (no entrypoint)", func(ctx context.Context, t *testctx.T) {
		require.NoError(t, err)
		require.Empty(t, res.Container.From.Entrypoint)
		require.Equal(t, []string{"/bin/sh"}, res.Container.From.DefaultArgs)
	})

	t.Run("with nil default args", func(ctx context.Context, t *testctx.T) {
		require.Empty(t, res.Container.From.WithDefaultArgs.Entrypoint)
		require.Empty(t, res.Container.From.WithDefaultArgs.DefaultArgs)
	})

	t.Run("with entrypoint set", func(ctx context.Context, t *testctx.T) {
		require.Equal(t, []string{"sh", "-c"}, res.Container.From.WithEntrypoint.Entrypoint)
		require.Empty(t, res.Container.From.WithEntrypoint.DefaultArgs)
	})

	t.Run("with exec args", func(ctx context.Context, t *testctx.T) {
		require.Equal(t, "/root\n", res.Container.From.WithEntrypoint.WithExec.Stdout)
	})

	t.Run("with default args set", func(ctx context.Context, t *testctx.T) {
		require.Equal(t, []string{"sh", "-c"}, res.Container.From.WithEntrypoint.WithDefaultArgs.Entrypoint)
		require.Equal(t, []string{"id"}, res.Container.From.WithEntrypoint.WithDefaultArgs.DefaultArgs)

		require.Equal(t, "uid=0(root) gid=0(root) groups=0(root),1(bin),2(daemon),3(sys),4(adm),6(disk),10(wheel),11(floppy),20(dialout),26(tape),27(video)\n", res.Container.From.WithEntrypoint.WithDefaultArgs.WithExec.Stdout)
	})
}

func (ContainerSuite) TestExecWithoutDefaultArgs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	res, err := c.Container().
		From(alpineImage).
		WithEntrypoint([]string{"echo", "-n"}).
		WithDefaultArgs([]string{"foo"}).
		WithoutDefaultArgs().
		WithExec(nil, dagger.ContainerWithExecOpts{
			UseEntrypoint: true,
		}).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "", res)
}

func (ContainerSuite) TestExecWithEnvVariable(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				WithEnvVariable struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](t,
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
		}`, nil)
	require.NoError(t, err)
	require.Contains(t, res.Container.From.WithEnvVariable.WithExec.Stdout, "FOO=bar\n")
}

func (ContainerSuite) TestVariables(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				EnvVariables []core.EnvVariable
				WithExec     struct {
					Stdout string
				}
			}
		}
	}](t,
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
		}`, nil)
	require.NoError(t, err)
	require.Equal(t, []core.EnvVariable{
		{Name: "PATH", Value: "/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		{Name: "GOLANG_VERSION", Value: "1.18.2"},
		{Name: "GOPATH", Value: "/go"},
	}, res.Container.From.EnvVariables)
	require.Contains(t, res.Container.From.WithExec.Stdout, "GOPATH=/go\n")
}

func (ContainerSuite) TestVariable(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	res, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				EnvVariable *string
			}
		}
	}](c, t,
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					envVariable(name: "GOLANG_VERSION")
				}
			}
		}`, nil)
	require.NoError(t, err)
	require.NotNil(t, res.Container.From.EnvVariable)
	require.Equal(t, "1.18.2", *res.Container.From.EnvVariable)

	res, err = testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				EnvVariable *string
			}
		}
	}](c, t,
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					envVariable(name: "UNKNOWN")
				}
			}
		}`, nil)
	require.NoError(t, err)
	require.Nil(t, res.Container.From.EnvVariable)
}

func (ContainerSuite) TestWithoutVariable(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				WithoutEnvVariable struct {
					EnvVariables []core.EnvVariable
					WithExec     struct {
						Stdout string
					}
				}
			}
		}
	}](t,
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
		}`, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithoutEnvVariable.EnvVariables, []core.EnvVariable{
		{Name: "PATH", Value: "/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		{Name: "GOPATH", Value: "/go"},
	})
	require.NotContains(t, res.Container.From.WithoutEnvVariable.WithExec.Stdout, "GOLANG_VERSION")
}

func (ContainerSuite) TestEnvVariablesReplace(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				WithEnvVariable struct {
					EnvVariables []core.EnvVariable
					WithExec     struct {
						Stdout string
					}
				}
			}
		}
	}](t,
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
		}`, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithEnvVariable.EnvVariables, []core.EnvVariable{
		{Name: "PATH", Value: "/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"},
		{Name: "GOLANG_VERSION", Value: "1.18.2"},
		{Name: "GOPATH", Value: "/gone"},
	})
	require.Contains(t, res.Container.From.WithEnvVariable.WithExec.Stdout, "GOPATH=/gone\n")
}

func (ContainerSuite) TestWithEnvVariableExpand(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("add env var without expansion", func(ctx context.Context, t *testctx.T) {
		out, err := c.Container().
			From(alpineImage).
			WithEnvVariable("FOO", "foo:$PATH").
			WithExec([]string{"printenv", "FOO"}).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "foo:$PATH\n", out)
	})

	t.Run("add env var with expansion", func(ctx context.Context, t *testctx.T) {
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

func (ContainerSuite) TestLabel(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("container with new label", func(ctx context.Context, t *testctx.T) {
		label, err := c.Container().From(alpineImage).WithLabel("FOO", "BAR").Label(ctx, "FOO")

		require.NoError(t, err)
		require.Contains(t, label, "BAR")
	})

	// implementing this test as GraphQL query until
	// https://github.com/dagger/dagger/issues/4398 gets resolved
	t.Run("container labels", func(ctx context.Context, t *testctx.T) {
		res, err := testutil.QueryWithClient[struct {
			Container struct {
				From struct {
					Labels []schema.Label
				}
			}
		}](c, t,
			`{
				container {
				  from(address: "nginx") {
					labels {
					  name
					  value
					}
				  }
				}
			  }`, nil)
		require.NoError(t, err)
		require.Equal(t, []schema.Label{
			{Name: "maintainer", Value: "NGINX Docker Maintainers <docker-maint@nginx.com>"},
		}, res.Container.From.Labels)
	})

	t.Run("container without label", func(ctx context.Context, t *testctx.T) {
		label, err := c.Container().From("nginx").WithoutLabel("maintainer").Label(ctx, "maintainer")

		require.NoError(t, err)
		require.Empty(t, label)
	})

	t.Run("container replace label", func(ctx context.Context, t *testctx.T) {
		label, err := c.Container().From("nginx").WithLabel("maintainer", "bar").Label(ctx, "maintainer")

		require.NoError(t, err)
		require.Contains(t, label, "bar")
	})

	t.Run("container with new label - nil panics", func(ctx context.Context, t *testctx.T) {
		label, err := c.Container().WithLabel("FOO", "BAR").Label(ctx, "FOO")

		require.NoError(t, err)
		require.Contains(t, label, "BAR")
	})

	t.Run("container label - nil panics", func(ctx context.Context, t *testctx.T) {
		label, err := c.Container().Label(ctx, "FOO")

		require.NoError(t, err)
		require.Empty(t, label)
	})

	t.Run("container without label - nil panics", func(ctx context.Context, t *testctx.T) {
		label, err := c.Container().WithoutLabel("maintainer").Label(ctx, "maintainer")

		require.NoError(t, err)
		require.Empty(t, label)
	})

	// implementing this test as GraphQL query until
	// https://github.com/dagger/dagger/issues/4398 gets resolved
	t.Run("container labels - nil panics", func(ctx context.Context, t *testctx.T) {
		res, err := testutil.QueryWithClient[struct {
			Container struct {
				From struct {
					Labels []schema.Label
				}
			}
		}](c, t,
			`{
				container {
				  labels {
					name
					value
				  }
				}
			  }`, nil)
		require.NoError(t, err)
		require.Empty(t, res.Container.From.Labels)
	})
}

func (ContainerSuite) TestWorkdir(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			From struct {
				Workdir  string
				WithExec struct {
					Stdout string
				}
			}
		}
	}](t,
		`{
			container {
			  from(address: "golang:1.18.2-alpine") {
				workdir
				withExec(args: ["pwd"]) {
				  stdout
				}
			  }
			}
		  }`, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.Workdir, "/go")
	require.Equal(t, res.Container.From.WithExec.Stdout, "/go\n")
}

func (ContainerSuite) TestWithWorkdir(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
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
	}](t,
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
		}`, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.WithWorkdir.Workdir, "/usr")
	require.Equal(t, res.Container.From.WithWorkdir.WithExec.Stdout, "/usr\n")
}

func (ContainerSuite) TestWithMountedDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dirRes, err := testutil.QueryWithClient[struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					ID string
				}
			}
		}
	}](c, t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
						id
					}
				}
			}
		}`, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.WithNewFile.ID

	execRes, err := testutil.QueryWithClient[struct {
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
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	require.Equal(t, "some-content", execRes.Container.From.WithMountedDirectory.WithExec.Stdout)
	require.Equal(t, "sub-content", execRes.Container.From.WithMountedDirectory.WithExec.WithExec.Stdout)
}

func (ContainerSuite) TestWithMountedDirectorySourcePath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dirRes, err := testutil.QueryWithClient[struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					Directory struct {
						ID string
					}
				}
			}
		}
	}](c, t,
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
		}`, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.WithNewFile.Directory.ID

	execRes, err := testutil.QueryWithClient[struct {
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
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	require.Equal(t, "sub-content\nmore-content", execRes.Container.From.WithMountedDirectory.WithExec.WithExec.Stdout)
}

func (ContainerSuite) TestWithMountedDirectoryPropagation(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dirRes, err := testutil.QueryWithClient[struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}](c, t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					id
				}
			}
		}`, &testutil.QueryOptions{})
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.ID

	execRes, err := testutil.QueryWithClient[struct {
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
	}](c, t,
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
		}`, &testutil.QueryOptions{
			Variables: map[string]any{
				"id": id,
			},
		})
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

func (ContainerSuite) TestWithMountedFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dirRes, err := testutil.QueryWithClient[struct {
		Directory struct {
			WithNewFile struct {
				File struct {
					ID core.FileID
				}
			}
		}
	}](c, t,
		`{
			directory {
				withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
					file(path: "some-dir/sub-file") {
						id
					}
				}
			}
		}`, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.File.ID

	execRes, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedFile struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	require.Equal(t, "sub-content", execRes.Container.From.WithMountedFile.WithExec.Stdout)
}

func (ContainerSuite) TestWithMountedCache(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	cache := c.CacheVolume(t.Name())

	saveCache := preventCacheMountPrune(c, t, cache)

	rand1 := identity.NewID()
	out1, err := c.Container().
		From(alpineImage).
		With(saveCache).
		WithEnvVariable("RAND", rand1).
		WithMountedCache("/mnt/cache", cache).
		WithExec([]string{"sh", "-c", "echo $RAND >> /mnt/cache/sub-file; cat /mnt/cache/sub-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, rand1+"\n", out1)

	rand2 := identity.NewID()
	out2, err := c.Container().
		From(alpineImage).
		With(saveCache).
		WithEnvVariable("RAND", rand2).
		WithMountedCache("/mnt/cache", cache).
		WithExec([]string{"sh", "-c", "echo $RAND >> /mnt/cache/sub-file; cat /mnt/cache/sub-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, rand1+"\n"+rand2+"\n", out2)
}

func (ContainerSuite) TestWithMountedCacheFromDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	cache := c.CacheVolume(t.Name())

	srcDir := c.Directory().
		WithNewFile("some-dir/sub-file", "initial-content\n").
		Directory("some-dir")

	saveCache := preventCacheMountPrune(c, t, cache, dagger.ContainerWithMountedCacheOpts{Source: srcDir})

	rand1 := identity.NewID()
	out1, err := c.Container().
		From(alpineImage).
		With(saveCache).
		WithEnvVariable("RAND", rand1).
		WithMountedCache("/mnt/cache", cache, dagger.ContainerWithMountedCacheOpts{
			Source: srcDir,
		}).
		WithExec([]string{"sh", "-c", "echo $RAND >> /mnt/cache/sub-file; cat /mnt/cache/sub-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "initial-content\n"+rand1+"\n", out1)

	rand2 := identity.NewID()
	out2, err := c.Container().
		From(alpineImage).
		With(saveCache).
		WithEnvVariable("RAND", rand2).
		WithMountedCache("/mnt/cache", cache, dagger.ContainerWithMountedCacheOpts{
			Source: srcDir,
		}).
		WithExec([]string{"sh", "-c", "echo $RAND >> /mnt/cache/sub-file; cat /mnt/cache/sub-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "initial-content\n"+rand1+"\n"+rand2+"\n", out2)
}

func (ContainerSuite) TestWithMountedTemp(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	output := func(opts []dagger.ContainerWithMountedTempOpts) (string, error) {
		o, err := c.Container().
			From(alpineImage).
			WithMountedTemp("/mnt/tmp", opts...).
			WithExec([]string{"grep", "/mnt/tmp", "/proc/mounts"}).
			Stdout(ctx)

		return o, err
	}

	t.Run("default", func(ctx context.Context, t *testctx.T) {
		output, err := output([]dagger.ContainerWithMountedTempOpts{})

		require.NoError(t, err)
		require.Contains(t, output, "tmpfs /mnt/tmp tmpfs")
		require.NotContains(t, output, "size")
	})

	t.Run("sized", func(ctx context.Context, t *testctx.T) {
		output, err := output([]dagger.ContainerWithMountedTempOpts{
			{Size: 4000},
		})

		require.NoError(t, err)
		require.Contains(t, output, "size=4k")
	})
}

func (ContainerSuite) TestWithDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestWithFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestWithoutPath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().
		From(alpineImage).
		WithWorkdir("/workdir").
		WithNewFile("moo", "").
		WithNewFile("foo", "").
		WithNewFile("bar/man", "").
		WithNewFile("bat/man", "").
		WithNewFile("/ual", "")

	t.Run("no error if not exists", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithoutFile("not-exists").
			WithExec([]string{"ls", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar\nbat\nfoo\nmoo\n", out)
	})

	t.Run("files, with pattern", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithoutFile("*oo").
			WithExec([]string{"ls", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar\nbat\n", out)
	})

	t.Run("directory", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithoutDirectory("bar").
			WithExec([]string{"ls", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bat\nfoo\nmoo\n", out)
	})

	t.Run("current dir", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithoutDirectory("").
			WithExec([]string{"find", "/workdir"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/workdir\n", out)
	})

	t.Run("absolute", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithoutFile("/ual").
			WithExec([]string{"ls", "-1", "/"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "workdir")
		require.NotContains(t, out, "ual")
	})
}

func (ContainerSuite) TestWithoutPaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().
		From(alpineImage).
		WithWorkdir("/workdir").
		WithNewFile("xyz", "").
		WithNewFile("moo", "").
		WithNewFile("foo", "").
		WithNewFile("bar/man", "").
		WithNewFile("bat/man", "").
		WithNewFile("/ual", "")

	t.Run("no error if not exists", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithoutFiles([]string{"xyz", "not-exists"}).
			WithExec([]string{"ls", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar\nbat\nfoo\nmoo\n", out)
	})

	t.Run("files, with pattern", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithoutFiles([]string{"xyz", "*oo"}).
			WithExec([]string{"ls", "-1"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar\nbat\n", out)
	})

	t.Run("absolute", func(ctx context.Context, t *testctx.T) {
		out, err := ctr.
			WithoutFiles([]string{"xyz", "/ual"}).
			WithExec([]string{"ls", "-1", "/"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "workdir")
		require.NotContains(t, out, "ual")
	})
}

func (ContainerSuite) TestWithFiles(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	file1 := c.Directory().
		WithNewFile("first-file", "file1 content").
		File("first-file")
	file2 := c.Directory().
		WithNewFile("second-file", "file2 content").
		File("second-file")
	files := []*dagger.File{file1, file2}

	check := func(ctx context.Context, t *testctx.T, ctr *dagger.Container) {
		contents, err := ctr.WithExec([]string{"cat", "/myfiles/first-file"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "file1 content", contents)

		contents, err = ctr.WithExec([]string{"cat", "/myfiles/second-file"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "file2 content", contents)
	}

	t.Run("no trailing slash", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().
			From(alpineImage).
			WithFiles("myfiles", files)
		check(ctx, t, ctr)
	})

	t.Run("trailing slash", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().
			From(alpineImage).
			WithFiles("myfiles/", files)
		check(ctx, t, ctr)
	})
}

func (ContainerSuite) TestWithFilesAbsolute(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	file1 := c.Directory().
		WithNewFile("first-file", "file1 content").
		File("first-file")
	file2 := c.Directory().
		WithNewFile("second-file", "file2 content").
		File("second-file")
	files := []*dagger.File{file1, file2}

	ctr := c.Container().
		From(alpineImage).
		WithWorkdir("/work").
		WithFiles("/opt/myfiles", files)

	contents, err := ctr.
		WithExec([]string{"cat", "/opt/myfiles/first-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "file1 content", contents)

	contents, err = ctr.
		WithExec([]string{"cat", "/opt/myfiles/second-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "file2 content", contents)
}

func (ContainerSuite) TestWithFilesNested(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// Create a directory with a nested file
	dir := c.Directory().
		WithNewFile("/sub/file", "nested content").
		Directory("/sub")
	file := dir.File("file")

	// WithFile should place the file directly at the target path
	ctrWithFile := c.Container().
		From(alpineImage).
		WithFile("/tmp", file)
	filesWithFile, err := ctrWithFile.Directory("/tmp").Glob(ctx, "**/*")
	require.NoError(t, err)
	require.Equal(t, []string{"file"}, filesWithFile)

	// WithFiles should place the file at its absolute path under the target
	ctrWithFiles := c.Container().
		From(alpineImage).
		WithFiles("/tmp", []*dagger.File{file})
	filesWithFiles, err := ctrWithFiles.Directory("/tmp").Glob(ctx, "**/*")
	require.NoError(t, err)
	require.Equal(t, []string{"file"}, filesWithFiles)
}

func (ContainerSuite) TestWithNewFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().
		From(alpineImage).
		WithWorkdir("/workdir").
		WithNewFile("some-file", "some-content")

	contents, err := ctr.WithExec([]string{"cat", "some-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", contents)

	contents, err = ctr.WithExec([]string{"cat", "/workdir/some-file"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "some-content", contents)
}

func (ContainerSuite) TestMountsWithoutMount(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	scratchID, err := c.Directory().ID(ctx)
	require.NoError(t, err)

	dirRes, err := testutil.QueryWithClient[struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					ID string
				}
			}
		}
	}](c, t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
						id
					}
				}
			}
		}`, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.WithNewFile.ID

	execRes, err := testutil.QueryWithClient[struct {
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
	}](c, t,
		`query Test($id: DirectoryID!, $scratch: DirectoryID!) {
			container {
				from(address: "`+alpineImage+`") {
					withDirectory(path: "/mnt/dir", source: $scratch) {
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
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

func (ContainerSuite) TestReplacedMounts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	lower := c.Directory().WithNewFile("some-file", "lower-content")

	upper := c.Directory().WithNewFile("some-file", "upper-content")

	ctr := c.Container().
		From(alpineImage).
		WithMountedDirectory("/mnt/dir", lower)

	t.Run("initial content is lower", func(ctx context.Context, t *testctx.T) {
		mnts, err := ctr.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt/dir"}, mnts)

		out, err := ctr.WithExec([]string{"cat", "/mnt/dir/some-file"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "lower-content", out)
	})

	replaced := ctr.WithMountedDirectory("/mnt/dir", upper)

	t.Run("mounts of same path are replaced", func(ctx context.Context, t *testctx.T) {
		mnts, err := replaced.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt/dir"}, mnts)

		out, err := replaced.WithExec([]string{"cat", "/mnt/dir/some-file"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "upper-content", out)
	})

	t.Run("removing a replaced mount does not reveal previous mount", func(ctx context.Context, t *testctx.T) {
		removed := replaced.WithoutMount("/mnt/dir")
		mnts, err := removed.Mounts(ctx)
		require.NoError(t, err)
		require.Empty(t, mnts)
	})

	clobberedDir := c.Directory().WithNewFile("some-file", "clobbered-content")
	clobbered := replaced.WithMountedDirectory("/mnt", clobberedDir)

	t.Run("replacing parent of a mount clobbers child", func(ctx context.Context, t *testctx.T) {
		mnts, err := clobbered.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt"}, mnts)

		out, err := clobbered.WithExec([]string{"cat", "/mnt/some-file"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "clobbered-content", out)
	})

	clobberedSubDir := c.Directory().WithNewFile("some-file", "clobbered-sub-content")
	clobberedSub := clobbered.WithMountedDirectory("/mnt/dir", clobberedSubDir)

	t.Run("restoring mount under clobbered mount", func(ctx context.Context, t *testctx.T) {
		mnts, err := clobberedSub.Mounts(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"/mnt", "/mnt/dir"}, mnts)

		out, err := clobberedSub.WithExec([]string{"cat", "/mnt/dir/some-file"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "clobbered-sub-content", out)
	})
}

func (ContainerSuite) TestDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dirRes, err := testutil.QueryWithClient[struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					ID core.DirectoryID
				}
			}
		}
	}](c, t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
						id
					}
				}
			}
		}`, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.WithNewFile.ID

	writeRes, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithMountedDirectory struct {
						WithExec struct {
							Directory struct {
								ID string
							}
						}
					}
				}
			}
		}
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)

	writtenID := writeRes.Container.From.WithMountedDirectory.WithMountedDirectory.WithExec.Directory.ID

	execRes, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "hello\n", execRes.Container.From.WithMountedDirectory.WithExec.Stdout)
}

func (ContainerSuite) TestDirectoryErrors(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dirRes, err := testutil.QueryWithClient[struct {
		Directory struct {
			WithNewFile struct {
				WithNewFile struct {
					ID core.DirectoryID
				}
			}
		}
	}](c, t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					withNewFile(path: "some-dir/sub-file", contents: "sub-content") {
						id
					}
				}
			}
		}`, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.WithNewFile.ID

	_, err = testutil.QueryWithClient[any](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.Error(t, err)
	requireErrOut(t, err, "path /mnt/dir/some-file is a file, not a directory")

	_, err = testutil.QueryWithClient[any](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.Error(t, err)
	requireErrOut(t, err, "bogus: no such file or directory")

	_, err = testutil.QueryWithClient[any](c, t,
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
		}`, nil)
	require.Error(t, err)
	requireErrOut(t, err, "bogus: cannot retrieve path from tmpfs")

	cacheID := newCache(t)
	_, err = testutil.QueryWithClient[any](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"cache": cacheID,
		}})
	require.Error(t, err)
	requireErrOut(t, err, "bogus: cannot retrieve path from cache")
}

func (ContainerSuite) TestDirectorySourcePath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dirRes, err := testutil.QueryWithClient[struct {
		Directory struct {
			WithNewFile struct {
				Directory struct {
					ID core.DirectoryID
				}
			}
		}
	}](c, t,
		`{
			directory {
				withNewFile(path: "some-dir/sub-dir/sub-file", contents: "sub-content\n") {
					directory(path: "some-dir") {
						id
					}
				}
			}
		}`, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.Directory.ID

	writeRes, err := testutil.QueryWithClient[struct {
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
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)

	writtenID := writeRes.Container.From.WithMountedDirectory.WithExec.Directory.ID

	execRes, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "sub-content\nmore-content\n", execRes.Container.From.WithMountedDirectory.WithExec.Stdout)
}

func (ContainerSuite) TestFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	id := newDirWithFile(t, "some-file", "some-content-")

	writeRes, err := testutil.QueryWithClient[struct {
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
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)

	writtenID := writeRes.Container.From.WithMountedDirectory.WithMountedDirectory.WithExec.File.ID

	execRes, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedFile struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "some-content-appended", execRes.Container.From.WithMountedFile.WithExec.Stdout)
}

func (ContainerSuite) TestFileErrors(ctx context.Context, t *testctx.T) {
	id := newDirWithFile(t, "some-file", "some-content")

	t.Run("path not found", func(ctx context.Context, t *testctx.T) {
		_, err := testutil.Query[any](t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
				"id": id,
			}})
		require.Error(t, err)
		requireErrOut(t, err, "bogus: no such file or directory")
	})

	t.Run("get directory as file", func(ctx context.Context, t *testctx.T) {
		_, err := testutil.Query[any](t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
				"id": id,
			}})
		require.Error(t, err)
		requireErrOut(t, err, "path /mnt/dir is a directory, not a file")
	})

	t.Run("get path under tmpfs", func(ctx context.Context, t *testctx.T) {
		_, err := testutil.Query[any](t,
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
		}`, nil)
		require.Error(t, err)
		requireErrOut(t, err, "bogus: cannot retrieve path from tmpfs")
	})

	t.Run("get path under cache", func(ctx context.Context, t *testctx.T) {
		cacheID := newCache(t)
		_, err := testutil.Query[any](t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
				"cache": cacheID,
			}})
		require.Error(t, err)
		requireErrOut(t, err, "bogus: cannot retrieve path from cache")
	})

	t.Run("get secret mount contents", func(ctx context.Context, t *testctx.T) {
		_, err := testutil.Query[any](t,
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
		}`, &testutil.QueryOptions{Secrets: map[string]string{
				"secret": "some-secret",
			}})
		require.Error(t, err)
		requireErrOut(t, err, "sekret: no such file or directory")
	})
}

func (ContainerSuite) TestFSDirectory(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dirRes, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				Directory struct {
					ID core.DirectoryID
				}
			}
		}
	}](c, t,
		`{
			container {
				from(address: "`+alpineImage+`") {
					directory(path: "/etc") {
						id
					}
				}
			}
		}`, nil)
	require.NoError(t, err)

	etcID := dirRes.Container.From.Directory.ID

	execRes, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": etcID,
		}})
	require.NoError(t, err)

	releaseStr := execRes.Container.From.WithMountedDirectory.WithExec.Stdout
	require.Equal(t, distconsts.AlpineVersion, strings.TrimSpace(releaseStr))
}

func (ContainerSuite) TestRelativePaths(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dirRes, err := testutil.QueryWithClient[struct {
		Directory struct {
			WithNewFile struct {
				ID core.DirectoryID
			}
		}
	}](c, t,
		`{
			directory {
				withNewFile(path: "some-file", contents: "some-content") {
					id
				}
			}
		}`, nil)
	require.NoError(t, err)

	id := dirRes.Directory.WithNewFile.ID

	cacheID := newCache(t)
	writeRes, err := testutil.QueryWithClient[struct {
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
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
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

	execRes, err := testutil.QueryWithClient[struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					WithExec struct {
						Stdout string
					}
				}
			}
		}
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": writtenID,
		}})
	require.NoError(t, err)

	require.Equal(t, "another-file\nsome-file\n", execRes.Container.From.WithMountedDirectory.WithExec.Stdout)
}

func (ContainerSuite) TestMultiFrom(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	dirRes, err := testutil.QueryWithClient[struct {
		Directory struct {
			ID core.DirectoryID
		}
	}](c, t,
		`{
			directory {
				id
			}
		}`, nil)
	require.NoError(t, err)

	id := dirRes.Directory.ID

	execRes, err := testutil.QueryWithClient[struct {
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
	}](c, t,
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
		}`, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	require.Contains(t, execRes.Container.From.WithMountedDirectory.WithExec.From.WithExec.WithExec.Stdout, "v18.10.0\n")
	require.Contains(t, execRes.Container.From.WithMountedDirectory.WithExec.From.WithExec.WithExec.Stdout, "go version go1.18.2")
}

func (ContainerSuite) TestPublish(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	testRef := registryRef("container-publish")

	args := []string{"echo", "im-a-default-arg"}
	ctr := c.Container().From(alpineImage).WithDefaultArgs(args)
	pushedRef, err := ctr.Publish(ctx, testRef)
	require.NoError(t, err)
	require.NotEqual(t, testRef, pushedRef)
	require.Contains(t, pushedRef, "@sha256:")

	pulledCtr := c.Container().From(pushedRef)
	contents, err := pulledCtr.File("/etc/alpine-release").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, distconsts.AlpineVersion, strings.TrimSpace(contents))

	output, err := pulledCtr.WithExec(nil).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "im-a-default-arg\n", output)
}

func (ContainerSuite) TestAnnotations(ctx context.Context, t *testctx.T) {
	build := func(c *dagger.Client, platform dagger.Platform) *dagger.Container {
		return c.Container(dagger.ContainerOpts{Platform: platform}).
			From(alpineImage).
			WithAnnotation("org.opencontainers.image.version", "v0.1.2")
	}

	t.Run("publish", func(ctx context.Context, t *testctx.T) {
		t.Run("single-platform", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			testRef := registryRef("container-annotations")

			ctr := build(c, "")
			pushedRef, err := ctr.Publish(ctx, testRef)
			require.NoError(t, err)
			require.NotEqual(t, testRef, pushedRef)
			require.Contains(t, pushedRef, "@sha256:")

			parsedRef, err := name.ParseReference(pushedRef, name.Insecure)
			require.NoError(t, err)

			imgDesc, err := remote.Get(parsedRef, remote.WithTransport(http.DefaultTransport))
			require.NoError(t, err)

			// check on manifest
			img, err := imgDesc.Image()
			require.NoError(t, err)
			manifest, err := img.Manifest()
			require.NoError(t, err)
			require.Equal(t, "v0.1.2", manifest.Annotations["org.opencontainers.image.version"])
		})

		t.Run("multi-platform", func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			testRef := registryRef("container-annotations")

			pushedRef, err := c.Container().Publish(ctx, testRef, dagger.ContainerPublishOpts{
				PlatformVariants: []*dagger.Container{
					build(c, "linux/amd64"),
					build(c, "linux/arm64"),
				},
			})
			require.NoError(t, err)
			require.NotEqual(t, testRef, pushedRef)
			require.Contains(t, pushedRef, "@sha256:")

			parsedRef, err := name.ParseReference(pushedRef, name.Insecure)
			require.NoError(t, err)

			imgDesc, err := remote.Get(parsedRef, remote.WithTransport(http.DefaultTransport))
			require.NoError(t, err)

			imgs, err := imgDesc.ImageIndex()
			require.NoError(t, err)
			idx, err := imgs.IndexManifest()
			require.NoError(t, err)
			require.Len(t, idx.Manifests, 2)
			for _, manifestDesc := range idx.Manifests {
				// check on manifest descriptor
				require.Equal(t, "v0.1.2", manifestDesc.Annotations["org.opencontainers.image.version"])
				require.NoError(t, err)

				// check on manifest
				img, err := imgs.Image(manifestDesc.Digest)
				require.NoError(t, err)
				manifest, err := img.Manifest()
				require.NoError(t, err)
				require.Equal(t, "v0.1.2", manifest.Annotations["org.opencontainers.image.version"])
			}
		})
	})

	testExport := func(asTarball bool) func(ctx context.Context, t *testctx.T) {
		return func(ctx context.Context, t *testctx.T) {
			t.Run("single-platform", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				dest := t.TempDir()
				imageTar := filepath.Join(dest, "image.tar")

				if asTarball {
					ctr := build(c, "")
					_, err := ctr.AsTarball().Export(ctx, imageTar)
					require.NoError(t, err)
				} else {
					ctr := build(c, "")
					_, err := ctr.Export(ctx, imageTar)
					require.NoError(t, err)
				}

				entries := tarEntries(t, imageTar)
				require.Contains(t, entries, "oci-layout")
				require.Contains(t, entries, "index.json")

				idxDt := readTarFile(t, imageTar, "index.json")
				idx := ocispecs.Index{}
				require.NoError(t, json.Unmarshal(idxDt, &idx))

				mfstDt := readTarFile(t, imageTar, "blobs/sha256/"+idx.Manifests[0].Digest.Encoded())
				mfst := ocispecs.Manifest{}
				require.NoError(t, json.Unmarshal(mfstDt, &mfst))

				require.Equal(t, "v0.1.2", mfst.Annotations["org.opencontainers.image.version"])
			})

			t.Run("multi-platform", func(ctx context.Context, t *testctx.T) {
				c := connect(ctx, t)

				dest := t.TempDir()
				imageTar := filepath.Join(dest, "image.tar")

				if asTarball {
					_, err := c.Container().
						AsTarball(dagger.ContainerAsTarballOpts{
							PlatformVariants: []*dagger.Container{
								build(c, "linux/amd64"),
								build(c, "linux/arm64"),
							},
						}).
						Export(ctx, imageTar)
					require.NoError(t, err)
				} else {
					_, err := c.Container().Export(ctx, imageTar, dagger.ContainerExportOpts{
						PlatformVariants: []*dagger.Container{
							build(c, "linux/amd64"),
							build(c, "linux/arm64"),
						},
					})
					require.NoError(t, err)
				}

				entries := tarEntries(t, imageTar)
				require.Contains(t, entries, "oci-layout")
				require.Contains(t, entries, "index.json")

				idxDt := readTarFile(t, imageTar, "index.json")
				var idx ocispecs.Index
				require.NoError(t, json.Unmarshal(idxDt, &idx))

				idxDt = readTarFile(t, imageTar, "blobs/sha256/"+idx.Manifests[0].Digest.Encoded())
				idx = ocispecs.Index{}
				require.NoError(t, json.Unmarshal(idxDt, &idx))

				require.Len(t, idx.Manifests, 2)
				for _, manifestDesc := range idx.Manifests {
					// check on manifest descriptor
					require.Equal(t, "v0.1.2", manifestDesc.Annotations["org.opencontainers.image.version"])

					// check on manifest
					mfstDt := readTarFile(t, imageTar, "blobs/sha256/"+manifestDesc.Digest.Encoded())
					mfst := ocispecs.Manifest{}
					require.NoError(t, json.Unmarshal(mfstDt, &mfst))
					require.Equal(t, "v0.1.2", mfst.Annotations["org.opencontainers.image.version"])
				}
			})
		}
	}
	t.Run("export", testExport(false))
	t.Run("export tarball", testExport(true))
}

func (ContainerSuite) TestExecFromScratch(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestMultipleMounts(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExport(ctx context.Context, t *testctx.T) {
	wd := t.TempDir()
	dest := t.TempDir()

	c := connect(ctx, t, dagger.WithWorkdir(wd))

	entrypoint := []string{"sh", "-c", "im-a-entrypoint"}
	ctr := c.Container().From(alpineImage).
		WithEntrypoint(entrypoint)

	t.Run("to absolute dir", func(ctx context.Context, t *testctx.T) {
		for _, useAsTarball := range []bool{true, false} {
			t.Run(fmt.Sprintf("useAsTarball=%t", useAsTarball), func(ctx context.Context, t *testctx.T) {
				imagePath := filepath.Join(dest, identity.NewID()+".tar")

				if useAsTarball {
					tarFile := ctr.AsTarball()
					actual, err := tarFile.Export(ctx, imagePath)
					require.NoError(t, err)
					require.Equal(t, imagePath, actual)
				} else {
					actual, err := ctr.Export(ctx, imagePath)
					require.NoError(t, err)
					require.Equal(t, imagePath, actual)
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

	t.Run("to workdir", func(ctx context.Context, t *testctx.T) {
		relPath := "./" + identity.NewID() + ".tar"
		actual, err := ctr.Export(ctx, relPath)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(wd, relPath), actual)

		stat, err := os.Stat(filepath.Join(wd, relPath))
		require.NoError(t, err)
		require.NotZero(t, stat.Size())
		require.EqualValues(t, 0o600, stat.Mode().Perm())

		entries := tarEntries(t, filepath.Join(wd, relPath))
		require.Contains(t, entries, "oci-layout")
		require.Contains(t, entries, "index.json")
		require.Contains(t, entries, "manifest.json")
	})

	t.Run("to subdir", func(ctx context.Context, t *testctx.T) {
		relPath := "./foo/" + identity.NewID() + ".tar"
		actual, err := ctr.Export(ctx, relPath)
		require.NoError(t, err)
		require.Equal(t, filepath.Join(wd, relPath), actual)

		entries := tarEntries(t, filepath.Join(wd, relPath))
		require.Contains(t, entries, "oci-layout")
		require.Contains(t, entries, "index.json")
		require.Contains(t, entries, "manifest.json")
	})

	t.Run("to outer dir", func(ctx context.Context, t *testctx.T) {
		actual, err := ctr.Export(ctx, "../")
		require.Error(t, err)
		require.Empty(t, actual)
	})
}

// NOTE: more test coverage of Container.AsTarball are in TestContainerExport and TestContainerMultiPlatformExport
func (ContainerSuite) TestAsTarball(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Container().From(alpineImage)
	output, err := ctr.
		WithMountedFile("/foo.tar", ctr.AsTarball()).
		WithExec([]string{"apk", "add", "file"}).
		WithExec([]string{"file", "/foo.tar"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "/foo.tar: POSIX tar archive\n", output)
}

func (ContainerSuite) TestAsTarballCached(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Container().From(alpineImage)
	first, err := ctr.
		WithMountedFile("/foo.tar", ctr.AsTarball()).
		WithExec([]string{"sha256sum", "/foo.tar"}).
		Stdout(ctx)
	require.NoError(t, err)

	// make sure the index.json timestamp changes so we get a different hash
	time.Sleep(2 * time.Second)

	// setup a second client, so we don't share the dagql cache
	c2 := connect(ctx, t)
	ctr2 := c2.Container().From(alpineImage)
	second, err := ctr2.
		WithMountedFile("/foo.tar", ctr2.AsTarball()).
		WithExec([]string{"sha256sum", "/foo.tar"}).
		Stdout(ctx)
	require.NoError(t, err)

	require.Equal(t, first, second)
}

func (ContainerSuite) TestImport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("OCI", func(ctx context.Context, t *testctx.T) {
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
			WithNewFile("config.yml", string(cfgYaml))

		imageFile := apko.
			WithExec([]string{
				"apko",
				"build",
				"config.yml", "latest", "output.tar",
			}).
			File("output.tar")

		imported := c.Container().Import(imageFile)

		out, err := imported.WithExec([]string{"sh", "-c", "echo $FOO"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar\n", out)
	})

	t.Run("Docker", func(ctx context.Context, t *testctx.T) {
		out, err := c.Container().
			Import(c.Container().From(alpineImage).WithEnvVariable("FOO", "bar").AsTarball(dagger.ContainerAsTarballOpts{
				MediaTypes: dagger.ImageMediaTypesDockerMediaTypes,
			})).
			WithExec([]string{"sh", "-c", "echo $FOO"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bar\n", out)
	})
}

func (ContainerSuite) TestFromImagePlatform(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	imageRef := alpineAmd
	var desiredPlatform dagger.Platform = "linux/amd64"
	targetPlatform := desiredPlatform
	if runtime.GOARCH == "amd64" {
		// need a platform that doesn't match the host
		imageRef = alpineArm
		desiredPlatform = "linux/arm64"
		targetPlatform = "linux/arm64/v8"
	}

	ctr := c.Container(dagger.ContainerOpts{
		Platform: targetPlatform,
	}).From(imageRef)
	ctrPlatform, err := ctr.Platform(ctx)
	require.NoError(t, err)
	require.Equal(t, desiredPlatform, ctrPlatform)
}

func (ContainerSuite) TestFromIDPlatform(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	var targetPlatform dagger.Platform = "linux/arm64/v8"
	var desiredPlatform dagger.Platform = "linux/arm64"

	id, err := c.Container(dagger.ContainerOpts{
		Platform: targetPlatform,
	}).From(alpineImage).ID(ctx)
	require.NoError(t, err)

	platform, err := c.LoadContainerFromID(id).Platform(ctx)
	require.NoError(t, err)
	require.Equal(t, desiredPlatform, platform)
}

func (ContainerSuite) TestMultiPlatformExport(ctx context.Context, t *testctx.T) {
	for _, useAsTarball := range []bool{true, false} {
		t.Run(fmt.Sprintf("useAsTarball=%t", useAsTarball), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

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
				actual, err := tarFile.Export(ctx, dest)
				require.NoError(t, err)
				require.Equal(t, dest, actual)
			} else {
				actual, err := c.Container().Export(ctx, dest, dagger.ContainerExportOpts{
					PlatformVariants: variants,
				})
				require.NoError(t, err)
				require.Equal(t, dest, actual)
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
func (ContainerSuite) TestMultiPlatformPublish(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	variants := make([]*dagger.Container, 0, len(platformToUname))
	for platform, uname := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(alpineImage).
			WithExec([]string{"uname", "-m"}).
			WithDefaultArgs([]string{"echo", uname})
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

func (ContainerSuite) TestMultiPlatformImport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	variants := make([]*dagger.Container, 0, len(platformToUname))
	for platform := range platformToUname {
		ctr := c.Container(dagger.ContainerOpts{Platform: platform}).
			From(alpineImage)

		variants = append(variants, ctr)
	}

	tmp := t.TempDir()
	imagePath := filepath.Join(tmp, "image.tar")

	actual, err := c.Container().Export(ctx, imagePath, dagger.ContainerExportOpts{
		PlatformVariants: variants,
	})
	require.NoError(t, err)
	require.Equal(t, imagePath, actual)

	for platform, uname := range platformToUname {
		imported := c.Container(dagger.ContainerOpts{Platform: platform}).
			Import(c.Host().Directory(tmp).File("image.tar"))

		out, err := imported.WithExec([]string{"uname", "-m"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, uname+"\n", out)
	}
}

func (ContainerSuite) TestWithDirectoryToMount(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestExecError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	outMsg := "THIS SHOULD GO TO STDOUT"
	encodedOutMsg := base64.StdEncoding.EncodeToString([]byte(outMsg))
	errMsg := "THIS SHOULD GO TO STDERR"
	encodedErrMsg := base64.StdEncoding.EncodeToString([]byte(errMsg))

	t.Run("includes output of failed exec in error", func(ctx context.Context, t *testctx.T) {
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

	t.Run("includes output of failed exec in error when redirects are enabled", func(ctx context.Context, t *testctx.T) {
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

	t.Run("truncates output past a maximum size", func(ctx context.Context, t *testctx.T) {
		const extraByteCount = 50

		// fill a byte buffer with a string that is slightly over the size of the max output
		// size, then base64 encode it
		// include some newlines to avoid https://github.com/dagger/dagger/issues/7786
		var stdoutBuf bytes.Buffer
		for i := range buildkit.MaxExecErrorOutputBytes + extraByteCount {
			if i > 0 && i%100 == 0 {
				stdoutBuf.WriteByte('\n')
			} else {
				stdoutBuf.WriteByte('a')
			}
		}
		stdoutStr := stdoutBuf.String()
		encodedOutMsg := base64.StdEncoding.EncodeToString(stdoutBuf.Bytes())

		var stderrBuf bytes.Buffer
		for i := range buildkit.MaxExecErrorOutputBytes + extraByteCount {
			if i > 0 && i%100 == 0 {
				stderrBuf.WriteByte('\n')
			} else {
				stderrBuf.WriteByte('b')
			}
		}
		stderrStr := stderrBuf.String()
		encodedErrMsg := base64.StdEncoding.EncodeToString(stderrBuf.Bytes())

		truncMsg := fmt.Sprintf(buildkit.TruncationMessage, extraByteCount)

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
		require.Equal(t, truncMsg+stdoutStr[extraByteCount+len(truncMsg):], exErr.Stdout)
		require.Equal(t, truncMsg+stderrStr[extraByteCount+len(truncMsg):], exErr.Stderr)
	})
}

func (ContainerSuite) TestWithRegistryAuth(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestImageRef(ctx context.Context, t *testctx.T) {
	t.Run("should test query returning imageRef", func(ctx context.Context, t *testctx.T) {
		res, err := testutil.Query[struct {
			Container struct {
				From struct {
					ImageRef string
				}
			}
		}](t,
			`{
				container {
					from(address: "`+alpineImage+`") {
						imageRef
					}
				}
			}`, nil)
		require.NoError(t, err)
		require.Contains(t, res.Container.From.ImageRef, "docker.io/library/"+alpineImage+"@sha256:")
	})

	t.Run("should throw error after the container image modification with exec", func(ctx context.Context, t *testctx.T) {
		_, err := testutil.Query[struct {
			Container struct {
				From struct {
					ImageRef string
				}
			}
		}](t,
			`{
				container {
					from(address:"hello-world") {
						withExec(args:["/hello"]) {
							imageRef
						}
					}
				}
			}`, nil)
		require.Error(t, err)
		requireErrOut(t, err, "Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
	})

	t.Run("should throw error after the container image modification with exec", func(ctx context.Context, t *testctx.T) {
		_, err := testutil.Query[struct {
			Container struct {
				From struct {
					ImageRef string
				}
			}
		}](t,
			`{
				container {
					from(address:"hello-world") {
						withExec(args:["/hello"]) {
							imageRef
						}
					}
				}
			}`, nil)
		require.Error(t, err)
		requireErrOut(t, err, "Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
	})

	t.Run("should throw error after the container image modification with directory", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

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
		requireErrOut(t, err, "Image reference can only be retrieved immediately after the 'Container.From' call. Error in fetching imageRef as the container image is changed")
	})
}

func (ContainerSuite) TestInsecureRootCapabilites(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestInsecureRootCapabilitesWithService(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	middleware := func(ctr *dagger.Container) *dagger.Container {
		return ctr.WithMountedCache("/tmp", c.CacheVolume("share-tmp"))
	}

	// verify the root capabilities setting works by executing dockerd with it and
	// testing it can startup, create containers and bind mount from its filesystem to
	// them.
	randID := identity.NewID()
	dockerc := dockerSetup(ctx, t, c, containerSetupOpts{name: "provisioner", version: "23.0.1", middleware: middleware})
	out, err := dockerc.
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

func (ContainerSuite) TestWithMountedFileOwner(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("simple file", func(ctx context.Context, t *testctx.T) {
		tmp := t.TempDir()

		err := os.WriteFile(filepath.Join(tmp, "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		file := c.Host().Directory(tmp).File("message.txt")

		testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithMountedFile(name, file, dagger.ContainerWithMountedFileOpts{
				Owner: owner,
			})
		})
	})

	t.Run("file from subdirectory", func(ctx context.Context, t *testctx.T) {
		tmp := t.TempDir()

		err := os.Mkdir(filepath.Join(tmp, "subdir"), 0o755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(tmp, "subdir", "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		file := c.Host().Directory(tmp).Directory("subdir").File("message.txt")

		testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithMountedFile(name, file, dagger.ContainerWithMountedFileOpts{
				Owner: owner,
			})
		})
	})
}

func (ContainerSuite) TestWithMountedDirectoryOwner(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("simple directory", func(ctx context.Context, t *testctx.T) {
		tmp := t.TempDir()

		err := os.WriteFile(filepath.Join(tmp, "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		dir := c.Host().Directory(tmp)

		testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithMountedDirectory(name, dir, dagger.ContainerWithMountedDirectoryOpts{
				Owner: owner,
			})
		})
	})

	t.Run("subdirectory", func(ctx context.Context, t *testctx.T) {
		tmp := t.TempDir()

		err := os.Mkdir(filepath.Join(tmp, "subdir"), 0o755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(tmp, "subdir", "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		dir := c.Host().Directory(tmp).Directory("subdir")

		testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithMountedDirectory(name, dir, dagger.ContainerWithMountedDirectoryOpts{
				Owner: owner,
			})
		})
	})

	t.Run("permissions", func(ctx context.Context, t *testctx.T) {
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

func (ContainerSuite) TestWithFileOwner(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("simple file", func(ctx context.Context, t *testctx.T) {
		tmp := t.TempDir()

		err := os.WriteFile(filepath.Join(tmp, "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		file := c.Host().Directory(tmp).File("message.txt")

		testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithFile(name, file, dagger.ContainerWithFileOpts{
				Owner: owner,
			})
		})
	})

	t.Run("file from subdirectory", func(ctx context.Context, t *testctx.T) {
		tmp := t.TempDir()

		err := os.Mkdir(filepath.Join(tmp, "subdir"), 0o755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(tmp, "subdir", "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		file := c.Host().Directory(tmp).Directory("subdir").File("message.txt")

		testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithFile(name, file, dagger.ContainerWithFileOpts{
				Owner: owner,
			})
		})
	})
}

func (ContainerSuite) TestWithDirectoryOwner(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("simple directory", func(ctx context.Context, t *testctx.T) {
		tmp := t.TempDir()

		err := os.WriteFile(filepath.Join(tmp, "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		dir := c.Host().Directory(tmp)

		testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithDirectory(name, dir, dagger.ContainerWithDirectoryOpts{
				Owner: owner,
			})
		})
	})

	t.Run("subdirectory", func(ctx context.Context, t *testctx.T) {
		tmp := t.TempDir()

		err := os.Mkdir(filepath.Join(tmp, "subdir"), 0o755)
		require.NoError(t, err)

		err = os.WriteFile(filepath.Join(tmp, "subdir", "message.txt"), []byte("hello world"), 0o600)
		require.NoError(t, err)

		dir := c.Host().Directory(tmp).Directory("subdir")

		testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
			return ctr.WithDirectory(name, dir, dagger.ContainerWithDirectoryOpts{
				Owner: owner,
			})
		})
	})
}

func (ContainerSuite) TestWithNewFileOwner(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
		return ctr.WithNewFile(name, "", dagger.ContainerWithNewFileOpts{Owner: owner})
	})
}

func (ContainerSuite) TestWithMountedCacheOwner(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	cache := c.CacheVolume("test")

	testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
		return ctr.WithMountedCache(name, cache, dagger.ContainerWithMountedCacheOpts{
			Owner: owner,
		})
	})

	t.Run("permissions (empty)", func(ctx context.Context, t *testctx.T) {
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

	t.Run("permissions (source)", func(ctx context.Context, t *testctx.T) {
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

func (ContainerSuite) TestWithMountedSecretOwner(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	secret := c.SetSecret("test", "hunter2")

	testOwnership(t, c, func(ctr *dagger.Container, name string, owner string) *dagger.Container {
		return ctr.WithMountedSecret(name, secret, dagger.ContainerWithMountedSecretOpts{
			Owner: owner,
		})
	})
}

func (ContainerSuite) TestParallelMutation(ctx context.Context, t *testctx.T) {
	res, err := testutil.Query[struct {
		Container struct {
			A struct {
				EnvVariable string
			}
			B string
		}
	}](t,
		`{
			container {
				a: withEnvVariable(name: "FOO", value: "BAR") {
					envVariable(name: "FOO")
				}
				b: envVariable(name: "FOO")
			}
		}`, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.A.EnvVariable, "BAR")
	require.Empty(t, res.Container.B, "BAR")
}

func (ContainerSuite) TestForceCompression(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		compression          dagger.ImageLayerCompression
		expectedOCIMediaType string
	}{
		{
			dagger.ImageLayerCompressionGzip,
			"application/vnd.oci.image.layer.v1.tar+gzip",
		},
		{
			dagger.ImageLayerCompressionZstd,
			"application/vnd.oci.image.layer.v1.tar+zstd",
		},
		{
			dagger.ImageLayerCompressionUncompressed,
			"application/vnd.oci.image.layer.v1.tar",
		},
		{
			dagger.ImageLayerCompressionEstarGz,
			"application/vnd.oci.image.layer.v1.tar+gzip",
		},
	} {
		t.Run(string(tc.compression), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

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

func (ContainerSuite) TestMediaTypes(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		mediaTypes           dagger.ImageMediaTypes
		expectedOCIMediaType string
	}{
		{
			"", // use default
			"application/vnd.oci.image.layer.v1.tar+gzip",
		},
		{
			dagger.ImageMediaTypesOcimediaTypes,
			"application/vnd.oci.image.layer.v1.tar+gzip",
		},
		{
			dagger.ImageMediaTypesDockerMediaTypes,
			"application/vnd.docker.image.rootfs.diff.tar.gzip",
		},
	} {
		t.Run(string(tc.mediaTypes), func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

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
				t.Run(fmt.Sprintf("useAsTarball=%t", useAsTarball), func(ctx context.Context, t *testctx.T) {
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

func (ContainerSuite) TestFromMergesWithParent(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (ContainerSuite) TestImageLoadCompatibility(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	for _, dockerVersion := range []string{"20.10", "23.0", "24.0"} {
		dockerc := dockerSetup(ctx, t, c, containerSetupOpts{name: t.Name(), version: dockerVersion})
		for _, mediaType := range []dagger.ImageMediaTypes{dagger.ImageMediaTypesOcimediaTypes, dagger.ImageMediaTypesDockerMediaTypes} {
			for _, compression := range []dagger.ImageLayerCompression{dagger.ImageLayerCompressionGzip, dagger.ImageLayerCompressionZstd, dagger.ImageLayerCompressionUncompressed} {
				t.Run(fmt.Sprintf("%s-%s-%s-%s", t.Name(), dockerVersion, mediaType, compression), func(ctx context.Context, t *testctx.T) {
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

					ctr := dockerc.
						WithMountedFile(path.Join("/", path.Base(tmpfile)), c.Host().File(tmpfile)).
						WithExec([]string{"docker", "load", "-i", "/" + path.Base(tmpfile)})

					output, err := ctr.Stdout(ctx)
					if dockerVersion == "20.10" && compression == dagger.ImageLayerCompressionZstd {
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

func (ContainerSuite) TestWithMountedSecretMode(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
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

func (ContainerSuite) TestNestedExec(ctx context.Context, t *testctx.T) {
	t.Run("basic", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := c.Container().From(alpineImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithNewFile("/query.graphql", `{ defaultPlatform }`). // arbitrary valid query
			WithExec([]string{"dagger", "query", "--doc", "/query.graphql"}, dagger.ContainerWithExecOpts{
				ExperimentalPrivilegedNesting: true,
			}).
			Sync(ctx)
		require.NoError(t, err)
	})

	t.Run("caching", func(ctx context.Context, t *testctx.T) {
		// This is regression test for a bug where nested exec cache keys were scoped to the dagql call ID digest
		// of the exec, which subtly resulted in content-based caching not working for nested execs.
		c1 := connect(ctx, t)
		c2 := connect(ctx, t)

		// write /tmpdir/a/f and /tmpdir/b/f
		tmpDir := t.TempDir()
		const subdirA = "a"
		const subdirB = "b"
		const subfileName = "f"
		require.NoError(t, os.Mkdir(filepath.Join(tmpDir, subdirA), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, subdirA, subfileName), []byte("1"), 0o644))
		require.NoError(t, os.Mkdir(filepath.Join(tmpDir, subdirB), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, subdirB, subfileName), []byte("1"), 0o644))

		runCtrs := func(c *dagger.Client, dir *dagger.Directory, subdir string) string {
			t.Helper()
			out, err := c.Container().From(alpineImage).
				WithDirectory("/mnt", dir.Directory(subdir)).
				WithExec([]string{"sh", "-c", "head -c 128 /dev/random | sha256sum"}).
				Stdout(ctx)
			require.NoError(t, err)
			return out
		}

		hostDir1 := c1.Host().Directory(tmpDir)
		// run an exec that has /tmpdir/a/f included
		output1a := runCtrs(c1, hostDir1, subdirA)
		// run an exec that has /tmpdir/b/f included
		output1b := runCtrs(c1, hostDir1, subdirB)

		// these should be cached execs, since f is the same in both a and b
		require.Equal(t, output1a, output1b)

		// change /tmpdir/b/f
		require.NoError(t, os.WriteFile(filepath.Join(tmpDir, subdirB, subfileName), []byte("2"), 0o644))

		hostDir2 := c2.Host().Directory(tmpDir)
		// run an exec that has /tmpdir/a/f included
		output2a := runCtrs(c2, hostDir2, subdirA)
		// run an exec that has /tmpdir/b/f included
		output2b := runCtrs(c2, hostDir2, subdirB)
		// sanity check: those should be different execs, *not* cached because f changed between a and b
		require.NotEqual(t, output2a, output2b)

		// we only changed /tmpdir/b/f, so the execs that included /tmpdir/a/f should be cached across clients
		// this is the assertion that failed before the fix this test was added for
		require.Equal(t, output1a, output2a)
		// and the execs that included /tmpdir/b/f should not be cached across clients since we modified that file
		require.NotEqual(t, output1b, output2b)
	})
}

func (ContainerSuite) TestEmptyExecDiff(ctx context.Context, t *testctx.T) {
	// if an exec makes no changes, the diff should be empty, including of files
	// mounted in by the engine like the init/resolv.conf/etc.

	c := connect(ctx, t)

	base := c.Container().From(alpineImage)
	ents, err := base.Rootfs().Diff(base.WithExec([]string{"true"}).Rootfs()).Entries(ctx)
	require.NoError(t, err)
	require.Len(t, ents, 0)
}

func (ContainerSuite) TestExecExpect(ctx context.Context, t *testctx.T) {
	t.Run("any", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		res, err := testutil.QueryWithClient[struct {
			Container struct {
				From struct {
					WithExec struct {
						ExitCode int
					}
				}
			}
		}](c, t,
			`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["sh", "-c", "exit 0"], expect: ANY) {
						exitCode
					}
				}
			}
		}`, nil)
		require.NoError(t, err)
		require.Equal(t, 0, res.Container.From.WithExec.ExitCode)

		res, err = testutil.QueryWithClient[struct {
			Container struct {
				From struct {
					WithExec struct {
						ExitCode int
					}
				}
			}
		}](c, t,
			`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["sh", "-c", "exit 1"], expect: ANY) {
						exitCode
					}
				}
			}
		}`, nil)
		require.NoError(t, err)
		require.Equal(t, 1, res.Container.From.WithExec.ExitCode)
	})

	t.Run("success", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		res, err := testutil.QueryWithClient[struct {
			Container struct {
				From struct {
					WithExec struct {
						ExitCode int
					}
				}
			}
		}](c, t,
			`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["sh", "-c", "exit 0"], expect: SUCCESS) {
						exitCode
					}
				}
			}
		}`, nil)
		require.NoError(t, err)
		require.Equal(t, 0, res.Container.From.WithExec.ExitCode)

		_, err = testutil.QueryWithClient[struct {
			Container struct {
				From struct {
					WithExec struct {
						ExitCode int
					}
				}
			}
		}](c, t,
			`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["sh", "-c", "exit 1"], expect: SUCCESS) {
						exitCode
					}
				}
			}
		}`, nil)
		requireErrOut(t, err, "exit code: 1")
	})

	t.Run("failure", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		_, err := testutil.QueryWithClient[struct {
			Container struct {
				From struct {
					WithExec struct {
						ExitCode int
					}
				}
			}
		}](c, t,
			`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["sh", "-c", "exit 0"], expect: FAILURE) {
						exitCode
					}
				}
			}
		}`, nil)
		requireErrOut(t, err, "exit code: 0")

		res, err := testutil.QueryWithClient[struct {
			Container struct {
				From struct {
					WithExec struct {
						ExitCode int
					}
				}
			}
		}](c, t,
			`{
			container {
				from(address: "`+alpineImage+`") {
					withExec(args: ["sh", "-c", "exit 1"], expect: FAILURE) {
						exitCode
					}
				}
			}
		}`, nil)
		require.NoError(t, err)
		require.Equal(t, 1, res.Container.From.WithExec.ExitCode)
	})
}

func (ContainerSuite) TestEnvExpand(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("env variable is expanded in WithNewFile", func(ctx context.Context, t *testctx.T) {
		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithNewFile("${foo}.txt", "contents in foo file", dagger.ContainerWithNewFileOpts{Expand: true}).
			File("bar.txt").Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "contents in foo file", output)
	})

	t.Run("env variable is expanded in WithFile", func(ctx context.Context, t *testctx.T) {
		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithFile(
				"${foo}.txt",
				c.Directory().WithNewFile("/foo.txt", "contents in foo file").File("/foo.txt"),
				dagger.ContainerWithFileOpts{Expand: true},
			).
			File("bar.txt").Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "contents in foo file", output)
	})

	t.Run("env variable is expanded in WithDirectory", func(ctx context.Context, t *testctx.T) {
		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithDirectory(
				"/some-path/${foo}",
				c.Directory().WithNewFile("/some-file.txt", "contents in foo file"),
				dagger.ContainerWithDirectoryOpts{Expand: true},
			).
			Directory("/some-path/bar", dagger.ContainerDirectoryOpts{Expand: true}).
			File("some-file.txt").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "contents in foo file", output)
	})

	t.Run("env variable is expanded in Directory", func(ctx context.Context, t *testctx.T) {
		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithDirectory(
				"/some-path/bar",
				c.Directory().WithNewFile("/some-file.txt", "contents in foo file"),
			).
			Directory("/some-path/${foo}", dagger.ContainerDirectoryOpts{Expand: true}).
			File("some-file.txt").
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "contents in foo file", output)
	})

	t.Run("env variable is expanded in File", func(ctx context.Context, t *testctx.T) {
		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithDirectory(
				"/some-path/bar",
				c.Directory().WithNewFile("/some-file.txt", "contents in foo file"),
				dagger.ContainerWithDirectoryOpts{Expand: true},
			).
			File("/some-path/${foo}/some-file.txt", dagger.ContainerFileOpts{Expand: true}).
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "contents in foo file", output)
	})

	t.Run("env variable is expanded in WithMountedDirectory", func(ctx context.Context, t *testctx.T) {
		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithMountedDirectory(
				"/some-path/${foo}",
				c.Directory().WithNewFile("/some-file.txt", "contents in foo file"),
				dagger.ContainerWithMountedDirectoryOpts{Expand: true},
			).
			File("/some-path/bar/some-file.txt", dagger.ContainerFileOpts{Expand: true}).
			Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "contents in foo file", output)
	})

	t.Run("env variable is expanded in WithMountedFile", func(ctx context.Context, t *testctx.T) {
		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithMountedFile(
				"/some-path/${foo}/some-file.txt",
				c.Directory().WithNewFile("/some-file.txt", "contents in foo file").File("/some-file.txt"),
				dagger.ContainerWithMountedFileOpts{Expand: true},
			).
			File("/some-path/bar/some-file.txt").Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "contents in foo file", output)
	})

	t.Run("env variable is expanded in WithoutDirectory", func(ctx context.Context, t *testctx.T) {
		_, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithExec([]string{"mkdir", "-p", "/some-path/bar"}).
			WithoutDirectory(
				"/some-path/${foo}",
				dagger.ContainerWithoutDirectoryOpts{Expand: true},
			).
			WithExec([]string{"ls", "/some-path/bar"}).Stdout(ctx)

		requireErrOut(t, err, "ls: /some-path/bar: No such file or directory")
	})

	t.Run("env variable is expanded in WithoutFile", func(ctx context.Context, t *testctx.T) {
		_, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithFile(
				"/some-path/bar/some-file.txt",
				c.Directory().WithNewFile("/some-file.txt", "contents in foo file").File("/some-file.txt"),
			).
			WithoutFile("/some-path/${foo}/some-file.txt", dagger.ContainerWithoutFileOpts{Expand: true}).
			WithExec([]string{"ls", "/some-path/bar/some-file.txt"}).Stdout(ctx)

		requireErrOut(t, err, "ls: /some-path/bar/some-file.txt: No such file or directory")
	})

	t.Run("env variable is expanded in WithoutFiles", func(ctx context.Context, t *testctx.T) {
		_, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithFile(
				"/some-path/bar/some-file.txt",
				c.Directory().WithNewFile("/some-file.txt", "contents in foo file").File("/some-file.txt"),
			).
			WithoutFiles([]string{"/some-path/${foo}/some-file.txt"}, dagger.ContainerWithoutFilesOpts{Expand: true}).
			WithExec([]string{"ls", "/some-path/bar/some-file.txt"}).Stdout(ctx)

		requireErrOut(t, err, "ls: /some-path/bar/some-file.txt: No such file or directory")
	})

	t.Run("env variable is expanded in WithExec", func(ctx context.Context, t *testctx.T) {
		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithExec([]string{"echo", `/some-arg/${foo}`}, dagger.ContainerWithExecOpts{Expand: true}).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "/some-arg/bar\n", output)
	})

	t.Run("env variable is expanded in WithoutMount", func(ctx context.Context, t *testctx.T) {
		_, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithMountedDirectory("/mnt/bar", c.Directory().WithNewDirectory("/foo")).
			WithoutMount("/mnt/${foo}", dagger.ContainerWithoutMountOpts{Expand: true}).
			WithExec([]string{"ls", `/mnt/bar`}).
			Stdout(ctx)

		require.Error(t, err)
		requireErrOut(t, err, "ls: /mnt/bar: No such file or directory")
	})

	t.Run("env variable is expanded in WithUnixSocket", func(ctx context.Context, t *testctx.T) {
		tmp := t.TempDir()
		sock := filepath.Join(tmp, "test.sock")

		l, err := net.Listen("unix", sock)
		require.NoError(t, err)

		defer l.Close()

		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithUnixSocket("/opt/${foo}.sock", c.Host().UnixSocket(sock), dagger.ContainerWithUnixSocketOpts{Expand: true}).
			WithExec([]string{"ls", `/opt/bar.sock`}).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "/opt/bar.sock\n", output)
	})

	t.Run("env variable is expanded in WithoutUnixSocket", func(ctx context.Context, t *testctx.T) {
		tmp := t.TempDir()
		sock := filepath.Join(tmp, "test.sock")

		l, err := net.Listen("unix", sock)
		require.NoError(t, err)

		defer l.Close()

		_, err = c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithUnixSocket("/opt/bar.sock", c.Host().UnixSocket(sock)).
			WithoutUnixSocket("/opt/${foo}.sock", dagger.ContainerWithoutUnixSocketOpts{Expand: true}).
			WithExec([]string{"ls", `/opt/bar.sock`}).
			Stdout(ctx)

		require.Error(t, err)
		requireErrOut(t, err, "ls: /opt/bar.sock: No such file or directory")
	})

	t.Run("env variable is expanded in WithMountedSecret", func(ctx context.Context, t *testctx.T) {
		// Generate 512000 random bytes (non UTF-8)
		// This is our current limit: secrets break at 512001 bytes
		data := make([]byte, 512000)
		_, err := rand.Read(data)
		if err != nil {
			panic(err)
		}

		// Compute the MD5 hash of the data
		hash := md5.Sum(data)
		hashStr := hex.EncodeToString(hash[:])

		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "some-file"), data, 0o600))

		secret := c.Secret("file://" + filepath.Join(dir, "some-file"))
		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("foo", "bar").
			WithEnvVariable("CACHEBUST", identity.NewID()).
			WithMountedSecret(
				"/${foo}.mysecret",
				secret,
				dagger.ContainerWithMountedSecretOpts{Expand: true},
			).
			WithExec([]string{"md5sum", "/bar.mysecret"}).
			Stdout(ctx)

		require.NoError(t, err)
		// Extract the MD5 hash from the command output
		hashStrCmd := strings.Split(output, " ")[0]
		require.Equal(t, hashStr, hashStrCmd)
	})

	t.Run("using secret variable with expand returns error", func(ctx context.Context, t *testctx.T) {
		secret := c.SetSecret("gitea-token", "password2")
		_, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("CACHEBUST", identity.NewID()).
			WithSecretVariable("GITEA_TOKEN", secret).
			WithExec([]string{"sh", "-c", "test ${GITEA_TOKEN} = \"password\""}, dagger.ContainerWithExecOpts{Expand: true}).
			Sync(ctx)

		requireErrOut(t, err, "expand cannot be used with secret env variable \"GITEA_TOKEN\"")
	})

	t.Run("env variable is expanded in Export", func(ctx context.Context, t *testctx.T) {
		wd := t.TempDir()

		c := connect(ctx, t, dagger.WithWorkdir(wd))

		entrypoint := []string{"sh", "-c", "im-a-entrypoint"}
		ctr := c.Container().From(alpineImage).
			WithEntrypoint(entrypoint)

		actual, err := ctr.
			WithEnvVariable("foo", "bar").
			Export(ctx, "./${foo}.tar", dagger.ContainerExportOpts{Expand: true})
		require.NoError(t, err)
		require.Equal(t, filepath.Join(wd, "./bar.tar"), actual)

		stat, err := os.Stat(filepath.Join(wd, "./bar.tar"))
		require.NoError(t, err)
		require.NotZero(t, stat.Size())
		require.EqualValues(t, 0o600, stat.Mode().Perm())

		entries := tarEntries(t, filepath.Join(wd, "./bar.tar"))
		require.Contains(t, entries, "oci-layout")
		require.Contains(t, entries, "index.json")
		require.Contains(t, entries, "manifest.json")
	})

	t.Run("env variable is expanded in WithSymlink", func(ctx context.Context, t *testctx.T) {
		output, err := c.Container().
			From("alpine:latest").
			WithEnvVariable("a", "alpha").
			WithEnvVariable("b", "bravo").
			WithNewFile("bravo.txt", "phonetic data").
			WithSymlink("${b}.txt", "${a}.txt", dagger.ContainerWithSymlinkOpts{Expand: true}).
			File("alpha.txt").Contents(ctx)

		require.NoError(t, err)
		require.Equal(t, "phonetic data", output)
	})
}

func (ContainerSuite) TestExecInit(ctx context.Context, t *testctx.T) {
	t.Run("automatic init", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := c.Container().From(alpineImage).
			WithExec([]string{"ps", "-o", "pid,comm"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "1 .init")
	})

	t.Run("automatic init in dockerfile build", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		dir := c.Directory().
			WithNewFile("Dockerfile",
				`FROM `+alpineImage+`
RUN sh -c 'ps -o pid,comm > /output.txt'
`)
		out, err := dir.DockerBuild().File("output.txt").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "1 .init")
	})

	t.Run("disable automatic init", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		out, err := c.Container().From(alpineImage).
			WithExec([]string{"ps", "-o", "pid,comm"}, dagger.ContainerWithExecOpts{
				NoInit: true,
			}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "1 ps")
	})

	t.Run("disable automatic init in dockerfile build", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		dir := c.Directory().
			WithNewFile("Dockerfile",
				`FROM `+alpineImage+`
RUN sh -c 'ps -o pid,comm > /output.txt'
`)
		out, err := dir.DockerBuild(dagger.DirectoryDockerBuildOpts{
			NoInit: true,
		}).File("output.txt").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "1 ps")
	})
}

func (ContainerSuite) TestContainerAsService(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	maingo := `package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

func main() {
	http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "args: %s", strings.Join(os.Args, ","))
	})

	fmt.Println(http.ListenAndServe(":8080", nil))
}`
	buildctr := c.Container().
		From(golangImage).
		WithWorkdir("/work").
		WithNewFile("/work/main.go", maingo).
		WithExec([]string{"go", "build", "-o=app", "main.go"})

	binctr := c.Container().
		From(alpineImage).
		WithFile("/bin/app", buildctr.File("/work/app")).
		WithEntrypoint([]string{"/bin/app", "via-entrypoint"}).
		WithDefaultArgs([]string{"/bin/app", "via-default-args"}).
		WithExposedPort(8080)

	curlctr := c.Container().
		From(alpineImage).
		WithExec([]string{"sh", "-c", "apk add curl"})

	t.Run("use default args and entrypoint by default", func(ctx context.Context, t *testctx.T) {
		// create new container with default values
		defaultBin := c.Container().Import(binctr.AsTarball())

		// NOTE: when doing an Import (or container.From), the ports show up under the image config; but
		// do not _actually_ get setup under the container -- this is similar to a Dockerfile's EXPOSE keyword
		// which is merely a _suggestion_ rather than exposing the ports when running the container.
		// TODO: maybe re-evaluate this choice? It's difficult to say what the expected behaviour should be.
		// but for now we will keep it to match what Dockerfiles do with EXPOSE vs docker run --expose.
		exposedPorts, err := defaultBin.ExposedPorts(ctx)
		require.NoError(t, err)
		require.Len(t, exposedPorts, 1)

		port, err := exposedPorts[0].Port(ctx)
		require.NoError(t, err)
		require.Equal(t, port, 8080)

		// as a result of the above image config vs container.Ports distinction, we must re-expose
		// these ports in order to have a healthcheck setup; otherwise there's a race condition
		// where the curl command might run before the server has started up.
		defaultBin = defaultBin.WithExposedPort(8080)

		output, err := curlctr.
			WithServiceBinding("myapp", defaultBin.AsService()).
			WithExec([]string{"sh", "-c", "curl -vXGET 'http://myapp:8080/hello'"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-entrypoint,/bin/app,via-default-args", output)
	})

	t.Run("can override default args", func(ctx context.Context, t *testctx.T) {
		withargsOverwritten := binctr.
			AsService(dagger.ContainerAsServiceOpts{Args: []string{"sh", "-c", "/bin/app via-service-override"}})

		output, err := curlctr.
			WithServiceBinding("myapp", withargsOverwritten).
			WithExec([]string{"sh", "-c", "curl -vXGET 'http://myapp:8080/hello'"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-service-override", output)
	})

	t.Run("can enable entrypoint", func(ctx context.Context, t *testctx.T) {
		withargsOverwritten := binctr.
			AsService(dagger.ContainerAsServiceOpts{
				UseEntrypoint: true,
			})

		output, err := curlctr.
			WithServiceBinding("myapp", withargsOverwritten).
			WithExec([]string{"sh", "-c", "curl -vXGET 'http://myapp:8080/hello'"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-entrypoint,/bin/app,via-default-args", output)
	})

	t.Run("use both args and entrypoint", func(ctx context.Context, t *testctx.T) {
		withargsOverwritten := binctr.
			AsService(dagger.ContainerAsServiceOpts{
				UseEntrypoint: true,
				Args:          []string{"/bin/app via-service-override"},
			})

		output, err := curlctr.
			WithServiceBinding("myapp", withargsOverwritten).
			WithExec([]string{"sh", "-c", "curl -vXGET 'http://myapp:8080/hello'"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-entrypoint,/bin/app via-service-override", output)
	})

	t.Run("error when no cmd and entrypoint is set", func(ctx context.Context, t *testctx.T) {
		withargsOverwritten := binctr.
			WithoutEntrypoint().
			WithoutDefaultArgs().
			AsService()

		_, err := curlctr.
			WithServiceBinding("myapp", withargsOverwritten).
			WithExec([]string{"sh", "-c", "curl -vXGET 'http://myapp:8080/hello'"}).
			Stdout(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), core.ErrNoSvcCommand.Error())
	})
	t.Run("default args no entrypoint", func(ctx context.Context, t *testctx.T) {
		withargsOverwritten := binctr.
			WithDefaultArgs([]string{"sh", "-c", "/bin/app via-override-args"}).
			AsService()

		output, err := curlctr.
			WithServiceBinding("myapp", withargsOverwritten).
			WithExec([]string{"sh", "-c", "curl -vXGET 'http://myapp:8080/hello'"}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-override-args", output)
	})
}

func (ContainerSuite) TestSymlink(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("symlink can be created", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().
			From(alpineImage).
			WithWorkdir("/test").
			WithNewFile("f", "data").
			WithSymlink("f", "my-symlink")

		_, err := ctr.WithExec([]string{"test", "-L", "/test/my-symlink"}).Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("symlink can be created above working dir", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().
			From(alpineImage).
			WithNewFile("f", "data").
			WithWorkdir("/test").
			WithSymlink("../f", "my-symlink")

		_, err := ctr.WithExec([]string{"test", "-L", "/test/my-symlink"}).Stdout(ctx)
		require.NoError(t, err)
	})

	t.Run("symlink can be created to root", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().
			From(alpineImage).
			WithNewFile("f", "data").
			WithSymlink("/", "/sub/my-symlink")

		content, err := ctr.File("/sub/my-symlink/f").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", content)
	})

	t.Run("symlink can be created to directory above working dir", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().
			From(alpineImage).
			WithNewFile("other-dir/sub/f", "data").
			WithWorkdir("/test").
			WithSymlink("../other-dir/sub/", "my-symlink")

		content, err := ctr.File("/test/my-symlink/f").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", content)
	})

	t.Run("symlink can be used to read data", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().
			From(alpineImage).
			WithWorkdir("/test").
			WithNewFile("f", "data").
			WithSymlink("f", "my-symlink")

		content, err := ctr.File("my-symlink").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", content)
	})

	t.Run("symlink work with mounted directory", func(ctx context.Context, t *testctx.T) {
		d := c.Directory().WithNewFile("f", "data")
		d2 := c.Directory().WithNewFile("f", "otherdata")

		ctr := c.Container().
			From(alpineImage).
			WithMountedDirectory("/mnt", d).
			WithMountedDirectory("/mnt-to-other-dir", d2).
			WithSymlink("f", "/mnt/my-symlink")

		_, err := ctr.WithExec([]string{"test", "-L", "/mnt/my-symlink"}).Stdout(ctx)
		require.NoError(t, err)

		// make sure the other mount wasn't changed
		_, err = ctr.File("/mnt-to-other-dir/my-symlink").Sync(ctx)
		require.ErrorContains(t, err, "no such file or directory")

		content, err := ctr.File("/mnt/my-symlink").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", content)
	})

	t.Run("symlink work with mounted directory with workdir set", func(ctx context.Context, t *testctx.T) {
		d := c.Directory().WithNewFile("sub/submarine/f", "data")

		ctr := c.Container().
			From(alpineImage).
			WithMountedDirectory("/mnt", d).
			WithWorkdir("/mnt/sub").
			WithSymlink("f", "submarine/my-symlink")

		content, err := ctr.File("/mnt/sub/submarine/my-symlink").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", content)
	})

	t.Run("symlink cant escape root fs", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().
			From(alpineImage).
			WithNewFile("some-file", "data").
			WithSymlink("some-file", "../../../../../../../../../../../../../../../this-should-be-in-the-root-fs")

		entries, err := ctr.Rootfs().Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, entries, "this-should-be-in-the-root-fs")

		s, err := ctr.File("this-should-be-in-the-root-fs").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", s)
	})

	t.Run("symlink cant target above root fs", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().
			From(alpineImage).
			WithSymlink("../../../../../../../../../../../../../../..", "escape").
			WithNewFile("escape/some-file", "data")

		s, err := ctr.File("some-file").Contents(ctx)
		require.NoError(t, err)
		require.Equal(t, "data", s)
	})

	t.Run("symlink cant follow other symlink above root fs", func(ctx context.Context, t *testctx.T) {
		ctr := c.Container().
			From(alpineImage).
			WithSymlink("/root", "escape").
			WithSymlink("_", "escape/foo/bar")

		entries, err := ctr.Directory("root/foo").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, entries, "bar")
	})

	t.Run("symlink works with scratch container", func(ctx context.Context, t *testctx.T) {
		_, err := c.Container().
			WithSymlink("doesnt-matter", "symlink").
			Sync(ctx)
		require.NoError(t, err)
	})
}

func (ContainerSuite) TestSymlinkCaching(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	out1, err := c.Container().
		From(alpineImage).
		WithSymlink("bar", "foo").
		WithExec([]string{"sh", "-c", "head -c 99 /dev/random | base64 -w0"}).
		Stdout(ctx)

	require.NoError(t, err)
	require.Len(t, out1, 132) // test that 99 chars were randomly produced, this accounts for 4/3 times base64 bloat

	out2, err := c.Container().
		From(alpineImage).
		WithSymlink("bar", "foo").
		WithExec([]string{"sh", "-c", "head -c 99 /dev/random | base64 -w0"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, out1, out2)

	out3, err := c.Container().
		From(alpineImage).
		WithSymlink("barf", "oo"). // Note the args here are different, and should bust the cache
		WithExec([]string{"sh", "-c", "head -c 99 /dev/random | base64 -w0"}).
		Stdout(ctx)
	require.NoError(t, err)
	require.NotEqual(t, out1, out3) // make sure the call to read from /dev/random was re-run
	require.Len(t, out3, 132)
}

func (ContainerSuite) TestSaveHostDocker(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dockerc := dockerSetup(ctx, t, c, containerSetupOpts{name: "provisioner"})
	dockerc, err := dockerLoadEngine(ctx, c, dockerc, "registry.dagger.io/engine:dev")
	require.NoError(t, err)

	dockerc = dockerc.
		WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:dev?container=dagger.test&port=1234").
		WithExec([]string{"dagger", "core", "version"})

	t.Run("docker-image driver", func(ctx context.Context, t *testctx.T) {
		imageName := "foobar:" + identity.NewID()
		_, err := dockerc.WithExec([]string{"dagger", "shell", "-c", `container | from "alpine" | with-exec touch,foo | export-image "` + imageName + `"`}).Sync(ctx)
		require.NoError(t, err)

		_, err = dockerc.WithExec([]string{"docker", "inspect", imageName}).Sync(ctx)
		require.NoError(t, err)

		out, err := dockerc.WithExec([]string{"docker", "run", imageName, "ls", "/foo"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/foo\n", out)
	})

	t.Run("docker-container driver", func(ctx context.Context, t *testctx.T) {
		alt := dockerc.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-container://dagger.test")

		imageName := "foobar:" + identity.NewID()
		_, err := alt.WithExec([]string{"dagger", "shell", "-c", `container | from "alpine" | with-exec touch,foo | export-image "` + imageName + `"`}).Sync(ctx)
		require.NoError(t, err)

		_, err = alt.WithExec([]string{"docker", "inspect", imageName}).Sync(ctx)
		require.NoError(t, err)

		out, err := alt.WithExec([]string{"docker", "run", imageName, "ls", "/foo"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/foo\n", out)
	})

	t.Run("tcp driver", func(ctx context.Context, t *testctx.T) {
		alt := dockerc.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://docker:1234")

		imageName := "foobar:" + identity.NewID()
		_, err := alt.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_IMAGESTORE", "docker-image").
			WithExec([]string{"dagger", "shell", "-c", `container | from "alpine" | with-exec touch,foo | export-image "` + imageName + `"`}).
			Sync(ctx)
		require.NoError(t, err)

		_, err = alt.WithExec([]string{"docker", "inspect", imageName}).Sync(ctx)
		require.NoError(t, err)

		out, err := alt.WithExec([]string{"docker", "run", imageName, "ls", "/foo"}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/foo\n", out)
	})
}

func (ContainerSuite) TestSaveHostContainerd(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	nerdctl := nerdctlSetup(ctx, t, c, containerSetupOpts{name: "save-host-containerd", version: "v2.1.2"})
	nerdctl, err := nerdctlLoadEngine(ctx, c, nerdctl, "registry.dagger.io/engine:dev")
	require.NoError(t, err)

	nerdctl = nerdctl.
		WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "image+nerdctl://registry.dagger.io/engine:dev?container=dagger.test&port=1234").
		WithExec([]string{"dagger", "core", "version"}, dagger.ContainerWithExecOpts{InsecureRootCapabilities: true})

	t.Run("tcp driver", func(ctx context.Context, t *testctx.T) {
		alt := nerdctl.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://containerd:1234")

		imageName := "foobar:" + identity.NewID()
		_, err := alt.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_IMAGESTORE", "containerd").
			WithExec([]string{"dagger", "shell", "-c", `container | from "alpine" | with-exec touch,foo | export-image "` + imageName + `"`}).
			Sync(ctx)
		require.NoError(t, err)

		_, err = alt.WithExec([]string{"nerdctl", "inspect", imageName}).Sync(ctx)
		require.NoError(t, err)

		out, err := alt.WithExec([]string{"nerdctl", "run", imageName, "ls", "/foo"}, dagger.ContainerWithExecOpts{
			InsecureRootCapabilities: true,
		}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/foo\n", out)
	})
}

func (ContainerSuite) TestLoadHostDocker(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dockerc := dockerSetup(ctx, t, c, containerSetupOpts{name: "provisioner"})
	dockerc, err := dockerLoadEngine(ctx, c, dockerc, "registry.dagger.io/engine:dev")
	require.NoError(t, err)

	dockerc = dockerc.
		WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:dev?container=dagger.test&port=1234").
		WithExec([]string{"dagger", "core", "version"})

	t.Run("docker-image driver", func(ctx context.Context, t *testctx.T) {
		imageName := "foobar:" + identity.NewID()
		_, err := dockerc.WithExec([]string{"docker", "build", "-t", imageName, "-"}, dagger.ContainerWithExecOpts{Stdin: "FROM alpine\nRUN touch /foo\n"}).Sync(ctx)
		require.NoError(t, err)

		out, err := dockerc.WithExec([]string{"dagger", "shell", "-c", `host | container-image ` + imageName + ` | with-exec ls,/foo | stdout`}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/foo\n", out)
	})

	t.Run("docker-container driver", func(ctx context.Context, t *testctx.T) {
		alt := dockerc.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-container://dagger.test")

		imageName := "foobar:" + identity.NewID()
		_, err := dockerc.WithExec([]string{"docker", "build", "-t", imageName, "-"}, dagger.ContainerWithExecOpts{Stdin: "FROM alpine\nRUN touch /foo\n"}).Sync(ctx)
		require.NoError(t, err)

		out, err := alt.WithExec([]string{"dagger", "shell", "-c", `host | container-image ` + imageName + ` | with-exec ls,/foo | stdout`}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/foo\n", out)
	})

	t.Run("tcp driver", func(ctx context.Context, t *testctx.T) {
		alt := dockerc.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://docker:1234")

		imageName := "foobar:" + identity.NewID()
		_, err := dockerc.WithExec([]string{"docker", "build", "-t", imageName, "-"}, dagger.ContainerWithExecOpts{Stdin: "FROM alpine\nRUN touch /foo\n"}).Sync(ctx)
		require.NoError(t, err)

		out, err := alt.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_IMAGESTORE", "docker-image").
			WithExec([]string{"dagger", "shell", "-c", `host | container-image ` + imageName + ` | with-exec ls,/foo | stdout`}).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/foo\n", out)
	})
}

func (ContainerSuite) TestLoadHostContainerd(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	nerdctl := nerdctlSetup(ctx, t, c, containerSetupOpts{name: "load-host-containerd", version: "v2.1.2"})
	nerdctl, err := nerdctlLoadEngine(ctx, c, nerdctl, "registry.dagger.io/engine:dev")
	require.NoError(t, err)

	nerdctl = nerdctl.
		WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
		WithSymlink("/usr/local/bin/nerdctl", "/usr/local/bin/docker").
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:dev?container=dagger.test&port=1234").
		WithExec([]string{"dagger", "core", "version"}, dagger.ContainerWithExecOpts{InsecureRootCapabilities: true}).
		WithoutFile("/usr/local/bin/docker")

	t.Run("tcp driver", func(ctx context.Context, t *testctx.T) {
		alt := nerdctl.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://containerd:1234")

		imageName := "foobar:" + identity.NewID()
		_, err := alt.WithExec([]string{"nerdctl", "pull", "alpine"}).Sync(ctx)
		require.NoError(t, err)

		_, err = alt.
			// HACK: buildkit isn't distributed in the nerdctl image we use, so
			// just tag the image instead of building it
			// WithExec([]string{"nerdctl", "build", "-t", imageName, "-"}, dagger.ContainerWithExecOpts{Stdin: "FROM alpine\nRUN touch /foo\n"}).
			WithExec([]string{"nerdctl", "pull", "alpine"}).
			WithExec([]string{"nerdctl", "tag", "alpine", imageName}).
			Sync(ctx)
		require.NoError(t, err)

		out, err := alt.
			WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_IMAGESTORE", "containerd").
			WithExec([]string{"dagger", "shell", "-c", `host | container-image ` + imageName + ` | with-exec ls,/etc/fstab | stdout`}, dagger.ContainerWithExecOpts{
				InsecureRootCapabilities: true,
			}).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "/etc/fstab\n", out)
	})
}

func (ContainerSuite) TestLoadSaveNone(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	dockerc := dockerSetup(ctx, t, c, containerSetupOpts{name: "provisioner"})
	dockerc, err := dockerLoadEngine(ctx, c, dockerc, "registry.dagger.io/engine:dev")
	require.NoError(t, err)

	dockerc = dockerc.
		WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:dev?container=dagger.test&port=1234").
		WithExec([]string{"dagger", "core", "version"})

	alt := dockerc.
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "tcp://docker:1234")

	imageName := "foobar:" + identity.NewID()
	out, err := alt.WithExec([]string{
		"dagger", "shell", "-c",
		`container | from "alpine" | with-exec touch,foo | export-image "` + imageName + `"`,
	}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeFailure}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "client has no supported api for loading image")

	out, err = dockerc.WithExec([]string{"docker", "inspect", imageName}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeFailure}).Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "No such object")

	out, err = alt.WithExec([]string{
		"dagger", "shell", "-c",
		`host | container-image ` + imageName + ` | with-exec echo,foo | stdout`,
	}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeFailure}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "client has no supported api for loading image")
}

func (ContainerSuite) TestSaveInNested(ctx context.Context, t *testctx.T) {
	// this shouldn't be possible! we shouldn't allow access to the external client.
	c := connect(ctx, t)
	dockerc := dockerSetup(ctx, t, c, containerSetupOpts{name: "provisioner"})
	dockerc, err := dockerLoadEngine(ctx, c, dockerc, "registry.dagger.io/engine:dev")
	require.NoError(t, err)

	dockerc = dockerc.
		WithMountedFile("/bin/dagger", daggerCliFile(t, c)).
		WithEnvVariable("_EXPERIMENTAL_DAGGER_RUNNER_HOST", "docker-image://registry.dagger.io/engine:dev")

	out, err := dockerc.WithWorkdir("/src/test").
		WithExec([]string{"dagger", "init", "--sdk=go"}).
		WithNewFile("main.go", `package main

import "context"

type Test struct{}

func (m *Test) Try(ctx context.Context) error {
	return dag.Container().
		From("alpine").
		WithExec([]string{"touch", "/foo"}).
		ExportImage(ctx, "foobar:latest")
}

		`).
		WithExec([]string{"dagger", "call", "try"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeFailure}).
		Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "client has no supported api for loading image")

	out, err = dockerc.WithExec([]string{"docker", "inspect", "foobar:latest"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeFailure}).Stderr(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "No such object")
}

func (ContainerSuite) TestExists(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ctr := c.Container().
		From(alpineImage).
		WithWorkdir("/sub").
		WithNewFile("subdir/data", "contents")
	exists, err := ctr.Exists(ctx, "subdir/data")
	require.NoError(t, err)
	require.Equal(t, true, exists)
}

func (ContainerSuite) TestWithoutFileOnMountedFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	f1 := c.File("f", "1")
	f2 := c.File("f", "2")
	ents, err := c.Container().
		From(alpineImage).
		WithFile("/mnt/f", f1).
		WithMountedFile("/mnt/f", f2).
		WithoutFile("/mnt/f").
		Directory("/mnt").
		Entries(ctx)
	require.NoError(t, err)
	require.Empty(t, ents)
}

func (ContainerSuite) TestWithFileOnMountedFile(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	d := c.Directory().WithNewFile("f1", "1").WithNewFile("f2", "2")
	f3 := c.File("f3", "3")
	ctr := c.Container().
		From(alpineImage).
		WithMountedDirectory("/mnt", d).
		WithMountedFile("/mnt/f2", f3)

	f1Contents, err := ctr.File("/mnt/f1").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "1", f1Contents)

	f2Contents, err := ctr.File("/mnt/f2").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "3", f2Contents)

	f4 := c.File("f4", "4")

	f2Contents, err = ctr.WithFile("/mnt/f2", f4).File("/mnt/f2").Contents(ctx)
	require.NoError(t, err)
	require.Equal(t, "4", f2Contents)
}

func (ContainerSuite) TestWithHostMount(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().
		From(alpineImage).
		WithMountedHostDirectory(".", "/hostdir").
		WithExec([]string{"touch", "/hostdir/newfile"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeSuccess})

	_, err := ctr.Stdout(ctx)
	require.NoError(t, err)

	ctr = c.Container().
		From(alpineImage).
		WithMountedHostDirectory(".", "/hostdir").
		WithExec([]string{"ls", "-la", "/hostdir"}, dagger.ContainerWithExecOpts{Expect: dagger.ReturnTypeSuccess})

	output, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, output, "newfile")
}
