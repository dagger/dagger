package core

import (
	"testing"

	"github.com/dagger/cloak/internal/testutil"
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
					file(path: "README.md")
				}
			}
		}`, &res, nil)
	require.NoError(t, err)
	require.Contains(t, res.Core.Git.File, "dagger")
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
