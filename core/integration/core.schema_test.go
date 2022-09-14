package core

import (
	"context"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/cloak/engine"
	"github.com/dagger/cloak/internal/testutil"
	"github.com/moby/buildkit/identity"
	"github.com/stretchr/testify/require"
)

func init() {
	if err := testutil.SetupBuildkitd(); err != nil {
		panic(err)
	}
}

func TestCoreImage(t *testing.T) {
	t.Parallel()

	res := struct {
		Core struct {
			Image struct {
				File string
			}
		}
	}{}

	err := testutil.Query(
		`{
			core {
				image(ref: "alpine:3.16.2") {
					file(path: "/etc/alpine-release")
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Equal(t, res.Core.Image.File, "3.16.2\n")
}

func TestCoreGit(t *testing.T) {
	t.Parallel()

	res := struct {
		Core struct {
			Git struct {
				File string
			}
		}
	}{}

	err := testutil.Query(
		`{
			core {
				git(remote: "github.com/dagger/dagger") {
					file(path: "README.md", lines: 1)
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Contains(t, res.Core.Git.File, "Dagger")
}

func TestFilesystemCopy(t *testing.T) {
	t.Parallel()

	alpine := struct {
		Core struct {
			Image struct {
				ID string
			}
		}
	}{}

	err := testutil.Query(
		`{
			core {
				image(ref: "alpine:3.16.2") {
					id
				}
			}
		}`, &alpine, nil)
	require.NoError(t, err)
	require.NotEmpty(t, alpine.Core.Image.ID)

	res := struct {
		Core struct {
			Filesystem struct {
				Copy struct {
					File string
				}
			}
		}
	}{}

	testutil.Query(
		`query ($from: FSID!) {
			core {
				filesystem(id: "scratch") {
					copy(
						from: $from
						srcPath: "/etc/alpine-release"
						destPath: "/test"
					) {
						file(path: "/test")
					}
				}
			}
		}`, &res, &testutil.QueryOptions{
			Variables: map[string]any{
				"from": alpine.Core.Image.ID,
			},
		})
	require.NoError(t, err)
	require.Equal(t, "3.16.2\n", res.Core.Filesystem.Copy.File)
}

func TestCoreExec(t *testing.T) {
	t.Parallel()

	imageRes := struct {
		Core struct {
			Image struct {
				ID string
			}
		}
	}{}
	err := testutil.Query(
		`{
			core {
				image(ref: "alpine:3.16.2") {
					id
				}
			}
		}`, &imageRes, nil)
	require.NoError(t, err)
	id := imageRes.Core.Image.ID

	execRes := struct {
		Core struct {
			Image struct {
				Exec struct {
					Mount struct {
						File string
					}
				}
			}
		}
	}{}
	err = testutil.Query(
		`query TestExec($id: FSID!) {
			core {
				image(ref: "alpine:3.16.2") {
					exec(input: {
						args: ["sh", "-c", "echo hi > /mnt/hello"]
						mounts: [{fs: $id, path: "/mnt/"}]
					}) {
						mount(path: "/mnt") {
							file(path: "/hello")
						}
					}
				}
			}
		}`, &execRes, &testutil.QueryOptions{Variables: map[string]any{
			"id": id,
		}})
	require.NoError(t, err)
	require.Equal(t, "hi\n", execRes.Core.Image.Exec.Mount.File)
}

func TestCoreImageExport(t *testing.T) {
	// FIXME:(sipsma) this test is a bit hacky+brittle, but unless we push to a real registry
	// or flesh out the idea of local services, it's probably the best we can do for now.

	// include a random ID so it runs every time (hack until we have no-cache or equivalent support)
	randomID := identity.NewID()
	err := engine.Start(context.Background(), nil, func(ctx engine.Context) error {
		go func() {
			err := ctx.Client.MakeRequest(ctx,
				&graphql.Request{
					Query: `query RunRegistry($rand: String!) {
						core {
							image(ref: "registry:2") {
								exec(input: {
									args: ["/entrypoint.sh", "/etc/docker/registry/config.yml"]
									env: [{name: "RANDOM", value: $rand}]
								}) {
									stdout
									stderr
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
					core {
						image(ref: "alpine:3.16.2") {
							exec(input: {
								args: ["sh", "-c", "for i in $(seq 1 60); do nc -zv 127.0.0.1 5000 && exit 0; sleep 1; done; exit 1"]
								env: [{name: "RANDOM", value: $rand}]
							}) {
								stdout
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
					core {
						image(ref: "alpine:3.16.2") {
							pushImage(ref: $ref)
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
			Core struct {
				Image struct {
					File string
				}
			}
		}{}
		err = ctx.Client.MakeRequest(ctx,
			&graphql.Request{
				Query: `query TestImagePull($ref: String!) {
					core {
						image(ref: $ref) {
							file(path: "/etc/alpine-release")
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
		require.Equal(t, res.Core.Image.File, "3.16.2\n")
		return nil
	})
	require.NoError(t, err)
}
