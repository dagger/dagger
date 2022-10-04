package core

import (
	"context"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/core"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/internal/testutil"
)

func TestContainerScratch(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			ID     string
			Rootfs struct {
				Contents []string
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				id
				rootfs {
					contents
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Empty(t, res.Container.ID)
	require.Empty(t, res.Container.Rootfs.Contents)
}

func TestContainerFrom(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Rootfs struct {
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
					rootfs {
						file(path: "/etc/alpine-release") {
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.Rootfs.File.Contents, "3.16.2\n")
}

func TestContainerExecExitCode(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Exec struct {
					ExitCode int
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
	require.Equal(t, res.Container.From.Exec.ExitCode, 0)

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

						withEntrypoint {
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

func TestContainerExecWithVariable(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithVariable struct {
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
					withVariable(name: "FOO", value: "bar") {
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
	require.Contains(t, res.Container.From.WithVariable.Exec.Stdout.Contents, "FOO=bar\n")
}

func TestContainerVariables(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Variables []string
				Exec      struct {
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
					variables
					exec(args: ["env"]) {
						stdout {
							contents
						}
					}
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Container.From.Variables, []string{
		"PATH=/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"GOLANG_VERSION=1.18.2",
		"GOPATH=/go",
	})
	require.Contains(t, res.Container.From.Exec.Stdout.Contents, "GOPATH=/go\n")
}

func TestContainerVariable(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				Variable *string
			}
		}
	}{}

	err := testutil.Query(
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					variable(name: "GOLANG_VERSION")
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.NotNil(t, res.Container.From.Variable)
	require.Equal(t, "1.18.2", *res.Container.From.Variable)

	err = testutil.Query(
		`{
			container {
				from(address: "golang:1.18.2-alpine") {
					variable(name: "UNKNOWN")
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Nil(t, res.Container.From.Variable)
}

func TestContainerWithoutVariable(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithoutVariable struct {
					Variables []string
					Exec      struct {
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
					withoutVariable(name: "GOLANG_VERSION") {
						variables
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
	require.Equal(t, res.Container.From.WithoutVariable.Variables, []string{
		"PATH=/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"GOPATH=/go",
	})
	require.NotContains(t, res.Container.From.WithoutVariable.Exec.Stdout.Contents, "GOLANG_VERSION")
}

func TestContainerVariablesReplace(t *testing.T) {
	t.Parallel()

	res := struct {
		Container struct {
			From struct {
				WithVariable struct {
					Variables []string
					Exec      struct {
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
					withVariable(name: "GOPATH", value: "/gone") {
						variables
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
	require.Equal(t, res.Container.From.WithVariable.Variables, []string{
		"PATH=/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"GOLANG_VERSION=1.18.2",
		"GOPATH=/gone",
	})
	require.Contains(t, res.Container.From.WithVariable.Exec.Stdout.Contents, "GOPATH=/gone\n")
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
						Exec struct {
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
				from(address: "alpine:3.16.2") {
					withMountedDirectory(path: "/mnt", source: $id) {
						exec(args: ["sh", "-c", "test $(cat /mnt/some-file) = some-content"]) {
							exec(args: ["sh", "-c", "test $(cat /mnt/some-dir/sub-file) = sub-content"]) {
								exec(args: ["sh", "-c", "echo -n hi > /mnt/hello"]) {
									exec(args: ["cat", "/mnt/hello"]) {
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
	require.Equal(t, "hi", execRes.Container.From.WithMountedDirectory.Exec.Exec.Exec.Exec.Stdout.Contents)
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
	// FIXME:(sipsma) this test is a bit hacky+brittle, but unless we push to a real registry
	// or flesh out the idea of local services, it's probably the best we can do for now.

	// include a random ID so it runs every time (hack until we have no-cache or equivalent support)
	randomID := identity.NewID()
	err := engine.Start(context.Background(), nil, func(ctx engine.Context) error {
		go func() {
			err := ctx.Client.MakeRequest(ctx,
				&graphql.Request{
					Query: `query RunRegistry($rand: String!) {
						container {
							from(address: "registry:2") {
								withVariable(name: "RANDOM", value: $rand) {
									exec(args: ["/etc/docker/registry/config.yml"]) {
										stdout {
											contents
										}
										stderr {
											contents
										}
									}
								}
							}
						}
					}`,
					Variables: map[string]any{
						"rand": randomID,
					},
				},
				&graphql.Response{Data: new(map[string]any)},
			)
			if err != nil {
				t.Logf("error running registry: %v", err)
			}
		}()

		err := ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `query WaitForRegistry($rand: String!) {
					container {
						from(address: "alpine:3.16.2") {
							withVariable(name: "RANDOM", value: $rand) {
								exec(args: ["sh", "-c", "for i in $(seq 1 60); do nc -zv 127.0.0.1 5000 && exit 0; sleep 1; done; exit 1"]) {
									stdout {
										contents
									}
								}
							}
						}
					}
				}`,
				Variables: map[string]any{
					"rand": randomID,
				},
			},
			&graphql.Response{Data: new(map[string]any)},
		)
		require.NoError(t, err)

		testRef := core.ContainerAddress("127.0.0.1:5000/testimagepush:latest")
		err = ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `query TestImagePush($ref: ContainerAddress!) {
					container {
						from(address: "alpine:3.16.2") {
							publish(address: $ref)
						}
					}
				}`,
				Variables: map[string]any{
					"ref": testRef,
				},
			},
			&graphql.Response{Data: new(map[string]any)},
		)
		require.NoError(t, err)

		res := struct {
			Container struct {
				From struct {
					Rootfs struct {
						File struct {
							Contents string
						}
					}
				}
			}
		}{}
		err = ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `query TestImagePull($ref: ContainerAddress!) {
					container {
						from(address: $ref) {
							rootfs {
								file(path: "/etc/alpine-release") {
									contents
								}
							}
						}
					}
				}`,
				Variables: map[string]any{
					"ref": testRef,
				},
			},
			&graphql.Response{Data: &res},
		)
		require.NoError(t, err)
		require.Equal(t, res.Container.From.Rootfs.File.Contents, "3.16.2\n")
		return nil
	})
	require.NoError(t, err)
}
