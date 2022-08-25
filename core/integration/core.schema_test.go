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
