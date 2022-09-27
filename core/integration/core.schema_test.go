package core

import (
	"context"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/engine"
	"go.dagger.io/dagger/internal/testutil"
)

func init() {
	if err := testutil.SetupBuildkitd(); err != nil {
		panic(err)
	}
}

func TestContainerFrom(t *testing.T) {
	t.Skip("not implemented yet")

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

func TestContainerImageConfig(t *testing.T) {
	t.Skip("not implemented yet")

	t.Parallel()

	t.Run("propagates env", func(t *testing.T) {
		res := struct {
			Core struct {
				Image struct {
					Exec struct {
						Stdout string
					}
				}
			}
		}{}

		err := testutil.Query(
			`{
			core {
				image(ref: "golang:1.19") {
					exec(input: {args: ["env"]}) {
						stdout
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Contains(t, res.Core.Image.Exec.Stdout, "GOLANG_VERSION=")
	})

	t.Run("exec env overrides", func(t *testing.T) {
		res := struct {
			Core struct {
				Image struct {
					Exec struct {
						Stdout string
					}
				}
			}
		}{}

		err := testutil.Query(
			`{
			core {
				image(ref: "golang:1.19") {
					exec(input: {args: ["env"], env: [{name: "GOLANG_VERSION", value: "banana"}]}) {
						stdout
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Contains(t, res.Core.Image.Exec.Stdout, "GOLANG_VERSION=banana")
	})

	t.Run("propagates dir", func(t *testing.T) {
		res := struct {
			Core struct {
				Image struct {
					Exec struct {
						Stdout string
					}
				}
			}
		}{}

		err := testutil.Query(
			`{
			core {
				image(ref: "golang:1.19") {
					exec(input: {args: ["pwd"]}) {
						stdout
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, res.Core.Image.Exec.Stdout, "/go\n")
	})

	t.Run("exec dir overrides", func(t *testing.T) {
		res := struct {
			Core struct {
				Image struct {
					Exec struct {
						Stdout string
					}
				}
			}
		}{}

		err := testutil.Query(
			`{
			core {
				image(ref: "golang:1.19") {
					exec(input: {args: ["pwd"], workdir: "/usr"}) {
						stdout
					}
				}
			}
		}`, &res, nil)
		require.NoError(t, err)
		require.Equal(t, res.Core.Image.Exec.Stdout, "/usr\n")
	})
}

func TestCopiedFile(t *testing.T) {
	t.Skip("not implemented yet")

	t.Parallel()

	alpine := struct {
		Container struct {
			From struct {
				Rootfs struct {
					File struct {
						ID string
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
							id
						}
					}
				}
			}
		}`, &alpine, nil)
	require.NoError(t, err)
	require.NotEmpty(t, alpine.Container.From.Rootfs.File.ID)

	res := struct {
		Directory struct {
			WithCopiedFile struct {
				File struct {
					Contents string
				}
			}
		}
	}{}

	testutil.Query(
		`query ($from: FileID!) {
			directory {
				withCopiedFile(path: "/test", source: $from) {
					file(path: "/test") {
						contents
					}
				}
			}
		}`, &res, &testutil.QueryOptions{
			Variables: map[string]any{
				"from": alpine.Container.From.Rootfs.File.ID,
			},
		})
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", res.Directory.WithCopiedFile.File.Contents)
}

func TestContainerMountExec(t *testing.T) {
	t.Skip("not implemented yet")

	t.Parallel()

	ctrRes := struct {
		Container struct {
			From struct {
				Rootfs struct {
					ID string
				}
			}
		}
	}{}
	err := testutil.Query(
		`{
			container {
				from(address: "alpine:3.16.2") {
					rootfs {
						id
					}
				}
			}
		}`, &ctrRes, nil)
	require.NoError(t, err)
	id := ctrRes.Container.From.Rootfs.ID

	execRes := struct {
		Container struct {
			From struct {
				WithMountedDirectory struct {
					Exec struct {
						Directory struct {
							File struct {
								Contents string
							}
						}
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query TestExec($id: DirectoryID!) {
			container {
				from(address: "alpine:3.16.2") {
					withMountedDirectory(path: "/mnt", source: $id) {
						exec(args: ["sh", "-c", "echo hi > /mnt/hello"]) {
							directory(path: "/mnt") {
								file(path: "/hello") {
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
	require.Equal(t, "hi\n", execRes.Container.From.WithMountedDirectory.Exec.Directory.File.Contents)
}

func TestContainerImageExport(t *testing.T) {
	t.Skip("not implemented yet")

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
									exec(args: ["/entrypoint.sh", "/etc/docker/registry/config.yml"]) {
										stdout
										stderr
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
									stdout
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

		testRef := "127.0.0.1:5000/testimagepush:latest"
		err = ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `query TestImagePush($ref: String!) {
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
				Query: `query TestImagePull($ref: String!) {
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
