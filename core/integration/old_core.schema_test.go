package core

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/moby/buildkit/identity"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
	"go.dagger.io/dagger/internal/testutil"
	"go.dagger.io/dagger/sdk/go/dagger"
)

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
	ctx := context.Background()
	c, err := dagger.Connect(ctx)
	require.NoError(t, err)
	defer c.Close()

	// FIXME:(sipsma) this test is a bit hacky+brittle, but unless we push to a real registry
	// or flesh out the idea of local services, it's probably the best we can do for now.

	// include a random ID so it runs every time (hack until we have no-cache or equivalent support)
	randomID := identity.NewID()
	wg := new(sync.WaitGroup)
	defer wg.Wait()

	registryCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	wg.Add(1)
	go func() {
		defer wg.Done()

		err := c.Do(registryCtx,
			&dagger.Request{
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
			&dagger.Response{Data: new(map[string]any)},
		)
		if err != nil {
			t.Logf("error running registry: %v", err)
		}
	}()

	err = c.Do(ctx,
		&dagger.Request{
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
		&dagger.Response{Data: new(map[string]any)},
	)
	require.NoError(t, err)

	testRef := "127.0.0.1:5000/testimagepush:latest"
	err = c.Do(ctx,
		&dagger.Request{
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
		&dagger.Response{Data: new(map[string]any)},
	)
	require.NoError(t, err)

	res := struct {
		Core struct {
			Image struct {
				File string
			}
		}
	}{}
	err = c.Do(ctx,
		&dagger.Request{
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
		&dagger.Response{Data: &res},
	)
	require.NoError(t, err)
	require.Equal(t, res.Core.Image.File, "3.16.2\n")

	testRef2 := "127.0.0.1:5000/testimageconfigpush:latest"
	res2 := struct {
		Core struct {
			Image struct {
				File string
			}
		}
	}{}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `query TestImageConfigPush($ref: String!) {
					core {
						image(ref: "php:8.1-apache") {
							pushImage(ref: $ref)
						}
					}
				}`,
			Variables: map[string]any{
				"ref": testRef2,
			},
		},
		&dagger.Response{Data: &res2},
	)
	require.NoError(t, err)

	res3 := struct {
		Core struct {
			Image struct {
				Exec struct {
					FS struct {
						Exec struct {
							Stdout string
						}
					}
				}
			}
		}
	}{}
	err = c.Do(ctx,
		&dagger.Request{
			Query: `query TestImageConfigPush($ref: String!) {
					core {
						image(ref: "regclient/regctl:latest") {
							exec(input: {
								args: ["/regctl", "registry", "set", "--tls=disabled", $ref]
							}) {
								fs {
									exec(input: {
										args: ["/regctl", "image", "inspect", $ref]
									}) {
										stdout
									}
								}
							}
						}
					}
				}`,
			Variables: map[string]any{
				"ref": testRef2,
			},
		},
		&dagger.Response{Data: &res3},
	)
	require.NoError(t, err)

	var img specs.Image
	err = json.Unmarshal([]byte(res3.Core.Image.Exec.FS.Exec.Stdout), &img)
	require.NoError(t, err)
	require.Equal(t, []string{"docker-php-entrypoint"}, img.Config.Entrypoint)
}
